package conformance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/flare-foundation/fce-tooling/internal/fakenode"
)

// Defaults for Config.
const (
	DefaultStartup  = 10 * time.Second
	DefaultShutdown = 5 * time.Second
	// DefaultPostWait bounds the wait for the posts long-running fixtures expect.
	DefaultPostWait = 10 * time.Second
)

// Config is one conformance run.
type Config struct {
	// Command starts the extension: the program and its arguments.
	Command []string
	// Env holds the extension's own variables, as KEY=VALUE; the runner sets
	// EXTENSION_PORT and SIGN_PORT itself.
	Env      []string
	Fixtures []Fixture
	// Startup bounds the wait for /state to answer, Shutdown the wait for the
	// exit after SIGTERM, PostWait the wait for posts; zero takes the default.
	Startup  time.Duration
	Shutdown time.Duration
	PostWait time.Duration
}

// Report is a run's outcome.
type Report struct {
	Results []Result
	// Err is set when the extension didn't start, or exited during the checks.
	Err error
	// Output is the extension's stdout and stderr.
	Output []byte
}

// OK reports whether the extension started and passed every required check and, unless relaxed, every convention.
func (r Report) OK(relaxed bool) bool {
	if r.Err != nil {
		return false
	}
	for _, res := range r.Results {
		if res.Err != nil || (!relaxed && res.Advice != nil) {
			return false
		}
	}
	return true
}

// Run starts the extension on free loopback ports with a fake sign port,
// runs every check, then stops it with SIGTERM and checks for exit code 0.
// Cancelling ctx kills the extension.
func Run(ctx context.Context, cfg Config) Report {
	if len(cfg.Command) == 0 {
		return Report{Err: errors.New("no command")}
	}
	startup := orDefault(cfg.Startup, DefaultStartup)
	shutdown := orDefault(cfg.Shutdown, DefaultShutdown)
	postWait := orDefault(cfg.PostWait, DefaultPostWait)

	node := fakenode.New()
	var lc net.ListenConfig
	nl, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return Report{Err: fmt.Errorf("starting the fake sign port: %w", err)}
	}
	ns := &http.Server{Handler: node, ReadHeaderTimeout: 5 * time.Second}
	go ns.Serve(nl)  //nolint:errcheck // returns once Close below runs
	defer ns.Close() //nolint:errcheck // the run is over; nothing left to report to

	extPort, err := freePort(ctx)
	if err != nil {
		return Report{Err: err}
	}

	var out bytes.Buffer
	p, err := start(cfg.Command, environ(cfg.Env, extPort, port(nl.Addr())), &out)
	if err != nil {
		return Report{Err: err}
	}

	var rep Report
	url := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(extPort))
	if err := p.waitReady(ctx, url, startup); err != nil {
		p.kill()
		rep.Err = err
	} else {
		rep.Results = newChecker(url, node).run(ctx, cfg.Fixtures, postWait)
		var stopErr error
		if rep.Err, stopErr = p.stop(ctx, shutdown); rep.Err == nil {
			rep.Results = append(rep.Results, Result{Name: "exit code 0 after SIGTERM", Advice: stopErr})
		}
	}
	rep.Output = out.Bytes()
	return rep
}

// process is the running extension; exited closes once Wait returns, with err set.
type process struct {
	cmd    *exec.Cmd
	exited chan struct{}
	err    error
}

func start(command, env []string, out *bytes.Buffer) (*process, error) {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env = env
	// one writer: exec then writes from a single goroutine
	cmd.Stdout, cmd.Stderr = out, out
	// a child that keeps the output open can't hold up Wait
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting the extension: %w", err)
	}
	p := &process{cmd: cmd, exited: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		close(p.exited)
	}()
	return p, nil
}

func (p *process) waitReady(ctx context.Context, url string, limit time.Duration) error {
	client := &http.Client{Timeout: actionTimeout}
	deadline := time.After(limit)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/state", nil)
		if err != nil {
			return fmt.Errorf("building the request: %w", err)
		}
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close() //nolint:errcheck,gosec // only the status matters
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.exited:
			return fmt.Errorf("the extension exited before answering GET /state: %s", exitText(p.err))
		case <-deadline:
			return fmt.Errorf("no 200 on GET /state within %s", limit)
		case <-time.After(5 * pollInterval):
		}
	}
}

// stop sends SIGTERM and waits for the exit, at most limit, then kills the extension.
// crash is set when it had exited already, or the run was cancelled; exit when it didn't exit with code 0.
func (p *process) stop(ctx context.Context, limit time.Duration) (crash, exit error) {
	select {
	case <-p.exited:
		return fmt.Errorf("the extension exited during the checks: %s", exitText(p.err)), nil
	default:
	}
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		p.kill()
		if errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("the extension exited during the checks: %s", exitText(p.err)), nil
		}
		return fmt.Errorf("sending SIGTERM: %w", err), nil
	}
	select {
	case <-p.exited:
		if p.err != nil {
			return nil, fmt.Errorf("after SIGTERM: %s, want exit code 0", exitText(p.err))
		}
		return nil, nil
	case <-time.After(limit):
		p.kill()
		return nil, fmt.Errorf("still running %s after SIGTERM", limit)
	case <-ctx.Done():
		p.kill()
		return ctx.Err(), nil
	}
}

// kill stops the extension, if still running, and waits for it.
func (p *process) kill() {
	p.cmd.Process.Kill() //nolint:errcheck,gosec // it may have exited already
	<-p.exited
}

// exitText describes how the extension ended, given Wait's error.
func exitText(err error) string {
	var ee *exec.ExitError
	switch {
	case err == nil:
		return "exit code 0"
	case errors.As(err, &ee) && ee.ExitCode() >= 0:
		return fmt.Sprintf("exit code %d", ee.ExitCode())
	default:
		return err.Error()
	}
}

// environ passes the runner's environment, the extension's own variables, then the ports.
func environ(extra []string, extPort, signPort int) []string {
	env := make([]string, 0, len(os.Environ())+len(extra)+2)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "EXTENSION_PORT=") && !strings.HasPrefix(kv, "SIGN_PORT=") {
			env = append(env, kv)
		}
	}
	env = append(env, extra...)
	return append(env, "EXTENSION_PORT="+strconv.Itoa(extPort), "SIGN_PORT="+strconv.Itoa(signPort))
}

// freePort returns a loopback port nothing listens on. Another process may take
// it before the extension binds it; the extension then fails to start, loudly.
func freePort(ctx context.Context) (int, error) {
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("finding a free port: %w", err)
	}
	p := port(l.Addr())
	if err := l.Close(); err != nil {
		return 0, fmt.Errorf("finding a free port: %w", err)
	}
	return p, nil
}

func port(a net.Addr) int {
	if tcp, ok := a.(*net.TCPAddr); ok {
		return tcp.Port
	}
	return 0
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}
