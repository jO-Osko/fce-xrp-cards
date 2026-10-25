package main

import (
	"context"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/flare-foundation/fce-tooling/internal/chain"
	"github.com/flare-foundation/fce-tooling/internal/record"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/extensionmanager"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseGovernance(t *testing.T) {
	t.Parallel()
	a, b := "0x00000000000000000000000000000000000000a1", "0x00000000000000000000000000000000000000B2"
	g, err := parseGovernance(a+","+b, 2)
	require.NoError(t, err)
	assert.Equal(t, record.Governance{Signers: []common.Address{common.HexToAddress(a), common.HexToAddress(b)}, Threshold: 2}, g)

	for _, c := range []struct {
		signers   string
		threshold uint64
		err       string
	}{
		{"", 1, "-signers and -threshold"},
		{a, 0, "-signers and -threshold"},
		{a, 2, "more than the 1 signers"},
		{a + "," + a, 1, "named twice"},
		{"0x0000000000000000000000000000000000000000", 1, "zero"},
		{"alice", 1, "not an address"},
		{a + ",", 1, "not an address"},
	} {
		_, err := parseGovernance(c.signers, c.threshold)
		require.ErrorContains(t, err, c.err, c)
	}
}

func TestUncommitted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	assert.ErrorContains(t, uncommitted(ctx, t.TempDir()), "not a git checkout")

	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		out, err := exec.CommandContext(ctx, "git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module m\n"), 0o644))
	assert.ErrorContains(t, uncommitted(ctx, root), "go.mod")
	git("add", ".")
	git("commit", "-q", "-m", "c")
	require.NoError(t, uncommitted(ctx, root))
	c, err := head(ctx, root)
	require.NoError(t, err)
	assert.Regexp(t, `^[0-9a-f]{40}$`, c)

	require.NoError(t, os.MkdirAll(filepath.Join(root, "template", record.Dir), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "template", record.Dir, "coston2.json"), []byte("{}"), 0o644))
	require.NoError(t, uncommitted(ctx, root), "records aside")
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module n\n"), 0o644))
	assert.ErrorContains(t, uncommitted(ctx, root), "go.mod")
}

// TestFork registers a copy of the scaffold's template on an anvil fork of the
// chain at FCE_FORK_URL, such as Coston2: deploy, add-version, governance,
// status, then each again; then deploys each example. It needs anvil, forge,
// Docker and the network; make test-fork runs it.
func TestFork(t *testing.T) {
	url := os.Getenv("FCE_FORK_URL")
	if url == "" {
		t.Skip("FCE_FORK_URL unset; make test-fork runs this")
	}
	ctx := t.Context()
	src := os.Getenv("FCE_SCAFFOLD")
	if src == "" {
		src = filepath.Join("..", "..", "..", "extension")
	}
	root := copyScaffold(t, src)
	rpc := startAnvil(t, url)

	// anvil's first dev account, funded on the fork; only this test uses it
	key, err := crypto.HexToECDSA("ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80")
	require.NoError(t, err)
	addr := crypto.PubkeyToAddress(key.PublicKey)
	dir := t.TempDir()
	j, err := keystore.EncryptKey(&keystore.Key{Address: addr, PrivateKey: key}, "pw", keystore.LightScryptN, keystore.LightScryptP)
	require.NoError(t, err)
	ks, pw := filepath.Join(dir, "dev.json"), filepath.Join(dir, "password")
	require.NoError(t, os.WriteFile(ks, j, 0o600))
	require.NoError(t, os.WriteFile(pw, []byte("pw\n"), 0o600))

	ext := filepath.Join(root, "template")
	signed := func(cmd string, args ...string) int {
		return run(ctx, append(append([]string{cmd, "-rpc", rpc, "-keystore", ks, "-password-file", pw}, args...), ext))
	}
	path := record.Path(root, "template", "anvil-coston2")

	require.Equal(t, 0, signed("deploy"))
	r, err := record.Load(path)
	require.NoError(t, err)
	require.NotNil(t, r)
	require.NotNil(t, r.ExtensionID)
	assert.Equal(t, addr, r.Deployer)
	assert.NotEqual(t, common.Address{}, r.InstructionSender)
	assert.Nil(t, r.StateVerifier)
	left, err := filepath.Glob(filepath.Join(ext, "contracts", "deployments", "fce-*.json"))
	require.NoError(t, err)
	assert.Empty(t, left, "Deploy.s.sol's output removed once recorded")

	var code int
	log := capture(t, func() { code = signed("add-version", "-test") })
	require.Equal(t, 0, code, log)
	assert.Contains(t, log, "WARN  a test version binds no code")
	require.Equal(t, 0, signed("governance", "-signers", addr.Hex(), "-threshold", "1"))
	log = capture(t, func() { code = run(ctx, []string{"status", "-rpc", rpc, ext}) })
	require.Equal(t, 0, code, log)
	assert.Contains(t, log, "(tee-node's test hash)")
	assert.Contains(t, log, "WARN  the test hash is allowed")

	// a call that sends no instruction: mined, nothing to wait for
	sent := capture(t, func() {
		code = run(ctx, []string{"send", "-rpc", rpc, "-keystore", ks, "-password-file", pw, ext, "setProjectFee(uint256)", "7"})
	})
	require.Equal(t, 0, code, sent)
	assert.Contains(t, sent, "the call sent no instruction; no result to wait for")
	cl0, err := ethclient.Dial(rpc)
	require.NoError(t, err)
	defer cl0.Close()
	fee, err := cl0.CallContract(ctx, ethereum.CallMsg{To: &r.InstructionSender, Data: crypto.Keccak256([]byte("projectFee()"))[:4]}, nil)
	require.NoError(t, err)
	assert.Equal(t, common.BigToHash(big.NewInt(7)).Bytes(), fee)
	assert.Equal(t, 1, run(ctx, []string{"send", "-rpc", rpc, "-keystore", ks, "-password-file", pw, ext, "withdraw(address)", "0x12"}), "a bad argument")

	r, err = record.Load(path)
	require.NoError(t, err)
	require.Len(t, r.Versions, 1)
	assert.Equal(t, record.Version{Version: "0.1.0", CodeHash: chain.TestCodeHash, Platforms: []string{chain.TestPlatform}}, r.Versions[0])
	assert.Equal(t, &record.Governance{Signers: []common.Address{addr}, Threshold: 1}, r.Governance)

	before, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, 0, signed("deploy"))
	require.Equal(t, 0, signed("add-version", "-test"))
	require.Equal(t, 0, signed("governance", "-signers", addr.Hex(), "-threshold", "1"))
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "nothing left to do")

	// a deploy cut short: Deploy.s.sol's output not recorded, the id only in register's receipt
	out := filepath.Join(ext, "contracts", "deployments", "fce-anvil-coston2.json")
	require.NoError(t, os.WriteFile(out, []byte(`{"instructionSender": "`+r.InstructionSender.Hex()+`"}`), 0o644))
	cut := *r
	cut.InstructionSender, cut.ExtensionID = common.Address{}, nil
	require.NoError(t, cut.Save(path))
	require.Equal(t, 0, signed("deploy"))
	after, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "finished from the output and the receipt")
	assert.NoFileExists(t, out)

	// a register that reverted is forgotten, so the next deploy sends it again
	cl, err := ethclient.Dial(rpc)
	require.NoError(t, err)
	defer cl.Close()
	opts, err := bind.NewKeyedTransactorWithChainID(key, big.NewInt(114))
	require.NoError(t, err)
	opts.GasLimit = 30_000 // too little: mined, out of gas
	em, err := extensionmanager.NewExtensionManager(r.FlareTeeManager, cl)
	require.NoError(t, err)
	tx, err := em.Register(opts, common.Address{}, r.InstructionSender)
	require.NoError(t, err)
	receipt, err := bind.WaitMined(ctx, cl, tx)
	require.NoError(t, err)
	require.Equal(t, types.ReceiptStatusFailed, receipt.Status)
	reverted := *r
	h := tx.Hash()
	reverted.ExtensionID, reverted.RegisterTx = nil, &h
	require.NoError(t, reverted.Save(path))
	require.Equal(t, 1, signed("deploy"))
	got, err := record.Load(path)
	require.NoError(t, err)
	assert.Nil(t, got.RegisterTx)
	require.NoError(t, r.Save(path))

	for _, e := range forkExtensions[1:] {
		dir := filepath.Join(root, filepath.FromSlash(e))
		require.Equal(t, 0, run(ctx, []string{"deploy", "-rpc", rpc, "-keystore", ks, "-password-file", pw, dir}), e)
		require.Equal(t, 0, run(ctx, []string{"status", "-rpc", rpc, dir}), e)
		r, err := record.Load(record.Path(root, e, "anvil-coston2"))
		require.NoError(t, err)
		if e == "examples/direct" {
			assert.Equal(t, &r.InstructionSender, r.StateVerifier, "direct's contract is its state verifier")
		} else {
			assert.Nil(t, r.StateVerifier, e)
		}
	}

	assert.Equal(t, 1, signed("add-version", "-platform", "GCP_NOWHERE"), "an unsupported platform")
	assert.Equal(t, 1, signed("governance", "-signers", addr.Hex(), "-threshold", "2"))

	// the image's own hash, from a committed fork only, unless relaxed
	require.Equal(t, 0, signed("add-version", "-platform", "GCP_AMD_SEV"))
	r, err = record.Load(path)
	require.NoError(t, err)
	require.Len(t, r.Versions, 2)
	assert.Equal(t, []string{"GCP_AMD_SEV"}, r.Versions[1].Platforms)
	assert.NotEqual(t, chain.TestCodeHash, r.Versions[1].CodeHash)
	require.NoError(t, os.WriteFile(filepath.Join(root, "notes.txt"), []byte("not in the image"), 0o644))
	assert.Equal(t, 1, signed("add-version", "-platform", "GCP_AMD_SEV"), "uncommitted changes")
	assert.Equal(t, 0, signed("add-version", "-i-know-what-im-doing", "-platform", "GCP_AMD_SEV"), "allowed already")
	assert.Equal(t, 1, signed("add-version", "-platform", "GCP_INTEL_TDX", "-i-know-what-im-doing"), "a hash can't be added to")

	// retiring a version is permanent
	image := r.Versions[1].CodeHash.Hex()
	assert.Equal(t, 1, signed("disable-version", "-hash", "0x12"), "not a code hash")
	assert.Equal(t, 1, signed("disable-version", "-hash", image, "-platform", "GCP_INTEL_TDX"), "not allowed there")
	log = capture(t, func() { code = signed("disable-version", "-hash", image) })
	require.Equal(t, 0, code, log)
	assert.Contains(t, log, "WARN  disabling is permanent")
	log = capture(t, func() { code = signed("disable-version", "-hash", image) })
	require.Equal(t, 0, code, log)
	assert.Contains(t, log, "(disabled already)")
	assert.NotContains(t, log, "WARN  disabling is permanent", "nothing sent")
	require.Equal(t, 0, signed("disable-version", "-test"))
	assert.Equal(t, 1, signed("add-version", "-test"), "a disabled hash can't be allowed again")
	r, err = record.Load(path)
	require.NoError(t, err)
	assert.Equal(t, []string{chain.TestPlatform}, r.Versions[0].Disabled)
	assert.Equal(t, []string{"GCP_AMD_SEV"}, r.Versions[1].Disabled)
	log = capture(t, func() { code = run(ctx, []string{"status", "-rpc", rpc, ext}) })
	require.Equal(t, 0, code, log)
	assert.Contains(t, log, "disabled on GCP_AMD_SEV")
	assert.NotContains(t, log, "WARN  the test hash is allowed")

	// a record that missed a disable
	r.Versions[1].Disabled = nil
	require.NoError(t, r.Save(path))
	log = capture(t, func() { code = run(ctx, []string{"status", "-rpc", rpc, ext}) })
	assert.Equal(t, 1, code, log)
	assert.Contains(t, log, "on chain on [GCP_AMD_SEV]")
}

// copyScaffold copies the scaffold's tracked files into a temporary fork, committed, and its extensions' Foundry dependencies if installed.
func copyScaffold(t *testing.T, src string) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "git", "-C", src, "ls-files", "-z").Output()
	require.NoError(t, err, "FCE_SCAFFOLD: a scaffold checkout")
	root := t.TempDir()
	for f := range strings.SplitSeq(strings.TrimRight(string(out), "\x00"), "\x00") {
		copyFile(t, filepath.Join(src, f), filepath.Join(root, f))
	}
	git := func(args ...string) {
		out, err := exec.CommandContext(t.Context(), "git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "copy")
	for _, ext := range forkExtensions {
		deps := filepath.Join(src, filepath.FromSlash(ext), "contracts", "dependencies")
		if _, err := os.Stat(deps); err == nil {
			require.NoError(t, os.CopyFS(filepath.Join(root, filepath.FromSlash(ext), "contracts", "dependencies"), os.DirFS(deps)))
		}
	}
	return root
}

// forkExtensions are the scaffold's extensions TestFork deploys.
var forkExtensions = []string{"template", "examples/direct", "examples/external-api", "examples/wallets"}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	fi, err := os.Lstat(from)
	require.NoError(t, err)
	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(from)
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(to), 0o755))
		require.NoError(t, os.Symlink(target, to))
		return
	}
	b, err := os.ReadFile(from)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(to), 0o755))
	require.NoError(t, os.WriteFile(to, b, fi.Mode().Perm()))
}

// startAnvil forks url on a free port and returns its RPC.
func startAnvil(t *testing.T, url string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port //nolint:forcetypeassert // a TCP listener
	require.NoError(t, l.Close())

	cmd := exec.CommandContext(context.Background(), "anvil", "--fork-url", url, "--port", strconv.Itoa(port), "--silent")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	rpc := "http://127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(30 * time.Second)
	for {
		if c, err := ethclient.Dial(rpc); err == nil {
			_, err = c.ChainID(t.Context())
			c.Close()
			if err == nil {
				return rpc
			}
		}
		require.True(t, time.Now().Before(deadline), "anvil didn't start")
		time.Sleep(200 * time.Millisecond)
	}
}
