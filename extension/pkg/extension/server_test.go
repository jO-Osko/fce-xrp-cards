package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	"github.com/flare-foundation/go-flare-common/pkg/tee/op"
	"github.com/flare-foundation/tee-node/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every field present, empty bytes as "0x", hashes full width.
func TestActionResultGolden(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	rec := do(t, s, http.MethodPost, "/action", instructionAction(t, testType, testCommand, []byte("hi")))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{
		"id": "0x0000000000000000000000000000000000000000000000000000000000000001",
		"submissionTag": "threshold",
		"status": 1,
		"log": "ok",
		"opType": "0x5445535400000000000000000000000000000000000000000000000000000000",
		"opCommand": "0x4543484f00000000000000000000000000000000000000000000000000000000",
		"additionalResultStatus": "0x",
		"version": "1.0.0",
		"data": "0x6869"
	}`, rec.Body.String())
}

func TestActionResults(t *testing.T) {
	t.Parallel()
	const (
		failing op.Command = "FAIL"
		public  op.Command = "PUBLIC"
		panics  op.Command = "PANIC"
	)
	secret := errors.New("secret detail")

	s := newTestServer(t)
	s.RegisterInstructionHandler(testType, failing, func(context.Context, *instruction.DataFixed) ([]byte, Run, error) {
		return nil, nil, errors.New("boom")
	})
	s.RegisterInstructionHandler(testType, public, func(context.Context, *instruction.DataFixed) ([]byte, Run, error) {
		return nil, nil, fmt.Errorf("checking input: %w", PublicError("bad input", secret))
	})
	s.RegisterInstructionHandler(testType, panics, func(context.Context, *instruction.DataFixed) ([]byte, Run, error) {
		panic("secret detail")
	})

	payload := []byte("payload")
	tests := []struct {
		name   string
		body   []byte
		tag    types.SubmissionTag
		status uint8
		log    string
		data   []byte
		opType op.Type // echoed; empty when the message wasn't decoded
	}{
		{"instruction", instructionAction(t, testType, testCommand, payload), types.Threshold, statusOK, "ok", payload, testType},
		{"direct", directAction(t, testType, testDirectCommand, payload), types.Submit, statusOK, "ok", payload, testType},
		{"instruction for a direct pair", instructionAction(t, testType, testDirectCommand, payload), types.Threshold, statusFailed, "error: no handler for this op", nil, testType},
		{"direct for an instruction pair", directAction(t, testType, testCommand, payload), types.Submit, statusFailed, "error: no handler for this op", nil, testType},
		{"unknown pair", instructionAction(t, "OTHER", "OTHER", payload), types.Threshold, statusFailed, "error: no handler for this op", nil, "OTHER"},
		{"system pair", directAction(t, "F_XRP", "PAY", payload), types.Submit, statusFailed, "error: op type reserved for the system", nil, "F_XRP"},
		{"handler error", instructionAction(t, testType, failing, payload), types.Threshold, statusFailed, "error: boom", nil, testType},
		{"public error", instructionAction(t, testType, public, payload), types.Threshold, statusFailed, "error: checking input: bad input", nil, testType},
		{"panic", instructionAction(t, testType, panics, payload), types.Threshold, statusFailed, "error: internal error", nil, testType},
		{"instruction end phase", rawAction(t, types.Instruction, types.End, []byte(`{}`)), types.End, statusFailed, "error: unsupported action type or submission tag", nil, ""},
		{"direct with threshold tag", rawAction(t, types.Direct, types.Threshold, []byte(`{}`)), types.Threshold, statusFailed, "error: unsupported action type or submission tag", nil, ""},
		{"unknown action type", rawAction(t, "other", types.Submit, []byte(`{}`)), types.Submit, statusFailed, "error: unsupported action type or submission tag", nil, ""},
		{"invalid message", rawAction(t, types.Instruction, types.Threshold, []byte("not json")), types.Threshold, statusFailed, "error: invalid message", nil, ""},
		{"instruction message too large", rawAction(t, types.Instruction, types.Threshold, bytes.Repeat([]byte(" "), MaxInstructionMessageSize+1)), types.Threshold, statusFailed, "error: message too large", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := postAction(t, s, tt.body)
			assert.Equal(t, testID, res.ID)
			assert.Equal(t, tt.tag, res.SubmissionTag)
			assert.Equal(t, tt.status, res.Status)
			assert.Equal(t, tt.log, res.Log)
			assert.Equal(t, resultVersion, res.Version)
			assert.Equal(t, string(tt.data), string(res.Data))
			assert.NotContains(t, res.Log, "secret")
			if tt.opType == "" {
				assert.Equal(t, common.Hash{}, res.OPType)
			} else {
				assert.Equal(t, tt.opType, op.HashToOPType(res.OPType))
			}
		})
	}
}

// A direct action may carry a tag the extension added, never an instruction's; instructions keep threshold.
func TestDirectTags(t *testing.T) {
	t.Parallel()
	const custom types.SubmissionTag = "custom"
	s := New(WithLogger(logger.Nop{}), WithDirectTags(custom, "", types.Threshold, types.End))
	s.RegisterInstructionHandler(testType, testCommand, echoInstruction)
	s.RegisterDirectHandler(testType, testDirectCommand, echoDirect)

	direct, err := json.Marshal(types.DirectInstruction{OPType: testType.Hash(), OPCommand: testDirectCommand.Hash(), Message: []byte("hi")})
	require.NoError(t, err)
	instr, err := json.Marshal(instruction.DataFixed{OPType: testType.Hash(), OPCommand: testCommand.Hash(), OriginalMessage: []byte("hi")})
	require.NoError(t, err)

	tests := []struct {
		name   string
		typ    types.ActionType
		tag    types.SubmissionTag
		msg    []byte
		status uint8
	}{
		{"direct, added tag", types.Direct, custom, direct, statusOK},
		{"direct, submit", types.Direct, types.Submit, direct, statusOK},
		{"direct, other tag", types.Direct, "other", direct, statusFailed},
		{"direct, empty tag", types.Direct, "", direct, statusFailed},
		{"direct, threshold", types.Direct, types.Threshold, direct, statusFailed},
		{"direct, end", types.Direct, types.End, direct, statusFailed},
		{"instruction, added tag", types.Instruction, custom, instr, statusFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := postAction(t, s, rawAction(t, tt.typ, tt.tag, tt.msg))
			assert.Equal(t, tt.tag, res.SubmissionTag)
			assert.Equal(t, tt.status, res.Status)
		})
	}
}

// withField adds a field the library doesn't know, as a newer tee-node might.
func withField(t *testing.T, obj []byte) []byte {
	t.Helper()
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(obj, &m))
	m["futureField"] = json.RawMessage(`"x"`)
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return b
}

// Node-defined envelopes are decoded leniently, at every level.
func TestUnknownFieldsTolerated(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	payload := []byte("payload")
	instr, err := json.Marshal(instruction.DataFixed{OPType: testType.Hash(), OPCommand: testCommand.Hash(), OriginalMessage: payload})
	require.NoError(t, err)
	direct, err := json.Marshal(types.DirectInstruction{OPType: testType.Hash(), OPCommand: testDirectCommand.Hash(), Message: payload})
	require.NoError(t, err)

	tests := []struct {
		name string
		typ  types.ActionType
		tag  types.SubmissionTag
		msg  []byte
	}{
		{"instruction", types.Instruction, types.Threshold, instr},
		{"direct", types.Direct, types.Submit, direct},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data, err := json.Marshal(types.ActionData{ID: testID, Type: tt.typ, SubmissionTag: tt.tag, Message: withField(t, tt.msg)})
			require.NoError(t, err)
			body := withField(t, []byte(`{"data":`+string(withField(t, data))+`}`))

			res := postAction(t, s, body)
			assert.Equal(t, uint8(statusOK), res.Status)
			assert.Equal(t, string(payload), string(res.Data))
		})
	}
}

// Own server: a short deadline shared with parallel cases would make them flaky.
func TestActionDeadline(t *testing.T) {
	t.Parallel()
	const slow op.Command = "SLOW"
	s := newTestServer(t)
	s.timeout = 20 * time.Millisecond
	s.RegisterDirectHandler(testType, slow, func(ctx context.Context, _ *types.DirectInstruction) ([]byte, Run, error) {
		<-ctx.Done()
		return []byte("late"), nil, nil
	})

	res := postAction(t, s, directAction(t, testType, slow, []byte("x")))
	assert.Equal(t, uint8(statusFailed), res.Status)
	assert.Equal(t, "error: deadline exceeded", res.Log)
	assert.Empty(t, res.Data)
}

// A replaced default would otherwise get the system pairs tee-node forwards.
func TestSystemOpsNeverReachHandlers(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	s.RegisterDefaultInstructionHandler(func(context.Context, *instruction.DataFixed) ([]byte, Run, error) { return []byte("handled"), nil, nil })
	s.RegisterDefaultDirectHandler(func(context.Context, *types.DirectInstruction) ([]byte, Run, error) {
		return []byte("handled"), nil, nil
	})

	for _, body := range [][]byte{
		instructionAction(t, "F_XRP", "PAY", []byte("x")),
		directAction(t, "F_XRP", "PAY", []byte("x")),
	} {
		res := postAction(t, s, body)
		assert.Equal(t, uint8(statusFailed), res.Status)
		assert.Equal(t, "error: op type reserved for the system", res.Log)
		assert.Empty(t, res.Data)
	}
}

func TestNotAnAction(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	zeroID := []byte(`{"data":{"id":"0x0000000000000000000000000000000000000000000000000000000000000000","type":"direct","submissionTag":"submit","message":"0x"}}`)
	tests := []struct {
		name string
		body []byte
	}{
		{"invalid json", []byte("{")},
		{"null", []byte("null")},
		{"zero id", zeroID},
		{"body too large", bytes.Repeat([]byte(" "), MaxBodySize+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := do(t, s, http.MethodPost, "/action", tt.body)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Equal(t, "invalid action\n", rec.Body.String())
		})
	}
}

func TestRouting(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	tests := []struct {
		method, path string
		code         int
		allow        string
	}{
		{http.MethodGet, "/action", http.StatusMethodNotAllowed, "POST"},
		{http.MethodPost, "/state", http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodGet, "/other", http.StatusNotFound, ""},
	}
	for _, tt := range tests {
		t.Run(tt.method+tt.path, func(t *testing.T) {
			t.Parallel()
			rec := do(t, s, tt.method, tt.path, nil)
			require.Equal(t, tt.code, rec.Code)
			require.Equal(t, tt.allow, rec.Header().Get("Allow"))
		})
	}
}

func TestServe(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() { errCh <- s.Serve(ctx, l) }()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+l.Addr().String()+"/state", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)

	cancel()
	require.NoError(t, <-errCh)
}

func TestListenAndServePortInUse(t *testing.T) {
	t.Parallel()
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, l.Close()) })

	tcp, ok := l.Addr().(*net.TCPAddr)
	require.True(t, ok)
	err = newTestServer(t).ListenAndServe(t.Context(), tcp.Port)
	require.Error(t, err)
}
