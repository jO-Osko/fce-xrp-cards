package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	"github.com/flare-foundation/go-flare-common/pkg/tee/op"
	"github.com/flare-foundation/tee-node/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePoster stands in for the sign port's /result; block holds each post until ctx ends.
type fakePoster struct {
	block  bool
	posted chan types.ActionResult
}

func newFakePoster(block bool) *fakePoster {
	return &fakePoster{block: block, posted: make(chan types.ActionResult, 300)}
}

func (f *fakePoster) PostResult(ctx context.Context, r *types.ActionResult) error {
	if f.block {
		<-ctx.Done()
		return fmt.Errorf("giving up: %w", ctx.Err())
	}
	f.posted <- *r
	return nil
}

func (f *fakePoster) next(t *testing.T) types.ActionResult {
	t.Helper()
	select {
	case r := <-f.posted:
		return r
	case <-time.After(5 * time.Second):
		require.FailNow(t, "no result posted")
		return types.ActionResult{}
	}
}

type recordLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *recordLogger) Errorf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *recordLogger) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func newJobServer(t *testing.T, p resultPoster, opts ...Option) *Server {
	t.Helper()
	s := New(append([]Option{WithLogger(logger.Nop{})}, opts...)...)
	s.poster = p
	t.Cleanup(s.stopJobs)
	return s
}

const jobCommand op.Command = "JOB"

// jobHandlers answer "ack" and run j in the background.
func jobHandlers(j Run) (InstructionHandler, DirectHandler) {
	return func(context.Context, *instruction.DataFixed) ([]byte, Run, error) { return []byte("ack"), j, nil },
		func(context.Context, *types.DirectInstruction) ([]byte, Run, error) { return []byte("ack"), j, nil }
}

func TestLongRunning(t *testing.T) {
	t.Parallel()
	job := func(ctx context.Context, p *Progress) ([]byte, error) {
		for _, d := range []string{"p1", "p2"} {
			if err := p.Post(ctx, []byte(d)); err != nil {
				return nil, err
			}
		}
		return []byte("done"), nil
	}
	ih, dh := jobHandlers(job)

	for _, direct := range []bool{false, true} {
		t.Run(fmt.Sprintf("direct=%v", direct), func(t *testing.T) {
			t.Parallel()
			fp := newFakePoster(false)
			s := newJobServer(t, fp)
			var body []byte
			tag := types.Threshold
			if direct {
				s.RegisterDirectHandler(testType, jobCommand, dh)
				body, tag = directAction(t, testType, jobCommand, nil), types.Submit
			} else {
				s.RegisterInstructionHandler(testType, jobCommand, ih)
				body = instructionAction(t, testType, jobCommand, nil)
			}

			res := postAction(t, s, body)
			assert.Equal(t, uint8(statusPending), res.Status)
			assert.Equal(t, "pending", res.Log)
			assert.Equal(t, "ack", string(res.Data))

			for _, want := range []struct {
				status uint8
				log    string
				data   string
			}{{4, "partial", "p1"}, {5, "partial", "p2"}, {1, "ok", "done"}} {
				r := fp.next(t)
				assert.Equal(t, want.status, r.Status)
				assert.Equal(t, want.log, r.Log)
				assert.Equal(t, want.data, string(r.Data))
				assert.Equal(t, testID, r.ID)
				assert.Equal(t, tag, r.SubmissionTag)
				assert.Equal(t, testType, op.HashToOPType(r.OPType))
				assert.Equal(t, jobCommand, op.HashToOPCommand(r.OPCommand))
				assert.Equal(t, resultVersion, r.Version)
			}
		})
	}
}

func TestJobFinalFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		job  Run
		log  string
	}{
		{"error", func(context.Context, *Progress) ([]byte, error) { return nil, errors.New("boom") }, "error: boom"},
		{"panic", func(context.Context, *Progress) ([]byte, error) { panic("secret detail") }, "error: internal error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fp := newFakePoster(false)
			s := newJobServer(t, fp)
			ih, _ := jobHandlers(tt.job)
			s.RegisterInstructionHandler(testType, jobCommand, ih)

			res := postAction(t, s, instructionAction(t, testType, jobCommand, nil))
			require.Equal(t, uint8(statusPending), res.Status)
			r := fp.next(t)
			assert.Equal(t, uint8(statusFailed), r.Status)
			assert.Equal(t, tt.log, r.Log)
			assert.Empty(t, r.Data)
		})
	}
}

// Every way the quick part can fail leaves the Run unstarted.
func TestRunNotStarted(t *testing.T) {
	t.Parallel()
	ack := func(job Run) InstructionHandler {
		ih, _ := jobHandlers(job)
		return ih
	}
	tests := []struct {
		name       string
		handler    func(job Run) InstructionHandler
		noSignPort bool
		timeout    time.Duration
		opts       []Option
		log        string
	}{
		{name: "precheck error", handler: func(job Run) InstructionHandler {
			return func(context.Context, *instruction.DataFixed) ([]byte, Run, error) {
				return nil, job, errors.New("precheck failed")
			}
		}, log: "error: precheck failed"},
		{name: "no sign port", handler: ack, noSignPort: true, log: "error: long-running mode needs the sign port"},
		{name: "deadline", handler: func(job Run) InstructionHandler {
			return func(ctx context.Context, _ *instruction.DataFixed) ([]byte, Run, error) {
				<-ctx.Done()
				return nil, job, nil
			}
		}, timeout: 20 * time.Millisecond, log: "error: deadline exceeded"},
		{name: "pending answer too large", handler: func(job Run) InstructionHandler {
			return func(context.Context, *instruction.DataFixed) ([]byte, Run, error) {
				return bytes.Repeat([]byte("x"), 1000), job, nil
			}
		}, opts: []Option{WithMaxResultSize(1000)}, log: "error: result too large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var poster resultPoster = newFakePoster(false)
			if tt.noSignPort {
				poster = nil
			}
			s := newJobServer(t, poster, tt.opts...)
			if tt.timeout > 0 {
				s.timeout = tt.timeout
			}
			var started atomic.Bool
			s.RegisterInstructionHandler(testType, jobCommand, tt.handler(func(context.Context, *Progress) ([]byte, error) {
				started.Store(true)
				return nil, nil
			}))

			res := postAction(t, s, instructionAction(t, testType, jobCommand, nil))
			assert.Equal(t, uint8(statusFailed), res.Status)
			assert.Equal(t, tt.log, res.Log)
			time.Sleep(50 * time.Millisecond)
			assert.False(t, started.Load())
		})
	}
}

func TestPartialLimit(t *testing.T) {
	t.Parallel()
	fp := newFakePoster(false)
	s := newJobServer(t, fp)
	errs := make(chan error, 1)
	ih, _ := jobHandlers(func(ctx context.Context, p *Progress) ([]byte, error) {
		for range lastPartial - firstPartial + 1 {
			if err := p.Post(ctx, nil); err != nil {
				return nil, err
			}
		}
		errs <- p.Post(ctx, nil)
		return []byte("done"), nil
	})
	s.RegisterInstructionHandler(testType, jobCommand, ih)

	postAction(t, s, instructionAction(t, testType, jobCommand, nil))
	for want := firstPartial; want <= lastPartial; want++ {
		require.Equal(t, uint8(want), fp.next(t).Status)
	}
	require.ErrorIs(t, <-errs, errNoStatusLeft)
	assert.Equal(t, uint8(statusOK), fp.next(t).Status, "the final still goes out")
}

// Posts from the job's own goroutines are numbered in call order, each status once.
func TestConcurrentPosts(t *testing.T) {
	t.Parallel()
	fp := newFakePoster(false)
	s := newJobServer(t, fp)
	ih, _ := jobHandlers(func(ctx context.Context, p *Progress) ([]byte, error) {
		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() { _ = p.Post(ctx, nil) })
		}
		wg.Wait()
		return nil, nil
	})
	s.RegisterInstructionHandler(testType, jobCommand, ih)

	postAction(t, s, instructionAction(t, testType, jobCommand, nil))
	seen := map[uint8]bool{}
	for range 10 {
		seen[fp.next(t).Status] = true
	}
	for st := firstPartial; st < firstPartial+10; st++ {
		assert.True(t, seen[uint8(st)], "status %d", st)
	}
	assert.Equal(t, uint8(statusOK), fp.next(t).Status)
}

func TestPostAfterFinish(t *testing.T) {
	t.Parallel()
	fp := newFakePoster(false)
	s := newJobServer(t, fp)
	progress := make(chan *Progress, 1)
	ih, _ := jobHandlers(func(_ context.Context, p *Progress) ([]byte, error) {
		progress <- p
		return nil, nil
	})
	s.RegisterInstructionHandler(testType, jobCommand, ih)

	postAction(t, s, instructionAction(t, testType, jobCommand, nil))
	require.Equal(t, uint8(statusOK), fp.next(t).Status)
	require.ErrorIs(t, (<-progress).Post(t.Context(), nil), errJobDone)
}

func TestResultSize(t *testing.T) {
	t.Parallel()
	big := bytes.Repeat([]byte("x"), 1000) // hex doubles it past the 1000-byte limit

	t.Run("immediate", func(t *testing.T) {
		t.Parallel()
		s := newJobServer(t, newFakePoster(false), WithMaxResultSize(1000))
		s.RegisterInstructionHandler(testType, "BIG", func(context.Context, *instruction.DataFixed) ([]byte, Run, error) { return big, nil, nil })
		s.RegisterInstructionHandler(testType, "LOUD", func(context.Context, *instruction.DataFixed) ([]byte, Run, error) {
			return nil, nil, errors.New(string(big))
		})
		s.RegisterInstructionHandler(testType, "SMALL", func(context.Context, *instruction.DataFixed) ([]byte, Run, error) {
			return []byte("x"), nil, nil
		})

		for _, c := range []op.Command{"BIG", "LOUD", "SMALL"} {
			res := postAction(t, s, instructionAction(t, testType, c, nil))
			if c == "SMALL" {
				assert.Equal(t, uint8(statusOK), res.Status, "a small result passes")
				continue
			}
			assert.Equal(t, uint8(statusFailed), res.Status, c)
			assert.Equal(t, "error: result too large", res.Log, c)
			assert.Empty(t, res.Data, c)
		}
	})

	t.Run("partial and final", func(t *testing.T) {
		t.Parallel()
		fp := newFakePoster(false)
		s := newJobServer(t, fp, WithMaxResultSize(1000))
		errs := make(chan error, 1)
		ih, _ := jobHandlers(func(ctx context.Context, p *Progress) ([]byte, error) {
			errs <- p.Post(ctx, big)
			return big, nil
		})
		s.RegisterInstructionHandler(testType, jobCommand, ih)

		postAction(t, s, instructionAction(t, testType, jobCommand, nil))
		require.ErrorIs(t, <-errs, ErrResultTooLarge)
		r := fp.next(t)
		assert.Equal(t, uint8(statusFailed), r.Status, "the oversized partial isn't posted; the final fails")
		assert.Equal(t, "error: result too large", r.Log)
	})
}

// The size check matches the body the node posts, byte for byte.
func TestResultSizeExact(t *testing.T) {
	t.Parallel()
	r := types.ActionResult{ID: testID, Status: statusOK, Log: "error: \"quoted\"\n<tag>", Version: resultVersion, Data: []byte("data")}
	b, err := json.Marshal(types.ActionResponse{Result: r, Signature: make([]byte, crypto.SignatureLength)})
	require.NoError(t, err)

	assert.False(t, newJobServer(t, nil, WithMaxResultSize(len(b))).tooLarge(&r))
	assert.True(t, newJobServer(t, nil, WithMaxResultSize(len(b)-1)).tooLarge(&r))
}

func TestPostWindowDropsAndLogs(t *testing.T) {
	t.Parallel()
	log := &recordLogger{}
	s := newJobServer(t, newFakePoster(true), WithLogger(log), WithPostWindow(20*time.Millisecond))
	errs := make(chan error, 1)
	ih, _ := jobHandlers(func(ctx context.Context, p *Progress) ([]byte, error) {
		errs <- p.Post(ctx, []byte("secret partial"))
		return []byte("secret final"), nil
	})
	s.RegisterInstructionHandler(testType, jobCommand, ih)

	postAction(t, s, instructionAction(t, testType, jobCommand, nil))
	require.ErrorIs(t, <-errs, context.DeadlineExceeded)
	require.Eventually(t, func() bool { return strings.Contains(log.text(), "dropping status 1") }, 5*time.Second, 10*time.Millisecond)
	assert.Contains(t, log.text(), "dropping status 4 for action "+testID.Hex())
	assert.NotContains(t, log.text(), "secret")
}

// Shutdown stops a post even when the job posts under its own context.
func TestShutdownStopsPosts(t *testing.T) {
	t.Parallel()
	s := newJobServer(t, newFakePoster(true))
	errs := make(chan error, 1)
	ih, _ := jobHandlers(func(_ context.Context, p *Progress) ([]byte, error) {
		errs <- p.Post(context.Background(), nil)
		return nil, nil
	})
	s.RegisterInstructionHandler(testType, jobCommand, ih)

	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- s.Serve(ctx, l) }()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+l.Addr().String()+"/action",
		bytes.NewReader(instructionAction(t, testType, jobCommand, nil)))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	cancel()
	require.NoError(t, <-served)
	select {
	case err := <-errs:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the post outlived shutdown")
	}
}

// WithSignPort wires the real sign-port client; the fake decodes as strictly as tee-node.
func TestWithSignPort(t *testing.T) {
	t.Parallel()
	posted := make(chan types.ActionResult, 2)
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var res types.ActionResult
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if r.URL.Path != "/result" || dec.Decode(&res) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		posted <- res
	}))
	t.Cleanup(node.Close)
	tcp, ok := node.Listener.Addr().(*net.TCPAddr)
	require.True(t, ok)

	s := New(WithLogger(logger.Nop{}), WithSignPort(tcp.Port))
	t.Cleanup(s.stopJobs)
	ih, _ := jobHandlers(func(ctx context.Context, p *Progress) ([]byte, error) {
		if err := p.Post(ctx, []byte("p1")); err != nil {
			return nil, err
		}
		return []byte("done"), nil
	})
	s.RegisterInstructionHandler(testType, jobCommand, ih)

	require.Equal(t, uint8(statusPending), postAction(t, s, instructionAction(t, testType, jobCommand, nil)).Status)
	for _, want := range []uint8{4, statusOK} {
		select {
		case r := <-posted:
			assert.Equal(t, want, r.Status)
			assert.Equal(t, testID, r.ID)
		case <-time.After(5 * time.Second):
			require.FailNow(t, "no result posted")
		}
	}
}
