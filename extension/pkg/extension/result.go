package extension

import (
	"errors"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/tee-node/pkg/types"
)

// resultVersion is tee-node's result encoding version (settings.EncodingVersion), not the extension's.
const resultVersion = "1.0.0"

// The proxy keeps 0 and 1 as final; it replaces 2 and up only by a higher
// status or a final one. tee-node posts 3 when the extension misses its deadline.
const (
	statusFailed  = 0
	statusOK      = 1
	statusPending = 2
	firstPartial  = 4
	lastPartial   = 255
)

var (
	// ErrNoHandler is returned by the built-in default handlers: no handler is
	// registered for the op pair on the action's channel.
	ErrNoHandler = errors.New("no handler for this op")
	// ErrResultTooLarge means the result would exceed the limit set with
	// WithMaxResultSize; an oversized partial is not posted.
	ErrResultTooLarge = errors.New("result too large")
)

// The library's own errors reach the public log; their texts are fixed and never echo payload bytes.
var (
	errUnsupportedAction = errors.New("unsupported action type or submission tag")
	errSystemOp          = errors.New("op type reserved for the system")
	errInvalidMessage    = errors.New("invalid message")
	errMessageTooLarge   = errors.New("message too large")
	errDeadline          = errors.New("deadline exceeded")
	errPanic             = errors.New("internal error")
	errNoSignPort        = errors.New("long-running mode needs the sign port")
	errNoStatusLeft      = errors.New("no status left for a partial; 255 is the last")
	errJobDone           = errors.New("job already finished")
)

// PublicError returns an error whose text is only msg, so only msg reaches the
// public log. err stays reachable through errors.Is and errors.As.
func PublicError(msg string, err error) error {
	return &publicError{msg: msg, err: err}
}

type publicError struct {
	msg string
	err error
}

func (e *publicError) Error() string { return e.msg }

func (e *publicError) Unwrap() error { return e.err }

func newResult(a *types.Action, opType, opCommand common.Hash) types.ActionResult {
	return types.ActionResult{
		ID:            a.Data.ID,
		SubmissionTag: a.Data.SubmissionTag,
		OPType:        opType,
		OPCommand:     opCommand,
		Version:       resultVersion,
	}
}

func succeed(r types.ActionResult, data []byte) types.ActionResult {
	r.Status = statusOK
	r.Log = "ok"
	r.Data = data
	return r
}

func pending(r types.ActionResult, data []byte) types.ActionResult {
	r.Status = statusPending
	r.Log = "pending"
	r.Data = data
	return r
}

func partial(r types.ActionResult, status uint8, data []byte) types.ActionResult {
	r.Status = status
	r.Log = "partial"
	r.Data = data
	return r
}

func fail(r types.ActionResult, err error) types.ActionResult {
	r.Status = statusFailed
	r.Log = "error: " + err.Error()
	r.Data = nil
	return r
}
