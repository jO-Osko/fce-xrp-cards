package imagecheck

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// saveTar builds a docker save tar holding config under name and manifest.json pointing at it.
func saveTar(t *testing.T, name string, config []byte) []byte {
	t.Helper()
	manifest, err := json.Marshal([]map[string]any{{"Config": name, "Layers": []string{}}})
	require.NoError(t, err)
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range []struct {
		name string
		b    []byte
	}{{name, config}, {"manifest.json", manifest}} {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.b)), Typeflag: tar.TypeReg}))
		_, err := tw.Write(f.b)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

func TestReadConfig(t *testing.T) {
	t.Parallel()
	config := []byte(`{"created":"2026-10-01T00:00:00Z"}`)
	sum := sha256.Sum256(config)
	h := hex.EncodeToString(sum[:])

	for _, name := range []string{"blobs/sha256/" + h, h + ".json"} {
		raw, digest, err := readConfig(bytes.NewReader(saveTar(t, name, config)))
		require.NoError(t, err, name)
		assert.Equal(t, config, raw)
		assert.Equal(t, "sha256:"+h, digest)
	}

	_, _, err := readConfig(bytes.NewReader(saveTar(t, "blobs/sha256/"+strings.Repeat("0", 64), config)))
	require.ErrorContains(t, err, "has digest")
	_, _, err = readConfig(bytes.NewReader([]byte("not a tar")))
	require.Error(t, err)
}

// goodConfig is the template's config.
func goodConfig() imageConfig {
	var c imageConfig
	c.Created = time.Unix(1790812800, 0).UTC()
	c.Config.Env = []string{"PATH=/bin", "MODE=0", "SIGN_PORT=8888", "EXTENSION_PORT=8889"}
	c.Config.Entrypoint = []string{"/app/launcher", "--node", "/app/tee-node", "--uid", "65532", "--env", "MY_VAR", "--", "/app/extension"}
	c.Config.ExposedPorts = map[string]struct{}{"5500/tcp": {}}
	c.Config.Labels = map[string]string{
		labelTitle:   "my-extension",
		labelVersion: "0.1.0",
		labelEnv:     strings.Join(teeNodeEnv, ",") + ",MY_VAR",
	}
	return c
}

func configResultsByName(t *testing.T, c imageConfig) (map[string]Result, target) {
	t.Helper()
	res, tg := configResults(c, 1790812800, nil)
	m := map[string]Result{}
	for _, r := range res {
		m[r.Name] = r
	}
	return m, tg
}

func TestConfigResultsPass(t *testing.T) {
	t.Parallel()
	res, tg := configResultsByName(t, goodConfig())
	for name, r := range res {
		assert.NoError(t, r.Err, name)
		assert.NoError(t, r.Advice, name)
	}
	require.NoError(t, tg.launchErr)
	assert.Equal(t, 8888, tg.signPort)
	assert.Equal(t, 8889, tg.extPort)
	assert.Equal(t, "/app/tee-node", tg.launch.node)
	assert.Equal(t, []string{"/app/extension"}, tg.launch.command)
}

func TestConfigResultsFail(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		change   func(c *imageConfig)
		rule     string
		required bool // the image can't work or be registered: Err, not Advice
		noLaunch bool // the launcher's arguments are unusable
	}{
		{"created", func(c *imageConfig) { c.Created = c.Created.Add(time.Second) }, "created", false, false},
		{"mode", func(c *imageConfig) { c.Config.Env[1] = "MODE=1" }, "MODE=0", true, false},
		{"no mode", func(c *imageConfig) { c.Config.Env = c.Config.Env[2:] }, "MODE=0", true, false},
		{"bad port", func(c *imageConfig) { c.Config.Env[2] = "SIGN_PORT=x" }, "SIGN_PORT", false, false},
		{"config port", func(c *imageConfig) { c.Config.Env[3] = "EXTENSION_PORT=5500" }, "SIGN_PORT", false, false},
		{"same ports", func(c *imageConfig) { c.Config.Env[3] = "EXTENSION_PORT=8888" }, "SIGN_PORT", false, false},
		{"another port", func(c *imageConfig) { c.Config.ExposedPorts["8889/tcp"] = struct{}{} }, "exposed", false, false},
		{"no port", func(c *imageConfig) { c.Config.ExposedPorts = nil }, "exposed", false, false},
		{"cmd", func(c *imageConfig) { c.Config.Cmd = []string{"x"} }, "ENTRYPOINT", false, false},
		{"no entrypoint", func(c *imageConfig) { c.Config.Entrypoint = nil }, "ENTRYPOINT", false, true},
		{"root extension", func(c *imageConfig) { c.Config.Entrypoint[4] = "0" }, "ENTRYPOINT", false, true},
		{"tee-node's variable missing", func(c *imageConfig) {
			c.Config.Labels[labelEnv] = strings.Join(teeNodeEnv[1:], ",") + ",MY_VAR"
		}, "variables", false, false},
		{"mode overridable", func(c *imageConfig) { c.Config.Labels[labelEnv] += ",MODE" }, "variables", false, false},
		{"label without --env", func(c *imageConfig) { c.Config.Labels[labelEnv] += ",OTHER" }, "variables", false, false},
		{"--env without label", func(c *imageConfig) { c.Config.Entrypoint[6] = "MY_VAR,OTHER" }, "variables", false, false},
		{"no title", func(c *imageConfig) { delete(c.Config.Labels, labelTitle) }, "title", true, false},
		{"long version", func(c *imageConfig) { c.Config.Labels[labelVersion] = strings.Repeat("1", 33) }, "title", true, false},
		{"cmd override", func(c *imageConfig) { c.Config.Labels[labelCmdOverride] = "TRUE" }, "git labels", false, false},
		{"log redirect", func(c *imageConfig) { c.Config.Labels[labelLogRedirect] = "always" }, "git labels", false, false},
		{"git label", func(c *imageConfig) { c.Config.Labels["org.opencontainers.image.revision"] = "abc" }, "git labels", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := goodConfig()
			tt.change(&c)
			res, tg := configResultsByName(t, c)
			assert.Equal(t, tt.noLaunch, tg.launchErr != nil)
			for name, r := range res {
				broken := strings.Contains(name, tt.rule)
				assert.Equal(t, broken && tt.required, r.Err != nil, "%s: %v", name, r.Err)
				assert.Equal(t, broken && !tt.required, r.Advice != nil, "%s: %v", name, r.Advice)
			}
		})
	}
}

func TestConfigResultsNoEpoch(t *testing.T) {
	t.Parallel()
	res, _ := configResults(goodConfig(), 0, assert.AnError)
	require.ErrorContains(t, res[0].Advice, "no SOURCE_DATE_EPOCH")
}

// An unset port is tee-node's default, so the smoke run still dials it.
func TestConfigResultsDefaultPorts(t *testing.T) {
	t.Parallel()
	c := goodConfig()
	c.Config.Env = c.Config.Env[:2]
	res, tg := configResultsByName(t, c)
	assert.Equal(t, defaultSignPort, tg.signPort)
	assert.Equal(t, defaultExtPort, tg.extPort)
	assert.ErrorContains(t, res["config: SIGN_PORT and EXTENSION_PORT set"].Advice, "SIGN_PORT is unset;")
}

func TestConfigResultsModeValue(t *testing.T) {
	t.Parallel()
	c := goodConfig()
	c.Config.Env = c.Config.Env[2:]
	res, _ := configResultsByName(t, c)
	assert.ErrorContains(t, res["config: MODE=0"].Err, "MODE is unset;")
	c.Config.Env = append(c.Config.Env, "MODE=")
	res, _ = configResultsByName(t, c)
	assert.ErrorContains(t, res["config: MODE=0"].Err, `MODE is "";`)
}
