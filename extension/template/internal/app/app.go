// Package app is the XRPL credit-card FCC extension: CARD direct actions plus
// on-chain settle instructions.
package app

import (
	"github.com/flare-foundation/fce-go-scaffold/pkg/card"
	"github.com/flare-foundation/fce-go-scaffold/pkg/extension"
	"github.com/flare-foundation/go-flare-common/pkg/tee/op"
)

const (
	OPTypeCard op.Type = card.OPType

	OPCommandRegister      op.Command = card.CmdRegister
	OPCommandCreditDeposit op.Command = card.CmdCreditDeposit
	OPCommandReserve       op.Command = card.CmdReserve
	OPCommandGetPosition   op.Command = card.CmdGetPosition
	OPCommandStats         op.Command = card.CmdStats
	OPCommandSettleDaily   op.Command = card.CmdSettleDaily
	OPCommandSettleConfirm op.Command = card.CmdSettleConfirm

	OPCommandConvertRequest       op.Command = card.CmdConvertRequest
	OPCommandCreditRLUSDRepayment op.Command = card.CmdCreditRLUSDRepayment

	OPCommandLPRegister op.Command = card.CmdLPRegister
	OPCommandLPDeposit  op.Command = card.CmdLPDeposit
	OPCommandLPRedeem   op.Command = card.CmdLPRedeem
	OPCommandGetVault   op.Command = card.CmdGetVault
)

// Register registers CARD handlers on s.
// Settle is an instruction (on-chain via InstructionSender); the rest are direct.
// GET /state stays empty (zero version, no bytes): there is no state verifier
// on chain, and a non-empty state makes toProduction revert InvalidResponseData.
func Register(s *extension.Server, cfg card.Config) *card.Ledger {
	ledger := card.NewLedger(cfg)
	st := &state{
		ledger:     ledger,
		credential: ledger.Config().CardProcessorCredential,
	}

	s.RegisterDirectHandler(OPTypeCard, OPCommandRegister, st.register)
	s.RegisterDirectHandler(OPTypeCard, OPCommandCreditDeposit, st.creditDeposit)
	s.RegisterDirectHandler(OPTypeCard, OPCommandReserve, st.reserve)
	s.RegisterDirectHandler(OPTypeCard, OPCommandGetPosition, st.getPosition)
	s.RegisterDirectHandler(OPTypeCard, OPCommandStats, st.stats)
	s.RegisterDirectHandler(OPTypeCard, OPCommandConvertRequest, st.convertRequest)
	s.RegisterDirectHandler(OPTypeCard, OPCommandCreditRLUSDRepayment, st.creditRLUSDRepayment)
	s.RegisterDirectHandler(OPTypeCard, OPCommandLPRegister, st.lpRegister)
	s.RegisterDirectHandler(OPTypeCard, OPCommandLPDeposit, st.lpDeposit)
	s.RegisterDirectHandler(OPTypeCard, OPCommandLPRedeem, st.lpRedeem)
	s.RegisterDirectHandler(OPTypeCard, OPCommandGetVault, st.getVault)
	s.RegisterInstructionHandler(OPTypeCard, OPCommandSettleDaily, st.settleDaily)
	s.RegisterInstructionHandler(OPTypeCard, OPCommandSettleConfirm, st.settleConfirm)
	return ledger
}

type state struct {
	ledger     *card.Ledger
	credential string
}
