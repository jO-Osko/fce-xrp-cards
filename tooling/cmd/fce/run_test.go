package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/flare-foundation/fce-tooling/internal/chain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLive runs a copy of the scaffold's template end to end on Coston2:
// deploy, add-version -test, governance, run -detach behind a quick tunnel,
// send, direct and stop. It sends real transactions signed with the keystore
// FCE_LIVE_KEYSTORE (password in FCE_LIVE_PASSWORD_FILE), and tee-proxy reads
// the indexer database FCE_LIVE_INDEXER_DB names; make test-live runs it.
func TestLive(t *testing.T) {
	ks := os.Getenv("FCE_LIVE_KEYSTORE")
	if ks == "" {
		t.Skip("FCE_LIVE_KEYSTORE unset; make test-live runs this")
	}
	pw, db := os.Getenv("FCE_LIVE_PASSWORD_FILE"), os.Getenv("FCE_LIVE_INDEXER_DB")
	require.NotEmpty(t, pw, "FCE_LIVE_PASSWORD_FILE")
	require.NotEmpty(t, db, "FCE_LIVE_INDEXER_DB")
	ctx := t.Context()
	src := os.Getenv("FCE_SCAFFOLD")
	if src == "" {
		src = filepath.Join("..", "..", "..", "extension")
	}
	root := copyScaffold(t, src)
	ext := filepath.Join(root, "template")
	w, err := chain.Open("", ks, pw)
	require.NoError(t, err)
	owner := w.Address
	w.Close()

	key := []string{"-chain", "coston2", "-keystore", ks, "-password-file", pw}
	fce := func(cmd string, flags []string, rest ...string) (int, string) {
		args := append(append(append([]string{cmd}, flags...), ext), rest...)
		var code int
		out := capture(t, func() { code = run(ctx, args) })
		return code, out
	}
	require.Equal(t, 0, run(ctx, append(append([]string{"deploy"}, key...), ext)))
	require.Equal(t, 0, run(ctx, append(append([]string{"add-version"}, key...), "-test", ext)))
	require.Equal(t, 0, run(ctx, append(append([]string{"governance"}, key...), "-signers", owner.Hex(), "-threshold", "1", ext)))

	t.Cleanup(func() {
		// stops a run the test left behind; nothing to do after its own stop
		_ = run(context.Background(), append(append([]string{"stop"}, key...), ext))
	})
	require.Equal(t, 0, run(ctx, append(append([]string{"run"}, key...), "-detach", "-indexer-db", db, ext)))

	code, out := fce("send", key, "myCommand(bytes)", hexutil.Encode([]byte("hello")))
	require.Equal(t, 0, code, out)
	// the template echoes abi.encode(sender, message)
	args := abi.Arguments{{Type: mustType(t, "address")}, {Type: mustType(t, "bytes")}}
	want, err := args.Pack(owner, []byte("hello"))
	require.NoError(t, err)
	assert.Contains(t, out, "data "+hexutil.Encode(want))

	code, out = fce("direct", []string{"-chain", "coston2"}, "MY_EXTENSION", "MY_DIRECT_COMMAND", hexutil.Encode([]byte(`{"message":"hello"}`)))
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "data "+hexutil.Encode([]byte(`{"message":"hello"}`)))

	code, out = fce("stop", key)
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, "paused")
}

func mustType(t *testing.T, s string) abi.Type {
	t.Helper()
	typ, err := abi.NewType(s, "", nil)
	require.NoError(t, err)
	return typ
}

// capture returns what f prints to stdout, and passes it through.
func capture(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	stdout := os.Stdout
	os.Stdout = w
	var b bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.MultiWriter(&b, stdout), r)
		close(done)
	}()
	f()
	os.Stdout = stdout
	require.NoError(t, w.Close())
	<-done
	return strings.TrimSpace(b.String())
}
