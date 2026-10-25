package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/flare-foundation/fce-go-scaffold/pkg/extension"
	"github.com/flare-foundation/fce-tooling/internal/fakenode"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	"github.com/flare-foundation/go-flare-common/pkg/tee/op"
	"github.com/flare-foundation/tee-node/pkg/types"
	"github.com/stretchr/testify/require"
)

const (
	testType      op.Type    = "TEST"
	echoCommand   op.Command = "ECHO"
	jobCommand    op.Command = "JOB"
	directCommand op.Command = "DIRECT_ECHO"
	onceCommand   op.Command = "ONCE"

	// customTag is a direct tag the extension added, for an adapted tee-proxy.
	customTag types.SubmissionTag = "custom"
)

// testPostWait bounds the posts check; a passing run returns as soon as the posts settle.
const testPostWait = 2 * time.Second

// goodExtension is a well-behaved extension: an instruction echo, a direct
// job that posts one partial and a final result, a direct echo on its own tag,
// and a direct echo that takes each message once.
func goodExtension(opts ...extension.Option) *extension.Server {
	return withJob(func(ctx context.Context, p *extension.Progress) ([]byte, error) {
		if err := p.Post(ctx, []byte("p1")); err != nil {
			return nil, err
		}
		return []byte("done"), nil
	}, opts...)
}

// withJob is goodExtension with run as its job.
func withJob(run extension.Run, opts ...extension.Option) *extension.Server {
	s := extension.New(append([]extension.Option{extension.WithLogger(logger.Nop{}), extension.WithDirectTags(customTag)}, opts...)...)
	s.RegisterInstructionHandler(testType, echoCommand, func(_ context.Context, i *instruction.DataFixed) ([]byte, extension.Run, error) {
		return i.OriginalMessage, nil, nil
	})
	s.RegisterDirectHandler(testType, jobCommand, func(context.Context, *types.DirectInstruction) ([]byte, extension.Run, error) {
		return []byte("ack"), run, nil
	})
	s.RegisterDirectHandler(testType, directCommand, echoDirect)
	var mu sync.Mutex
	seen := map[string]bool{}
	s.RegisterDirectHandler(testType, onceCommand, func(_ context.Context, d *types.DirectInstruction) ([]byte, extension.Run, error) {
		mu.Lock()
		defer mu.Unlock()
		if seen[string(d.Message)] {
			return nil, nil, extension.PublicError("seen", nil)
		}
		seen[string(d.Message)] = true
		return d.Message, nil, nil
	})
	return s
}

func echoDirect(_ context.Context, d *types.DirectInstruction) ([]byte, extension.Run, error) {
	return d.Message, nil, nil
}

// goodFixtures describe goodExtension.
func goodFixtures() []Fixture {
	return []Fixture{
		{
			Name: "echo", Description: "echoes", Channel: types.Instruction, OPType: testType, OPCommand: echoCommand,
			Message: []byte("hi"), Result: Expected{Status: 1, Log: "ok", Data: []byte("hi")},
		},
		{
			Name: "job", Description: "posts a partial, then the final", Channel: types.Direct, OPType: testType, OPCommand: jobCommand,
			Message: []byte("x"), Result: Expected{Status: 2, Log: "pending", Data: []byte("ack")},
			Posts: []Expected{{Status: 4, Log: "partial", Data: []byte("p1")}, {Status: 1, Log: "ok", Data: []byte("done")}},
		},
		{
			Name: "tagged", Description: "echoes on its own tag", Channel: types.Direct, SubmissionTag: customTag,
			OPType: testType, OPCommand: directCommand,
			Message: []byte("hey"), Result: Expected{Status: 1, Log: "ok", Data: []byte("hey")},
		},
		{
			Name: "once", Description: "takes each message once", Channel: types.Direct, OPType: testType, OPCommand: onceCommand,
			Message: []byte("m"), Result: Expected{Status: 1, Log: "ok", Data: []byte("m")},
			Again: &Expected{Status: 0, Log: "error: seen", Data: []byte{}},
		},
	}
}

// call is an /action request as a broken extension sees it; op is the pair in
// the message, zero when the message doesn't decode.
type call struct {
	body    []byte
	action  types.Action
	op      types.OpID
	decoded bool
}

// breaking serves next, except the /action calls f answers itself (returning true).
func breaking(next http.Handler, f func(w http.ResponseWriter, c call) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/action" {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "reading", http.StatusBadRequest)
				return
			}
			c := call{body: body}
			if json.Unmarshal(body, &c.action) == nil {
				c.decoded = json.Unmarshal(c.action.Data.Message, &c.op) == nil
			}
			if f(w, c) {
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		next.ServeHTTP(w, r)
	})
}

// accept answers c with status 1, as a handler that took it would.
func accept(w http.ResponseWriter, c call) {
	d := c.action.Data
	writeResult(w, types.ActionResult{
		ID: d.ID, SubmissionTag: d.SubmissionTag, Status: 1, Log: "ok",
		OPType: c.op.OPType, OPCommand: c.op.OPCommand, Version: resultVersion,
	})
}

// rewriting serves next, then lets edit change each 200 answer to /action.
func rewriting(next http.Handler, edit func(*types.ActionResult)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)
		var res types.ActionResult
		if r.URL.Path != "/action" || rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &res) != nil {
			maps.Copy(w.Header(), rec.Header())
			w.WriteHeader(rec.Code)
			w.Write(rec.Body.Bytes()) //nolint:errcheck,gosec // test fake
			return
		}
		edit(&res)
		writeResult(w, res)
	})
}

func writeResult(w http.ResponseWriter, res types.ActionResult) {
	b, err := json.Marshal(res)
	if err != nil {
		http.Error(w, "encoding", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(b) //nolint:errcheck,gosec // test fake
}

// target serves h as the extension and returns a checker for it, with the fake sign port it posts to.
func target(t *testing.T, h func(signPort int) http.Handler) *checker {
	t.Helper()
	node := fakenode.New()
	ns := httptest.NewServer(node)
	t.Cleanup(ns.Close)
	es := httptest.NewServer(h(port(ns.Listener.Addr())))
	t.Cleanup(es.Close)
	return newChecker(es.URL, node)
}

// results runs every check and returns them by name.
func results(t *testing.T, c *checker, fixtures []Fixture) map[string]Result {
	t.Helper()
	got := map[string]Result{}
	for _, r := range c.run(t.Context(), fixtures, testPostWait) {
		_, dup := got[r.Name]
		require.False(t, dup, "check %q twice", r.Name)
		got[r.Name] = r
	}
	return got
}

// postResult posts r to n's /result, as the extension would.
func postResult(t *testing.T, n *fakenode.Node, r types.ActionResult) {
	t.Helper()
	b, err := json.Marshal(r)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	n.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/result", bytes.NewReader(b)))
	require.Equal(t, http.StatusOK, rec.Code)
}
