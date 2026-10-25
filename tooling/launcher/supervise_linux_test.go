package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The launcher reaps every child of its process, so these tests run it in a
// subprocess: this test binary, re-run as the launcher, the fake tee-node and
// the fake extension.

// nodeModeVar selects the fake tee-node's mode; tee-node gets the whole environment.
const nodeModeVar = "LAUNCHER_TEST_NODE"

const waitLimit = 10 * time.Second

func TestMain(m *testing.M) {
	switch {
	case len(os.Args) > 1 && os.Args[1] == "launcher":
		os.Exit(run(os.Args[2:]))
	case len(os.Args) > 2 && os.Args[1] == "fake-extension":
		fake("extension", os.Args[2])
	case len(os.Args) == 1 && os.Getenv(nodeModeVar) != "":
		fake("node", os.Getenv(nodeModeVar))
	}
	os.Exit(m.Run())
}

// fake acts as a child. It prints "<role>: ready", then by mode:
//   - die: has no signal handler, so a stop signal ends it, as tee-node v0.0.29;
//   - obey: exits 0 on SIGTERM or SIGINT, as the template;
//   - fail: exits 1 on SIGTERM or SIGINT;
//   - ignore: ignores SIGTERM and SIGINT;
//   - exit0, exit3: exits at once with that code;
//   - env, nnp: prints its environment or its NoNewPrivs line first, then obeys.
func fake(role, mode string) {
	out := func(s string) { fmt.Printf("%s: %s\n", role, s) }
	switch mode {
	case "env":
		for _, kv := range os.Environ() {
			out("env " + kv)
		}
		mode = "obey"
	case "nnp":
		out(statusLine("NoNewPrivs:"))
		mode = "obey"
	}

	sigs := make(chan os.Signal, 1)
	switch mode {
	case "obey", "fail":
		signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	case "ignore":
		signal.Ignore(syscall.SIGTERM, syscall.SIGINT)
	}
	out("ready")

	switch mode {
	case "exit0":
		os.Exit(0)
	case "exit3":
		os.Exit(3)
	case "obey", "fail":
		out("got " + (<-sigs).String())
		if mode == "fail" {
			os.Exit(1)
		}
		os.Exit(0)
	}
	time.Sleep(time.Hour) // die, ignore: until a signal or SIGKILL ends it
	os.Exit(0)
}

func statusLine(prefix string) string {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return err.Error()
	}
	for l := range strings.Lines(string(b)) {
		if strings.HasPrefix(l, prefix) {
			return strings.Join(strings.Fields(l), " ")
		}
	}
	return prefix + " missing"
}

// setup is one launcher run: the fake children's modes, and overrides.
type setup struct {
	node, ext string
	nodePath  string   // tee-node's binary; this test binary when empty
	command   []string // the extension's command; this test binary in mode ext when empty
	env       []string // added to the launcher's environment
	flags     []string
	// grace is the launcher's --grace; 5s when empty, since a race-built child sleeps 1s at exit
	grace string
}

// launched is a running launcher; lines carries its output and its children's.
type launched struct {
	cmd   *exec.Cmd
	lines chan string
	seen  []string
}

func launch(t *testing.T, s setup) *launched {
	t.Helper()
	if os.Geteuid() == 0 || os.Getgid() == 0 {
		t.Skip("needs a non-root user and group; the image smoke test covers the drop from root")
	}
	if s.nodePath == "" {
		s.nodePath = os.Args[0]
	}
	if s.command == nil {
		s.command = []string{os.Args[0], "fake-extension", s.ext}
	}
	if s.grace == "" {
		s.grace = "5s"
	}
	args := []string{"launcher", "--node", s.nodePath,
		"--uid", strconv.Itoa(os.Getuid()), "--gid", strconv.Itoa(os.Getgid()), "--grace", s.grace}
	args = append(append(append(args, s.flags...), "--"), s.command...)

	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(append(baseEnv(), nodeModeVar+"="+s.node), s.env...)
	// its own process group, so cleanup also ends children a failed test leaves behind
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	w.Close() //nolint:errcheck,gosec // the children hold their own copies
	t.Cleanup(func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) //nolint:errcheck,gosec // the group is usually gone already
		r.Close()                                       //nolint:errcheck,gosec // reading is over
	})

	lines := make(chan string, 256)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	return &launched{cmd: cmd, lines: lines}
}

// baseEnv is this process's environment without the variables the tests set.
func baseEnv() []string {
	drop := append(slices.Clone(nodeVars), nodeModeVar, "MY_VAR", "OTHER", "ABSENT")
	return slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.Contains(drop, name)
	})
}

// waitFor reads output until every line in want has appeared.
func (l *launched) waitFor(t *testing.T, want ...string) {
	t.Helper()
	timeout := time.After(waitLimit)
	for !l.saw(want...) {
		select {
		case line, ok := <-l.lines:
			if !ok {
				t.Fatalf("output ended before %q; output:\n%s", want, strings.Join(l.seen, "\n"))
			}
			l.seen = append(l.seen, line)
		case <-timeout:
			t.Fatalf("no %q within %s; output:\n%s", want, waitLimit, strings.Join(l.seen, "\n"))
		}
	}
}

func (l *launched) saw(want ...string) bool {
	for _, w := range want {
		if !slices.Contains(l.seen, w) {
			return false
		}
	}
	return true
}

// finish waits for the launcher and all its output, and returns its exit code.
func (l *launched) finish(t *testing.T) int {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- l.cmd.Wait() }()
	timeout := time.After(waitLimit)
	var err error
	for waiting := true; waiting || l.lines != nil; {
		select {
		case err = <-done:
			waiting = false
		case line, ok := <-l.lines:
			if !ok {
				l.lines = nil
				continue
			}
			l.seen = append(l.seen, line)
		case <-timeout:
			t.Fatalf("still running after %s; output:\n%s", waitLimit, strings.Join(l.seen, "\n"))
		}
	}
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee):
		return ee.ExitCode()
	default:
		t.Fatal(err)
		return -1
	}
}

func (l *launched) signal(t *testing.T, sig syscall.Signal) {
	t.Helper()
	if err := l.cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
}

// expect checks the exit code and that each line in want appeared and none in absent did.
func (l *launched) expect(t *testing.T, code, want int, lines []string, absent ...string) {
	t.Helper()
	out := strings.Join(l.seen, "\n")
	if code != want {
		t.Errorf("exit code %d, want %d", code, want)
	}
	for _, line := range lines {
		if !slices.Contains(l.seen, line) {
			t.Errorf("no line %q", line)
		}
	}
	for _, a := range absent {
		if strings.Contains(out, a) {
			t.Errorf("unexpected %q", a)
		}
	}
	if t.Failed() {
		t.Logf("output:\n%s", out)
	}
}

func TestRequestedStop(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		node, ext string
		grace     string
		sig       syscall.Signal
		code      int
		lines     []string
		absent    []string
	}{
		{
			name: "both obey SIGTERM", node: "die", ext: "obey", sig: syscall.SIGTERM, code: exitOK,
			lines:  []string{"launcher: stopping: received terminated", "extension: got terminated", "launcher: tee-node ended by signal: terminated", "launcher: extension exited with code 0"},
			absent: []string{"killing"},
		},
		{
			name: "both obey SIGINT", node: "die", ext: "obey", sig: syscall.SIGINT, code: exitOK,
			lines:  []string{"extension: got interrupt", "launcher: tee-node ended by signal: interrupt"},
			absent: []string{"killing"},
		},
		{
			name: "tee-node fails", node: "fail", ext: "obey", sig: syscall.SIGTERM, code: exitFailure,
			lines: []string{"node: got terminated", "launcher: tee-node exited with code 1"},
		},
		{
			name: "the extension ignores the stop", node: "die", ext: "ignore", grace: "300ms", sig: syscall.SIGTERM, code: exitFailure,
			lines: []string{"launcher: killing extension: still running 300ms after the stop", "launcher: extension ended by signal: killed"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := launch(t, setup{node: tt.node, ext: tt.ext, grace: tt.grace})
			l.waitFor(t, "node: ready", "extension: ready")
			l.signal(t, tt.sig)
			l.expect(t, l.finish(t), tt.code, tt.lines, tt.absent...)
		})
	}
}

// A child that ends on its own stops the other; the launcher then fails, whatever the child's code.
func TestChildEnds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, node, ext, ended string
	}{
		{"the extension exits 0", "die", "exit0", "extension exited with code 0"},
		{"the extension exits 3", "die", "exit3", "extension exited with code 3"},
		{"tee-node exits 3", "exit3", "obey", "tee-node exited with code 3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := launch(t, setup{node: tt.node, ext: tt.ext})
			l.expect(t, l.finish(t), exitFailure, []string{"launcher: stopping: " + tt.ended}, "killing")
		})
	}
}

func TestStartFails(t *testing.T) {
	t.Parallel()
	missing := "/nonexistent/binary"
	t.Run("tee-node", func(t *testing.T) {
		t.Parallel()
		l := launch(t, setup{nodePath: missing, ext: "obey"})
		l.expect(t, l.finish(t), exitFailure, nil, "extension: ready")
		if !strings.Contains(strings.Join(l.seen, "\n"), "launcher: starting tee-node: ") {
			t.Errorf("no start error; output:\n%s", strings.Join(l.seen, "\n"))
		}
	})
	t.Run("extension", func(t *testing.T) {
		t.Parallel()
		l := launch(t, setup{node: "die", command: []string{missing}})
		l.expect(t, l.finish(t), exitFailure, []string{"launcher: stopping: the extension didn't start"}, "killing")
	})
}

func TestEnvironment(t *testing.T) {
	t.Parallel()
	l := launch(t, setup{
		node: "env", ext: "env",
		env:   []string{"EXTENSION_PORT=1", "SIGN_PORT=2", "LOG_LEVEL=", "EXTENSION_ID=0x01", "MY_VAR=x", "OTHER=y"},
		flags: []string{"--env", "MY_VAR,ABSENT"},
	})
	l.waitFor(t, "node: ready", "extension: ready")

	var ext []string
	for _, line := range l.seen {
		if kv, ok := strings.CutPrefix(line, "extension: env "); ok {
			ext = append(ext, kv)
		}
	}
	// CHAIN_ID and ABSENT unset: left out; LOG_LEVEL set but empty: passed; OTHER and the rest: not listed
	want := []string{"EXTENSION_PORT=1", "SIGN_PORT=2", "LOG_LEVEL=", "EXTENSION_ID=0x01", "MY_VAR=x"}
	if !slices.Equal(ext, want) {
		t.Errorf("the extension's environment is %q, want %q", ext, want)
	}
	if !l.saw("node: env OTHER=y", "node: env MY_VAR=x") {
		t.Error("tee-node didn't get the whole environment")
	}

	l.signal(t, syscall.SIGTERM)
	l.expect(t, l.finish(t), exitOK, nil)
}

func TestNoNewPrivs(t *testing.T) {
	t.Parallel()
	l := launch(t, setup{node: "nnp", ext: "nnp"})
	l.waitFor(t, "node: NoNewPrivs: 1", "extension: NoNewPrivs: 1", "node: ready", "extension: ready")
	l.signal(t, syscall.SIGTERM)
	l.expect(t, l.finish(t), exitOK, nil)
}

func TestObeyed(t *testing.T) {
	t.Parallel()
	exited := func(code int) syscall.WaitStatus { return syscall.WaitStatus(code << 8) }
	signaled := func(sig syscall.Signal) syscall.WaitStatus { return syscall.WaitStatus(sig) }
	tests := []struct {
		name string
		ws   syscall.WaitStatus
		want bool
	}{
		{"exit 0", exited(0), true},
		{"exit 1", exited(1), false},
		{"ended by the signal sent", signaled(syscall.SIGTERM), true},
		{"ended by another signal", signaled(syscall.SIGINT), false},
		{"killed", signaled(syscall.SIGKILL), false},
	}
	for _, tt := range tests {
		if got := obeyed(tt.ws, syscall.SIGTERM); got != tt.want {
			t.Errorf("%s: obeyed %v, want %v", tt.name, got, tt.want)
		}
	}
}
