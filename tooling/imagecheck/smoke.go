package imagecheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/flare-foundation/fce-tooling/imagecheck/internal/probe"
)

// startup bounds the wait for both children of the launcher.
const startup = 30 * time.Second

// settle is how long an image without the launcher gets to fail before it counts as running.
const settle = 10 * time.Second

// smoke runs the image as shipped and checks its processes, its ports, and how it stops.
func (c *checker) smoke(ctx context.Context) []Result {
	boot := Result{Name: "smoke: the image boots and keeps running"}
	a := c.d.name("smoke")
	if _, err := c.d.run(ctx, nil, "run", "-d", "--platform", platform, "--name", a, c.cfg.Image); err != nil {
		boot.Err = err
		return []Result{boot}
	}
	procs, err := c.started(ctx, a)
	if err != nil {
		boot.Err = err
		c.addLogs(a)
		return []Result{boot}
	}
	res := []Result{{Name: "smoke: the processes and their privileges", Advice: skipped(errNoLauncher)}}
	if c.t.launchErr == nil {
		res = procResults(procs, c.t)
	}
	res = append(res, c.ports(ctx, a))
	running, err := c.d.run(ctx, nil, "inspect", "-f", "{{.State.Running}}", a)
	switch {
	case err != nil:
		boot.Err = err
	case strings.TrimSpace(running) != "true":
		boot.Err = errors.New("it stopped during the checks")
	}
	res = append([]Result{boot}, res...)
	res = append(res, c.kill(ctx, a))
	if failed(res, false) {
		c.addLogs(a)
	}
	return append(res, c.stop(ctx))
}

// started waits until the image in container has started and returns its processes:
// with the launcher, once both its children run; without, after settle, and none.
func (c *checker) started(ctx context.Context, container string) ([]probe.Proc, error) {
	if c.t.launchErr == nil {
		return c.procs(ctx, container)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(settle):
		return nil, nil
	}
}

// procs waits until the launcher in container has started both children, and returns every process there.
func (c *checker) procs(ctx context.Context, container string) ([]probe.Proc, error) {
	out, err := c.d.probe(ctx, c.cfg.Image, c.cfg.Probe, "procs-"+container[len(c.d.prefix)+1:],
		[]string{"--pid", "container:" + container}, "procs", "-children", "2", "-timeout", startup.String())
	if err != nil {
		return nil, fmt.Errorf("waiting for tee-node and the extension: %w", err)
	}
	var procs []probe.Proc
	if err := json.Unmarshal([]byte(out), &procs); err != nil {
		return nil, fmt.Errorf("reading the probe's processes: %w", err)
	}
	return procs, nil
}

// procResults checks who runs as whom, from the processes of a running container.
func procResults(procs []probe.Proc, t target) []Result {
	l := t.launch
	var launcher, node, ext *probe.Proc
	for i := range procs {
		p := &procs[i]
		switch {
		case p.PID == 1:
			launcher = p
		case p.PPID == 1 && len(p.Argv) > 0 && p.Argv[0] == l.node:
			node = p
		case p.PPID == 1 && slices.Equal(p.Argv, l.command):
			ext = p
		}
	}

	r1 := Result{Name: "smoke: the launcher is PID 1, as root"}
	switch {
	case launcher == nil:
		r1.Advice = errors.New("no PID 1")
	case !slices.Equal(launcher.Argv, t.entrypoint):
		r1.Advice = fmt.Errorf("PID 1 runs %q, not the ENTRYPOINT", launcher.Argv)
	case !all(launcher.UID, 0) || !all(launcher.GID, 0):
		r1.Advice = fmt.Errorf("PID 1 runs as uid %v, gid %v", launcher.UID, launcher.GID)
	}

	r2 := Result{Name: "smoke: tee-node runs as root, with no_new_privs"}
	switch {
	case node == nil:
		r2.Advice = fmt.Errorf("no child of PID 1 runs %s", l.node)
	case !all(node.UID, 0) || !all(node.GID, 0):
		r2.Advice = fmt.Errorf("it runs as uid %v, gid %v", node.UID, node.GID)
	case node.NoNewPrivs != 1:
		r2.Advice = errors.New("no_new_privs is off")
	}

	r3 := Result{Name: fmt.Sprintf("smoke: the extension runs as %d:%d, with no groups, no capabilities and no_new_privs", l.uid, l.gid)}
	switch {
	case ext == nil:
		r3.Advice = fmt.Errorf("no child of PID 1 runs %q", l.command)
	case !all(ext.UID, l.uid) || !all(ext.GID, l.gid):
		r3.Advice = fmt.Errorf("it runs as uid %v, gid %v", ext.UID, ext.GID)
	case len(ext.Groups) > 0:
		r3.Advice = fmt.Errorf("it has groups %v", ext.Groups)
	case ext.CapEff != 0:
		r3.Advice = fmt.Errorf("it has capabilities %016x", ext.CapEff)
	case ext.NoNewPrivs != 1:
		r3.Advice = errors.New("no_new_privs is off")
	}
	return []Result{r1, r2, r3}
}

// all reports whether every id (real, effective, saved, filesystem) is want.
func all(ids []uint32, want uint32) bool {
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		if id != want {
			return false
		}
	}
	return true
}

// ports dials the container's three ports from another container: only tee-node's config port may answer.
func (c *checker) ports(ctx context.Context, container string) Result {
	r := Result{Name: "smoke: from another container, only tee-node's config port answers"}
	out, err := c.d.run(ctx, nil, "inspect", "-f", "{{json .NetworkSettings.Networks}}", container)
	if err != nil {
		r.Advice = err
		return r
	}
	var nets map[string]struct {
		IPAddress string `json:"IPAddress"`
	}
	if err := json.Unmarshal([]byte(out), &nets); err != nil {
		r.Advice = fmt.Errorf("reading the container's networks: %w", err)
		return r
	}
	ip := ""
	for _, n := range slices.Sorted(maps.Keys(nets)) {
		if ip = nets[n].IPAddress; ip != "" {
			break
		}
	}
	if ip == "" {
		r.Advice = errors.New("the container has no IP address")
		return r
	}

	addr := func(port int) string { return ip + ":" + strconv.Itoa(port) }
	want := map[string]bool{addr(configPort): true, addr(c.t.signPort): false, addr(c.t.extPort): false}
	out, err = c.d.probe(ctx, c.cfg.Image, c.cfg.Probe, "dial", nil, append([]string{"dial"}, slices.Sorted(maps.Keys(want))...)...)
	if err != nil {
		r.Advice = err
		return r
	}
	var open map[string]bool
	if err := json.Unmarshal([]byte(out), &open); err != nil {
		r.Advice = fmt.Errorf("reading the probe's dials: %w", err)
		return r
	}
	var errs []error
	for _, a := range slices.Sorted(maps.Keys(want)) {
		if open[a] != want[a] {
			state := map[bool]string{true: "answers", false: "doesn't answer"}[open[a]]
			errs = append(errs, fmt.Errorf("%s %s", a, state))
		}
	}
	r.Advice = errors.Join(errs...)
	return r
}

// kill kills the extension in container: the launcher must stop tee-node and exit non-zero.
func (c *checker) kill(ctx context.Context, container string) Result {
	r := Result{Name: "smoke: killing the extension stops the container, non-zero"}
	if c.t.launchErr != nil {
		r.Advice = skipped(errNoLauncher)
		return r
	}
	if _, err := c.d.probe(ctx, c.cfg.Image, c.cfg.Probe, "kill", []string{"--pid", "container:" + container},
		"kill", c.t.launch.command[0]); err != nil {
		r.Advice = err
		return r
	}
	code, err := c.wait(ctx, container)
	switch {
	case err != nil:
		r.Advice = err
	case code == 0:
		r.Advice = errors.New("it exited 0")
	}
	return r
}

// stop runs a fresh container and stops it: a requested stop exits 0.
func (c *checker) stop(ctx context.Context) Result {
	r := Result{Name: "smoke: docker stop exits 0"}
	b := c.d.name("stop")
	if _, err := c.d.run(ctx, nil, "run", "-d", "--platform", platform, "--name", b, c.cfg.Image); err != nil {
		r.Advice = err
		return r
	}
	if _, err := c.started(ctx, b); err != nil {
		r.Advice = err
		c.addLogs(b)
		return r
	}
	// longer than the launcher's grace, so docker's own SIGKILL can't decide the outcome
	t := strconv.Itoa(int((c.t.launch.grace + 20*time.Second).Seconds()))
	if _, err := c.d.run(ctx, nil, "stop", "-t", t, b); err != nil {
		r.Advice = err
		return r
	}
	code, err := c.wait(ctx, b)
	switch {
	case err != nil:
		r.Advice = err
	case code != 0:
		r.Advice = fmt.Errorf("it exited %d", code)
	}
	if r.Advice != nil {
		c.addLogs(b)
	}
	return r
}

// wait waits for container to exit, at most the launcher's grace and a margin, and returns its exit code.
func (c *checker) wait(ctx context.Context, container string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, c.t.launch.grace+30*time.Second)
	defer cancel()
	out, err := c.d.run(ctx, nil, "wait", container)
	if err != nil {
		return 0, fmt.Errorf("waiting for the container to exit: %w", err)
	}
	return strconv.Atoi(strings.TrimSpace(out))
}
