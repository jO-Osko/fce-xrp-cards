package signport

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/tee-node/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resultSink serves POST /result, answering the scripted statuses in turn,
// then 200; it decodes as strictly as tee-node v0.0.29.
type resultSink struct {
	mu     sync.Mutex
	script []int
	bodies [][]byte
}

func (s *resultSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read", http.StatusBadRequest)
		return
	}
	var res types.ActionResult
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&res); err != nil {
		http.Error(w, "secret detail", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.bodies = append(s.bodies, body)
	code := http.StatusOK
	if len(s.script) > 0 {
		code, s.script = s.script[0], s.script[1:]
	}
	if code != http.StatusOK {
		http.Error(w, "secret detail", code)
	}
}

func (s *resultSink) posts() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bodies
}

// startFake serves h on l, or on a new loopback listener, and returns its port.
func startFake(t *testing.T, h http.Handler, l net.Listener) int {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	if l != nil {
		require.NoError(t, srv.Listener.Close())
		srv.Listener = l
	}
	srv.Start()
	t.Cleanup(srv.Close)
	return portOf(t, srv.Listener.Addr())
}

func portOf(t *testing.T, a net.Addr) int {
	t.Helper()
	tcp, ok := a.(*net.TCPAddr)
	require.True(t, ok)
	return tcp.Port
}

// freePort returns a loopback port nothing listens on.
func freePort(t *testing.T) int {
	t.Helper()
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	p := portOf(t, l.Addr())
	require.NoError(t, l.Close())
	return p
}

func testResult() *types.ActionResult {
	return &types.ActionResult{
		ID:            common.HexToHash("0x01"),
		SubmissionTag: types.Threshold,
		Status:        4,
		Log:           "ok",
		OPType:        common.HexToHash("0x02"),
		OPCommand:     common.HexToHash("0x03"),
		Version:       "1.0.0",
		Data:          []byte("partial"),
	}
}

func TestPostResult(t *testing.T) {
	t.Parallel()
	sink := &resultSink{}
	c := New(startFake(t, sink, nil))

	r := testResult()
	require.NoError(t, c.PostResult(t.Context(), r))

	want, err := json.Marshal(r)
	require.NoError(t, err)
	require.Len(t, sink.posts(), 1)
	assert.JSONEq(t, string(want), string(sink.posts()[0]))
}

// The node answers 500 without a proxy URL and 502 when the proxy didn't take it.
func TestPostResultRetries5xx(t *testing.T) {
	t.Parallel()
	sink := &resultSink{script: []int{http.StatusInternalServerError, http.StatusBadGateway}}
	c := New(startFake(t, sink, nil))

	require.NoError(t, c.PostResult(t.Context(), testResult()))
	posts := sink.posts()
	require.Len(t, posts, 3)
	assert.Equal(t, posts[0], posts[2], "the same result each time")
}

func TestPostResultGivesUp(t *testing.T) {
	t.Parallel()
	sink := &resultSink{script: []int{502, 502, 502, 502, 502, 502, 502, 502, 502, 502, 502, 502}}
	c := New(startFake(t, sink, nil))

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	err := c.PostResult(ctx, testResult())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ErrorContains(t, err, "sign port answered 502")
	assert.NotContains(t, err.Error(), "secret")
	assert.NotContains(t, err.Error(), "\n", "logged as one line")
}

func TestPostResultBadRequestNotRetried(t *testing.T) {
	t.Parallel()
	sink := &resultSink{script: []int{http.StatusBadRequest}}
	c := New(startFake(t, sink, nil))

	require.ErrorIs(t, c.PostResult(t.Context(), testResult()), ErrBadRequest)
	assert.Len(t, sink.posts(), 1)
}

// Only result posts are safe to repeat; other calls fail on the first 5xx.
func TestCallDoesNotRetry5xx(t *testing.T) {
	t.Parallel()
	var calls int
	var mu sync.Mutex
	c := New(startFake(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		http.Error(w, "secret detail", http.StatusInternalServerError)
	}), nil))

	err := c.Call(t.Context(), "/sign", struct{}{}, &struct{}{})
	require.ErrorContains(t, err, "sign port answered 500")
	assert.NotContains(t, err.Error(), "secret")
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, calls)
}

// A dropped connection may have reached the node; only a refused one is retried.
func TestDroppedConnectionNotRetried(t *testing.T) {
	t.Parallel()
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	var accepts atomic.Int32
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			accepts.Add(1)
			_ = conn.Close()
		}
	}()

	c := New(portOf(t, l.Addr()))
	require.ErrorContains(t, c.Call(t.Context(), "/sign", struct{}{}, &struct{}{}), "calling the sign port")
	require.ErrorContains(t, c.PostResult(t.Context(), testResult()), "calling the sign port")
	assert.Equal(t, int32(2), accepts.Load())
}

func TestRetriesUntilListening(t *testing.T) {
	t.Parallel()
	port := freePort(t)
	c := New(port)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.PostResult(ctx, testResult()) }()

	time.Sleep(200 * time.Millisecond)
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	require.NoError(t, err)
	sink := &resultSink{}
	startFake(t, sink, l)

	require.NoError(t, <-done)
	assert.Len(t, sink.posts(), 1)
}

func TestGivesUpAtDeadline(t *testing.T) {
	t.Parallel()
	c := New(freePort(t))

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	err := c.Call(ctx, "/sign", struct{}{}, &struct{}{})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ErrorContains(t, err, "not listening")
}

func TestGet(t *testing.T) {
	t.Parallel()
	c := New(startFake(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if r.Method != http.MethodGet || r.URL.Path != "/key-info/a/0" || err != nil || len(body) > 0 {
			http.Error(w, "unexpected request", http.StatusTeapot)
			return
		}
		//nolint:errcheck,gosec // test fake
		w.Write([]byte(`{"KeyId":7}`))
	}), nil))

	var out struct {
		KeyID uint64 `json:"KeyId"`
	}
	require.NoError(t, c.Get(t.Context(), "/key-info/a/0", &out))
	assert.Equal(t, uint64(7), out.KeyID)
}

func TestNotFound(t *testing.T) {
	t.Parallel()
	c := New(startFake(t, http.NotFoundHandler(), nil))

	require.ErrorIs(t, c.Get(t.Context(), "/key-info/a/0", &struct{}{}), ErrNotFound)
	require.ErrorIs(t, c.Call(t.Context(), "/sign/a/0", struct{}{}, &struct{}{}), ErrNotFound)
}
