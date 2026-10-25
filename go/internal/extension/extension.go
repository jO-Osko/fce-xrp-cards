package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"

	"extension-scaffold/internal/config"

	"github.com/flare-foundation/fce-go-scaffold/pkg/card"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
	teeutils "github.com/flare-foundation/tee-node/pkg/utils"

	"github.com/flare-foundation/tee-node/pkg/processorutils"
)

// Extension hosts the CARD virtual loan ledger (mock TEE / POST /card).
type Extension struct {
	Server       *http.Server
	ledger       *card.Ledger
	pollerCancel context.CancelFunc
	snapshotPath string
	// saveMu serializes snapshot writes: handlers run concurrently and share the tmp file.
	saveMu sync.Mutex
}

func New(extensionPort, signPort int) *Extension {
	cfg := card.DefaultConfig()

	e := &Extension{
		ledger:       card.NewLedger(cfg),
		snapshotPath: config.StateSnapshotPath(),
	}
	e.ledger.Persist = e.persist

	if e.snapshotPath != "" {
		if err := e.loadSnapshot(); err != nil {
			fmt.Fprintf(os.Stderr, "extension: loadSnapshot: %v\n", err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /state", e.stateHandler)
	mux.HandleFunc("POST /action", e.actionHandler)
	mux.HandleFunc("POST /card", e.cardHandler)
	mux.HandleFunc("OPTIONS /card", e.corsHandler)

	e.Server = &http.Server{Addr: fmt.Sprintf(":%d", extensionPort), Handler: mux}

	ctx, cancel := context.WithCancel(context.Background())
	e.pollerCancel = cancel
	go e.ledger.StartPricePoller(ctx.Done())

	_ = signPort
	return e
}

func (e *Extension) Shutdown() {
	if e.pollerCancel != nil {
		e.pollerCancel()
	}
}

func (e *Extension) corsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Card-Credential")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.WriteHeader(http.StatusNoContent)
}

func (e *Extension) cardHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	var req card.Request
	// Unknown fields are refused, as the real TEE's payload decoding does.
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(card.Envelope{OK: false, Error: fmt.Sprintf("decoding: %v", err)})
		return
	}
	if hdr := r.Header.Get("X-Card-Credential"); hdr != "" && req.Credential == "" {
		req.Credential = hdr
	}

	data, err := e.ledger.Dispatch(req)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(card.Envelope{
			OK: false, Command: req.Command, Error: err.Error(),
		})
		return
	}
	_ = json.NewEncoder(w).Encode(card.Envelope{
		OK: true, Command: req.Command, Data: marshalData(data),
	})
}

func (e *Extension) stateHandler(w http.ResponseWriter, r *http.Request) {
	sum := e.ledger.Summary()
	type stateResponse struct {
		StateVersion interface{}       `json:"stateVersion"`
		State        card.StateSummary `json:"state"`
	}
	resp := stateResponse{
		StateVersion: teeutils.ToHash(config.Version),
		State:        sum,
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, fmt.Sprintf("sending response: %v", err), http.StatusInternalServerError)
	}
}

func (e *Extension) processAction(action teetypes.Action) (int, []byte) {
	dataFixed, err := processorutils.Parse[instruction.DataFixed](action.Data.Message)
	if err != nil {
		return http.StatusBadRequest, []byte(fmt.Sprintf("decoding fixed data: %v", err))
	}

	if dataFixed.OPType != teeutils.ToHash(card.OPType) {
		return http.StatusNotImplemented, []byte(fmt.Sprintf(
			"unsupported op type: received %s, expected %s",
			dataFixed.OPType.Hex(), card.OPType,
		))
	}

	var req card.Request
	if len(dataFixed.OriginalMessage) > 0 {
		if err := json.Unmarshal(dataFixed.OriginalMessage, &req); err != nil {
			ar := buildResult(action, dataFixed, nil, 0, fmt.Errorf("decoding CARD body: %w", err))
			b, _ := json.Marshal(ar)
			return http.StatusOK, b
		}
	}

	if req.Command == "" {
		switch dataFixed.OPCommand {
		case teeutils.ToHash(card.CmdRegister):
			req.Command = card.CmdRegister
		case teeutils.ToHash(card.CmdCreditDeposit):
			req.Command = card.CmdCreditDeposit
		case teeutils.ToHash(card.CmdReserve):
			req.Command = card.CmdReserve
		case teeutils.ToHash(card.CmdGetPosition):
			req.Command = card.CmdGetPosition
		case teeutils.ToHash(card.CmdStats):
			req.Command = card.CmdStats
		case teeutils.ToHash(card.CmdSettleDaily):
			req.Command = card.CmdSettleDaily
		case teeutils.ToHash(card.CmdSettleConfirm):
			req.Command = card.CmdSettleConfirm
		case teeutils.ToHash(card.CmdConvertRequest):
			req.Command = card.CmdConvertRequest
		case teeutils.ToHash(card.CmdCreditRLUSDRepayment):
			req.Command = card.CmdCreditRLUSDRepayment
		case teeutils.ToHash(card.CmdLPRegister):
			req.Command = card.CmdLPRegister
		case teeutils.ToHash(card.CmdLPDeposit):
			req.Command = card.CmdLPDeposit
		case teeutils.ToHash(card.CmdLPRedeem):
			req.Command = card.CmdLPRedeem
		case teeutils.ToHash(card.CmdGetVault):
			req.Command = card.CmdGetVault
		default:
			return http.StatusNotImplemented, []byte(fmt.Sprintf(
				"unsupported op command: %s", dataFixed.OPCommand.Hex(),
			))
		}
	}

	data, err := e.ledger.Dispatch(req)
	if err != nil {
		ar := buildResult(action, dataFixed, nil, 0, err)
		b, _ := json.Marshal(ar)
		return http.StatusOK, b
	}
	payload, _ := json.Marshal(data)
	ar := buildResult(action, dataFixed, payload, 1, nil)
	b, _ := json.Marshal(ar)
	return http.StatusOK, b
}

func marshalData(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
