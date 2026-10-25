package conformance

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/flare-foundation/fce-go-scaffold/pkg/extension"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// childVar makes the test binary act as the extension under test.
const childVar = "CONFORMANCE_TEST_EXTENSION"

func TestMain(m *testing.M) {
	switch os.Getenv(childVar) {
	case "":
		os.Exit(m.Run())
	case "exit":
		os.Exit(3)
	case "ignore-term":
		signal.Ignore(syscall.SIGTERM)
		os.Exit(serve(context.Background()))
	case "crash":
		fmt.Fprintln(os.Stderr, crash())
		os.Exit(1)
	default:
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
		defer stop()
		os.Exit(serve(ctx))
	}
}

func serve(ctx context.Context) int {
	env, err := extension.ReadEnv()
	if err == nil {
		err = goodExtension(extension.WithSignPort(env.SignPort)).ListenAndServe(ctx, env.ExtensionPort)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// crash answers GET /state, then exits with code 4 at the first action.
func crash() error {
	env, err := extension.ReadEnv()
	if err != nil {
		return err
	}
	s := &http.Server{
		Addr:              net.JoinHostPort("127.0.0.1", strconv.Itoa(env.ExtensionPort)),
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/action" {
				os.Exit(4)
			}
			goodExtension().ServeHTTP(w, r)
		}),
	}
	return s.ListenAndServe()
}

func TestRun(t *testing.T) {
	t.Parallel()
	rep := Run(t.Context(), Config{
		Command:  []string{os.Args[0]},
		Env:      []string{childVar + "=serve"},
		Fixtures: goodFixtures(),
	})
	require.NoError(t, rep.Err, "%s", rep.Output)
	assert.True(t, rep.OK(false))
	assert.Len(t, rep.Results, 3*len(goodFixtures())+3+1+11, "three direct fixtures, one has again")
}

func TestRunLifecycleFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, mode, reason string
		advice             bool // a convention broken, not a required rule
	}{
		{"exits at once", "exit", "exited before answering GET /state: exit code 3", false},
		{"ignores SIGTERM", "ignore-term", "still running 300ms after SIGTERM", true},
		{"crashes during the checks", "crash", "exited during the checks: exit code 4", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rep := Run(t.Context(), Config{
				Command:  []string{os.Args[0]},
				Env:      []string{childVar + "=" + tt.mode},
				Shutdown: 300 * time.Millisecond,
			})
			assert.False(t, rep.OK(false))
			if !tt.advice {
				require.ErrorContains(t, rep.Err, tt.reason)
				return
			}
			require.NoError(t, rep.Err)
			assert.True(t, rep.OK(true))
			last := rep.Results[len(rep.Results)-1]
			assert.Equal(t, "exit code 0 after SIGTERM", last.Name)
			require.ErrorContains(t, last.Advice, tt.reason)
		})
	}
}

func TestRunNoCommand(t *testing.T) {
	t.Parallel()
	assert.ErrorContains(t, Run(t.Context(), Config{}).Err, "no command")
}
