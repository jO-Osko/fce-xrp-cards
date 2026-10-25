package fork

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFind(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, VersionFile), []byte("v0.1\n"), 0o644))
	ext := filepath.Join(root, "examples", "sign")
	require.NoError(t, os.MkdirAll(ext, 0o755))

	e, err := Find(ext)
	require.NoError(t, err)
	assert.Equal(t, Extension{Root: root, Dir: "examples/sign", Want: "v0.1"}, e)

	require.NoError(t, os.WriteFile(filepath.Join(ext, RelaxFile), []byte("we run as root on purpose\n"), 0o644))
	e, err = Find(ext)
	require.NoError(t, err)
	assert.True(t, e.Relaxed)
	assert.Equal(t, "we run as root on purpose", e.Reason)

	_, err = Find(root)
	require.ErrorContains(t, err, "the fork's root")
	_, err = Find(filepath.Join(root, "missing"))
	require.ErrorContains(t, err, "not a directory")
	_, err = Find(t.TempDir())
	require.ErrorContains(t, err, "no "+VersionFile)

	bad := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bad, VersionFile), []byte(">=0.1"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(bad, "template"), 0o755))
	_, err = Find(filepath.Join(bad, "template"))
	require.ErrorContains(t, err, "not a version prefix")
}

func TestCheck(t *testing.T) {
	t.Parallel()
	tests := []struct {
		want, version string
		ok, warn      bool
	}{
		{"v0.1", "v0.1.0", true, false},
		{"v0.1", "v0.1.12", true, false},
		{"v0.1", "v0.1.0-rc.1", true, false},
		{"v1", "v1.4.2", true, false},
		{"v0.1", "v0.10.0", false, false},
		{"v0.1", "v0.2.0", false, false},
		{"v0.1", "v0.2.0-rc.1", false, false},
		{"v0.1", "v0.0.0-20261008140503-5c8325c938cf", true, true},
		{"v0.1", "v0.1.1-0.20261008140503-5c8325c938cf", true, true},
		{"v0.1", "v0.3.0-rc.1.0.20261008140503-5c8325c938cf", true, true},
		{"v0.1", "(devel)", true, true},
		{"v0.1", "v0.2.0+dirty", true, true},
	}
	for _, tt := range tests {
		warn, err := Check(tt.want, tt.version)
		if tt.ok {
			assert.NoError(t, err, tt.version)
		} else {
			assert.ErrorIs(t, err, ErrUnsupported, tt.version)
		}
		assert.Equal(t, tt.warn, warn != "", tt.version)
	}
}
