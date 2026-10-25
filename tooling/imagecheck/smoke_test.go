package imagecheck

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/flare-foundation/fce-tooling/imagecheck/internal/probe"
)

func goodProcs() []probe.Proc {
	root := []uint32{0, 0, 0, 0}
	ext := []uint32{65532, 65532, 65532, 65532}
	return []probe.Proc{
		{PID: 1, Argv: []string{"/app/launcher", "--node", "/app/tee-node", "--uid", "65532", "--", "/app/extension"}, UID: root, GID: root, CapEff: 0xa80425fb, NoNewPrivs: 1},
		{PID: 7, PPID: 1, Argv: []string{"/app/tee-node"}, UID: root, GID: root, CapEff: 0xa80425fb, NoNewPrivs: 1},
		{PID: 8, PPID: 1, Argv: []string{"/app/extension"}, UID: ext, GID: ext, Groups: []uint32{}, NoNewPrivs: 1},
	}
}

func smokeTarget(t *testing.T) target {
	t.Helper()
	tg := target{entrypoint: goodProcs()[0].Argv}
	var err error
	tg.launch, err = parseLaunch(tg.entrypoint[1:])
	require.NoError(t, err)
	return tg
}

func TestProcResults(t *testing.T) {
	t.Parallel()
	for _, r := range procResults(goodProcs(), smokeTarget(t)) {
		assert.NoError(t, r.Advice, r.Name)
	}

	tests := []struct {
		name   string
		change func(p []probe.Proc) []probe.Proc
		failed int // index of the result that fails
	}{
		{"launcher not root", func(p []probe.Proc) []probe.Proc { p[0].UID = []uint32{1, 1, 1, 1}; return p }, 0},
		{"launcher not PID 1", func(p []probe.Proc) []probe.Proc { return p[1:] }, 0},
		{"another entrypoint", func(p []probe.Proc) []probe.Proc { p[0].Argv = []string{"/bin/sh"}; return p }, 0},
		{"no tee-node", func(p []probe.Proc) []probe.Proc { return append(p[:1], p[2]) }, 1},
		{"tee-node not root", func(p []probe.Proc) []probe.Proc { p[1].GID = []uint32{0, 1, 0, 0}; return p }, 1},
		{"tee-node privs", func(p []probe.Proc) []probe.Proc { p[1].NoNewPrivs = 0; return p }, 1},
		{"no extension", func(p []probe.Proc) []probe.Proc { return p[:2] }, 2},
		{"extension saved uid root", func(p []probe.Proc) []probe.Proc { p[2].UID = []uint32{65532, 65532, 0, 65532}; return p }, 2},
		{"extension groups", func(p []probe.Proc) []probe.Proc { p[2].Groups = []uint32{65532}; return p }, 2},
		{"extension capabilities", func(p []probe.Proc) []probe.Proc { p[2].CapEff = 1; return p }, 2},
		{"extension privs", func(p []probe.Proc) []probe.Proc { p[2].NoNewPrivs = 0; return p }, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := procResults(tt.change(goodProcs()), smokeTarget(t))
			for i, r := range res {
				if i == tt.failed {
					assert.Error(t, r.Advice, r.Name)
				} else {
					assert.NoError(t, r.Advice, r.Name)
				}
			}
		})
	}
}

func TestLogSection(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "--- c: no output ---\n", logSection("c", " \n"))
	assert.Equal(t, "--- c ---\nboom\n", logSection("c", "boom"))
	assert.Equal(t, "--- c ---\nboom\n", logSection("c", "boom\n"))
}
