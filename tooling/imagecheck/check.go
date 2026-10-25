// Package imagecheck checks an extension's built image from the outside:
// static rules on its Dockerfile and its attested config, hadolint, the
// binaries it runs, and a smoke run of the image as shipped. It drives the
// docker CLI, and reads the fork's go.mod with the go command.
package imagecheck

import (
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Result is one check's outcome.
type Result struct {
	Name string
	// Err is set when a required check failed: the image can't work or be registered.
	Err error
	// Advice is set when the image doesn't follow a recommendation, or a check was skipped.
	Advice error
}

// Hadolint is hadolint's image, pinned by digest.
const Hadolint = "hadolint/hadolint:v2.15.1@sha256:32dac94127fd60b7b7e3fbfc65e1383b9b5e25c9bfd7b8536de7a539fe68a12d"

// Config is one image's check.
type Config struct {
	// Root is the fork's root, where its go.mod is.
	Root string
	// Image is the loaded image, e.g. fce/template; Dockerfile is the file it was built from.
	Image      string
	Dockerfile string
	// Probe is the probe BuildProbe built.
	Probe string
}

// Report is an image check's outcome.
type Report struct {
	// Digest is the image's config digest, the hash Confidential Space attests.
	Digest  string
	Results []Result
	// Logs holds the output of containers whose checks failed.
	Logs []string
}

// OK reports whether every required check passed and, unless relaxed, every recommendation was followed.
func (r Report) OK(relaxed bool) bool { return !failed(r.Results, relaxed) }

func failed(res []Result, relaxed bool) bool {
	for _, r := range res {
		if r.Err != nil || (!relaxed && r.Advice != nil) {
			return true
		}
	}
	return false
}

// skipped is the Advice of a check that couldn't run.
func skipped(why error) error { return fmt.Errorf("skipped: %w", why) }

// errNoLauncher skips the checks that need the launcher's arguments; the launcher check says why they're unusable.
var errNoLauncher = errors.New("no usable launcher in ENTRYPOINT")

type checker struct {
	cfg  Config
	d    *docker
	t    target
	logs []string
}

func (c *checker) addLogs(container string) {
	c.logs = append(c.logs, logSection(container, c.d.logs(container)))
}

// logSection is a container's output under a header, ending in a newline.
func logSection(container, out string) string {
	if strings.TrimSpace(out) == "" {
		return fmt.Sprintf("--- %s: no output ---\n", container)
	}
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return fmt.Sprintf("--- %s ---\n%s", container, out)
}

// Run checks cfg.Image. Cancelling ctx stops the checks; their containers are removed either way.
func Run(ctx context.Context, cfg Config) (rep Report) {
	c := &checker{cfg: cfg, d: newDocker()}
	defer c.d.cleanup()
	add := func(r ...Result) { rep.Results = append(rep.Results, r...) }
	defer func() { rep.Logs = c.logs }()

	src, err := os.ReadFile(cfg.Dockerfile)
	if err != nil {
		add(Result{Name: "Dockerfile", Err: err})
		return rep
	}
	df, err := parseDockerfile(string(src))
	if err != nil {
		add(Result{Name: "Dockerfile: parsing", Advice: fmt.Errorf("%w; the Dockerfile rules skipped", err)})
	} else {
		add(dockerfileResults(df)...)
	}
	add(c.hadolint(ctx, string(src)))

	dir, err := os.MkdirTemp("", "imagecheck")
	if err != nil {
		add(Result{Name: "config", Err: err})
		return rep
	}
	defer os.RemoveAll(dir) //nolint:errcheck // a temporary directory

	ic, digest, err := c.config(ctx, dir)
	if err != nil {
		add(Result{Name: "config", Err: err})
		return rep
	}
	rep.Digest = digest
	epoch, epochErr := df.epoch()
	res, t := configResults(ic, epoch, epochErr)
	add(res...)
	c.t = t
	add(c.binaries(ctx, dir)...)
	add(c.smoke(ctx)...)
	return rep
}

// hadolint lints the Dockerfile with the pinned hadolint.
func (c *checker) hadolint(ctx context.Context, src string) Result {
	r := Result{Name: "hadolint"}
	out, err := c.d.run(ctx, strings.NewReader(src), "run", "--rm", "-i", "--name", c.d.name("hadolint"), Hadolint, "hadolint", "--no-color", "-")
	if err != nil {
		r.Advice = fmt.Errorf("%w\n%s", err, out)
	}
	return r
}

// config reads the image's config from docker save, as attested.
func (c *checker) config(ctx context.Context, dir string) (imageConfig, string, error) {
	var ic imageConfig
	tarPath := filepath.Join(dir, "image.tar")
	if _, err := c.d.run(ctx, nil, "save", "-o", tarPath, c.cfg.Image); err != nil {
		return ic, "", err
	}
	f, err := os.Open(tarPath)
	if err != nil {
		return ic, "", err
	}
	defer f.Close() //nolint:errcheck // read-only
	raw, digest, err := readConfig(f)
	if err != nil {
		return ic, "", err
	}
	if err := json.Unmarshal(raw, &ic); err != nil {
		return ic, "", fmt.Errorf("reading the config: %w", err)
	}
	return ic, digest, nil
}

// binaries copies the launcher and tee-node out of the image and checks what they were built from.
func (c *checker) binaries(ctx context.Context, dir string) []Result {
	launcherRes := Result{Name: "binaries: the ENTRYPOINT runs the launcher"}
	nodeRes := Result{Name: "binaries: tee-node at go.mod's version"}
	fail := func(err error) []Result {
		launcherRes.Advice, nodeRes.Advice = err, err
		return []Result{launcherRes, nodeRes}
	}
	if len(c.t.entrypoint) == 0 {
		return fail(skipped(errors.New("no ENTRYPOINT")))
	}

	n := c.d.name("files")
	if _, err := c.d.run(ctx, nil, "create", "--platform", platform, "--name", n, c.cfg.Image); err != nil {
		return fail(err)
	}
	read := func(path, local string) (*buildinfo.BuildInfo, error) {
		dst := filepath.Join(dir, local)
		if _, err := c.d.run(ctx, nil, "cp", n+":"+path, dst); err != nil {
			return nil, err
		}
		bi, err := buildinfo.ReadFile(dst)
		if err != nil {
			return nil, fmt.Errorf("reading %s's build info: %w", path, err)
		}
		return bi, nil
	}
	node, err := goList(ctx, c.cfg.Root, teeNodeModule)
	if err != nil {
		return fail(err)
	}

	if bi, err := read(c.t.entrypoint[0], "launcher"); err != nil {
		launcherRes.Advice = err
	} else {
		launcherRes = launcherResult(bi)
	}
	if c.t.launchErr != nil {
		nodeRes.Advice = skipped(errNoLauncher)
	} else if bi, err := read(c.t.launch.node, "tee-node"); err != nil {
		nodeRes.Advice = err
	} else {
		nodeRes = teeNodeResult(bi, node)
	}
	return []Result{launcherRes, nodeRes}
}

// goList reports a module of root's build list, or the main module without a path, as go list -m sees it there.
func goList(ctx context.Context, root string, path ...string) (*goModule, error) {
	cmd := exec.CommandContext(ctx, "go", append([]string{"list", "-m", "-json"}, path...)...)
	// the image sees only go.mod and go.sum
	cmd.Dir, cmd.Env = root, append(os.Environ(), "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -m %s: %w", strings.Join(path, " "), err)
	}
	var m goModule
	if err := json.Unmarshal(out, &m); err != nil {
		return nil, fmt.Errorf("reading go list's output: %w", err)
	}
	return &m, nil
}
