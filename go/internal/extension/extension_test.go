package extension

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/flare-foundation/fce-go-scaffold/pkg/card"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
	teeutils "github.com/flare-foundation/tee-node/pkg/utils"
)

func toHash(s string) common.Hash { return teeutils.ToHash(s) }

func buildTestAction(opType, opCommand common.Hash, originalMessage []byte) teetypes.Action {
	type dataFixed struct {
		InstructionID      common.Hash    `json:"instructionId"`
		TeeID              common.Address `json:"teeId"`
		Timestamp          uint64         `json:"timestamp"`
		RewardEpochID      uint32         `json:"rewardEpochId"`
		OPType             common.Hash    `json:"opType"`
		OPCommand          common.Hash    `json:"opCommand"`
		Cosigners          []string       `json:"cosigners"`
		CosignersThreshold uint64         `json:"cosignersThreshold"`
		OriginalMessage    hexutil.Bytes  `json:"originalMessage"`
	}

	df := dataFixed{
		OPType:          opType,
		OPCommand:       opCommand,
		OriginalMessage: originalMessage,
	}
	msg, _ := json.Marshal(df)

	return teetypes.Action{
		Data: teetypes.ActionData{
			ID:            common.HexToHash("0x1234"),
			SubmissionTag: "submit",
			Message:       msg,
		},
	}
}

func TestProcessAction_UnknownOPType(t *testing.T) {
	e := &Extension{ledger: card.NewLedger(card.DefaultConfig())}
	action := buildTestAction(toHash("UNKNOWN_TYPE"), toHash(card.CmdRegister), nil)
	status, _ := e.processAction(action)
	if status != http.StatusNotImplemented {
		t.Fatalf("expected %d, got %d", http.StatusNotImplemented, status)
	}
}

func TestProcessAction_Register(t *testing.T) {
	e := &Extension{ledger: card.NewLedger(card.Config{
		DepositAccount: "rDep", DefaultXRPUSD: "2.50",
		CardProcessorCredential: "test",
	})}
	body, _ := json.Marshal(card.Request{
		Command:     card.CmdRegister,
		XRPLAddress: "rActionUser1111111111",
	})
	action := buildTestAction(
		toHash(card.OPType),
		toHash(card.CmdRegister),
		body,
	)
	status, raw := e.processAction(action)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	var ar teetypes.ActionResult
	if err := json.Unmarshal(raw, &ar); err != nil {
		t.Fatal(err)
	}
	if ar.Status != 1 {
		t.Fatalf("log=%s", ar.Log)
	}
}
