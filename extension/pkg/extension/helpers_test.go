package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	"github.com/flare-foundation/go-flare-common/pkg/tee/op"
	"github.com/flare-foundation/tee-node/pkg/types"
	"github.com/stretchr/testify/require"
)

const (
	testType          op.Type    = "TEST"
	testCommand       op.Command = "ECHO"
	testDirectCommand op.Command = "DIRECT_ECHO"
)

var testID = common.HexToHash("0x01")

func echoInstruction(_ context.Context, i *instruction.DataFixed) ([]byte, Run, error) {
	return i.OriginalMessage, nil, nil
}

func echoDirect(_ context.Context, d *types.DirectInstruction) ([]byte, Run, error) {
	return d.Message, nil, nil
}

// newTestServer has TEST / ECHO on instructions and TEST / DIRECT_ECHO on direct actions.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	s := New(WithLogger(logger.Nop{}))
	s.RegisterInstructionHandler(testType, testCommand, echoInstruction)
	s.RegisterDirectHandler(testType, testDirectCommand, echoDirect)
	return s
}

func rawAction(t *testing.T, typ types.ActionType, tag types.SubmissionTag, msg []byte) []byte {
	t.Helper()
	b, err := json.Marshal(types.Action{Data: types.ActionData{ID: testID, Type: typ, SubmissionTag: tag, Message: msg}})
	require.NoError(t, err)
	return b
}

func instructionAction(t *testing.T, opType op.Type, opCommand op.Command, payload []byte) []byte {
	t.Helper()
	msg, err := json.Marshal(instruction.DataFixed{OPType: opType.Hash(), OPCommand: opCommand.Hash(), OriginalMessage: payload})
	require.NoError(t, err)
	return rawAction(t, types.Instruction, types.Threshold, msg)
}

func directAction(t *testing.T, opType op.Type, opCommand op.Command, payload []byte) []byte {
	t.Helper()
	msg, err := json.Marshal(types.DirectInstruction{OPType: opType.Hash(), OPCommand: opCommand.Hash(), Message: payload})
	require.NoError(t, err)
	return rawAction(t, types.Direct, types.Submit, msg)
}

func do(t *testing.T, s *Server, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func postAction(t *testing.T, s *Server, body []byte) types.ActionResult {
	t.Helper()
	rec := do(t, s, http.MethodPost, "/action", body)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var res types.ActionResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	return res
}
