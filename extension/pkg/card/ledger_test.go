package card

import (
	"math/big"
	"testing"
	"time"
)

// Persist must not run under the ledger write lock — MarshalSnapshot takes RLock.
func TestRegisterPersistNoDeadlock(t *testing.T) {
	t.Parallel()
	l := NewLedger(DefaultConfig())
	l.Persist = func() {
		_, _ = l.MarshalSnapshot()
	}
	done := make(chan error, 1)
	go func() {
		_, err := l.Register(Request{XRPLAddress: "rUserPersistDeadlockAAA"})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Register deadlocked: Persist called MarshalSnapshot while holding write lock")
	}
}

func TestRegisterCreditReserveSettle(t *testing.T) {
	l := NewLedger(Config{
		CardProcessorCredential: "test-cred",
		DepositAccount:          "rDepositTest123",
		SettlementAddress:       "rSettlementTest456",
		DefaultXRPUSD:           "2.50",
	})
	p, _ := parsePriceE8("2.50")
	l.SetPrice(p)
	fundPool(t, l, "1000")

	reg, err := l.Register(Request{XRPLAddress: "rUserAAAAAAAAAAAAAAA"})
	if err != nil {
		t.Fatal(err)
	}
	regResp := reg.(RegisterResponse)
	if regResp.Tag != 1001 {
		t.Fatalf("tag=%d", regResp.Tag)
	}

	_, err = l.CreditDeposit(Request{Tag: regResp.Tag, Drops: "100000000", TxHash: "ABCD"})
	if err != nil {
		t.Fatal(err)
	}

	dup, err := l.CreditDeposit(Request{Tag: regResp.Tag, Drops: "100000000", TxHash: "ABCD"})
	if err != nil {
		t.Fatal(err)
	}
	if !dup.(CreditDepositResponse).Duplicate {
		t.Fatal("expected duplicate")
	}

	res, err := l.Reserve(Request{
		Credential: "test-cred", Tag: regResp.Tag, RlusdAmount: "25.00", Label: "Lunch",
	})
	if err != nil {
		t.Fatal(err)
	}
	rr := res.(ReserveResponse)
	if !rr.Approved {
		t.Fatalf("not approved: %s", rr.Reason)
	}
	// 25 RLUSD less the 1% card fee is owed to the merchant.
	if l.virtualSettlement.Cmp(big.NewInt(24_750_000)) != 0 {
		t.Fatalf("settlement=%s", l.virtualSettlement)
	}

	settle, err := l.SettleDaily(Request{Credential: "test-cred"})
	if err != nil {
		t.Fatal(err)
	}
	sr := settle.(SettleDailyResponse)
	if sr.SettleNonce != "0000000000000001" {
		t.Fatalf("nonce=%s", sr.SettleNonce)
	}

	confirm, err := l.SettleConfirm(Request{
		Credential: "test-cred", SettleNonce: sr.SettleNonce, TxHash: "SETTLE1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !confirm.(SettleConfirmResponse).WireQueued {
		t.Fatal("expected wire")
	}
}
