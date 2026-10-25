package extension

import (
	"math/big"
	"path/filepath"
	"testing"

	"github.com/flare-foundation/fce-go-scaffold/pkg/card"
)

func TestRegisterCreditReserveSettle(t *testing.T) {
	// New loads and saves the snapshot; keep it away from the real one.
	t.Setenv("STATE_SNAPSHOT_PATH", filepath.Join(t.TempDir(), "card-state.json"))
	e := New(0, 0)
	defer e.Shutdown()
	// Replace ledger with known config/price (New already started a poller on port 0 — fine for test).
	e.ledger = card.NewLedger(card.Config{
		CardProcessorCredential: "test-cred",
		DepositAccount:          "rDepositTest123",
		SettlementAddress:       "rSettlementTest456",
		DefaultXRPUSD:           "2.50",
	})
	p, _ := new(big.Int).SetString("250000000", 10) // $2.50 e8
	e.ledger.SetPrice(p)
	fundPool(t, e.ledger, "1000")

	reg, err := e.ledger.Register(card.Request{XRPLAddress: "rUserAAAAAAAAAAAAAAA"})
	if err != nil {
		t.Fatal(err)
	}
	regResp := reg.(card.RegisterResponse)
	if regResp.Tag != 1001 {
		t.Fatalf("tag=%d", regResp.Tag)
	}

	_, err = e.ledger.CreditDeposit(card.Request{
		Tag: regResp.Tag, Drops: "100000000", TxHash: "ABCD",
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := e.ledger.Reserve(card.Request{
		Credential: "test-cred", Tag: regResp.Tag, RlusdAmount: "25.00",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.(card.ReserveResponse).Approved {
		t.Fatalf("not approved: %+v", res)
	}
}
