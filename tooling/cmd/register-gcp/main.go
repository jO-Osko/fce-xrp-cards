// register-gcp takes a live GCP (or other) tee-proxy URL to production on chain:
// MachineManager.register, wait for attestation, availability check, toProduction.
// Unlike fce run it does not start a local Docker stack.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math/big"
	mrand "math/rand/v2"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/flare-foundation/fce-tooling/internal/chain"
	"github.com/flare-foundation/fce-tooling/internal/machine"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/fdc2/fdc2requestfeeconfigurations"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/machinemanager"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/operationfees"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/verification"
	"github.com/flare-foundation/go-flare-common/pkg/tee/op"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

func main() {
	os.Exit(run(context.Background()))
}

func run(ctx context.Context) int {
	var chainName, rpc, account, keystore, passwordFile, url string
	fs := flag.NewFlagSet("register-gcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&chainName, "chain", "coston2", "network name")
	fs.StringVar(&rpc, "rpc", "", "RPC URL override")
	fs.StringVar(&account, "account", "", "Foundry keystore account name")
	fs.StringVar(&keystore, "keystore", "", "keystore file instead of -account")
	fs.StringVar(&passwordFile, "password-file", "", "keystore password file")
	fs.StringVar(&url, "url", "", "public tee-proxy URL (http://host:6662)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return 2
	}
	if url == "" || (account == "" && keystore == "") {
		fmt.Fprintln(os.Stderr, "usage: register-gcp -url http://host:6662 (-account name | -keystore file) [-password-file file] [-chain coston2]")
		return 2
	}
	url = strings.TrimRight(url, "/")

	w, err := chain.Open(account, keystore, passwordFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer w.Close()

	c, err := chain.Dial(ctx, chainName, rpc)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer c.Client.Close()

	p := machine.New(url, 30*time.Second)
	info, err := p.Info(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "info:", err)
		return 1
	}
	teeID, err := machine.TeeID(info.MachineData.PublicKey)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	proxyID, err := machine.ProxyID(info)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("OK    machine TEE %s proxy %s at %s\n", teeID, proxyID, url)

	reg := &registrar{
		c: c, w: w, proxy: p, url: url,
		teeID: teeID, proxyID: proxyID,
	}
	if err := reg.init(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := reg.toProduction(ctx, info); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("OK    machine %s in production\n", teeID)
	return 0
}

type registrar struct {
	c                *chain.Chain
	w                *chain.Wallet
	proxy            machine.Proxy
	url              string
	teeID, proxyID   common.Address
	mm               *machinemanager.MachineManager
	v                *verification.Verification
	fees             *operationfees.OperationFeesCaller
	validity         time.Duration
}

func (r *registrar) init() error {
	var err error
	if r.mm, err = machinemanager.NewMachineManager(r.c.Manager, r.c.Client); err != nil {
		return err
	}
	if r.v, err = verification.NewVerification(r.c.Manager, r.c.Client); err != nil {
		return err
	}
	if r.fees, err = operationfees.NewOperationFeesCaller(r.c.Manager, r.c.Client); err != nil {
		return err
	}
	settings, err := r.v.GetSettings(&bind.CallOpts{})
	if err != nil {
		return fmt.Errorf("verification settings: %w", err)
	}
	r.validity = time.Duration(settings.ChallengeValidityDurationSeconds.Int64()) * time.Second
	return nil
}

func (r *registrar) toProduction(ctx context.Context, info *teetypes.SignedTeeInfoResponse) error {
	ch, err := r.register(ctx, info)
	if err != nil {
		return err
	}
	for try := 1; ; try++ {
		err := r.attempt(ctx, ch)
		if !errors.Is(err, errChallengeExpired) || try == 3 {
			return err
		}
		expires := ch.at.Add(r.validity)
		fmt.Printf("WARN  %v; new challenge after %s\n", err, expires.Format(time.TimeOnly))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Until(expires) + 5*time.Second):
		}
		next, err := r.requestAttestation(ctx)
		if err != nil {
			return err
		}
		if next.value == ch.value {
			return fmt.Errorf("requestTeeAttestation reused challenge %s", ch.value)
		}
		ch = next
	}
}

var errChallengeExpired = errors.New("the challenge expired")

type challenge struct {
	id, value common.Hash
	at        time.Time
}

func (r *registrar) attempt(ctx context.Context, c challenge) error {
	if err := r.waitAttestation(ctx, c.id, c.at.Add(r.validity/2)); err != nil {
		return err
	}
	proof, err := r.availability(ctx, c.id, c.at.Add(r.validity))
	if err != nil {
		return err
	}
	return r.production(ctx, proof)
}

func (r *registrar) abis() []*abi.ABI {
	var out []*abi.ABI
	for _, md := range []interface{ GetAbi() (*abi.ABI, error) }{machinemanager.MachineManagerMetaData, verification.VerificationMetaData} {
		if a, err := md.GetAbi(); err == nil {
			out = append(out, a)
		}
	}
	return out
}

func (r *registrar) fee(ctx context.Context, t op.Type, c op.Command, tees ...common.Address) (*big.Int, error) {
	f, err := r.fees.CalculateFeeByTeeIds(&bind.CallOpts{Context: ctx}, t.Hash(), c.Hash(), tees)
	if err != nil {
		return nil, fmt.Errorf("fee of %s %s: %w", t, c, err)
	}
	return f, nil
}

func (r *registrar) send(ctx context.Context, what string, value *big.Int, tx fn) (*types.Receipt, error) {
	opts, err := r.w.Opts(ctx, r.c)
	if err != nil {
		return nil, err
	}
	opts.Value = value
	t, err := tx(opts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, chain.Explain(err, r.abis()...))
	}
	receipt, err := r.c.Wait(ctx, t.Hash())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	fmt.Printf("OK    %s tx %s\n", what, t.Hash())
	return receipt, nil
}

type fn func(*bind.TransactOpts) (*types.Transaction, error)

func (r *registrar) register(ctx context.Context, info *teetypes.SignedTeeInfoResponse) (challenge, error) {
	data, sig, err := machine.Registration(info)
	if err != nil {
		return challenge{}, err
	}
	fee, err := r.fee(ctx, op.Reg, op.TEEAttestation, r.teeID)
	if err != nil {
		return challenge{}, err
	}
	receipt, err := r.send(ctx, "register", fee, func(o *bind.TransactOpts) (*types.Transaction, error) {
		return r.mm.Register(o, data, sig, r.proxyID, r.url, r.w.Address)
	})
	if err != nil {
		// Already on-chain (e.g. prior run stuck at INITIALIZED) — request a fresh attestation.
		if strings.Contains(err.Error(), "AlreadyRegistered") {
			fmt.Println("OK    register skipped (AlreadyRegistered); requesting attestation")
			return r.requestAttestation(ctx)
		}
		return challenge{}, err
	}
	return r.challenge(ctx, receipt)
}

func (r *registrar) requestAttestation(ctx context.Context) (challenge, error) {
	fee, err := r.fee(ctx, op.Reg, op.TEEAttestation, r.teeID)
	if err != nil {
		return challenge{}, err
	}
	receipt, err := r.send(ctx, "requestTeeAttestation", fee, func(o *bind.TransactOpts) (*types.Transaction, error) {
		return r.v.RequestTeeAttestation(o, r.teeID, r.w.Address)
	})
	if err != nil {
		return challenge{}, err
	}
	return r.challenge(ctx, receipt)
}

func (r *registrar) challenge(ctx context.Context, receipt *types.Receipt) (challenge, error) {
	e, at, err := r.instruction(ctx, receipt)
	if err != nil {
		return challenge{}, err
	}
	for _, l := range receipt.Logs {
		if l.Address != r.c.Manager {
			continue
		}
		if a, err := r.mm.ParseTeeAttestationRequested(*l); err == nil && a.TeeId == r.teeID {
			return challenge{id: e.InstructionId, value: a.Challenge, at: at}, nil
		}
	}
	return challenge{}, fmt.Errorf("tx %s set no challenge", receipt.TxHash)
}

func (r *registrar) instruction(ctx context.Context, receipt *types.Receipt) (*machinemanager.MachineManagerTeeInstructionsSent, time.Time, error) {
	for _, l := range receipt.Logs {
		if l.Address != r.c.Manager {
			continue
		}
		if e, err := r.mm.ParseTeeInstructionsSent(*l); err == nil {
			h, err := r.c.Client.HeaderByHash(ctx, receipt.BlockHash)
			if err != nil {
				return nil, time.Time{}, err
			}
			return e, time.Unix(int64(h.Time), 0), nil //nolint:gosec
		}
	}
	return nil, time.Time{}, fmt.Errorf("tx %s sent no instruction", receipt.TxHash)
}

func (r *registrar) waitAttestation(ctx context.Context, id common.Hash, deadline time.Time) error {
	var res *teetypes.ActionResponse
	err := poll(ctx, deadline, 2*time.Second, func() (bool, error) {
		got, err := r.proxy.Result(ctx, id, teetypes.Threshold)
		if errors.Is(err, machine.ErrNotFound) {
			return false, nil
		}
		res = got
		return err == nil, err
	})
	if errors.Is(err, errTimeout) {
		return fmt.Errorf("attestation %s not at machine by %s", id, deadline.Format(time.TimeOnly))
	}
	if err != nil {
		return err
	}
	if err := machine.Verify(res, r.c.ID.Uint64(), r.teeID, r.proxyID); err != nil {
		return err
	}
	if res.Result.Status != 1 {
		return fmt.Errorf("attestation failed: status %d, %s", res.Result.Status, res.Result.Log)
	}
	fmt.Printf("OK    attestation %s\n", id)
	return nil
}

func (r *registrar) availability(ctx context.Context, attestation common.Hash, expires time.Time) (machinemanager.ITeeAvailabilityCheckProof, error) {
	var proof machinemanager.ITeeAvailabilityCheckProof
	active, err := r.mm.GetActiveTeeMachines(&bind.CallOpts{Context: ctx}, big.NewInt(0))
	if err != nil {
		return proof, fmt.Errorf("extension 0 machines: %w", err)
	}
	if len(active.TeeIds) == 0 {
		return proof, errors.New("extension 0 has no production machine to check this one")
	}
	attester := active.TeeIds[mrand.IntN(len(active.TeeIds))] //nolint:gosec
	feesAddr, err := r.c.Lookup(ctx, "Fdc2RequestFeeConfigurations")
	if err != nil {
		return proof, err
	}
	fc, err := fdc2requestfeeconfigurations.NewFdc2RequestFeeConfigurationsCaller(feesAddr, r.c.Client)
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
	fee, err := fc.GetTypeAndSourceFee(&bind.CallOpts{Context: ctx}, typ, src)
	if err != nil {
		return proof, fmt.Errorf("availability fee: %w", err)
	}
	prove, err := r.fee(ctx, op.FDC2, op.Prove, attester)
	if err != nil {
		return proof, err
	}
	receipt, err := r.send(ctx, "requestAvailabilityCheckAttestation", new(big.Int).Add(fee, prove), func(o *bind.TransactOpts) (*types.Transaction, error) {
		return r.v.RequestAvailabilityCheckAttestation(o, r.teeID, attestation, attester, r.w.Address, r.w.Address)
	})
	if err != nil {
		if strings.Contains(err.Error(), "ChallengeExpired") {
			return proof, fmt.Errorf("%w: %v", errChallengeExpired, err)
		}
		return proof, err
	}
	e, _, err := r.instruction(ctx, receipt)
	if err != nil {
		return proof, err
	}
	if len(e.TeeMachines) != 1 {
		return proof, fmt.Errorf("availability check went to %d machines", len(e.TeeMachines))
	}
	m := e.TeeMachines[0]
	fmt.Printf("OK    availability requested %s from %s\n", common.Hash(e.InstructionId), m.Url)

	ap := machine.New(m.Url, 10*time.Second)
	var res *teetypes.ActionResponse
	err = poll(ctx, expires.Add(-30*time.Second), 2*time.Second, func() (bool, error) {
		got, err := ap.Result(ctx, e.InstructionId, teetypes.Threshold)
		if err != nil {
			return false, nil
		}
		res = got
		return true, nil
	})
	if errors.Is(err, errTimeout) {
		return proof, fmt.Errorf("%w: %s gave no proof", errChallengeExpired, m.Url)
	}
	if err != nil {
		return proof, err
	}
	if err := machine.Verify(res, r.c.ID.Uint64(), m.TeeId, m.TeeProxyId); err != nil {
		return proof, err
	}
	if res.Result.Status != 1 {
		return proof, fmt.Errorf("availability check failed: status %d, %s", res.Result.Status, res.Result.Log)
	}
	if proof, err = machine.Proof(res); err != nil {
		return proof, err
	}
	if proof.ResponseBody.Status != 0 {
		return proof, fmt.Errorf("availability check not OK (status %d)", proof.ResponseBody.Status)
	}
	fmt.Println("OK    availability proved")
	return proof, nil
}

func (r *registrar) production(ctx context.Context, proof machinemanager.ITeeAvailabilityCheckProof) error {
	_, err := r.send(ctx, "toProduction", nil, func(o *bind.TransactOpts) (*types.Transaction, error) {
		return r.mm.ToProduction(o, proof)
	})
	if err != nil {
		if strings.Contains(err.Error(), "ChallengeExpired") {
			return fmt.Errorf("%w: %v", errChallengeExpired, err)
		}
		return err
	}
	return nil
}

var errTimeout = errors.New("timed out")

func poll(ctx context.Context, deadline time.Time, interval time.Duration, f func() (bool, error)) error {
	for {
		done, err := f()
		if done || err != nil {
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
