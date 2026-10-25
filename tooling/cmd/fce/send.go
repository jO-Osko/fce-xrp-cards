package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/flare-foundation/fce-tooling/internal/chain"
	"github.com/flare-foundation/fce-tooling/internal/machine"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/instructions"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/machinemanager"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
)

func sendCmd(ctx context.Context, args []string) int {
	var value string
	var timeout time.Duration
	extra := func(fs *flag.FlagSet) {
		fs.StringVar(&value, "value", "", "the value to send, in wei; found by simulating the call otherwise")
		fs.DurationVar(&timeout, "timeout", 5*time.Minute, "how long to wait for each result")
	}
	return argsCmd(ctx, "send", args, true, extra, true, func(s *session, rest []string) error {
		if len(rest) == 0 {
			return errors.New("a function's signature and its arguments, such as 'myCommand(bytes)' 0x1234, or calldata")
		}
		return s.send(ctx, rest[0], rest[1:], value, timeout)
	})
}

func directCmd(ctx context.Context, args []string) int {
	var timeout time.Duration
	extra := func(fs *flag.FlagSet) {
		fs.DurationVar(&timeout, "timeout", 5*time.Minute, "how long to wait for the result")
	}
	return argsCmd(ctx, "direct", args, false, extra, true, func(s *session, rest []string) error {
		if len(rest) != 2 && len(rest) != 3 {
			return errors.New("an op type, an op command and, optionally, the message in hex: MY_OP MY_COMMAND 0x1234")
		}
		return s.direct(ctx, rest, timeout)
	})
}

// send calls the extension's contract, then waits for each instruction the call sent, if any, and checks its result.
func (s *session) send(ctx context.Context, sig string, args []string, value string, timeout time.Duration) error {
	if _, err := s.extensionID(); err != nil {
		return err
	}
	data, err := encodeCall(sig, args)
	if err != nil {
		return err
	}
	to := s.rec.InstructionSender
	abis := s.errorABIs()
	v := new(big.Int)
	if value != "" {
		if _, ok := v.SetString(value, 10); !ok || v.Sign() < 0 {
			return fmt.Errorf("-value %q is not an amount in wei", value)
		}
	} else if v, err = s.callValue(ctx, to, data, abis); err != nil {
		return err
	}
	opts, err := s.w.Opts(ctx, s.c)
	if err != nil {
		return err
	}
	opts.Value = v
	tx, err := bind.NewBoundContract(to, abi.ABI{}, s.c.Client, s.c.Client, s.c.Client).RawTransact(opts, data)
	if err != nil {
		return fmt.Errorf("sending: %w", chain.Explain(err, abis...))
	}
	r, err := s.c.Wait(ctx, tx.Hash())
	if err != nil {
		return err
	}
	stepOK("send", "%s, value %s wei", tx.Hash(), v)

	f, err := instructions.NewInstructionsFilterer(s.c.Manager, s.c.Client)
	if err != nil {
		return err
	}
	var sent []*instructions.InstructionsTeeInstructionsSent
	for _, l := range r.Logs {
		if l.Address != s.c.Manager {
			continue
		}
		if e, err := f.ParseTeeInstructionsSent(*l); err == nil {
			sent = append(sent, e)
		}
	}
	if len(sent) == 0 {
		fmt.Println("the call sent no instruction; no result to wait for")
		return nil
	}
	failed := false
	for _, e := range sent {
		id := common.Hash(e.InstructionId)
		fmt.Printf("instruction %s: %s %s\n", id, chain.String32(e.OpType), chain.String32(e.OpCommand))
		for _, m := range e.TeeMachines {
			p := machine.New(m.Url, 30*time.Second)
			if err := waitResult(ctx, p, id, teetypes.Threshold, timeout, s.c.ID.Uint64(), m.TeeId, m.TeeProxyId); err != nil {
				fmt.Printf("FAIL  TEE %s at %s: %s\n", m.TeeId, m.Url, indent(err))
				failed = true
			}
		}
	}
	if failed {
		return errPrinted
	}
	return nil
}

// direct posts a direct action to the extension's running machine and waits for its result.
func (s *session) direct(ctx context.Context, args []string, timeout time.Duration) error {
	st, state, err := s.stack()
	if err != nil {
		return err
	}
	if state == nil || state.URL == "" || state.TeeID == nil || state.ProxyID == nil {
		return fmt.Errorf("no machine of %s runs on %s: fce run first", s.ext.Dir, s.c.Name)
	}
	key, err := st.DirectKey()
	if err != nil {
		return err
	}
	opType, err := chain.Bytes32(args[0])
	if err != nil {
		return fmt.Errorf("op type: %w", err)
	}
	opCommand, err := chain.Bytes32(args[1])
	if err != nil {
		return fmt.Errorf("op command: %w", err)
	}
	var msg []byte
	if len(args) == 3 {
		if msg, err = hexutil.Decode(args[2]); err != nil {
			return fmt.Errorf("message %q: %w", args[2], err)
		}
	}
	p := machine.New(state.URL, 30*time.Second)
	id, err := p.Direct(ctx, key, opType, opCommand, msg)
	if err != nil {
		return err
	}
	stepOK("direct", "%s %s queued as %s at %s", args[0], args[1], id, state.URL)
	if err := waitResult(ctx, p, id, teetypes.Submit, timeout, state.ChainID, *state.TeeID, *state.ProxyID); err != nil {
		return err
	}
	return nil
}

// waitResult waits for an action's final result, showing pending and partial
// ones, checks the TEE's and the proxy's signatures, and prints it.
func waitResult(ctx context.Context, p machine.Proxy, id common.Hash, tag teetypes.SubmissionTag, timeout time.Duration, chainID uint64, tee, proxy common.Address) error {
	deadline := time.Now().Add(timeout)
	var seen uint8
	var last error
	for {
		r, err := p.Result(ctx, id, tag)
		switch {
		case err == nil:
			if err := machine.Verify(r, chainID, tee, proxy); err != nil {
				return err
			}
			if st := r.Result.Status; st >= 2 {
				if st != seen {
					seen = st
					fmt.Printf("      status %d (%s)\n", st, r.Result.Log)
				}
				break
			}
			return printResult(r, tee)
		case !errors.Is(err, machine.ErrNotFound):
			last = err
		}
		if time.Now().After(deadline) {
			if last != nil {
				return fmt.Errorf("no final result in %s: %w", timeout, last)
			}
			return fmt.Errorf("no final result in %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func printResult(r *teetypes.ActionResponse, tee common.Address) error {
	res := r.Result
	if res.Status != 1 {
		return fmt.Errorf("status %d: %s", res.Status, res.Log)
	}
	stepOK("result", "status 1 from TEE %s, signed; %s %s, version %s", tee, chain.String32(res.OPType), chain.String32(res.OPCommand), res.Version)
	fmt.Printf("      data %s\n", res.Data)
	return nil
}

// callValue finds the value a call needs: it simulates the call and reads
// ExtensionBase's InsufficientFee(price, available), raising the value until the call passes.
func (s *session) callValue(ctx context.Context, to common.Address, data []byte, abis []*abi.ABI) (*big.Int, error) {
	v := new(big.Int)
	for range 8 {
		_, err := s.c.Client.CallContract(ctx, ethereum.CallMsg{From: s.w.Address, To: &to, Value: v, Data: data}, nil)
		if err == nil {
			return v, nil
		}
		price, available, ok := insufficientFee(err)
		if !ok || price.Cmp(available) <= 0 {
			return nil, fmt.Errorf("simulating the call: %w", chain.Explain(err, abis...))
		}
		v.Add(v, new(big.Int).Sub(price, available))
	}
	return nil, errors.New("simulating the call: its fee kept rising; set -value")
}

var insufficientFeeErr = func() abi.Error {
	a, err := abi.JSON(strings.NewReader(`[{"type":"error","name":"InsufficientFee","inputs":[{"name":"price","type":"uint256"},{"name":"available","type":"uint256"}]}]`))
	if err != nil {
		panic(err)
	}
	return a.Errors["InsufficientFee"]
}()

func insufficientFee(err error) (price, available *big.Int, ok bool) {
	var de rpc.DataError
	if !errors.As(err, &de) {
		return nil, nil, false
	}
	s, _ := de.ErrorData().(string)
	data, derr := hexutil.Decode(s)
	if derr != nil || len(data) < 4 || !reflect.DeepEqual(data[:4], insufficientFeeErr.ID[:4]) {
		return nil, nil, false
	}
	args, uerr := insufficientFeeErr.Inputs.Unpack(data[4:])
	if uerr != nil {
		return nil, nil, false
	}
	price, ok1 := args[0].(*big.Int)
	available, ok2 := args[1].(*big.Int)
	return price, available, ok1 && ok2
}

// errorABIs are the custom errors a send may revert with: the extension's
// contracts' (from forge's artifacts, when built) and FlareTeeManager's.
func (s *session) errorABIs() []*abi.ABI {
	var out []*abi.ABI
	for _, md := range []interface{ GetAbi() (*abi.ABI, error) }{instructions.InstructionsMetaData, machinemanager.MachineManagerMetaData} {
		if a, err := md.GetAbi(); err == nil {
			out = append(out, a)
		}
	}
	files, _ := filepath.Glob(filepath.Join(s.contracts(), "out", "*", "*.json"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var art struct{ ABI json.RawMessage }
		if json.Unmarshal(b, &art) != nil || len(art.ABI) == 0 {
			continue
		}
		if a, err := abi.JSON(strings.NewReader(string(art.ABI))); err == nil && len(a.Errors) > 0 {
			out = append(out, &a)
		}
	}
	return out
}

// encodeCall encodes a call from a function's signature, such as
// myCommand(bytes,uint256), and its arguments; or takes calldata as it is.
func encodeCall(sig string, args []string) ([]byte, error) {
	if strings.HasPrefix(sig, "0x") {
		if len(args) > 0 {
			return nil, errors.New("calldata takes no arguments")
		}
		return hexutil.Decode(sig)
	}
	open := strings.IndexByte(sig, '(')
	if open <= 0 || !strings.HasSuffix(sig, ")") {
		return nil, fmt.Errorf("%q is not a signature such as myCommand(bytes)", sig)
	}
	name, list := sig[:open], sig[open+1:len(sig)-1]
	if strings.ContainsAny(list, "()[]") {
		return nil, fmt.Errorf("%q: tuples and arrays aren't supported; pass calldata instead", sig)
	}
	var inputs abi.Arguments
	if list != "" {
		for t := range strings.SplitSeq(list, ",") {
			typ, err := abi.NewType(strings.TrimSpace(t), "", nil)
			if err != nil {
				return nil, fmt.Errorf("%q: %w", sig, err)
			}
			inputs = append(inputs, abi.Argument{Type: typ})
		}
	}
	if len(args) != len(inputs) {
		return nil, fmt.Errorf("%s takes %d arguments, got %d", sig, len(inputs), len(args))
	}
	vals := make([]any, len(args))
	for i, a := range args {
		v, err := parseArg(inputs[i].Type, a)
		if err != nil {
			return nil, fmt.Errorf("argument %d (%s): %w", i+1, inputs[i].Type, err)
		}
		vals[i] = v
	}
	m := abi.NewMethod(name, name, abi.Function, "", false, true, inputs, nil)
	packed, err := inputs.Pack(vals...)
	if err != nil {
		return nil, err
	}
	return append(m.ID, packed...), nil
}

// parseArg converts s to what abi packs for t.
func parseArg(t abi.Type, s string) (any, error) {
	switch t.T {
	case abi.AddressTy:
		if !common.IsHexAddress(s) {
			return nil, fmt.Errorf("%q is not an address", s)
		}
		return common.HexToAddress(s), nil
	case abi.BoolTy:
		return strconv.ParseBool(s)
	case abi.StringTy:
		return s, nil
	case abi.BytesTy:
		return hexutil.Decode(s)
	case abi.FixedBytesTy:
		b, err := hexutil.Decode(s)
		if err != nil {
			return nil, err
		}
		if len(b) != t.Size {
			return nil, fmt.Errorf("%q is %d bytes, not %d", s, len(b), t.Size)
		}
		v := reflect.New(t.GetType()).Elem()
		reflect.Copy(v, reflect.ValueOf(b))
		return v.Interface(), nil
	case abi.UintTy, abi.IntTy:
		n, ok := new(big.Int).SetString(s, 0)
		if !ok {
			return nil, fmt.Errorf("%q is not a number", s)
		}
		lo, hi := new(big.Int), new(big.Int).Lsh(big.NewInt(1), uint(t.Size)) //nolint:gosec // sizes are at most 256
		if t.T == abi.IntTy {
			hi.Rsh(hi, 1)
			lo.Neg(hi)
		}
		if n.Cmp(lo) < 0 || n.Cmp(hi) >= 0 {
			return nil, fmt.Errorf("%s is out of range", s)
		}
		if t.Size > 64 {
			return n, nil
		}
		v := reflect.New(t.GetType()).Elem()
		if t.T == abi.UintTy {
			v.SetUint(n.Uint64())
		} else {
			v.SetInt(n.Int64())
		}
		return v.Interface(), nil
	default:
		return nil, fmt.Errorf("type %s isn't supported; pass calldata instead", t)
	}
}
