package imagecheck

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

// teeNodeEnv is tee-node's own launch-policy list, as its Dockerfile has it; a test compares it with the pinned tee-node's.
var teeNodeEnv = []string{
	"LOG_LEVEL", "PROXY_URL", "INITIAL_OWNER", "EXTENSION_ID", "CHAIN_ID",
	"GOVERNANCE_SIGNERS", "GOVERNANCE_THRESHOLD", "GOVERNANCE_SAFE", "GOVERNANCE_TEE_MANAGER",
}

// Labels the rules read.
const (
	labelEnv         = "tee.launch_policy.allow_env_override"
	labelCmdOverride = "tee.launch_policy.allow_cmd_override"
	labelLogRedirect = "tee.launch_policy.log_redirect"
	labelTitle       = "org.opencontainers.image.title"
	labelVersion     = "org.opencontainers.image.version"
)

// fixedEnv is set by the image; an operator must not override it (MODE=1 is test mode).
var fixedEnv = []string{"MODE", "SIGN_PORT", "EXTENSION_PORT"}

// gitLabels carry the commit (buildx's BUILDX_GIT_LABELS), so the same code would hash differently at every commit.
var gitLabels = []string{"org.opencontainers.image.revision", "org.opencontainers.image.source", "com.docker.image.source.entrypoint"}

// configPort is tee-node's config port, the only one the image exposes.
const configPort = 5500

// tee-node's ports when SIGN_PORT or EXTENSION_PORT is unset.
const (
	defaultSignPort = 8888
	defaultExtPort  = 8889
)

// imageConfig is the part of an image config the rules read.
type imageConfig struct {
	Created time.Time `json:"created"`
	Config  struct {
		Env          []string            `json:"Env"`
		Entrypoint   []string            `json:"Entrypoint"`
		Cmd          []string            `json:"Cmd"`
		ExposedPorts map[string]struct{} `json:"ExposedPorts"`
		Labels       map[string]string   `json:"Labels"`
	} `json:"config"`
}

// readConfig returns the config blob and its digest from a docker save tar, checking the blob against its name.
func readConfig(r io.Reader) ([]byte, string, error) {
	// manifest.json and configs are a few KiB; layers are skipped
	const maxSmall = 1 << 20
	small := map[string][]byte{}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("reading the image tar: %w", err)
		}
		if h.Typeflag != tar.TypeReg || h.Size > maxSmall {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return nil, "", fmt.Errorf("reading the image tar: %w", err)
		}
		small[h.Name] = b
	}

	var manifest []struct {
		Config string `json:"Config"`
	}
	if err := json.Unmarshal(small["manifest.json"], &manifest); err != nil {
		return nil, "", fmt.Errorf("reading manifest.json: %w", err)
	}
	if len(manifest) != 1 {
		return nil, "", fmt.Errorf("manifest.json lists %d images, want 1", len(manifest))
	}
	name := manifest[0].Config
	raw, ok := small[name]
	if !ok {
		return nil, "", fmt.Errorf("the tar has no config %s", name)
	}
	// both layouts name it by its digest: blobs/sha256/<hex> or <hex>.json
	want := strings.TrimSuffix(path.Base(name), ".json")
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != want {
		return nil, "", fmt.Errorf("config %s has digest %s", name, got)
	}
	return raw, "sha256:" + want, nil
}

// target is what the binaries and smoke checks need from the config.
type target struct {
	entrypoint []string
	launch     launch
	// launchErr is why launch is unusable: no launcher, or arguments it doesn't take.
	launchErr error
	signPort  int
	extPort   int
}

// configResults checks the image config; epoch is the Dockerfile's SOURCE_DATE_EPOCH, or an error.
func configResults(c imageConfig, epoch int64, epochErr error) (res []Result, t target) {
	var created error
	switch {
	case epochErr != nil:
		created = errors.New("no SOURCE_DATE_EPOCH in the Dockerfile to compare with")
	case !c.Created.Equal(time.Unix(epoch, 0)):
		created = fmt.Errorf("created is %s, SOURCE_DATE_EPOCH is %s: built without --build-arg SOURCE_DATE_EPOCH?",
			c.Created.UTC().Format(time.RFC3339), time.Unix(epoch, 0).UTC().Format(time.RFC3339))
	}
	res = append(res, Result{Name: "config: created is SOURCE_DATE_EPOCH", Advice: created})

	env := map[string]string{}
	for _, kv := range c.Config.Env {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	// value shows an unset variable as unset, a set one quoted
	value := func(name string) string {
		if v, set := env[name]; set {
			return strconv.Quote(v)
		}
		return "unset"
	}
	var mode error
	if env["MODE"] != "0" {
		mode = fmt.Errorf("MODE is %s; tee-node defaults to 1, test mode, which attests a test code hash: set MODE=0", value("MODE"))
	}
	res = append(res, Result{Name: "config: MODE=0", Err: mode})

	var portErrs []error
	// an unusable value stops tee-node at startup, which the boot check reports
	port := func(name string, def int) int {
		n, err := strconv.Atoi(env[name])
		if err != nil || n < 1 || n > 65535 || n == configPort {
			portErrs = append(portErrs, fmt.Errorf("%s is %s; the image sets it to a port other than %d", name, value(name), configPort))
			return def
		}
		return n
	}
	t.signPort, t.extPort = port("SIGN_PORT", defaultSignPort), port("EXTENSION_PORT", defaultExtPort)
	if len(portErrs) == 0 && t.signPort == t.extPort {
		portErrs = append(portErrs, errors.New("SIGN_PORT and EXTENSION_PORT are the same port"))
	}
	res = append(res, Result{Name: "config: SIGN_PORT and EXTENSION_PORT set", Advice: errors.Join(portErrs...)})

	var exposed error
	want := strconv.Itoa(configPort) + "/tcp"
	if _, ok := c.Config.ExposedPorts[want]; !ok || len(c.Config.ExposedPorts) != 1 {
		exposed = fmt.Errorf("exposes %v; only %s", slices.Sorted(maps.Keys(c.Config.ExposedPorts)), want)
	}
	res = append(res, Result{Name: "config: only tee-node's config port exposed", Advice: exposed})

	var cmdErr error
	t.entrypoint = c.Config.Entrypoint
	if len(c.Config.Cmd) > 0 {
		cmdErr = fmt.Errorf("CMD %q: every launcher argument belongs in ENTRYPOINT", c.Config.Cmd)
	}
	if len(t.entrypoint) == 0 {
		t.launchErr = errors.New("no ENTRYPOINT")
	} else if t.launch, t.launchErr = parseLaunch(t.entrypoint[1:]); t.launchErr != nil {
		t.launchErr = fmt.Errorf("the launcher's arguments: %w", t.launchErr)
	}
	if t.launchErr != nil {
		t.launch = launch{grace: defaultGrace}
	}
	res = append(res, Result{Name: "config: the launcher and its arguments in ENTRYPOINT, no CMD", Advice: errors.Join(cmdErr, t.launchErr)})

	return append(res, labelResults(c.Config.Labels, t.launch.env, t.launchErr == nil)...), t
}

// labelResults checks the labels; launchEnv is the launcher's --env list, compared when launchOK.
func labelResults(labels map[string]string, launchEnv []string, launchOK bool) []Result {
	var envErrs []error
	listed := map[string]bool{}
	for name := range strings.SplitSeq(labels[labelEnv], ",") {
		if name = strings.TrimSpace(name); name != "" {
			listed[name] = true
		}
	}
	for _, n := range teeNodeEnv {
		if !listed[n] {
			envErrs = append(envErrs, fmt.Errorf("tee-node's %s missing", n))
		}
	}
	for _, n := range fixedEnv {
		if listed[n] {
			envErrs = append(envErrs, fmt.Errorf("%s listed: the image fixes it", n))
		}
	}
	if launchOK {
		own := map[string]bool{}
		for n := range listed {
			if !slices.Contains(teeNodeEnv, n) && !slices.Contains(fixedEnv, n) {
				own[n] = true
			}
		}
		for _, n := range launchEnv {
			if !own[n] {
				envErrs = append(envErrs, fmt.Errorf("the launcher's --env has %s, the label doesn't", n))
			}
			delete(own, n)
		}
		for _, n := range slices.Sorted(maps.Keys(own)) {
			envErrs = append(envErrs, fmt.Errorf("the label has %s, the launcher's --env doesn't", n))
		}
	}

	var meta []error
	if labels[labelTitle] == "" {
		meta = append(meta, fmt.Errorf("no %s", labelTitle))
	}
	if v := labels[labelVersion]; v == "" || len(v) > 32 {
		meta = append(meta, fmt.Errorf("%s is %q; tooling registers it as bytes32, so 1 to 32 bytes", labelVersion, v))
	}

	var policy []error
	if strings.EqualFold(labels[labelCmdOverride], "true") {
		policy = append(policy, fmt.Errorf("%s=true lets an operator append launcher arguments", labelCmdOverride))
	}
	if strings.EqualFold(labels[labelLogRedirect], "always") {
		policy = append(policy, fmt.Errorf("%s=always lets production logs leave the TEE", labelLogRedirect))
	}
	for _, l := range gitLabels {
		if _, set := labels[l]; set {
			policy = append(policy, fmt.Errorf("%s carries the commit; build with BUILDX_GIT_LABELS unset", l))
		}
	}

	return []Result{
		{Name: "labels: the launch policy's variables match tee-node's and the launcher's --env", Advice: errors.Join(envErrs...)},
		{Name: "labels: title and version", Err: errors.Join(meta...)},
		{Name: "labels: launch policy and no git labels", Advice: errors.Join(policy...)},
	}
}
