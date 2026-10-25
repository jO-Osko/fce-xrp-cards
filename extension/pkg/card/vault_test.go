package card

import (
	"encoding/json"
	"fmt"
	"math/big"
	"testing"
	"time"
)

// fundPool deposits amount RLUSD into the pool as the operator, the way the
// demo's "operator deposit" does.
func fundPool(t *testing.T, l *Ledger, amount string) {
	t.Helper()
	addr := l.cfg.OperatorAccount
	if addr == "" {
		addr = "rOperatorTest"
	}
	reg, err := l.LPRegister(Request{XRPLAddress: addr})
	if err != nil {
		t.Fatal(err)
	}
	tag := reg.(LPRegisterResponse).Tag
	if _, err := l.LPDeposit(Request{Tag: tag, RlusdAmount: amount, TxHash: fmt.Sprintf("FUND-%s-%d", amount, len(l.processedTx))}); err != nil {
		t.Fatal(err)
	}
}

func vaultLedger(t *testing.T) (*Ledger, *time.Time) {
	t.Helper()
	l := NewLedger(Config{
		CardProcessorCredential: "test-cred",
		DepositAccount:          "rDepositTest",
		SettlementAddress:       "rSettlementTest",
		DefaultXRPUSD:           "2.50",
		PoolAccount:             "rPoolTest",
		LPDepositAccount:        "rLPDepositTest",
		TreasuryAccount:         "rTreasuryTest",
		OperatorAccount:         "rOperatorTest",
		ShareCurrency:           "XCLPTEST",
	})
	now := time.Unix(1_700_000_000, 0)
	l.now = func() time.Time { return now }
	return l, &now
}

// cardholder registers a user with 1000 XRP of collateral ($2,500 at $2.50).
func cardholder(t *testing.T, l *Ledger) uint32 {
	t.Helper()
	reg, err := l.Register(Request{XRPLAddress: "rCardholder"})
	if err != nil {
		t.Fatal(err)
	}
	tag := reg.(RegisterResponse).Tag
	if _, err := l.CreditDeposit(Request{Tag: tag, Drops: "1000000000", TxHash: "COLLATERAL"}); err != nil {
		t.Fatal(err)
	}
	return tag
}

func vault(t *testing.T, l *Ledger) VaultResponse {
	t.Helper()
	v, err := l.GetVault(Request{})
	if err != nil {
		t.Fatal(err)
	}
	return v.(VaultResponse)
}

func spend(t *testing.T, l *Ledger, tag uint32, amount string) {
	t.Helper()
	res, err := l.Reserve(Request{Credential: "test-cred", Tag: tag, RlusdAmount: amount})
	if err != nil {
		t.Fatal(err)
	}
	if !res.(ReserveResponse).Approved {
		t.Fatalf("declined: %+v", res)
	}
}

func TestLPDepositAndRedeem(t *testing.T) {
	t.Parallel()
	l, _ := vaultLedger(t)
	fundPool(t, l, "1000")

	reg, err := l.LPRegister(Request{XRPLAddress: "rLPAlice"})
	if err != nil {
		t.Fatal(err)
	}
	r := reg.(LPRegisterResponse)
	if r.Tag <= LPTagBase || r.DepositAccount != "rLPDepositTest" || r.ShareCurrency != "XCLPTEST" {
		t.Fatalf("register %+v", r)
	}
	again, _ := l.LPRegister(Request{XRPLAddress: "rLPAlice"})
	if again.(LPRegisterResponse).Tag != r.Tag {
		t.Fatal("LP_REGISTER not idempotent")
	}

	dep, err := l.LPDeposit(Request{Tag: r.Tag, RlusdAmount: "500", TxHash: "LPDEP"})
	if err != nil {
		t.Fatal(err)
	}
	d := dep.(LPDepositResponse)
	if d.Shares != "500000000" || d.XRPLAddress != "rLPAlice" || d.SharePrice != "1" {
		t.Fatalf("deposit %+v", d)
	}
	dup, _ := l.LPDeposit(Request{Tag: r.Tag, RlusdAmount: "500", TxHash: "LPDEP"})
	if d := dup.(LPDepositResponse); !d.Duplicate || d.Shares != "500000000" || d.RlusdAmount != "500" {
		t.Fatalf("duplicate deposit %+v: credited again, or lost the original shares", d)
	}

	v := vault(t, l)
	if v.TotalAssetsRlusd != "1500" || v.InternalLiquidityRlusd != "1000" || v.LPLiquidityRlusd != "500" || v.LPCount != 2 {
		t.Fatalf("vault %+v", v)
	}

	red, err := l.LPRedeem(Request{XRPLAddress: "rLPAlice", Shares: "200000000", TxHash: "LPRED"})
	if err != nil {
		t.Fatal(err)
	}
	if got := red.(LPRedeemResponse).RlusdAmount; got != "200" {
		t.Fatalf("redeemed %s", got)
	}
	redDup, _ := l.LPRedeem(Request{XRPLAddress: "rLPAlice", Shares: "200000000", TxHash: "LPRED"})
	if r := redDup.(LPRedeemResponse); !r.Duplicate || r.RlusdAmount != "200" {
		t.Fatalf("duplicate redeem %+v", r)
	}
	if v := vault(t, l); v.CashRlusd != "1300" || v.TotalShares != "1300000000" {
		t.Fatalf("after redeem %+v", v)
	}
	pos, _ := l.GetVault(Request{XRPLAddress: "rLPAlice"})
	if p := pos.(VaultResponse).Position; p == nil || len(p.Deposits) != 1 || len(p.Redemptions) != 1 || p.SharesRedeemed != "200000000" {
		t.Fatalf("position %+v", p)
	}
}

func TestCardSpendFeeAndSettlement(t *testing.T) {
	t.Parallel()
	l, _ := vaultLedger(t)
	fundPool(t, l, "1000")
	tag := cardholder(t, l)
	spend(t, l, tag, "100")

	v := vault(t, l)
	// The cardholder owes 100; 99 is owed to the merchant and 1 (1%) to the TEE treasury.
	if v.CardDebtRlusd != "100" || v.OwedToMerchantsRlusd != "99" || v.TreasuryOwedRlusd != "1" || v.FeesEarnedRlusd != "1" {
		t.Fatalf("after spend %+v", v)
	}
	if v.TotalAssetsRlusd != "1000" || v.FreeLiquidityRlusd != "900" {
		t.Fatalf("assets moved on spend %+v", v)
	}

	out, err := l.SettleDaily(Request{Credential: "test-cred"})
	if err != nil {
		t.Fatal(err)
	}
	s := out.(SettleDailyResponse)
	if s.RlusdAmount != "99" || s.TreasuryRlusd != "1" || s.TreasuryAddress != "rTreasuryTest" {
		t.Fatalf("settle %+v", s)
	}
	conf, err := l.SettleConfirm(Request{Credential: "test-cred", SettleNonce: s.SettleNonce, TxHash: "SETTLE"})
	if err != nil {
		t.Fatal(err)
	}
	if conf.(SettleConfirmResponse).TreasuryRlusd != "1" {
		t.Fatalf("confirm %+v", conf)
	}
	v = vault(t, l)
	if v.CashRlusd != "900" || v.TreasuryPaidRlusd != "1" || v.TreasuryOwedRlusd != "0" || v.TotalAssetsRlusd != "1000" {
		t.Fatalf("after settlement %+v", v)
	}
}

func TestInterestGoesToLPsAndTreasury(t *testing.T) {
	t.Parallel()
	l, now := vaultLedger(t)
	fundPool(t, l, "1000")
	tag := cardholder(t, l)
	spend(t, l, tag, "100")

	// Inside one accrual period nothing accrues.
	*now = now.Add(InterestAccrualPeriod - time.Second)
	if v := vault(t, l); v.CardDebtRlusd != "100" {
		t.Fatalf("accrued inside a period: %s", v.CardDebtRlusd)
	}

	// A year later, in one step: 12% on 100.
	*now = time.Unix(1_700_000_000, 0).Add(secondsPerYear * time.Second)
	v := vault(t, l)
	// Interest 12: 1.2 (10%) to the treasury on top of the 1 card fee, 10.8 to LPs.
	if v.CardDebtRlusd != "112" || v.InterestEarnedRlusd != "12" || v.InterestToLPsRlusd != "10.8" ||
		v.TreasuryOwedRlusd != "2.2" || v.TotalAssetsRlusd != "1010.8" {
		t.Fatalf("after a year %+v", v)
	}
	if v.UtilizationBps != 1108 || v.SupplyAPRBps != 119 {
		t.Fatalf("rates %+v", v)
	}
	pos, _ := l.GetPosition(Request{Tag: tag})
	if pos.(PositionResponse).RlusdDebt != "112" {
		t.Fatalf("position debt %s", pos.(PositionResponse).RlusdDebt)
	}

	// Reading did not change anything; a write accrues the same numbers for good.
	out, err := l.SettleDaily(Request{Credential: "test-cred"})
	if err != nil {
		t.Fatal(err)
	}
	if s := out.(SettleDailyResponse); s.RlusdAmount != "99" || s.TreasuryRlusd != "2.2" {
		t.Fatalf("settle %+v", s)
	}

	// The operator's shares are now worth more than it put in.
	if v := vault(t, l); v.InternalLiquidityRlusd != "1010.799999" {
		t.Fatalf("operator value %s", v.InternalLiquidityRlusd)
	}
}

func TestRepaymentAfterInterest(t *testing.T) {
	t.Parallel()
	l, now := vaultLedger(t)
	fundPool(t, l, "1000")
	tag := cardholder(t, l)
	spend(t, l, tag, "100")
	*now = now.Add(secondsPerYear * time.Second)

	out, err := l.CreditRLUSDRepayment(Request{Tag: tag, RlusdAmount: "112", TxHash: "REPAY"})
	if err != nil {
		t.Fatal(err)
	}
	r := out.(RepaymentResponse)
	if r.RlusdCredited != "112" || r.RlusdDebt != "0" || r.XrpLockedDrops != "0" {
		t.Fatalf("repayment %+v", r)
	}
	// Cash 1000 + 112 repaid; still owed: 99 to the merchant, 2.2 to the treasury.
	if v := vault(t, l); v.CashRlusd != "1112" || v.TotalAssetsRlusd != "1010.8" || v.CardDebtRlusd != "0" {
		t.Fatalf("vault %+v", v)
	}
}

func TestLiquidityRevertsAndErrors(t *testing.T) {
	t.Parallel()
	l, _ := vaultLedger(t)
	tag := cardholder(t, l)
	if _, err := l.Reserve(Request{Credential: "test-cred", Tag: tag, RlusdAmount: "1"}); err == nil {
		t.Fatal("spent from an empty pool")
	}
	fundPool(t, l, "100")
	spend(t, l, tag, "80")
	// Free cash: 100 − 79.2 owed to the merchant − 0.8 to the treasury = 20.
	if v := vault(t, l); v.FreeLiquidityRlusd != "20" {
		t.Fatalf("free %s", v.FreeLiquidityRlusd)
	}
	if _, err := l.Reserve(Request{Credential: "test-cred", Tag: tag, RlusdAmount: "21"}); err == nil {
		t.Fatal("card spend beyond free liquidity")
	}
	if _, err := l.LPRedeem(Request{XRPLAddress: "rOperatorTest", Shares: "50000000", TxHash: "R1"}); err == nil {
		t.Fatal("redeemed beyond free liquidity")
	}
	cases := []Request{
		{Command: CmdLPDeposit, Tag: 42, RlusdAmount: "1", TxHash: "X"},
		{Command: CmdLPDeposit, Tag: LPTagBase + 1, RlusdAmount: "0", TxHash: "Y"},
		{Command: CmdLPRedeem, XRPLAddress: "rOperatorTest", Shares: "0", TxHash: "Z"},
		{Command: CmdLPRedeem, XRPLAddress: "rOperatorTest", Shares: "999999999999", TxHash: "W"},
		{Command: CmdLPRegister},
	}
	for _, c := range cases {
		if _, err := l.Dispatch(c); err == nil {
			t.Errorf("%+v: no error", c)
		}
	}
}

func TestSnapshotKeepsPool(t *testing.T) {
	t.Parallel()
	l, _ := vaultLedger(t)
	fundPool(t, l, "1000")
	spend(t, l, cardholder(t, l), "10")

	b, err := l.MarshalSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := vaultLedger(t)
	var s Snapshot
	if err := jsonUnmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if err := restored.ImportSnapshot(s); err != nil {
		t.Fatal(err)
	}
	if a, b := vault(t, l), vault(t, restored); a != b {
		t.Fatalf("pool changed across snapshot:\n%+v\n%+v", a, b)
	}
	if restored.pool.TotalDebtScaled.Cmp(big.NewInt(10_000_000)) != 0 {
		t.Fatalf("scaled debt %s", restored.pool.TotalDebtScaled)
	}
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
