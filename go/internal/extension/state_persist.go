package extension

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"

	"github.com/flare-foundation/fce-go-scaffold/pkg/card"
)

// snapshotVersion 2 is card.Snapshot under "ledger"; version 1 had the fields
// at the top level and no sequence counters.
const snapshotVersion = 2

type snapshotV2 struct {
	Version int             `json:"version"`
	Ledger  json.RawMessage `json:"ledger"`
}

type snapshotV1 struct {
	Version           int                      `json:"version"`
	NextTag           uint32                   `json:"nextTag"`
	Accounts          map[uint32]*card.Account `json:"accounts"`
	AddrToTag         map[string]uint32        `json:"addrToTag"`
	ProcessedTx       map[string]bool          `json:"processedTx"`
	VirtualSettlement string                   `json:"virtualSettlement"`
	PendingSettle     *struct {
		Nonce   string `json:"nonce"`
		Amount  string `json:"amount"`
		Created int64  `json:"created"`
	} `json:"pendingSettle,omitempty"`
	LastSettleTx string `json:"lastSettleTx,omitempty"`
}

func (e *Extension) persist() {
	if e.snapshotPath == "" {
		return
	}
	if err := e.saveSnapshot(); err != nil {
		fmt.Fprintf(os.Stderr, "extension: saveSnapshot: %v\n", err)
	}
}

func (e *Extension) saveSnapshot() error {
	// One writer at a time, and the newest state is marshalled last, so a slower
	// writer can never rename an older state over a newer one.
	e.saveMu.Lock()
	defer e.saveMu.Unlock()
	ledger, err := e.ledger.MarshalSnapshot()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(snapshotV2{Version: snapshotVersion, Ledger: ledger}, "", "  ")
	if err != nil {
		return err
	}
	tmp := e.snapshotPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, e.snapshotPath)
}

func (e *Extension) loadSnapshot() error {
	b, err := os.ReadFile(e.snapshotPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return err
	}
	switch head.Version {
	case snapshotVersion:
		var snap snapshotV2
		if err := json.Unmarshal(b, &snap); err != nil {
			return err
		}
		var s card.Snapshot
		if err := json.Unmarshal(snap.Ledger, &s); err != nil {
			return err
		}
		return e.ledger.ImportSnapshot(s)
	case 1:
		return e.loadSnapshotV1(b)
	default:
		return fmt.Errorf("unsupported snapshot version %d", head.Version)
	}
}

func (e *Extension) loadSnapshotV1(b []byte) error {
	var snap snapshotV1
	if err := json.Unmarshal(b, &snap); err != nil {
		return err
	}
	s := card.Snapshot{
		NextTag:           snap.NextTag,
		Accounts:          snap.Accounts,
		AddrToTag:         snap.AddrToTag,
		ProcessedTx:       snap.ProcessedTx,
		VirtualSettlement: snap.VirtualSettlement,
		LastSettleTx:      snap.LastSettleTx,
	}
	if p := snap.PendingSettle; p != nil {
		amt, ok := new(big.Int).SetString(p.Amount, 10)
		if !ok {
			return fmt.Errorf("bad pending amount")
		}
		s.PendingSettle = &card.PendingSettle{Nonce: p.Nonce, Amount: amt, Created: p.Created}
	}
	return e.ledger.ImportSnapshot(s)
}
