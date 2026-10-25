package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/flare-foundation/fce-tooling/imagecheck"
	"github.com/flare-foundation/fce-tooling/internal/chain"
	"github.com/flare-foundation/fce-tooling/internal/forge"
	"github.com/flare-foundation/fce-tooling/internal/fork"
	"github.com/flare-foundation/fce-tooling/internal/record"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/extensiongovernance"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/extensionmanager"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/machinemanager"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/ownerallowlist"
)

// chainFlags pick the chain and, for commands that send transactions, the key.
type chainFlags struct {
	chain, rpc                      string
	account, keystore, passwordFile string
}

func (f *chainFlags) add(fs *flag.FlagSet, signs bool) {
	names := make([]string, 0, len(chain.Networks))
	for _, n := range chain.Networks {
		names = append(names, n.Name)
	}
	fs.StringVar(&f.chain, "chain", "", "the network, with its public RPC: "+strings.Join(names, ", "))
	fs.StringVar(&f.rpc, "rpc", "", "an RPC's URL, instead of the network's public one")
	if signs {
		fs.StringVar(&f.account, "account", "", "the Foundry keystore account to sign with, as cast wallet import named it")
		fs.StringVar(&f.keystore, "keystore", "", "a keystore file to sign with, instead of -account")
		fs.StringVar(&f.passwordFile, "password-file", "", "a file holding the keystore's password; asked for otherwise")
	}
}

// session is an on-chain command's extension, chain, record and key.
type session struct {
	ext  fork.Extension
	c    *chain.Chain
	path string // the record's
	rec  *record.Record
	w    *chain.Wallet // nil for read-only commands
}

func openSession(ctx context.Context, f chainFlags, dirs []string, signs bool) (*session, error) {
	if len(dirs) != 1 {
		return nil, errUsage
	}
	exts, err := start(dirs)
	if err != nil {
		return nil, err
	}
	s := &session{ext: exts[0]}
	if s.c, err = chain.Dial(ctx, f.chain, f.rpc); err != nil {
		return nil, err
	}
	fmt.Printf("chain %s (%s), FlareTeeManager %s\n", s.c.Name, s.c.ID, s.c.Manager)
	s.path = record.Path(s.ext.Root, s.ext.Dir, s.c.Name)
	if s.rec, err = record.Load(s.path); err != nil {
		s.close()
		return nil, err
	}
	if s.rec != nil && (s.rec.ChainID != s.c.ID.Uint64() || s.rec.FlareTeeManager != s.c.Manager) {
		s.close()
		return nil, fmt.Errorf("%s is for chain %d's FlareTeeManager %s; was FlareTeeManager redeployed? Move the record away to deploy again",
			s.relPath(), s.rec.ChainID, s.rec.FlareTeeManager)
	}
	if signs {
		if s.w, err = chain.Open(f.account, f.keystore, f.passwordFile); err != nil {
			s.close()
			return nil, err
		}
		fmt.Printf("account %s\n", s.w.Address)
	}
	return s, nil
}

func (s *session) close() {
	if s.w != nil {
		s.w.Close()
	}
	s.c.Close()
}

func (s *session) relPath() string {
	if r, err := filepath.Rel(s.ext.Root, s.path); err == nil {
		return filepath.ToSlash(r)
	}
	return s.path
}

func (s *session) save() error {
	if err := s.rec.Save(s.path); err != nil {
		return fmt.Errorf("writing %s: %w", s.relPath(), err)
	}
	return nil
}

func (s *session) contracts() string {
	return filepath.Join(s.ext.Root, filepath.FromSlash(s.ext.Dir), "contracts")
}

func (s *session) call(ctx context.Context) *bind.CallOpts { return &bind.CallOpts{Context: ctx} }

// extensionID is the recorded extension id, which every command but deploy needs.
func (s *session) extensionID() (*big.Int, error) {
	if s.rec == nil || s.rec.ExtensionID == nil {
		return nil, fmt.Errorf("%s isn't registered on %s: fce deploy first", s.ext.Dir, s.c.Name)
	}
	return new(big.Int).SetUint64(*s.rec.ExtensionID), nil
}

// owner fails unless the wallet owns the extension, as the owner's calls require.
func (s *session) owner(ctx context.Context, id *big.Int) error {
	em, err := extensionmanager.NewExtensionManagerCaller(s.c.Manager, s.c.Client)
	if err != nil {
		return err
	}
	o, err := em.GetExtensionOwner(s.call(ctx), id)
	if err != nil {
		return fmt.Errorf("reading the extension's owner: %w", err)
	}
	if o != s.w.Address {
		return fmt.Errorf("extension %s is owned by %s; sign with that account", id, o)
	}
	return nil
}

// errPrinted is a failure fce has printed already.
var errPrinted = errors.New("failed")

func stepOK(step, format string, a ...any) {
	fmt.Printf("ok    %s: %s\n", step, fmt.Sprintf(format, a...))
}

// sessionCmd parses args, opens a session and runs do; it prints do's error.
func sessionCmd(ctx context.Context, name string, args []string, signs bool, extra func(*flag.FlagSet), do func(*session) error) int {
	return argsCmd(ctx, name, args, signs, extra, false, func(s *session, _ []string) error { return do(s) })
}

// argsCmd is sessionCmd for a command that takes arguments after dir, when rest is set.
func argsCmd(ctx context.Context, name string, args []string, signs bool, extra func(*flag.FlagSet), rest bool, do func(*session, []string) error) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	var f chainFlags
	f.add(fs, signs)
	if extra != nil {
		extra(fs)
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	dirs, more := fs.Args(), []string(nil)
	if rest && len(dirs) > 1 {
		dirs, more = dirs[:1], dirs[1:]
	}
	s, err := openSession(ctx, f, dirs, signs)
	if err != nil {
		return startErr(err)
	}
	defer s.close()
	if err := do(s, more); err != nil {
		if !errors.Is(err, errPrinted) {
			fmt.Printf("FAIL  %s\n", indent(err))
		}
		return 1
	}
	return 0
}

func deployCmd(ctx context.Context, args []string) int {
	return sessionCmd(ctx, "deploy", args, true, nil, func(s *session) error { return s.deploy(ctx) })
}

// deploy runs the steps an extension needs once, each skipped when done:
// Deploy.s.sol, ExtensionManager.register, the deployer as machine owner, and PostRegister.s.sol.
func (s *session) deploy(ctx context.Context) error {
	if s.rec == nil {
		s.rec = &record.Record{ChainID: s.c.ID.Uint64(), FlareTeeManager: s.c.Manager, Deployer: s.w.Address}
	}
	if s.rec.Deployer != s.w.Address {
		return fmt.Errorf("%s deployed %s on %s; sign with that account", s.rec.Deployer, s.ext.Dir, s.c.Name)
	}
	if err := forge.Install(ctx, s.contracts()); err != nil {
		return err
	}
	if err := s.deployContracts(ctx); err != nil {
		return err
	}
	id, err := s.register(ctx)
	if err != nil {
		return err
	}
	if err := s.allowMachineOwner(ctx, id); err != nil {
		return err
	}
	if err := s.postRegister(ctx, id); err != nil {
		return err
	}
	fmt.Printf("record %s\n", s.relPath())
	return nil
}

// deployOutput is what Deploy.s.sol writes to DEPLOY_OUTPUT; other names are the extension's own.
type deployOutput struct {
	InstructionSender *common.Address `json:"instructionSender"`
	StateVerifier     *common.Address `json:"stateVerifier"`
}

func (s *session) deployContracts(ctx context.Context) error {
	if s.rec.InstructionSender != (common.Address{}) {
		stepOK("deploy", "instructionSender %s (recorded)", s.rec.InstructionSender)
		return nil
	}
	// under deployments/, where forge may write; kept until the record has its addresses
	out := filepath.Join("deployments", "fce-"+s.c.Name+".json")
	abs := filepath.Join(s.contracts(), out)
	adopted := true
	if _, err := os.Stat(abs); errors.Is(err, fs.ErrNotExist) {
		adopted = false
		env := []string{"FLARE_TEE_MANAGER=" + s.c.Manager.Hex(), "DEPLOY_OUTPUT=" + out}
		if err := forge.Script(ctx, s.contracts(), "script/Deploy.s.sol", s.c, s.w, env); err != nil {
			return err
		}
	}
	o, err := s.readDeployOutput(ctx, abs)
	if err != nil {
		return err
	}
	s.rec.InstructionSender, s.rec.StateVerifier = *o.InstructionSender, o.StateVerifier
	if err := s.save(); err != nil {
		return err
	}
	if err := os.Remove(abs); err != nil {
		return err
	}
	how := ""
	if adopted {
		how = fmt.Sprintf(" (from %s, left by a deploy that wasn't recorded)", out)
	}
	if o.StateVerifier != nil {
		stepOK("deploy", "instructionSender %s, stateVerifier %s%s", s.rec.InstructionSender, *o.StateVerifier, how)
	} else {
		stepOK("deploy", "instructionSender %s%s", s.rec.InstructionSender, how)
	}
	return nil
}

func (s *session) readDeployOutput(ctx context.Context, path string) (deployOutput, error) {
	var o deployOutput
	b, err := os.ReadFile(path)
	if err != nil {
		return o, fmt.Errorf("reading Deploy.s.sol's output: %w", err)
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return o, fmt.Errorf("reading %s: %w", path, err)
	}
	if o.InstructionSender == nil || *o.InstructionSender == (common.Address{}) {
		return o, fmt.Errorf("%s names no instructionSender", path)
	}
	if o.StateVerifier != nil && *o.StateVerifier == (common.Address{}) {
		o.StateVerifier = nil
	}
	for _, a := range []*common.Address{o.InstructionSender, o.StateVerifier} {
		if a == nil {
			continue
		}
		code, err := s.c.Client.CodeAt(ctx, *a, nil)
		if err != nil {
			return o, err
		}
		if len(code) == 0 {
			return o, fmt.Errorf("%s names %s, which has no code on %s", path, *a, s.c.Name)
		}
	}
	return o, nil
}

// register registers the contracts. The transaction is recorded before it is
// mined: the id is only in its receipt, and registering the sender again reverts.
func (s *session) register(ctx context.Context) (*big.Int, error) {
	if s.rec.ExtensionID != nil {
		id := new(big.Int).SetUint64(*s.rec.ExtensionID)
		stepOK("register", "extension %s (recorded)", id)
		return id, nil
	}
	em, err := extensionmanager.NewExtensionManager(s.c.Manager, s.c.Client)
	if err != nil {
		return nil, err
	}
	if s.rec.RegisterTx != nil {
		if _, _, err := s.c.Client.TransactionByHash(ctx, *s.rec.RegisterTx); errors.Is(err, ethereum.NotFound) {
			s.rec.RegisterTx = nil // dropped; send it again
		} else if err != nil {
			return nil, err
		}
	}
	if s.rec.RegisterTx == nil {
		opts, err := s.w.Opts(ctx, s.c)
		if err != nil {
			return nil, err
		}
		var verifier common.Address
		if s.rec.StateVerifier != nil {
			verifier = *s.rec.StateVerifier
		}
		tx, err := em.Register(opts, verifier, s.rec.InstructionSender)
		if err != nil {
			return nil, fmt.Errorf("register: %w", err)
		}
		h := tx.Hash()
		s.rec.RegisterTx = &h
		if err := s.save(); err != nil {
			return nil, err
		}
	}
	r, err := s.c.Wait(ctx, *s.rec.RegisterTx)
	if errors.Is(err, chain.ErrReverted) {
		s.rec.RegisterTx = nil // the next run sends it again
		if err := s.save(); err != nil {
			return nil, err
		}
	}
	if err != nil {
		return nil, fmt.Errorf("register: %w", err)
	}
	for _, l := range r.Logs {
		if l.Address != s.c.Manager {
			continue
		}
		if e, err := em.ParseTeeExtensionRegistered(*l); err == nil {
			if !e.ExtensionId.IsUint64() {
				return nil, fmt.Errorf("extension id %s is too large", e.ExtensionId)
			}
			id := e.ExtensionId.Uint64()
			s.rec.ExtensionID = &id
			if err := s.save(); err != nil {
				return nil, err
			}
			stepOK("register", "extension %d", id)
			return e.ExtensionId, nil
		}
	}
	return nil, fmt.Errorf("register: transaction %s has no TeeExtensionRegistered event", *s.rec.RegisterTx)
}

func (s *session) allowMachineOwner(ctx context.Context, id *big.Int) error {
	oa, err := ownerallowlist.NewOwnerAllowlist(s.c.Manager, s.c.Client)
	if err != nil {
		return err
	}
	all, err := oa.AllTeeMachineOwnersAllowed(s.call(ctx), id)
	if err != nil {
		return fmt.Errorf("reading the machine owners: %w", err)
	}
	allowed, err := oa.IsAllowedTeeMachineOwner(s.call(ctx), id, s.w.Address)
	if err != nil {
		return fmt.Errorf("reading the machine owners: %w", err)
	}
	if all || allowed {
		stepOK("machine owner", "%s (allowed already)", s.w.Address)
		return nil
	}
	opts, err := s.w.Opts(ctx, s.c)
	if err != nil {
		return err
	}
	tx, err := oa.AddAllowedTeeMachineOwners(opts, id, []common.Address{s.w.Address})
	if err != nil {
		return fmt.Errorf("allowing the machine owner: %w", err)
	}
	if _, err := s.c.Wait(ctx, tx.Hash()); err != nil {
		return fmt.Errorf("allowing the machine owner: %w", err)
	}
	stepOK("machine owner", "%s allowed", s.w.Address)
	return nil
}

// postRegister runs the extension's PostRegister.s.sol, if it has one; re-running it must change nothing.
func (s *session) postRegister(ctx context.Context, id *big.Int) error {
	const script = "script/PostRegister.s.sol"
	if _, err := os.Stat(filepath.Join(s.contracts(), filepath.FromSlash(script))); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	env := []string{"INSTRUCTION_SENDER=" + s.rec.InstructionSender.Hex(), "EXTENSION_ID=" + id.String()}
	if s.rec.StateVerifier != nil {
		env = append(env, "STATE_VERIFIER="+s.rec.StateVerifier.Hex())
	}
	if err := forge.Script(ctx, s.contracts(), script, s.c, s.w, env); err != nil {
		return err
	}
	stepOK("PostRegister.s.sol", "run")
	return nil
}

func addVersionCmd(ctx context.Context, args []string) int {
	var platforms string
	var test, relax bool
	extra := func(fs *flag.FlagSet) {
		fs.StringVar(&platforms, "platform", "", "the platforms the image may run on, comma-separated, such as GCP_AMD_SEV")
		fs.BoolVar(&test, "test", false, "register tee-node's test code hash on "+chain.TestPlatform+" instead, for a machine in test mode")
		fs.BoolVar(&relax, relaxFlag, false, relaxUsage)
	}
	return sessionCmd(ctx, "add-version", args, true, extra, func(s *session) error {
		return s.version(ctx, platforms, test, relax)
	})
}

// version builds the image and allows its code hash on the platforms, by ExtensionManager.addTeeVersion.
// testVersionWarning is printed by add-version -test: contracts can't tell a test machine from a TEE.
const testVersionWarning = "a test version binds no code: its machines run without a TEE, their operators hold their keys, and contracts accept their signatures; use it on an extension real users don't use, or disable it (fce disable-version -test) before they arrive"

func (s *session) version(ctx context.Context, platformList string, test, relax bool) error {
	id, err := s.extensionID()
	if err != nil {
		return err
	}
	if err := s.owner(ctx, id); err != nil {
		return err
	}
	var names []string
	switch {
	case test && platformList != "":
		return errors.New("-test or -platform, not both")
	case test:
		names = []string{chain.TestPlatform}
		fmt.Println("WARN  " + testVersionWarning)
	case platformList == "":
		return errors.New("-platform, or -test for a machine in test mode")
	default:
		names = strings.Split(platformList, ",")
	}
	em, err := extensionmanager.NewExtensionManager(s.c.Manager, s.c.Client)
	if err != nil {
		return err
	}
	platforms, err := s.platforms(ctx, em, names)
	if err != nil {
		return err
	}
	var commit string
	if !test {
		dirty := uncommitted(ctx, s.ext.Root)
		t := printChecks([]check{{name: "the fork has no uncommitted changes, so others can rebuild the image", advice: dirty}}, relaxed(relax, s.ext))
		if !t.ok() {
			return errPrinted
		}
		if dirty == nil {
			if commit, err = head(ctx, s.ext.Root); err != nil {
				return err
			}
		}
	}

	if err := runMake(ctx, s.ext, "image"); err != nil {
		return err
	}
	img, err := imagecheck.Inspect(ctx, "fce/"+s.ext.Dir)
	if err != nil {
		return err
	}
	if len(img.GitLabels) > 0 {
		return fmt.Errorf("the image carries the commit in %s, so the same code would hash differently at every commit: build with BUILDX_GIT_LABELS unset", strings.Join(img.GitLabels, ", "))
	}
	version, err := chain.Bytes32(img.Version)
	if err != nil {
		return fmt.Errorf("the image's version label: %w", err)
	}
	hash := common.HexToHash(strings.TrimPrefix(img.Digest, "sha256:"))
	if test {
		hash = chain.TestCodeHash
	}

	v := record.Version{Version: img.Version, CodeHash: hash, Platforms: names, Commit: commit}
	done, err := s.versionDone(ctx, em, id, v, version, platforms)
	if err != nil || done {
		return err
	}
	opts, err := s.w.Opts(ctx, s.c)
	if err != nil {
		return err
	}
	tx, err := em.AddTeeVersion(opts, id, version, hash, platforms)
	if err != nil {
		return fmt.Errorf("addTeeVersion: %w", err)
	}
	if _, err := s.c.Wait(ctx, tx.Hash()); err != nil {
		return fmt.Errorf("addTeeVersion: %w", err)
	}
	s.rec.Versions = append(s.rec.Versions, v)
	if err := s.save(); err != nil {
		return err
	}
	stepOK("version", "%s, %s on %s", v.Version, hash, strings.Join(names, ", "))
	return nil
}

// platforms converts names, refusing any the chain doesn't support.
func (s *session) platforms(ctx context.Context, em *extensionmanager.ExtensionManager, names []string) ([][32]byte, error) {
	supported, err := em.GetSystemSupportedPlatforms(s.call(ctx))
	if err != nil {
		return nil, fmt.Errorf("reading the supported platforms: %w", err)
	}
	var have []string
	for _, p := range supported {
		have = append(have, chain.String32(p))
	}
	out := make([][32]byte, 0, len(names))
	for _, n := range names {
		b, err := chain.Bytes32(n)
		if err != nil {
			return nil, fmt.Errorf("platform: %w", err)
		}
		if !slices.Contains(supported, b) {
			return nil, fmt.Errorf("%s doesn't support platform %q; it supports %s", s.c.Name, n, strings.Join(have, ", "))
		}
		if slices.Contains(out, b) {
			return nil, fmt.Errorf("platform %s named twice", n)
		}
		out = append(out, b)
	}
	return out, nil
}

// versionDone reports whether the chain allows v already; a hash allowed under
// another version or on other platforms can't be added to.
func (s *session) versionDone(ctx context.Context, em *extensionmanager.ExtensionManager, id *big.Int, v record.Version, version [32]byte, platforms [][32]byte) (bool, error) {
	hashes, err := em.GetSupportedCodeHashes(s.call(ctx), id)
	if err != nil {
		return false, fmt.Errorf("reading the extension's code hashes: %w", err)
	}
	if !slices.Contains(hashes, [32]byte(v.CodeHash)) {
		return false, nil
	}
	info, err := em.GetCodeHashInfo(s.call(ctx), id, v.CodeHash)
	if err != nil {
		return false, fmt.Errorf("reading code hash %s: %w", v.CodeHash, err)
	}
	var have []string
	for _, p := range info.Platforms {
		have = append(have, chain.String32(p))
	}
	for _, p := range platforms {
		off, err := em.IsCodeHashPlatformDisabled(s.call(ctx), id, v.CodeHash, p)
		if err != nil {
			return false, fmt.Errorf("reading code hash %s on %s: %w", v.CodeHash, chain.String32(p), err)
		}
		if off {
			return false, fmt.Errorf("code hash %s is disabled on %s, for good: a disabled code hash can't be allowed again", v.CodeHash, chain.String32(p))
		}
	}
	missing := slices.ContainsFunc(platforms, func(p [32]byte) bool { return !slices.Contains(info.Platforms, p) })
	if info.Version != version || missing {
		return false, fmt.Errorf("code hash %s is allowed already, as version %s on %s; a code hash can't be added again",
			v.CodeHash, chain.String32(info.Version), strings.Join(have, ", "))
	}
	if !slices.ContainsFunc(s.rec.Versions, func(r record.Version) bool { return r.CodeHash == v.CodeHash }) {
		s.rec.Versions = append(s.rec.Versions, record.Version{Version: v.Version, CodeHash: v.CodeHash, Platforms: have, Commit: v.Commit})
		if err := s.save(); err != nil {
			return false, err
		}
	}
	stepOK("version", "%s, %s on %s (allowed already)", v.Version, v.CodeHash, strings.Join(have, ", "))
	return true, nil
}

// head is the fork's checked-out commit.
func head(ctx context.Context, root string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// uncommitted is why others couldn't rebuild the image from the fork's commit, if they couldn't.
func uncommitted(ctx context.Context, root string) error {
	out, err := exec.CommandContext(ctx, "git", "-C", root, "status", "--porcelain", "--",
		".", ":(exclude,glob)**/"+record.Dir+"/**").CombinedOutput()
	if err != nil && strings.Contains(string(out), "not a git repository") {
		return errors.New("not a git checkout, so there is no commit to rebuild from")
	}
	if err != nil {
		return fmt.Errorf("git status: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if s := strings.TrimSpace(string(out)); s != "" {
		return fmt.Errorf("commit these first:\n%s", s)
	}
	return nil
}

func governanceCmd(ctx context.Context, args []string) int {
	var signers string
	var threshold uint64
	extra := func(fs *flag.FlagSet) {
		fs.StringVar(&signers, "signers", "", "the governance's signers, comma-separated addresses")
		fs.Uint64Var(&threshold, "threshold", 0, "how many of the signers a governance action needs")
	}
	return sessionCmd(ctx, "governance", args, true, extra, func(s *session) error {
		return s.governance(ctx, signers, threshold)
	})
}

// governance sets the extension's TEE governance, which a machine's GOVERNANCE_* settings must match.
func (s *session) governance(ctx context.Context, signerList string, threshold uint64) error {
	g, err := parseGovernance(signerList, threshold)
	if err != nil {
		return err
	}
	id, err := s.extensionID()
	if err != nil {
		return err
	}
	if err := s.owner(ctx, id); err != nil {
		return err
	}
	eg, err := extensiongovernance.NewExtensionGovernance(s.c.Manager, s.c.Client)
	if err != nil {
		return err
	}
	cur, err := latestGovernance(ctx, eg, id)
	if err != nil {
		return err
	}
	how := "set"
	if cur != nil && slices.Equal(cur.Signers, g.Signers) && cur.SignersThreshold == g.Threshold && cur.Safe == (common.Address{}) {
		how = "set already"
	} else {
		opts, err := s.w.Opts(ctx, s.c)
		if err != nil {
			return err
		}
		tx, err := eg.SetNewTeeGovernance(opts, id, g.Signers, g.Threshold)
		if err != nil {
			return fmt.Errorf("setNewTeeGovernance: %w", err)
		}
		if _, err := s.c.Wait(ctx, tx.Hash()); err != nil {
			return fmt.Errorf("setNewTeeGovernance: %w", err)
		}
	}
	s.rec.Governance = &g
	if err := s.save(); err != nil {
		return err
	}
	stepOK("governance", "%d of [%s] (%s)", g.Threshold, joinAddresses(g.Signers), how)
	return nil
}

func parseGovernance(signerList string, threshold uint64) (record.Governance, error) {
	var g record.Governance
	if signerList == "" || threshold == 0 {
		return g, errors.New("-signers and -threshold")
	}
	for a := range strings.SplitSeq(signerList, ",") {
		if !common.IsHexAddress(a) {
			return g, fmt.Errorf("signer %q is not an address", a)
		}
		addr := common.HexToAddress(a)
		if addr == (common.Address{}) || slices.Contains(g.Signers, addr) {
			return g, fmt.Errorf("signer %s is zero or named twice", addr)
		}
		g.Signers = append(g.Signers, addr)
	}
	if threshold > uint64(len(g.Signers)) {
		return g, fmt.Errorf("threshold %d is more than the %d signers", threshold, len(g.Signers))
	}
	g.Threshold = threshold
	return g, nil
}

func orNone(a common.Address) string {
	if a == (common.Address{}) {
		return "none"
	}
	return a.Hex()
}

func joinAddresses(as []common.Address) string {
	s := make([]string, 0, len(as))
	for _, a := range as {
		s = append(s, a.Hex())
	}
	return strings.Join(s, ", ")
}

func statusCmd(ctx context.Context, args []string) int {
	return sessionCmd(ctx, "status", args, false, nil, func(s *session) error { return s.status(ctx) })
}

// status compares the record with the chain.
func (s *session) status(ctx context.Context) error {
	id, err := s.extensionID()
	if err != nil {
		return err
	}
	fmt.Printf("record %s, extension %s\n", s.relPath(), id)
	em, err := extensionmanager.NewExtensionManagerCaller(s.c.Manager, s.c.Client)
	if err != nil {
		return err
	}
	call := s.call(ctx)
	var checks []check
	add := func(name string, err error) { checks = append(checks, check{name: name, err: err}) }
	same := func(what string, recorded, onChain common.Address, err error) {
		if err == nil && recorded != onChain {
			err = fmt.Errorf("recorded %s, on chain %s", orNone(recorded), orNone(onChain))
		}
		add(what+" "+orNone(recorded), err)
	}

	owner, err := em.GetExtensionOwner(call, id)
	add("owner "+owner.Hex(), err)
	sender, err := em.GetTeeExtensionInstructionsSender(call, id)
	same("instructionSender", s.rec.InstructionSender, sender, err)
	var verifier common.Address
	if s.rec.StateVerifier != nil {
		verifier = *s.rec.StateVerifier
	}
	onChain, err := em.GetTeeExtensionStateVerifier(call, id)
	same("stateVerifier", verifier, onChain, err)
	add("instructionSender bound to the extension (extensionId())", s.bound(ctx, id))

	hashes, err := em.GetSupportedCodeHashes(call, id)
	if err != nil {
		add("versions", err)
	}
	testAllowed := false
	for _, v := range s.rec.Versions {
		var err error
		var off []string
		if !slices.Contains(hashes, [32]byte(v.CodeHash)) {
			err = errors.New("recorded, not allowed on chain")
		} else if off, err = s.disabledOn(ctx, em, id, v); err == nil && !slices.Equal(off, v.Disabled) {
			err = fmt.Errorf("recorded disabled on [%s], on chain on [%s]", strings.Join(v.Disabled, ", "), strings.Join(off, ", "))
		}
		name := fmt.Sprintf("version %s, %s on %s", v.Version, v.CodeHash, strings.Join(v.Platforms, ", "))
		if len(v.Disabled) > 0 {
			name += ", disabled on " + strings.Join(v.Disabled, ", ")
		}
		if v.Commit != "" {
			name += ", built from commit " + v.Commit
		}
		if v.CodeHash == chain.TestCodeHash {
			name += " (tee-node's test hash)"
			testAllowed = err == nil && len(off) < len(v.Platforms)
		}
		add(name, err)
	}
	for _, h := range hashes {
		if !slices.ContainsFunc(s.rec.Versions, func(v record.Version) bool { return v.CodeHash == h }) {
			add(fmt.Sprintf("code hash %s", common.Hash(h)), errors.New("allowed on chain, not recorded"))
		}
	}

	eg, err := extensiongovernance.NewExtensionGovernance(s.c.Manager, s.c.Client)
	if err != nil {
		return err
	}
	g, err := latestGovernance(ctx, eg, id)
	name := "governance not set"
	if r := s.rec.Governance; r != nil {
		name = fmt.Sprintf("governance %d of [%s]", r.Threshold, joinAddresses(r.Signers))
	}
	if err == nil && !sameGovernance(s.rec.Governance, g) {
		err = errors.New("on chain: not set")
		if g != nil {
			err = fmt.Errorf("on chain %d of [%s]", g.SignersThreshold, joinAddresses(g.Signers))
		}
	}
	add(name, err)

	t := printChecks(checks, false)
	if testAllowed {
		fmt.Println("WARN  the test hash is allowed: test machines, whose operators hold their keys, can reach production and sign as this extension's TEEs")
	}
	mm, err := machinemanager.NewMachineManagerCaller(s.c.Manager, s.c.Client)
	if err != nil {
		return err
	}
	m, err := mm.GetActiveTeeMachines(call, id)
	if err != nil {
		return fmt.Errorf("reading the machines: %w", err)
	}
	fmt.Printf("machines in production: %d\n", len(m.TeeIds))
	for i, tee := range m.TeeIds {
		fmt.Printf("      %s %s\n", tee, m.Urls[i])
	}
	if !t.ok() {
		return errors.New("the record and the chain differ")
	}
	return nil
}

// disabledOn returns the platforms v's code hash is disabled on, sorted.
func (s *session) disabledOn(ctx context.Context, em *extensionmanager.ExtensionManagerCaller, id *big.Int, v record.Version) ([]string, error) {
	var off []string
	for _, n := range v.Platforms {
		p, err := chain.Bytes32(n)
		if err != nil {
			return nil, err
		}
		d, err := em.IsCodeHashPlatformDisabled(s.call(ctx), id, v.CodeHash, p)
		if err != nil {
			return nil, fmt.Errorf("reading code hash %s on %s: %w", v.CodeHash, n, err)
		}
		if d {
			off = append(off, n)
		}
	}
	slices.Sort(off)
	return off, nil
}

// governance is an extension's TEE governance as ExtensionGovernance returns it.
type governance = struct {
	Signers          []common.Address
	SignersThreshold uint64
	Safe             common.Address
}

// latestGovernance is the extension's governance; nil when none is set.
func latestGovernance(ctx context.Context, eg *extensiongovernance.ExtensionGovernance, id *big.Int) (*governance, error) {
	call := &bind.CallOpts{Context: ctx}
	h, err := eg.GetLatestTeeGovernanceHash(call, id)
	if err != nil {
		return nil, fmt.Errorf("reading the governance: %w", err)
	}
	if h == ([32]byte{}) {
		return nil, nil
	}
	g, err := eg.GetLatestTeeGovernance(call, id)
	if err != nil {
		return nil, fmt.Errorf("reading the governance: %w", err)
	}
	return &g, nil
}

func sameGovernance(r *record.Governance, g *governance) bool {
	if r == nil || g == nil {
		return r == nil && g == nil
	}
	return slices.Equal(g.Signers, r.Signers) && g.SignersThreshold == r.Threshold && g.Safe == (common.Address{})
}

// bound checks that the instruction sender's extensionId(), ExtensionBase's, is id.
func (s *session) bound(ctx context.Context, id *big.Int) error {
	out, err := s.c.Client.CallContract(ctx, ethereum.CallMsg{To: &s.rec.InstructionSender, Data: crypto.Keccak256([]byte("extensionId()"))[:4]}, nil)
	if err != nil {
		return fmt.Errorf("calling extensionId(): %w", err)
	}
	if len(out) != 32 {
		return errors.New("extensionId() returned no uint256")
	}
	if got := new(big.Int).SetBytes(out); got.Cmp(id) != 0 {
		return fmt.Errorf("it is bound to %s: run fce deploy again", got)
	}
	return nil
}
