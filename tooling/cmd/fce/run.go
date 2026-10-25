package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"math/big"
	mrand "math/rand/v2"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/flare-foundation/fce-tooling/internal/chain"
	"github.com/flare-foundation/fce-tooling/internal/machine"
	"github.com/flare-foundation/fce-tooling/internal/stack"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/fdc2/fdc2requestfeeconfigurations"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/system"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/extensiongovernance"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/extensionmanager"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/machinemanager"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/operationfees"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/ownerallowlist"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/verification"
	"github.com/flare-foundation/go-flare-common/pkg/tee/op"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

// The machine statuses MachineManager reports.
const (
	statusInitialized = 1
	statusProduction  = 2
)

type runOpts struct {
	indexerDB, url string
	port           int
	detach, relax  bool
}

func runCmd(ctx context.Context, args []string) int {
	var o runOpts
	extra := func(fs *flag.FlagSet) {
		fs.StringVar(&o.indexerDB, "indexer-db", "", "a TOML file naming the indexer database tee-proxy reads: host, port, database, username, password")
		fs.StringVar(&o.url, "url", "", "the machine's public URL, forwarding to tee-proxy's external API (see -port); a cloudflared quick tunnel otherwise")
		fs.IntVar(&o.port, "port", 0, "the port on 127.0.0.1 for tee-proxy's external API; Docker picks one otherwise")
		fs.BoolVar(&o.detach, "detach", false, "return once the machine is in production; fce stop stops it")
		fs.BoolVar(&o.relax, relaxFlag, false, relaxUsage)
	}
	return sessionCmd(ctx, "run", args, true, extra, func(s *session) error { return s.run(ctx, o) })
}

func stopCmd(ctx context.Context, args []string) int {
	return sessionCmd(ctx, "stop", args, true, nil, func(s *session) error {
		st, state, err := s.stack()
		if err != nil {
			return err
		}
		if state == nil {
			return fmt.Errorf("no run of %s on %s (%s)", s.ext.Dir, s.c.Name, st.Dir)
		}
		if state.Owner != s.w.Address {
			return fmt.Errorf("%s owns the machine; sign with that account", state.Owner)
		}
		return s.stop(ctx, st, state)
	})
}

// stack is the extension's stack on the session's chain, with its state if it has one.
func (s *session) stack() (stack.Stack, *stack.State, error) {
	base, err := stack.Base()
	if err != nil {
		return stack.Stack{}, nil, err
	}
	st := stack.For(base, s.c.Name, s.ext.Root, s.ext.Dir)
	state, err := st.LoadState()
	return st, state, err
}

// run starts a test machine for the extension and takes it to production:
// register (r), the TEE's attestation of the challenge, the availability check
// (a) and toProduction (p). Unless detached, it then shows the logs until
// interrupted; stopping pauses the machine and removes the stack.
func (s *session) run(ctx context.Context, o runOpts) (err error) {
	id, err := s.extensionID()
	if err != nil {
		return err
	}
	if o.indexerDB == "" {
		return errors.New("-indexer-db: tee-proxy reads an indexer's database, and fce runs none")
	}
	db, err := stack.ReadDB(o.indexerDB)
	if err != nil {
		return err
	}
	if o.url != "" {
		if err := checkURL(o.url); err != nil {
			return err
		}
	}
	st, state, err := s.stack()
	if err != nil {
		return err
	}
	if state != nil || st.Exists() {
		return fmt.Errorf("%s has a run on %s already, or one that wasn't stopped (%s): fce stop first", s.ext.Dir, s.c.Name, st.Dir)
	}
	g, err := s.preflight(ctx, id, o.relax)
	if err != nil {
		return err
	}
	cfg := stack.Config{
		Image: "fce/" + s.ext.Dir, ChainID: s.c.ID.Uint64(), ExtensionID: id, Owner: s.w.Address,
		Signers: g.Signers, Threshold: g.SignersThreshold, DB: db, Port: o.port, Tunnel: o.url == "",
	}
	for name, a := range map[string]*common.Address{"FlareSystemsManager": &cfg.FlareSystemsManager, "Relay": &cfg.Relay, "VoterRegistry": &cfg.VoterRegistry} {
		if *a, err = s.c.Lookup(ctx, name); err != nil {
			return err
		}
	}
	if err := runMake(ctx, s.ext, "image"); err != nil {
		return err
	}

	proxyKey, err := crypto.GenerateKey()
	if err != nil {
		return err
	}
	directKey := make([]byte, 32)
	if _, err := rand.Read(directKey); err != nil {
		return err
	}
	if err := st.Write(cfg, proxyKey, hex.EncodeToString(directKey)); err != nil {
		_ = st.Remove()
		return err
	}
	state = &stack.State{ChainID: s.c.ID.Uint64(), ExtensionID: id.Uint64(), Owner: s.w.Address}
	if err := st.SaveState(state); err != nil {
		_ = st.Remove()
		return err
	}
	defer func() {
		if err == nil && o.detach {
			return
		}
		// the cause first, then how stopping went
		switch {
		case errors.Is(err, context.Canceled) && ctx.Err() != nil:
			fmt.Println("interrupted; stopping")
			err = errPrinted
		case err != nil && !errors.Is(err, errPrinted):
			fmt.Printf("FAIL  %s\n", indent(err))
			err = errPrinted
		}
		if serr := s.stop(context.WithoutCancel(ctx), st, state); serr != nil {
			fmt.Printf("FAIL  %s\n", indent(serr))
			err = errPrinted
		}
	}()
	return s.startMachine(ctx, st, state, o, id, crypto.PubkeyToAddress(proxyKey.PublicKey))
}

// preflight checks what register and the node need, before anything starts; it returns the governance.
func (s *session) preflight(ctx context.Context, id *big.Int, relax bool) (*governance, error) {
	call := s.call(ctx)
	oa, err := ownerallowlist.NewOwnerAllowlistCaller(s.c.Manager, s.c.Client)
	if err != nil {
		return nil, err
	}
	all, err := oa.AllTeeMachineOwnersAllowed(call, id)
	if err != nil {
		return nil, fmt.Errorf("reading the machine owners: %w", err)
	}
	allowed, err := oa.IsAllowedTeeMachineOwner(call, id, s.w.Address)
	if err != nil {
		return nil, fmt.Errorf("reading the machine owners: %w", err)
	}
	if !all && !allowed {
		return nil, fmt.Errorf("%s is not an allowed machine owner of extension %s: run fce deploy with it, or sign with the deployer", s.w.Address, id)
	}
	em, err := extensionmanager.NewExtensionManagerCaller(s.c.Manager, s.c.Client)
	if err != nil {
		return nil, err
	}
	hashes, err := em.GetSupportedCodeHashes(call, id)
	if err != nil {
		return nil, fmt.Errorf("reading the extension's code hashes: %w", err)
	}
	testPlatform, err := chain.Bytes32(chain.TestPlatform)
	if err != nil {
		return nil, err
	}
	ok := slices.Contains(hashes, [32]byte(chain.TestCodeHash))
	if ok {
		info, err := em.GetCodeHashInfo(call, id, chain.TestCodeHash)
		if err != nil {
			return nil, fmt.Errorf("reading the test code hash: %w", err)
		}
		ok = slices.Contains(info.Platforms, testPlatform)
	}
	if !ok {
		return nil, fmt.Errorf("extension %s allows no test version: fce add-version -test first; the machine runs in test mode", id)
	}
	eg, err := extensiongovernance.NewExtensionGovernance(s.c.Manager, s.c.Client)
	if err != nil {
		return nil, err
	}
	g, err := latestGovernance(ctx, eg, id)
	if err != nil {
		return nil, err
	}
	if g == nil || g.Safe != (common.Address{}) {
		return nil, fmt.Errorf("extension %s has no governance of signers: fce governance first; the machine must start with it", id)
	}

	mm, err := machinemanager.NewMachineManagerCaller(s.c.Manager, s.c.Client)
	if err != nil {
		return nil, err
	}
	active, err := mm.GetActiveTeeMachines(call, id)
	if err != nil {
		return nil, fmt.Errorf("reading the machines: %w", err)
	}
	var advice error
	if len(active.TeeIds) > 0 {
		advice = fmt.Errorf("%s in production: sends can be routed to it, and a machine no longer running never answers; its owner pauses it with MachineManager.pause",
			joinAddresses(active.TeeIds))
	}
	if t := printChecks([]check{{name: "no other machine of the extension is in production", advice: advice}}, relaxed(relax, s.ext)); !t.ok() {
		return nil, errPrinted
	}
	return g, nil
}

// checkURL refuses a URL relays couldn't append "/instruction" to.
func checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("-url: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(raw, "/") {
		return fmt.Errorf("-url %q: want http(s)://host[:port][/path], with no trailing slash, credentials, query or fragment", raw)
	}
	return nil
}

func (s *session) startMachine(ctx context.Context, st stack.Stack, state *stack.State, o runOpts, id *big.Int, proxyID common.Address) error {
	if err := st.Up(ctx); err != nil {
		return err
	}
	port := o.port
	if port == 0 {
		var err error
		if port, err = st.Port(ctx); err != nil {
			return err
		}
	}
	stepOK("stack", "up; tee-proxy's external API at http://127.0.0.1:%d", port)
	local := machine.New(fmt.Sprintf("http://127.0.0.1:%d", port), 10*time.Second)
	info, err := s.waitInfo(ctx, st, local, id, proxyID)
	if err != nil {
		return err
	}
	teeID, err := machine.TeeID(info.MachineData.PublicKey)
	if err != nil {
		return err
	}
	stepOK("machine", "TEE %s, proxy %s, signing policy %d", teeID, proxyID, info.TeeInfo.LastSigningPolicyID)

	u := o.url
	if u == "" {
		if u, err = waitTunnel(ctx, st); err != nil {
			return err
		}
	}
	if err := waitPublic(ctx, st, u, teeID); err != nil {
		return err
	}
	stepOK("url", "%s serves the machine", u)
	state.URL, state.TeeID, state.ProxyID = u, &teeID, &proxyID
	if err := st.SaveState(state); err != nil {
		return err
	}

	if err := s.toProduction(ctx, st, state, local, info); err != nil {
		return err
	}
	fmt.Printf("machine %s of extension %s is in production at %s\n", teeID, id, u)
	if o.detach {
		fmt.Printf("fce stop %s pauses it and removes the stack\n", s.ext.Dir)
		return nil
	}
	fmt.Println("logs follow; Ctrl-C pauses the machine and removes the stack")
	if err := st.Logs(ctx, os.Stdout, true, 0, "node", "proxy"); err != nil {
		return err
	}
	if ctx.Err() != nil {
		fmt.Println()
		return nil
	}
	return alive(ctx, st)
}

// alive fails if a service of the stack stopped, showing its last logs.
func alive(ctx context.Context, st stack.Stack) error {
	exited, err := st.Exited(ctx)
	if err != nil || len(exited) == 0 {
		return err
	}
	var names []string
	for svc, code := range exited {
		names = append(names, fmt.Sprintf("%s (exit %d)", svc, code))
		fmt.Printf("---- %s's last logs\n", svc)
		_ = st.Logs(ctx, os.Stdout, false, 30, svc)
	}
	slices.Sort(names)
	return fmt.Errorf("stopped: %s", strings.Join(names, ", "))
}

// poll calls f every interval until it reports done, fails, or the deadline passes.
func poll(ctx context.Context, st stack.Stack, deadline time.Time, interval time.Duration, f func() (bool, error)) error {
	for {
		done, err := f()
		if done || err != nil {
			return err
		}
		if err := alive(ctx, st); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return errTimeout
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

var errTimeout = errors.New("timed out")

// waitInfo waits until the machine has the chain's current signing policy, then
// checks what it will register with.
func (s *session) waitInfo(ctx context.Context, st stack.Stack, local machine.Proxy, id *big.Int, proxyID common.Address) (*teetypes.SignedTeeInfoResponse, error) {
	fsmAddr, err := s.c.Lookup(ctx, "FlareSystemsManager")
	if err != nil {
		return nil, err
	}
	fsm, err := system.NewFlareSystemsManagerCaller(fsmAddr, s.c.Client)
	if err != nil {
		return nil, err
	}
	var info *teetypes.SignedTeeInfoResponse
	var epoch *big.Int
	// tee-proxy gives up on the node after 5 minutes; it may wait longer for the indexer
	err = poll(ctx, st, time.Now().Add(10*time.Minute), 2*time.Second, func() (bool, error) {
		i, err := local.Info(ctx)
		if err != nil {
			return false, nil
		}
		if epoch, err = fsm.GetCurrentRewardEpochId(s.call(ctx)); err != nil {
			return false, fmt.Errorf("reading the reward epoch: %w", err)
		}
		info = i
		return i.TeeInfo.LastSigningPolicyID >= uint32(epoch.Uint64()), nil
	})
	if errors.Is(err, errTimeout) {
		if info == nil {
			return nil, errors.New("tee-proxy served no TEE info in 10 minutes; its logs: docker logs " + st.Project + "-proxy-1")
		}
		return nil, fmt.Errorf("the machine has signing policy %d, the chain is at reward epoch %s: is the indexer current?", info.TeeInfo.LastSigningPolicyID, epoch)
	}
	if err != nil {
		return nil, err
	}

	eg, err := extensiongovernance.NewExtensionGovernanceCaller(s.c.Manager, s.c.Client)
	if err != nil {
		return nil, err
	}
	gh, err := eg.GetLatestTeeGovernanceHash(s.call(ctx), id)
	if err != nil {
		return nil, fmt.Errorf("reading the governance: %w", err)
	}
	platform, err := chain.Bytes32(chain.TestPlatform)
	if err != nil {
		return nil, err
	}
	got, err := machine.ProxyID(info)
	if err != nil {
		return nil, err
	}
	m := info.MachineData
	for _, c := range []struct {
		what      string
		got, want any
	}{
		{"chain id", info.TeeInfo.ChainID, s.c.ID.Uint64()},
		{"proxy", got, proxyID},
		{"extension id", m.ExtensionID, common.BigToHash(id)},
		{"initial owner", m.InitialOwner, s.w.Address},
		{"code hash", m.CodeHash, chain.TestCodeHash},
		{"platform", m.Platform, common.Hash(platform)},
		{"governance hash", m.GovernanceHash, common.Hash(gh)},
	} {
		if c.got != c.want {
			return nil, fmt.Errorf("the machine reports %s %v, want %v", c.what, c.got, c.want)
		}
	}
	return info, nil
}

func waitTunnel(ctx context.Context, st stack.Stack) (string, error) {
	var u string
	err := poll(ctx, st, time.Now().Add(time.Minute), time.Second, func() (bool, error) {
		var err error
		u, err = st.TunnelURL(ctx)
		return err == nil, nil
	})
	if errors.Is(err, errTimeout) {
		return "", errors.New("cloudflared logged no quick tunnel URL in a minute")
	}
	return u, err
}

// waitPublic waits until url serves the machine, as relays and verifiers will reach it.
func waitPublic(ctx context.Context, st stack.Stack, u string, teeID common.Address) error {
	p := machine.New(u, 10*time.Second)
	var last error
	err := poll(ctx, st, time.Now().Add(3*time.Minute), 3*time.Second, func() (bool, error) {
		info, err := p.Info(ctx)
		if err != nil {
			last = err
			return false, nil
		}
		got, err := machine.TeeID(info.MachineData.PublicKey)
		if err != nil {
			return false, err
		}
		if got != teeID {
			return false, fmt.Errorf("%s serves TEE %s, not this machine's %s", u, got, teeID)
		}
		return true, nil
	})
	if errors.Is(err, errTimeout) {
		return fmt.Errorf("%s didn't serve the machine in 3 minutes: %v", u, last)
	}
	return err
}

// production is a registration in progress.
type production struct {
	s     *session
	st    stack.Stack
	state *stack.State
	local machine.Proxy
	mm    *machinemanager.MachineManager
	v     *verification.Verification
	fees  *operationfees.OperationFeesCaller
	// validity is how long a challenge lasts
	validity time.Duration
}

// toProduction registers the machine and takes it to production, asking for a
// new challenge when one expires on the way.
func (s *session) toProduction(ctx context.Context, st stack.Stack, state *stack.State, local machine.Proxy, info *teetypes.SignedTeeInfoResponse) error {
	p := &production{s: s, st: st, state: state, local: local}
	var err error
	if p.mm, err = machinemanager.NewMachineManager(s.c.Manager, s.c.Client); err != nil {
		return err
	}
	if p.v, err = verification.NewVerification(s.c.Manager, s.c.Client); err != nil {
		return err
	}
	if p.fees, err = operationfees.NewOperationFeesCaller(s.c.Manager, s.c.Client); err != nil {
		return err
	}
	settings, err := p.v.GetSettings(s.call(ctx))
	if err != nil {
		return fmt.Errorf("reading the verification settings: %w", err)
	}
	p.validity = time.Duration(settings.ChallengeValidityDurationSeconds.Int64()) * time.Second

	c, err := p.register(ctx, info)
	if err != nil {
		return err
	}
	for try := 1; ; try++ {
		err := p.attempt(ctx, c)
		if !errors.Is(err, errChallengeExpired) || try == 3 {
			return err
		}
		// requestTeeAttestation reuses a challenge until it expires
		expires := c.at.Add(p.validity)
		fmt.Printf("WARN  %v; asking for a new challenge once it expires, at %s\n", err, expires.Format(time.TimeOnly))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Until(expires) + 5*time.Second):
		}
		next, err := p.requestAttestation(ctx)
		if err != nil {
			return err
		}
		if next.value == c.value {
			return fmt.Errorf("requestTeeAttestation reused challenge %s", c.value)
		}
		c = next
	}
}

var errChallengeExpired = errors.New("the challenge expired")

// challenge is a challenge the machine is to attest: its instruction, its value and when the chain set it.
type challenge struct {
	id, value common.Hash
	at        time.Time
}

// attempt takes the machine from an attestation request to production, within the challenge's validity.
func (p *production) attempt(ctx context.Context, c challenge) error {
	if err := p.waitAttestation(ctx, c.id, c.at.Add(p.validity/2)); err != nil {
		return err
	}
	proof, err := p.availability(ctx, c.id, c.at.Add(p.validity))
	if err != nil {
		return err
	}
	return p.production(ctx, proof)
}

func (p *production) abis() []*abi.ABI {
	var out []*abi.ABI
	for _, md := range []interface{ GetAbi() (*abi.ABI, error) }{machinemanager.MachineManagerMetaData, verification.VerificationMetaData} {
		if a, err := md.GetAbi(); err == nil {
			out = append(out, a)
		}
	}
	return out
}

func (p *production) fee(ctx context.Context, t op.Type, c op.Command, tees ...common.Address) (*big.Int, error) {
	f, err := p.fees.CalculateFeeByTeeIds(p.s.call(ctx), t.Hash(), c.Hash(), tees)
	if err != nil {
		return nil, fmt.Errorf("reading the fee of %s %s: %w", t, c, err)
	}
	return f, nil
}

// challenge is the challenge r set, with its attestation instruction.
func (p *production) challenge(ctx context.Context, r *types.Receipt) (challenge, error) {
	e, at, err := p.instruction(ctx, r)
	if err != nil {
		return challenge{}, err
	}
	for _, l := range r.Logs {
		if l.Address != p.s.c.Manager {
			continue
		}
		if a, err := p.mm.ParseTeeAttestationRequested(*l); err == nil && a.TeeId == *p.state.TeeID {
			return challenge{id: e.InstructionId, value: a.Challenge, at: at}, nil
		}
	}
	return challenge{}, fmt.Errorf("transaction %s set no challenge", r.TxHash)
}

// instruction is the TeeInstructionsSent in r, and the block's time.
func (p *production) instruction(ctx context.Context, r *types.Receipt) (*machinemanager.MachineManagerTeeInstructionsSent, time.Time, error) {
	for _, l := range r.Logs {
		if l.Address != p.s.c.Manager {
			continue
		}
		if e, err := p.mm.ParseTeeInstructionsSent(*l); err == nil {
			h, err := p.s.c.Client.HeaderByHash(ctx, r.BlockHash)
			if err != nil {
				return nil, time.Time{}, err
			}
			return e, time.Unix(int64(h.Time), 0), nil //nolint:gosec // block times fit int64
		}
	}
	return nil, time.Time{}, fmt.Errorf("transaction %s sent no instruction", r.TxHash)
}

// send sends a transaction from the owner, keeping its hash in *keep, if set, before waiting for it.
func (p *production) send(ctx context.Context, what string, value *big.Int, keep **common.Hash, tx func(*bind.TransactOpts) (*types.Transaction, error)) (*types.Receipt, error) {
	opts, err := p.s.w.Opts(ctx, p.s.c)
	if err != nil {
		return nil, err
	}
	opts.Value = value
	t, err := tx(opts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, chain.Explain(err, p.abis()...))
	}
	if keep != nil {
		h := t.Hash()
		*keep = &h
		if err := p.st.SaveState(p.state); err != nil {
			return nil, err
		}
	}
	r, err := p.s.c.Wait(ctx, t.Hash())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return r, nil
}

// register is r: MachineManager.register, which also sets a challenge for the machine to attest.
func (p *production) register(ctx context.Context, info *teetypes.SignedTeeInfoResponse) (challenge, error) {
	data, sig, err := machine.Registration(info)
	if err != nil {
		return challenge{}, err
	}
	fee, err := p.fee(ctx, op.Reg, op.TEEAttestation, *p.state.TeeID)
	if err != nil {
		return challenge{}, err
	}
	r, err := p.send(ctx, "register", fee, &p.state.RegisterTx, func(o *bind.TransactOpts) (*types.Transaction, error) {
		return p.mm.Register(o, data, sig, *p.state.ProxyID, p.state.URL, p.s.w.Address)
	})
	if err != nil {
		return challenge{}, err
	}
	c, err := p.challenge(ctx, r)
	if err != nil {
		return challenge{}, err
	}
	stepOK("register", "machine %s registered; attestation %s", *p.state.TeeID, c.id)
	return c, nil
}

// requestAttestation is R: a new challenge for the machine to attest.
func (p *production) requestAttestation(ctx context.Context) (challenge, error) {
	fee, err := p.fee(ctx, op.Reg, op.TEEAttestation, *p.state.TeeID)
	if err != nil {
		return challenge{}, err
	}
	r, err := p.send(ctx, "requestTeeAttestation", fee, nil, func(o *bind.TransactOpts) (*types.Transaction, error) {
		return p.v.RequestTeeAttestation(o, *p.state.TeeID, p.s.w.Address)
	})
	if err != nil {
		return challenge{}, err
	}
	c, err := p.challenge(ctx, r)
	if err != nil {
		return challenge{}, err
	}
	stepOK("attestation", "requested %s", c.id)
	return c, nil
}

// waitAttestation waits until the machine's own proxy serves its attestation, which verifiers fetch.
func (p *production) waitAttestation(ctx context.Context, id common.Hash, deadline time.Time) error {
	var res *teetypes.ActionResponse
	err := poll(ctx, p.st, deadline, 2*time.Second, func() (bool, error) {
		r, err := p.local.Result(ctx, id, teetypes.Threshold)
		if errors.Is(err, machine.ErrNotFound) {
			return false, nil
		}
		res = r
		return err == nil, err
	})
	if errors.Is(err, errTimeout) {
		return fmt.Errorf("the data providers' attestation instruction %s didn't reach the machine by %s; is its URL reachable from the internet?", id, deadline.Format(time.TimeOnly))
	}
	if err != nil {
		return err
	}
	if err := machine.Verify(res, p.s.c.ID.Uint64(), *p.state.TeeID, *p.state.ProxyID); err != nil {
		return err
	}
	if res.Result.Status != 1 {
		return fmt.Errorf("the machine failed its attestation: status %d, %s", res.Result.Status, res.Result.Log)
	}
	stepOK("attestation", "the machine attested %s", id)
	return nil
}

// availability is a: an extension-0 machine checks this one's attestation; its proof follows.
func (p *production) availability(ctx context.Context, attestation common.Hash, expires time.Time) (machinemanager.ITeeAvailabilityCheckProof, error) {
	var proof machinemanager.ITeeAvailabilityCheckProof
	active, err := p.mm.GetActiveTeeMachines(p.s.call(ctx), big.NewInt(0))
	if err != nil {
		return proof, fmt.Errorf("reading extension 0's machines: %w", err)
	}
	if len(active.TeeIds) == 0 {
		return proof, errors.New("extension 0 has no machine in production to check this one")
	}
	attester := active.TeeIds[mrand.IntN(len(active.TeeIds))] //nolint:gosec // spreading load, not security
	feesAddr, err := p.s.c.Lookup(ctx, "Fdc2RequestFeeConfigurations")
	if err != nil {
		return proof, err
	}
	fc, err := fdc2requestfeeconfigurations.NewFdc2RequestFeeConfigurationsCaller(feesAddr, p.s.c.Client)
	if err != nil {
		return proof, err
	}
	typ, err := chain.Bytes32("TeeAvailabilityCheck")
	if err != nil {
		return proof, err
	}
	src, err := chain.Bytes32("TEE")
	if err != nil {
		return proof, err
	}
	fee, err := fc.GetTypeAndSourceFee(p.s.call(ctx), typ, src)
	if err != nil {
		return proof, fmt.Errorf("reading the availability check's fee: %w", err)
	}
	prove, err := p.fee(ctx, op.FDC2, op.Prove, attester)
	if err != nil {
		return proof, err
	}
	r, err := p.send(ctx, "requestAvailabilityCheckAttestation", new(big.Int).Add(fee, prove), nil, func(o *bind.TransactOpts) (*types.Transaction, error) {
		return p.v.RequestAvailabilityCheckAttestation(o, *p.state.TeeID, attestation, attester, p.s.w.Address, p.s.w.Address)
	})
	if err != nil {
		if strings.Contains(err.Error(), "ChallengeExpired") {
			return proof, fmt.Errorf("%w: %v", errChallengeExpired, err)
		}
		return proof, err
	}
	e, _, err := p.instruction(ctx, r)
	if err != nil {
		return proof, err
	}
	if len(e.TeeMachines) != 1 {
		return proof, fmt.Errorf("the availability check went to %d machines", len(e.TeeMachines))
	}
	m := e.TeeMachines[0]
	stepOK("availability", "requested %s from %s", common.Hash(e.InstructionId), m.Url)

	ap := machine.New(m.Url, 10*time.Second)
	var res *teetypes.ActionResponse
	err = poll(ctx, p.st, expires.Add(-30*time.Second), 2*time.Second, func() (bool, error) {
		r, err := ap.Result(ctx, e.InstructionId, teetypes.Threshold)
		if err != nil {
			// a 404 until it is ready; anything else may pass
			return false, nil
		}
		res = r
		return true, nil
	})
	if errors.Is(err, errTimeout) {
		return proof, fmt.Errorf("%w: %s gave no proof", errChallengeExpired, m.Url)
	}
	if err != nil {
		return proof, err
	}
	if err := machine.Verify(res, p.s.c.ID.Uint64(), m.TeeId, m.TeeProxyId); err != nil {
		return proof, err
	}
	if res.Result.Status != 1 {
		return proof, fmt.Errorf("the availability check failed: status %d, %s", res.Result.Status, res.Result.Log)
	}
	if proof, err = machine.Proof(res); err != nil {
		return proof, err
	}
	if proof.ResponseBody.Status != 0 {
		return proof, fmt.Errorf("the availability check found the machine not OK (status %d)", proof.ResponseBody.Status)
	}
	stepOK("availability", "proved")
	return proof, nil
}

// production is p: MachineManager.toProduction with the availability proof.
func (p *production) production(ctx context.Context, proof machinemanager.ITeeAvailabilityCheckProof) error {
	if _, err := p.send(ctx, "toProduction", nil, &p.state.ProductionTx, func(o *bind.TransactOpts) (*types.Transaction, error) {
		return p.mm.ToProduction(o, proof)
	}); err != nil {
		if strings.Contains(err.Error(), "ChallengeExpired") {
			return fmt.Errorf("%w: %v", errChallengeExpired, err)
		}
		return err
	}
	stepOK("production", "machine %s", *p.state.TeeID)
	return nil
}

// stop pauses the run's machine if it reached production, then removes the
// stack; when pausing fails, the stack's directory stays so fce stop can retry.
func (s *session) stop(ctx context.Context, st stack.Stack, state *stack.State) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	perr := s.pause(ctx, state)
	if err := st.Down(ctx); err != nil {
		return err
	}
	if perr != nil {
		return fmt.Errorf("%w\nthe stack is down; fce stop %s tries pausing again", perr, s.ext.Dir)
	}
	if err := st.Remove(); err != nil {
		return err
	}
	stepOK("stop", "stack removed")
	return nil
}

func (s *session) pause(ctx context.Context, state *stack.State) error {
	if state.TeeID == nil || state.RegisterTx == nil {
		return nil
	}
	if ok, err := s.mined(ctx, *state.RegisterTx); err != nil {
		return fmt.Errorf("waiting for register: %w", err)
	} else if !ok {
		return nil // never registered
	}
	if state.ProductionTx != nil {
		// a toProduction still pending would undo the pause
		if _, err := s.mined(ctx, *state.ProductionTx); err != nil {
			return fmt.Errorf("waiting for toProduction: %w", err)
		}
	}
	mm, err := machinemanager.NewMachineManager(s.c.Manager, s.c.Client)
	if err != nil {
		return err
	}
	status, err := mm.GetTeeMachineStatus(s.call(ctx), *state.TeeID)
	if err != nil {
		return fmt.Errorf("reading machine %s's status: %w", *state.TeeID, err)
	}
	if status != statusProduction {
		if status == statusInitialized {
			stepOK("pause", "machine %s never reached production; no instruction goes to it", *state.TeeID)
		}
		return nil
	}
	opts, err := s.w.Opts(ctx, s.c)
	if err != nil {
		return err
	}
	tx, err := mm.Pause(opts, *state.TeeID)
	if err != nil {
		return fmt.Errorf("pausing machine %s: %w", *state.TeeID, err)
	}
	if _, err := s.c.Wait(ctx, tx.Hash()); err != nil {
		return fmt.Errorf("pausing machine %s: %w", *state.TeeID, err)
	}
	stepOK("pause", "machine %s paused", *state.TeeID)
	return nil
}

// mined waits for a transaction the chain knows and reports whether it succeeded; one it doesn't know was dropped.
func (s *session) mined(ctx context.Context, h common.Hash) (bool, error) {
	if _, _, err := s.c.Client.TransactionByHash(ctx, h); errors.Is(err, ethereum.NotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	_, err := s.c.Wait(ctx, h)
	if errors.Is(err, chain.ErrReverted) {
		return false, nil
	}
	return err == nil, err
}
