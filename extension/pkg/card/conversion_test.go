package card

import (
	"math/big"
	"testing"
	"time"
)

// conversionLedger returns a ledger at $2.50 with one user who deposited
// 100 XRP and spent 50 RLUSD (two 25 RLUSD reserves).
func conversionLedger(t *testing.T) (*Ledger, uint32, *time.Time) {
	t.Helper()
	l := NewLedger(Config{
		CardProcessorCredential: "test-cred",
		DepositAccount:          "rDepositTest",
		SettlementAddress:       "rSettlementTest",
		DefaultXRPUSD:           "2.50",
		PoolAccount:             "rPoolTest",
		CreditServerAccount:     "rCreditServerTest",
		LoanBrokerID:            "BROKER",
	})
	now := time.Unix(1_700_000_000, 0)
	l.now = func() time.Time { return now }
	fundPool(t, l, "10000")

	reg, err := l.Register(Request{XRPLAddress: "rUserConversion"})
	if err != nil {
		t.Fatal(err)
	}
	tag := reg.(RegisterResponse).Tag
	if _, err := l.CreditDeposit(Request{Tag: tag, Drops: "100000000", TxHash: "DEP1"}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		res, err := l.Reserve(Request{Credential: "test-cred", Tag: tag, RlusdAmount: "25"})
		if err != nil || !res.(ReserveResponse).Approved {
			t.Fatalf("reserve: %v %+v", err, res)
		}
	}
	return l, tag, &now
}

func position(t *testing.T, l *Ledger, tag uint32) PositionResponse {
	t.Helper()
	p, err := l.GetPosition(Request{Tag: tag})
	if err != nil {
		t.Fatal(err)
	}
	return p.(PositionResponse)
}

func TestConvertRequestReservesDebt(t *testing.T) {
	t.Parallel()
	l, tag, _ := conversionLedger(t)

	out, err := l.ConvertRequest(Request{Tag: tag, RlusdAmount: "20"})
	if err != nil {
		t.Fatal(err)
	}
	c := out.(ConvertResponse)
	if c.RlusdAmount != "20" || c.ConvertibleRlusd != "30" || c.DestinationTag != tag ||
		c.PoolAccount != "rPoolTest" || c.CreditServerAccount != "rCreditServerTest" || c.LoanBrokerID != "BROKER" {
		t.Fatalf("response %+v", c)
	}

	if _, err := l.ConvertRequest(Request{Tag: tag, RlusdAmount: "31"}); err == nil {
		t.Fatal("converted more than the unreserved debt")
	}
	rest, err := l.ConvertRequest(Request{Tag: tag})
	if err != nil {
		t.Fatal(err)
	}
	if got := rest.(ConvertResponse).RlusdAmount; got != "30" {
		t.Fatalf("empty amount should convert the rest, got %s", got)
	}
	if _, err := l.ConvertRequest(Request{Tag: tag}); err == nil {
		t.Fatal("converted with nothing left")
	}

	p := position(t, l, tag)
	if p.RlusdDebt != "50" || p.ConvertibleRlusd != "0" || len(p.PendingConversions) != 2 {
		t.Fatalf("position %+v", p)
	}
}

func TestConvertRequestExpires(t *testing.T) {
	t.Parallel()
	l, tag, now := conversionLedger(t)

	if _, err := l.ConvertRequest(Request{Tag: tag, RlusdAmount: "50"}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(ConversionTTL + time.Second)
	// The debt kept accruing interest meanwhile; all of it is convertible again.
	p := position(t, l, tag)
	debt, _ := parseRLUSD(p.RlusdDebt)
	if p.ConvertibleRlusd != p.RlusdDebt || debt.Cmp(big.NewInt(50_000_000)) <= 0 || len(p.PendingConversions) != 0 {
		t.Fatalf("expired conversion still reserves debt: %+v", p)
	}
	if _, err := l.ConvertRequest(Request{Tag: tag, RlusdAmount: "50"}); err != nil {
		t.Fatalf("debt not released after expiry: %v", err)
	}
}

func TestCreditRLUSDRepayment(t *testing.T) {
	t.Parallel()
	l, tag, _ := conversionLedger(t)
	before := position(t, l, tag)
	// 50 RLUSD at $2.50 and 75% LTV: 2 x 13333334 drops locked.
	if before.XrpLockedDrops != "26666668" {
		t.Fatalf("locked %s", before.XrpLockedDrops)
	}
	if _, err := l.ConvertRequest(Request{Tag: tag, RlusdAmount: "20"}); err != nil {
		t.Fatal(err)
	}

	out, err := l.CreditRLUSDRepayment(Request{Tag: tag, RlusdAmount: "20", TxHash: "REPAY1"})
	if err != nil {
		t.Fatal(err)
	}
	r := out.(RepaymentResponse)
	// 20/50 of the locked collateral, rounded down.
	if r.RlusdCredited != "20" || r.RlusdDebt != "30" || r.XrpUnlockedDrops != "10666667" ||
		r.XrpLockedDrops != "16000001" || r.XrpAvailableDrops != "83999999" || r.RlusdExcess != "" {
		t.Fatalf("partial %+v", r)
	}
	if p := position(t, l, tag); len(p.PendingConversions) != 0 || p.ConvertibleRlusd != "30" || len(p.Repayments) != 1 {
		t.Fatalf("conversion not released: %+v", p)
	}

	dup, err := l.CreditRLUSDRepayment(Request{Tag: tag, RlusdAmount: "20", TxHash: "REPAY1"})
	if err != nil {
		t.Fatal(err)
	}
	if d := dup.(RepaymentResponse); !d.Duplicate || d.RlusdDebt != "30" {
		t.Fatalf("duplicate %+v", d)
	}

	full, err := l.CreditRLUSDRepayment(Request{Tag: tag, RlusdAmount: "35.5", TxHash: "REPAY2"})
	if err != nil {
		t.Fatal(err)
	}
	f := full.(RepaymentResponse)
	if f.RlusdCredited != "30" || f.RlusdExcess != "5.5" || f.RlusdDebt != "0" ||
		f.XrpLockedDrops != "0" || f.XrpAvailableDrops != "100000000" {
		t.Fatalf("full %+v", f)
	}
}

func TestRepaymentReleasesOldestConversionFirst(t *testing.T) {
	t.Parallel()
	l, tag, _ := conversionLedger(t)
	for _, amt := range []string{"10", "15"} {
		if _, err := l.ConvertRequest(Request{Tag: tag, RlusdAmount: amt}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.CreditRLUSDRepayment(Request{Tag: tag, RlusdAmount: "12", TxHash: "R"}); err != nil {
		t.Fatal(err)
	}
	p := position(t, l, tag)
	if len(p.PendingConversions) != 1 || p.PendingConversions[0].RlusdAmount != "13" {
		t.Fatalf("pending %+v", p.PendingConversions)
	}
}

func TestConversionErrors(t *testing.T) {
	t.Parallel()
	l, tag, _ := conversionLedger(t)
	cases := []struct {
		name string
		run  func() error
	}{
		{"convert unknown tag", func() error { _, err := l.ConvertRequest(Request{Tag: 9}); return err }},
		{"convert no tag", func() error { _, err := l.ConvertRequest(Request{}); return err }},
		{"convert negative", func() error { _, err := l.ConvertRequest(Request{Tag: tag, RlusdAmount: "-1"}); return err }},
		{"repay unknown tag", func() error {
			_, err := l.CreditRLUSDRepayment(Request{Tag: 9, RlusdAmount: "1", TxHash: "X"})
			return err
		}},
		{"repay no hash", func() error {
			_, err := l.CreditRLUSDRepayment(Request{Tag: tag, RlusdAmount: "1"})
			return err
		}},
		{"repay zero", func() error {
			_, err := l.CreditRLUSDRepayment(Request{Tag: tag, RlusdAmount: "0", TxHash: "Y"})
			return err
		}},
	}
	for _, c := range cases {
		if c.run() == nil {
			t.Errorf("%s: no error", c.name)
		}
	}

	unconfigured := NewLedger(Config{CardProcessorCredential: "c"})
	if _, err := unconfigured.ConvertRequest(Request{Tag: 1}); err == nil {
		t.Error("conversion allowed without pool / broker configuration")
	}
}
