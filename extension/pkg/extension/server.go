package extension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/fce-go-scaffold/pkg/internal/signport"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	"github.com/flare-foundation/go-flare-common/pkg/tee/op"
	"github.com/flare-foundation/tee-node/pkg/types"
)

// Limits mirror tee-node v0.0.29 (internal/settings); re-check them on every tee-node bump.
const (
	// MaxBodySize caps the /action body; tee-node fetches actions with the same cap (MaxFetchResponseSize).
	MaxBodySize = 10 * 1024 * 1024
	// MaxInstructionMessageSize caps an instruction's message (MaxInstructionSize).
	MaxInstructionMessageSize = 100 * 1024
	// MaxDirectMessageSize caps a direct action's message (MaxActionSize).
	MaxDirectMessageSize = 10 * 1024 * 1024
	// MaxStateResponseSize caps the encoded /state response; tee-node cuts it there (MaxFetchResponseSize).
	MaxStateResponseSize = 10 * 1024 * 1024
)

// Defaults only: the developer owns these values (WithPostWindow, WithMaxResultSize).
const (
	// DefaultPostWindow is how long each result post from a long-running job retries.
	DefaultPostWindow = time.Minute
	// DefaultMaxResultSize caps a result as the node posts it to the proxy:
	// signed, data hex-encoded. It is tee-node v0.0.29's MaxFetchResponseSize,
	// what the node already moves from the proxy within 2 s; tee-proxy v0.0.25
	// refuses more than 25 MiB.
	DefaultMaxResultSize = 10 * 1024 * 1024
)

// HandlerTimeout bounds a handler and the state function.
// tee-node waits 2 s for a response (ProxyTimeout); the rest is headroom.
const HandlerTimeout = 1500 * time.Millisecond

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 10 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 5 * time.Second
)

// Logger receives the library's own diagnostics.
// They never include payloads, handler errors or panic values.
type Logger interface {
	Errorf(format string, args ...any)
}

// Option configures a Server.
type Option func(*Server)

// WithLogger sets the Logger; nil keeps the default, go-flare-common's logger.
func WithLogger(l Logger) Option {
	return func(s *Server) {
		if l != nil {
			s.log = l
		}
	}
}

// WithSignPort enables long-running mode: a handler's Run posts its results
// to the node's /result on 127.0.0.1:port.
func WithSignPort(port int) Option {
	return func(s *Server) {
		s.poster = signport.New(port)
	}
}

// WithPostWindow sets how long each result post from a long-running job
// retries before the result is dropped; d <= 0 keeps DefaultPostWindow.
func WithPostWindow(d time.Duration) Option {
	return func(s *Server) {
		if d > 0 {
			s.postWindow = d
		}
	}
}

// WithMaxResultSize sets the largest result the node may post to the proxy,
// measured as DefaultMaxResultSize is. A larger answer or final result fails
// with status 0; a larger partial is not posted. The developer owns this
// value: set it for your results and your link to the proxy. n <= 0 keeps
// DefaultMaxResultSize.
func WithMaxResultSize(n int) Option {
	return func(s *Server) {
		if n > 0 {
			s.maxResult = n
		}
	}
}

// WithDirectTags also accepts direct actions with these submission tags,
// besides submit, for a tee-proxy adapted to send them. Empty tags are ignored,
// and so are threshold and end: tee-proxy picks a direct action's id, so a
// direct result with an instruction's tag could pass as that instruction's.
// Results echo the tag; handlers don't see it. Instructions keep threshold:
// tee-node refuses any other instruction tag.
func WithDirectTags(tags ...types.SubmissionTag) Option {
	return func(s *Server) {
		for _, t := range tags {
			if t != "" && t != types.Threshold && t != types.End {
				s.directTags[t] = true
			}
		}
	}
}

// Server serves POST /action and GET /state to tee-node.
// Register handlers before serving; the server does not serialize calls.
type Server struct {
	mux    *http.ServeMux
	routes map[types.OpID]route

	defaultInstruction       InstructionHandler
	defaultDirect            DirectHandler
	customDefaultInstruction bool
	customDefaultDirect      bool
	directTags               map[types.SubmissionTag]bool

	state   StateFunc
	log     Logger
	timeout time.Duration

	poster     resultPoster // nil without WithSignPort
	postWindow time.Duration
	maxResult  int
	//nolint:containedctx // jobs outlive their requests; cancelled when Serve stops
	jobs     context.Context
	stopJobs context.CancelFunc
}

// New returns a Server with no handlers and the zero state.
func New(opts ...Option) *Server {
	s := &Server{
		mux:                http.NewServeMux(),
		routes:             make(map[types.OpID]route),
		defaultInstruction: func(context.Context, *instruction.DataFixed) ([]byte, Run, error) { return nil, nil, ErrNoHandler },
		defaultDirect:      func(context.Context, *types.DirectInstruction) ([]byte, Run, error) { return nil, nil, ErrNoHandler },
		directTags:         map[types.SubmissionTag]bool{types.Submit: true},
		log:                defaultLogger{},
		timeout:            HandlerTimeout,
		postWindow:         DefaultPostWindow,
		maxResult:          DefaultMaxResultSize,
	}
	s.jobs, s.stopJobs = context.WithCancel(context.Background())
	for _, o := range opts {
		o(s)
	}
	s.mux.HandleFunc("POST /action", s.handleAction)
	s.mux.HandleFunc("GET /state", s.handleState)
	return s
}

// ServeHTTP serves the extension API; other paths get 404, other methods 405.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// ListenAndServe serves on 127.0.0.1:port until ctx is cancelled, then shuts down.
func (s *Server) ListenAndServe(ctx context.Context, port int) error {
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("listening on port %d: %w", port, err)
	}
	return s.Serve(ctx, l)
}

// Serve serves on l until ctx is cancelled, then shuts down; it closes l.
// Bind l to loopback, as ListenAndServe does. When it returns, long-running
// jobs and their posts are stopped, without waiting. A Server serves once.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	defer s.stopJobs()
	srv := &http.Server{
		Handler:           s,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(l) }()

	select {
	case err := <-errCh:
		return fmt.Errorf("serving: %w", err)
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			return fmt.Errorf("shutting down: %w", err)
		}
		if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serving: %w", err)
		}
		return nil
	}
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodySize))
	if err != nil {
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}

	// not an action: tee-node then builds the failed result for the right id itself
	var a types.Action
	if err := json.Unmarshal(body, &a); err != nil || a.Data.ID == (common.Hash{}) {
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}

	res := s.fit(s.process(r.Context(), &a))
	out, err := json.Marshal(res)
	if err != nil {
		http.Error(w, "encoding result", http.StatusInternalServerError)
		return
	}
	writeJSON(w, out)
}

// process turns every failure after decoding into a status 0 result that echoes the id.
func (s *Server) process(ctx context.Context, a *types.Action) types.ActionResult {
	switch {
	case a.Data.Type == types.Instruction && a.Data.SubmissionTag == types.Threshold:
		res := newResult(a, common.Hash{}, common.Hash{})
		df, err := parse[instruction.DataFixed](a.Data.Message, MaxInstructionMessageSize)
		if err != nil {
			return fail(res, err)
		}
		res = newResult(a, df.OPType, df.OPCommand)
		// tee-node refuses these first; no handler, default included, may get them
		if isSystem(df.OPType) {
			return fail(res, errSystemOp)
		}
		h := s.instructionHandler(types.OpID{OPType: df.OPType, OPCommand: df.OPCommand})
		return s.run(ctx, res, func(ctx context.Context) ([]byte, Run, error) { return h(ctx, df) })

	case a.Data.Type == types.Direct && s.directTags[a.Data.SubmissionTag]:
		res := newResult(a, common.Hash{}, common.Hash{})
		di, err := parse[types.DirectInstruction](a.Data.Message, MaxDirectMessageSize)
		if err != nil {
			return fail(res, err)
		}
		res = newResult(a, di.OPType, di.OPCommand)
		if isSystem(di.OPType) {
			return fail(res, errSystemOp)
		}
		h := s.directHandler(types.OpID{OPType: di.OPType, OPCommand: di.OPCommand})
		return s.run(ctx, res, func(ctx context.Context) ([]byte, Run, error) { return h(ctx, di) })

	default:
		return fail(newResult(a, common.Hash{}, common.Hash{}), errUnsupportedAction)
	}
}

type outcome struct {
	data []byte
	run  Run
	err  error
}

// run calls f under the handler deadline; an abandoned f finishes into the
// buffered channel, and a Run it returns late is never started.
func (s *Server) run(ctx context.Context, res types.ActionResult, f func(context.Context) ([]byte, Run, error)) types.ActionResult {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	ch := make(chan outcome, 1)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				// the panic value isn't logged: it may carry secrets
				s.log.Errorf("recovered a panic in the handler for %q / %q", op.HashToOPType(res.OPType), op.HashToOPCommand(res.OPCommand))
				ch <- outcome{err: errPanic}
			}
		}()
		data, run, err := f(ctx)
		ch <- outcome{data: data, run: run, err: err}
	}()

	select {
	case o := <-ch:
		switch {
		case o.err != nil:
			return fail(res, o.err)
		case o.run == nil:
			return succeed(res, o.data)
		case s.poster == nil:
			s.log.Errorf("the handler for %q / %q went long-running without WithSignPort", op.HashToOPType(res.OPType), op.HashToOPCommand(res.OPCommand))
			return fail(res, errNoSignPort)
		}
		// start the job only once the answer is surely status 2
		p := s.fit(pending(res, o.data))
		if p.Status == statusPending {
			s.start(res, o.run)
		}
		return p
	case <-ctx.Done():
		return fail(res, errDeadline)
	}
}

// parse mirrors tee-node's processorutils.Parse: size-capped, lenient JSON.
func parse[T any](msg []byte, limit int) (*T, error) {
	if len(msg) > limit {
		return nil, errMessageTooLarge
	}
	var v T
	if err := json.Unmarshal(msg, &v); err != nil {
		return nil, errInvalidMessage
	}
	return &v, nil
}

func writeJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	//nolint:errcheck,gosec // the response is already committed; nothing left to report to
	w.Write(body)
}

// defaultLogger calls go-flare-common's logger on each use, so a later logger.Set applies.
type defaultLogger struct{}

func (defaultLogger) Errorf(format string, args ...any) { logger.Errorf(format, args...) }
