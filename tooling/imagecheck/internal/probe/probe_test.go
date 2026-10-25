package probe

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const status = `Name:	extension
Umask:	0022
State:	S (sleeping)
PPid:	1
Uid:	65532	65532	65532	65532
Gid:	65532	65532	65532	65532
Groups:
CapInh:	0000000000000000
CapEff:	00000000a80425fb
NoNewPrivs:	1
`

func TestParseStatus(t *testing.T) {
	t.Parallel()
	p, err := parseStatus([]byte(status))
	require.NoError(t, err)
	ids := []uint32{65532, 65532, 65532, 65532}
	assert.Equal(t, Proc{PPID: 1, UID: ids, GID: ids, Groups: []uint32{}, CapEff: 0xa80425fb, NoNewPrivs: 1}, p)

	_, err = parseStatus([]byte("Uid:\tx\n"))
	require.ErrorContains(t, err, "Uid")
}

// fakeProc writes a /proc entry for pid under root.
func fakeProc(t *testing.T, root string, pid, ppid int, argv string) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	st := "PPid:\t" + strconv.Itoa(ppid) + "\nUid:\t0\t0\t0\t0\nGid:\t0\t0\t0\t0\nGroups:\t\nCapEff:\t0\nNoNewPrivs:\t0\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "status"), []byte(st), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cmdline"), []byte(argv), 0o644))
}

func TestProcs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	fakeProc(t, root, 1, 0, "/app/launcher\x00--\x00/app/extension\x00")
	fakeProc(t, root, 9, 0, "/probe\x00")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "self"), 0o755))

	procs, err := Procs(root, 9)
	require.NoError(t, err)
	require.Len(t, procs, 1)
	assert.Equal(t, 1, procs[0].PID)
	assert.Equal(t, []string{"/app/launcher", "--", "/app/extension"}, procs[0].Argv)
}

func TestWaitChildren(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	fakeProc(t, root, 1, 0, "/app/launcher\x00")
	fakeProc(t, root, 7, 1, "/app/tee-node\x00")
	// forked, not yet exec'd: still the launcher's argv
	fakeProc(t, root, 8, 1, "/app/launcher\x00")

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	_, err := WaitChildren(ctx, root, 0, 2)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	fakeProc(t, root, 8, 1, "/app/extension\x00")
	procs, err := WaitChildren(t.Context(), root, 0, 2)
	require.NoError(t, err)
	assert.Len(t, procs, 3)
}

func TestDial(t *testing.T) {
	t.Parallel()
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	open := l.Addr().String()
	closed, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	shut := closed.Addr().String()
	require.NoError(t, closed.Close())
	defer l.Close() //nolint:errcheck // test listener

	assert.Equal(t, map[string]bool{open: true, shut: false}, Dial([]string{open, shut}, time.Second))
}
