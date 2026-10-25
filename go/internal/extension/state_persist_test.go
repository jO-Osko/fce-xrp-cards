package extension

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/flare-foundation/fce-go-scaffold/pkg/card"
)

func testLedgerConfig() card.Config {
	return card.Config{
		CardProcessorCredential: "test-cred",
		DepositAccount:          "rDep",
		SettlementAddress:       "rSet",
		DefaultXRPUSD:           "2.50",
		PoolAccount:             "rPool",
		CreditServerAccount:     "rCS",
		LoanBrokerID:            "BROKER",
	}
}

// A restart must keep accounts, pending conversions and the id counters.
func TestSnapshotRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "card-state.json")
	e := &Extension{ledger: card.NewLedger(testLedgerConfig()), snapshotPath: path}
	e.ledger.Persist = e.persist
	fundPool(t, e.ledger, "1000")

	reg, err := e.ledger.Register(card.Request{XRPLAddress: "rSnapshotUser"})
	if err != nil {
		t.Fatal(err)
	}
	tag := reg.(card.RegisterResponse).Tag
	if _, err := e.ledger.CreditDeposit(card.Request{Tag: tag, Drops: "100000000", TxHash: "D1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ledger.Reserve(card.Request{Credential: "test-cred", Tag: tag, RlusdAmount: "10"}); err != nil {
		t.Fatal(err)
	}
	first, err := e.ledger.ConvertRequest(card.Request{Tag: tag, RlusdAmount: "4"})
	if err != nil {
		t.Fatal(err)
	}

	restarted := &Extension{ledger: card.NewLedger(testLedgerConfig()), snapshotPath: path}
	if err := restarted.loadSnapshot(); err != nil {
		t.Fatal(err)
	}
	pos, err := restarted.ledger.GetPosition(card.Request{Tag: tag})
	if err != nil {
		t.Fatal(err)
	}
	p := pos.(card.PositionResponse)
	if p.RlusdDebt != "10" || p.ConvertibleRlusd != "6" || len(p.PendingConversions) != 1 {
		t.Fatalf("restored position %+v", p)
	}
	if v, _ := restarted.ledger.GetVault(card.Request{}); v.(card.VaultResponse).CashRlusd != "1000" {
		t.Fatalf("restored pool %+v", v)
	}
	next, err := restarted.ledger.ConvertRequest(card.Request{Tag: tag, RlusdAmount: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if next.(card.ConvertResponse).ConversionID == first.(card.ConvertResponse).ConversionID {
		t.Fatal("conversion id reused after restart")
	}
}

func TestLoadSnapshotV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "card-state.json")
	v1 := `{"version":1,"nextTag":1001,"accounts":{"1001":{"tag":1001,"xrplAddress":"rOld","xrpAvailable":5,"xrpLocked":0,"rlusdDebt":0}},` +
		`"addrToTag":{"rOld":1001},"processedTx":{"H":true},"virtualSettlement":"0",` +
		`"pendingSettle":{"nonce":"0000000000000001","amount":"7000000","created":1}}`
	if err := os.WriteFile(path, []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}
	e := &Extension{ledger: card.NewLedger(testLedgerConfig()), snapshotPath: path}
	if err := e.loadSnapshot(); err != nil {
		t.Fatal(err)
	}
	if s := e.ledger.Summary(); s.UserCount != 1 || s.PendingSettleRlusd != "7" {
		t.Fatalf("summary %+v", s)
	}
}

// fundPool deposits RLUSD into the lending pool as an LP, so card payments have liquidity.
func fundPool(t *testing.T, l *card.Ledger, amount string) {
	t.Helper()
	reg, err := l.LPRegister(card.Request{XRPLAddress: "rOperatorTest"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.LPDeposit(card.Request{Tag: reg.(card.LPRegisterResponse).Tag, RlusdAmount: amount, TxHash: "FUND-" + amount}); err != nil {
		t.Fatal(err)
	}
}
