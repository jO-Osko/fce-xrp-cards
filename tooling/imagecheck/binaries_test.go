package imagecheck

import (
	"debug/buildinfo"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLauncherResult(t *testing.T) {
	t.Parallel()
	require.NoError(t, launcherResult(&debug.BuildInfo{Path: launcherModule}).Advice)
	require.Error(t, launcherResult(&debug.BuildInfo{Path: "github.com/x/other"}).Advice)
}

func TestTeeNodeResult(t *testing.T) {
	t.Parallel()
	pin := &goModule{Path: teeNodeModule, Version: "v0.0.28", Sum: "h1:a="}
	built := func(version, sum string) *debug.BuildInfo {
		return &debug.BuildInfo{Path: teeNodeModule + "/cmd/extension", Main: debug.Module{Path: teeNodeModule, Version: version, Sum: sum}}
	}

	r := teeNodeResult(built("v0.0.28", "h1:a="), pin)
	require.NoError(t, r.Advice)

	require.Error(t, teeNodeResult(built("v0.0.27", "h1:b="), pin).Advice)
	require.Error(t, teeNodeResult(built("v0.0.28", "h1:b="), pin).Advice)
	require.Error(t, teeNodeResult(built("v0.0.28", "h1:a="), nil).Advice)
	other := built("v0.0.28", "h1:a=")
	other.Path = teeNodeModule + "/cmd"
	require.Error(t, teeNodeResult(other, pin).Advice)
	// a replacement, as a fork's own Dockerfile could build it
	require.Error(t, teeNodeResult(built("(devel)", ""), pin).Advice)
}

// TestTeeNodeEnv compares teeNodeEnv with the launch-policy label in the pinned tee-node's own Dockerfile.
func TestTeeNodeEnv(t *testing.T) {
	t.Parallel()
	dir, err := exec.CommandContext(t.Context(), "go", "list", "-m", "-f", "{{.Dir}}", teeNodeModule).Output()
	require.NoError(t, err)
	require.NotEmpty(t, strings.TrimSpace(string(dir)), "tee-node isn't downloaded; run go mod download")
	src, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(dir)), "Dockerfile"))
	require.NoError(t, err)
	m := regexp.MustCompile(`"` + regexp.QuoteMeta(labelEnv) + `"="([^"]*)"`).FindSubmatch(src)
	require.NotNil(t, m, "no %s label in tee-node's Dockerfile", labelEnv)
	assert.Equal(t, strings.Split(string(m[1]), ","), teeNodeEnv)
}

// TestTeeNodeImageFiles lists what the pinned tee-node's own image copies in, so a bump that adds a
// file fails here: the scaffold's image has the binary as /app/tee-node and leaves out the
// certificate, which tee-node v0.0.29 doesn't read.
func TestTeeNodeImageFiles(t *testing.T) {
	t.Parallel()
	dir, err := exec.CommandContext(t.Context(), "go", "list", "-m", "-f", "{{.Dir}}", teeNodeModule).Output()
	require.NoError(t, err)
	src, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(dir)), "Dockerfile"))
	require.NoError(t, err)
	df, err := parseDockerfile(string(src))
	require.NoError(t, err)

	var files []string
	for _, in := range df.instructions {
		switch in.cmd {
		case "FROM":
			files = files[:0] // only the last stage is the image
		case "COPY":
			_, words := flags(in.args)
			files = append(files, words[len(words)-1])
		}
	}
	assert.Equal(t, []string{"/app/server", "/app/assets/google_confidential_space_root.crt"}, files)
}

const scaffoldModule = "github.com/flare-foundation/fce-go-scaffold"

// TestLauncherMirror keeps launcher/ identical to the scaffold's launcher/, which every image builds.
func TestLauncherMirror(t *testing.T) {
	t.Parallel()
	dir, err := exec.CommandContext(t.Context(), "go", "list", "-m", "-f", "{{.Dir}}", scaffoldModule).Output()
	require.NoError(t, err)
	theirs := filepath.Join(strings.TrimSpace(string(dir)), "launcher")
	if _, err := os.Stat(theirs); err != nil {
		// a nested module isn't in the scaffold's module zip, only in a checkout go.mod replaces it with
		t.Skipf("no scaffold launcher at %s", theirs)
	}
	ours := filepath.Join("..", "launcher")

	names := func(d string) []string {
		entries, err := os.ReadDir(d)
		require.NoError(t, err)
		n := make([]string, 0, len(entries))
		for _, e := range entries {
			n = append(n, e.Name())
		}
		return n
	}
	require.Equal(t, names(theirs), names(ours), "launcher/ has other files than the scaffold's")
	for _, n := range names(ours) {
		a, err := os.ReadFile(filepath.Join(theirs, n))
		require.NoError(t, err)
		b, err := os.ReadFile(filepath.Join(ours, n))
		require.NoError(t, err)
		assert.Equal(t, string(a), string(b), "launcher/%s differs from the scaffold's", n)
	}
}

// TestBuildProbe builds the probe from the embedded source, so a file missing from the embed fails here.
func TestBuildProbe(t *testing.T) {
	t.Parallel()
	bin, err := BuildProbe(t.Context(), t.TempDir())
	require.NoError(t, err)
	bi, err := buildinfo.ReadFile(bin)
	require.NoError(t, err)
	settings := map[string]string{}
	for _, s := range bi.Settings {
		settings[s.Key] = s.Value
	}
	assert.Equal(t, "linux", settings["GOOS"])
	assert.Equal(t, "amd64", settings["GOARCH"])
	assert.Equal(t, "0", settings["CGO_ENABLED"])
}
