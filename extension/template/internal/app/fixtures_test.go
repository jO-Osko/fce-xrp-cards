package app

import (
	"encoding/json"
	"flag"
	"testing"

	"github.com/flare-foundation/fce-go-scaffold/conformance"
	"github.com/flare-foundation/fce-go-scaffold/internal/golden"
	"github.com/flare-foundation/fce-go-scaffold/pkg/card"
	"github.com/flare-foundation/tee-node/pkg/types"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite the conformance fixtures")

const fixturesDir = "../../conformance"

const (
	testAddr        = "rUserConformanceAAAAAAAAAAAAAAA"
	testDepositAcct = card.DefaultDepositAccount
	testSettleAcct  = card.DefaultSettlementAddress
	testCred        = "swell-demo-card-credential"
	testLPAddr      = "rLPConformanceAAAAAAAAAAAAAAAAA"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// fixtures are golden cases `fce conformance` checks against one long-lived process
// (name order). Later fixtures may rely on earlier ledger state.
func fixtures(t *testing.T) []conformance.Fixture {
	t.Helper()

	regMsg := mustJSON(t, card.Request{XRPLAddress: testAddr})
	regOut := mustJSON(t, card.RegisterResponse{
		Tag: 1001, XRPLAddress: testAddr, DepositAccount: testDepositAcct,
	})

	creditMsg := mustJSON(t, card.Request{Tag: 1001, Drops: "100000000", TxHash: "TXHASH1"})
	creditOut := mustJSON(t, card.CreditDepositResponse{
		Tag: 1001, CreditedDrops: "100000000", XrpAvailableDrops: "100000000", TxHash: "TXHASH1",
	})
	creditDupOut := mustJSON(t, card.CreditDepositResponse{
		Tag: 1001, CreditedDrops: "0", XrpAvailableDrops: "100000000", TxHash: "TXHASH1", Duplicate: true,
	})

	posEmptyMsg := mustJSON(t, card.Request{Tag: 1001})
	// After register only (before deposits in fixture order we call get-position-empty
	// right after register-idempotent — deposits empty).
	posEmptyOut := mustJSON(t, card.PositionResponse{
		Tag:                    1001,
		XRPLAddress:            testAddr,
		XrpAvailableDrops:      "0",
		XrpLockedDrops:         "0",
		RlusdDebt:              "0",
		VirtualSettlementRlusd: "0",
		XrpUsdPrice:            "2.5", // formatFixed trims trailing zeros
		LtvCapBps:              card.LTVCapBps,
		DepositAccount:         testDepositAcct,
		SettlementAddress:      testSettleAcct,
		Deposits:               []card.DepositRecord{},
		Payments:               []card.CardPaymentRecord{},
		ConvertibleRlusd:       "0",
		PendingConversions:     []card.PendingConversionRecord{},
		Repayments:             []card.Repayment{},
		PoolAccount:            card.DefaultPoolAccount,
		BorrowAPRBps:           card.BorrowAPRBps,
	})

	reserveBadMsg := mustJSON(t, card.Request{Credential: "wrong", Tag: 1001, RlusdAmount: "25.00"})
	reserveMsg := mustJSON(t, card.Request{
		Credential: testCred, Tag: 1001, RlusdAmount: "25.00", Label: "Lunch",
	})
	// 25 RLUSD at $2.50 and 75% LTV → lock 13333334 drops; available left 86666666.
	reserveOut := mustJSON(t, card.ReserveResponse{
		Approved:          true,
		RlusdAmount:       "25",
		XrpLockedDrops:    "13333334",
		XrpAvailableLeft:  "86666666",
		RlusdDebt:         "25",
		VirtualSettlement: "24.75", // the merchant is owed 25 less the 1% card fee
	})
	// After the required reserve send, conformance also re-sends with unknown
	// action fields (recommendation): that second reserve doubles the spend to 50:
	// 49.5 to the merchant, 0.5 in card fees to the TEE treasury.
	settleMsg, err := settleDailyArgs.Pack(uint8(0))
	require.NoError(t, err)
	settleOut := mustJSON(t, card.SettleDailyResponse{
		SettleNonce:       "0000000000000001",
		RlusdAmount:       "49.5",
		SettlementAddress: testSettleAcct,
		RLUSDCurrency:     card.RLUSDCurrencyHex,
		RLUSDIssuer:       card.RLUSDIssuer,
		TreasuryRlusd:     "0.5",
		TreasuryAddress:   card.DefaultTreasuryAccount,
	})

	confirmMsg, err := settleConfirmArgs.Pack("0000000000000001", "SETTLETX1")
	require.NoError(t, err)
	confirmOut := mustJSON(t, card.SettleConfirmResponse{
		RlusdAmount: "49.5", TreasuryRlusd: "0.5", TxHash: "SETTLETX1", WireQueued: true,
	})
	confirmAgain := conformance.Expected{Status: 0, Log: "error: no pending settlement"}

	// Debt is 50 (the reserve ran twice) with 26666668 drops locked.
	convertMsg := mustJSON(t, card.Request{Tag: 1001, RlusdAmount: "20"})
	convertOut := mustJSON(t, card.ConvertResponse{
		ConversionID:        "0000000000000001",
		Tag:                 1001,
		XRPLAddress:         testAddr,
		RlusdAmount:         "20",
		ConvertibleRlusd:    "30",
		TTLSeconds:          int64(card.ConversionTTL.Seconds()),
		PoolAccount:         card.DefaultPoolAccount,
		DestinationTag:      1001,
		CreditServerAccount: card.DefaultCreditServerAccount,
		LoanBrokerID:        card.DefaultLoanBrokerID,
		RLUSDCurrency:       card.RLUSDCurrencyHex,
		RLUSDIssuer:         card.RLUSDIssuer,
		Terms:               card.DefaultLoanTerms,
	})
	// 20 of 50 repaid unlocks 20/50 of the locked drops, rounded down.
	repayMsg := mustJSON(t, card.Request{Tag: 1001, RlusdAmount: "20", TxHash: "REPAYTX1"})
	repayOut := mustJSON(t, card.RepaymentResponse{
		Tag: 1001, TxHash: "REPAYTX1", RlusdCredited: "20", XrpUnlockedDrops: "10666667",
		RlusdDebt: "30", XrpAvailableDrops: "83999999", XrpLockedDrops: "16000001",
	})
	repayDupOut := mustJSON(t, card.RepaymentResponse{
		Tag: 1001, TxHash: "REPAYTX1", RlusdCredited: "0", XrpUnlockedDrops: "0",
		RlusdDebt: "30", XrpAvailableDrops: "83999999", XrpLockedDrops: "16000001", Duplicate: true,
	})

	// The pool needs liquidity before any card payment: an LP deposits 1000 RLUSD.
	lpRegMsg := mustJSON(t, card.Request{XRPLAddress: testLPAddr})
	lpRegOut := mustJSON(t, card.LPRegisterResponse{
		Tag: card.LPTagBase + 1, XRPLAddress: testLPAddr,
		DepositAccount: card.DefaultLPDepositAccount, ShareCurrency: card.DefaultShareCurrency,
	})
	lpDepMsg := mustJSON(t, card.Request{Tag: card.LPTagBase + 1, RlusdAmount: "1000", TxHash: "LPDEPTX1"})
	lpDepOut := mustJSON(t, card.LPDepositResponse{
		Tag: card.LPTagBase + 1, XRPLAddress: testLPAddr, TxHash: "LPDEPTX1", RlusdAmount: "1000",
		Shares: "1000000000", SharePrice: "1", ShareCurrency: card.DefaultShareCurrency,
	})
	lpDepDupOut := mustJSON(t, card.LPDepositResponse{
		Tag: card.LPTagBase + 1, XRPLAddress: testLPAddr, TxHash: "LPDEPTX1", RlusdAmount: "1000",
		Shares: "1000000000", ShareCurrency: card.DefaultShareCurrency, Duplicate: true,
	})
	// Cash is 1000 − 50 settled + 20 repaid = 970 and debt 30, so 100 shares pay 100 RLUSD.
	lpRedeemMsg := mustJSON(t, card.Request{XRPLAddress: testLPAddr, Shares: "100000000", TxHash: "LPREDTX1"})
	lpRedeemOut := mustJSON(t, card.LPRedeemResponse{
		XRPLAddress: testLPAddr, TxHash: "LPREDTX1", Shares: "100000000", RlusdAmount: "100", SharePrice: "1",
	})
	lpRedeemDupOut := mustJSON(t, card.LPRedeemResponse{
		XRPLAddress: testLPAddr, TxHash: "LPREDTX1", Shares: "100000000", RlusdAmount: "100", Duplicate: true,
	})
	// No interest yet: the whole run fits in one InterestAccrualPeriod.
	vaultOut := mustJSON(t, card.VaultResponse{
		TotalAssetsRlusd: "900", CashRlusd: "870", FreeLiquidityRlusd: "870", CardDebtRlusd: "30",
		OwedToMerchantsRlusd: "0", UtilizationBps: 333, BorrowAPRBps: card.BorrowAPRBps, SupplyAPRBps: 35,
		ReserveFactorBps: card.ReserveFactorBps, CardFeeBps: card.CardFeeBps,
		TotalShares: "900000000", SharePrice: "1", InternalShares: "0", InternalLiquidityRlusd: "0",
		LPShares: "900000000", LPLiquidityRlusd: "900", LPCount: 1,
		InterestEarnedRlusd: "0", InterestToLPsRlusd: "0", TreasuryOwedRlusd: "0", TreasuryPaidRlusd: "0.5",
		FeesEarnedRlusd: "0.5", InterestReserveRlusd: "0",
		LPDepositAccount: card.DefaultLPDepositAccount, PoolAccount: card.DefaultPoolAccount,
		TreasuryAccount: card.DefaultTreasuryAccount, OperatorAccount: card.DefaultOperatorAccount,
		ShareCurrency: card.DefaultShareCurrency,
	})

	ok := func(data []byte) conformance.Expected {
		return conformance.Expected{Status: 1, Log: "ok", Data: data}
	}

	return []conformance.Fixture{
		{
			Name: "01-register", Description: "allocates a destination tag",
			Channel: types.Direct, OPType: OPTypeCard, OPCommand: OPCommandRegister,
			Message: regMsg, Result: ok(regOut), Again: &conformance.Expected{Status: 1, Log: "ok", Data: regOut},
		},
		{
			Name: "02-register-idempotent", Description: "same address returns the same tag",
			Channel: types.Direct, OPType: OPTypeCard, OPCommand: OPCommandRegister,
			Message: regMsg, Result: ok(regOut),
		},
		{
			Name: "03-get-position-empty", Description: "position with zero balances",
			Channel: types.Direct, OPType: OPTypeCard, OPCommand: OPCommandGetPosition,
			Message: posEmptyMsg, Result: ok(posEmptyOut),
		},
		{
			Name: "04-credit-deposit", Description: "credits XRP collateral",
			Channel: types.Direct, OPType: OPTypeCard, OPCommand: OPCommandCreditDeposit,
			Message: creditMsg, Result: ok(creditOut), Again: &conformance.Expected{Status: 1, Log: "ok", Data: creditDupOut},
		},
		{
			Name: "05-credit-deposit-duplicate", Description: "replay of the same txHash is a no-op",
			Channel: types.Direct, OPType: OPTypeCard, OPCommand: OPCommandCreditDeposit,
			Message: creditMsg, Result: ok(creditDupOut),
		},
		{
			Name: "05a-lp-register", Description: "reserves an LP deposit tag",
			Channel: types.Direct, OPType: OPTypeCard, OPCommand: OPCommandLPRegister,
			Message: lpRegMsg, Result: ok(lpRegOut), Again: &conformance.Expected{Status: 1, Log: "ok", Data: lpRegOut},
		},
		{
			Name: "05b-lp-deposit", Description: "credits LP RLUSD and mints pool shares",
			Channel: types.Direct, OPType: OPTypeCard, OPCommand: OPCommandLPDeposit,
			Message: lpDepMsg, Result: ok(lpDepOut), Again: &conformance.Expected{Status: 1, Log: "ok", Data: lpDepDupOut},
		},
		{
			Name: "06-reserve-bad-credential", Description: "refuses a bad processor credential",
			Channel: types.Direct, OPType: OPTypeCard, OPCommand: OPCommandReserve,
			Message: reserveBadMsg, Result: conformance.Expected{Status: 0, Log: "error: invalid card processor credential"},
		},
		{
			Name: "07-reserve", Description: "locks XRP and borrows virtual RLUSD",
			Channel: types.Direct, OPType: OPTypeCard, OPCommand: OPCommandReserve,
			Message: reserveMsg, Result: ok(reserveOut),
		},
		{
			Name: "08-settle-daily", Description: "freezes the virtual settlement pile (on-chain instruction)",
			Channel: types.Instruction, OPType: OPTypeCard, OPCommand: OPCommandSettleDaily,
			Message: settleMsg, Result: ok(settleOut), Again: &conformance.Expected{Status: 1, Log: "ok", Data: settleOut},
		},
		{
			Name: "09-settle-confirm", Description: "confirms the on-chain RLUSD payment (on-chain instruction)",
			Channel: types.Instruction, OPType: OPTypeCard, OPCommand: OPCommandSettleConfirm,
			Message: confirmMsg, Result: ok(confirmOut), Again: &confirmAgain,
		},
		{
			Name: "10-register-unknown-field", Description: "refuses a field the command doesn't define",
			Channel: types.Direct, OPType: OPTypeCard, OPCommand: OPCommandRegister,
			Message: []byte(`{"xrplAddress":"rOther","extra":1}`),
			Result:  conformance.Expected{Status: 0, Log: "error: invalid payload"},
		},
		{
			Name: "11-register-empty-address", Description: "refuses a missing xrplAddress",
			Channel: types.Direct, OPType: OPTypeCard, OPCommand: OPCommandRegister,
			Message: []byte(`{}`),
			Result:  conformance.Expected{Status: 0, Log: "error: xrplAddress required (classic address starting with r)"},
		},
		{
			Name: "12-convert-request", Description: "reserves debt for an XLS-66 loan conversion",
			Channel: types.Direct, OPType: OPTypeCard, OPCommand: OPCommandConvertRequest,
			Message: convertMsg, Result: ok(convertOut),
		},
		{
			Name: "13-credit-rlusd-repayment", Description: "credits RLUSD repaid to the pool and unlocks collateral",
			Channel: types.Direct, OPType: OPTypeCard, OPCommand: OPCommandCreditRLUSDRepayment,
			Message: repayMsg, Result: ok(repayOut), Again: &conformance.Expected{Status: 1, Log: "ok", Data: repayDupOut},
		},
		{
			Name: "14-lp-redeem", Description: "burns LP shares for RLUSD out of the pool",
			Channel: types.Direct, OPType: OPTypeCard, OPCommand: OPCommandLPRedeem,
			Message: lpRedeemMsg, Result: ok(lpRedeemOut), Again: &conformance.Expected{Status: 1, Log: "ok", Data: lpRedeemDupOut},
		},
		{
			Name: "15-get-vault", Description: "reports the lending pool",
			Channel: types.Direct, OPType: OPTypeCard, OPCommand: OPCommandGetVault,
			Message: []byte(`{}`), Result: ok(vaultOut), Again: &conformance.Expected{Status: 1, Log: "ok", Data: vaultOut},
		},
	}
}

func TestFixtures(t *testing.T) {
	t.Parallel()
	require.NoError(t, golden.Check(fixturesDir, fixtures(t), *update))
}
