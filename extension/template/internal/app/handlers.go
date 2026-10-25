package app

import (
	"context"
	"encoding/json"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/flare-foundation/fce-go-scaffold/pkg/card"
	"github.com/flare-foundation/fce-go-scaffold/pkg/extension"
	"github.com/flare-foundation/fce-go-scaffold/pkg/payload"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	"github.com/flare-foundation/tee-node/pkg/types"
)

// settleDailyArgs is abi.encode(uint8 version), as InstructionSender.settleDaily builds it.
var settleDailyArgs = abi.Arguments{
	{Name: "version", Type: abiType("uint8")},
}

// settleConfirmArgs is abi.encode(settleNonce, txHash), as InstructionSender.settleConfirm builds it.
var settleConfirmArgs = abi.Arguments{
	{Name: "settleNonce", Type: abiType("string")},
	{Name: "txHash", Type: abiType("string")},
}

func abiType(t string) abi.Type {
	typ, err := abi.NewType(t, "", nil)
	if err != nil {
		panic(err)
	}
	return typ
}

func (st *state) register(_ context.Context, d *types.DirectInstruction) ([]byte, extension.Run, error) {
	return st.runDirect(d, card.CmdRegister)
}

func (st *state) creditDeposit(_ context.Context, d *types.DirectInstruction) ([]byte, extension.Run, error) {
	return st.runDirect(d, card.CmdCreditDeposit)
}

func (st *state) reserve(_ context.Context, d *types.DirectInstruction) ([]byte, extension.Run, error) {
	return st.runDirect(d, card.CmdReserve)
}

func (st *state) getPosition(_ context.Context, d *types.DirectInstruction) ([]byte, extension.Run, error) {
	return st.runDirect(d, card.CmdGetPosition)
}

// stats is the card-operator overview: totals plus a per-user breakdown.
func (st *state) stats(_ context.Context, d *types.DirectInstruction) ([]byte, extension.Run, error) {
	return st.runDirect(d, card.CmdStats)
}

// convertRequest reserves debt for conversion to an XLS-66 loan. The TEE never
// confirms the conversion; it only sees the RLUSD repaid to the pool.
func (st *state) convertRequest(_ context.Context, d *types.DirectInstruction) ([]byte, extension.Run, error) {
	return st.runDirect(d, card.CmdConvertRequest)
}

// creditRLUSDRepayment credits RLUSD the watcher saw arrive at the pool with a user's tag.
func (st *state) creditRLUSDRepayment(_ context.Context, d *types.DirectInstruction) ([]byte, extension.Run, error) {
	return st.runDirect(d, card.CmdCreditRLUSDRepayment)
}

// lpRegister reserves an LP deposit tag for an XRPL address.
func (st *state) lpRegister(_ context.Context, d *types.DirectInstruction) ([]byte, extension.Run, error) {
	return st.runDirect(d, card.CmdLPRegister)
}

// lpDeposit credits RLUSD the watcher saw arrive at the LP deposit account and mints shares.
func (st *state) lpDeposit(_ context.Context, d *types.DirectInstruction) ([]byte, extension.Run, error) {
	return st.runDirect(d, card.CmdLPDeposit)
}

// lpRedeem burns shares the watcher saw returned and names the RLUSD payout.
func (st *state) lpRedeem(_ context.Context, d *types.DirectInstruction) ([]byte, extension.Run, error) {
	return st.runDirect(d, card.CmdLPRedeem)
}

// getVault reports the lending pool, and an LP's records when xrplAddress is given.
func (st *state) getVault(_ context.Context, d *types.DirectInstruction) ([]byte, extension.Run, error) {
	return st.runDirect(d, card.CmdGetVault)
}

// settleDaily freezes the virtual settlement pile. Auth is onlyOwner on the
// contract; the handler injects the configured processor credential for the ledger.
func (st *state) settleDaily(_ context.Context, i *instruction.DataFixed) ([]byte, extension.Run, error) {
	var version uint8
	if err := payload.DecodeABI(settleDailyArgs, i.OriginalMessage, &version); err != nil {
		return nil, nil, extension.PublicError("invalid payload", err)
	}
	return st.dispatch(card.Request{
		Command:    card.CmdSettleDaily,
		Credential: st.credential,
	})
}

// settleConfirm clears a pending settlement after the XRPL RLUSD Payment.
func (st *state) settleConfirm(_ context.Context, i *instruction.DataFixed) ([]byte, extension.Run, error) {
	var p struct {
		SettleNonce string
		TxHash      string
	}
	if err := payload.DecodeABI(settleConfirmArgs, i.OriginalMessage, &p); err != nil {
		return nil, nil, extension.PublicError("invalid payload", err)
	}
	return st.dispatch(card.Request{
		Command:     card.CmdSettleConfirm,
		Credential:  st.credential,
		SettleNonce: p.SettleNonce,
		TxHash:      p.TxHash,
	})
}

func (st *state) runDirect(d *types.DirectInstruction, cmd string) ([]byte, extension.Run, error) {
	var req card.Request
	if err := payload.DecodeJSON(d.Message, &req); err != nil {
		return nil, nil, extension.PublicError("invalid payload", err)
	}
	req.Command = cmd
	return st.dispatch(req)
}

func (st *state) dispatch(req card.Request) ([]byte, extension.Run, error) {
	out, err := st.ledger.Dispatch(req)
	if err != nil {
		return nil, nil, extension.PublicError(err.Error(), nil)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, nil, extension.PublicError("encoding the result", err)
	}
	return b, nil, nil
}
