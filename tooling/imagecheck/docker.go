package imagecheck

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// platform is the TEE's; the image is built for it.
const platform = "linux/amd64"

// probePath is where the probe goes inside the checks' containers.
const probePath = "/fce-imageprobe"

// docker drives the docker CLI and tracks the containers it names, so cleanup removes them all.
type docker struct {
	prefix string
	names  []string
}

func newDocker() *docker {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return &docker{prefix: "fce-imagecheck-" + hex.EncodeToString(b)}
}

// name returns a container name for role and remembers it for cleanup.
func (d *docker) name(role string) string {
	n := d.prefix + "-" + role
	d.names = append(d.names, n)
	return n
}

// run runs docker with args and returns its stdout; an error carries its stderr.
func (d *docker) run(ctx context.Context, stdin io.Reader, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// logs returns a container's stdout and stderr, interleaved.
func (d *docker) logs(name string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "logs", name).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("(no logs: %v)\n", err)
	}
	return string(out)
}

// cleanup removes every container d named; it runs after cancellation too.
func (d *docker) cleanup() {
	if len(d.names) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// some names were never created; rm still removes the others
	_ = exec.CommandContext(ctx, "docker", append([]string{"rm", "-f", "-v"}, d.names...)...).Run()
}

// probe runs imageprobe with args in a new container made from image, created with opts, and returns its stdout.
func (d *docker) probe(ctx context.Context, image, probeBin, role string, opts []string, args ...string) (string, error) {
	n, err := d.createProbe(ctx, image, probeBin, role, opts, args...)
	if err != nil {
		return "", err
	}
	return d.run(ctx, nil, "start", "-a", n)
}

// createProbe creates a container from image that runs imageprobe with args, without starting it.
func (d *docker) createProbe(ctx context.Context, image, probeBin, role string, opts []string, args ...string) (string, error) {
	n := d.name(role)
	create := append([]string{"create", "--platform", platform, "--name", n, "--entrypoint", probePath}, opts...)
	create = append(append(create, image), args...)
	if _, err := d.run(ctx, nil, create...); err != nil {
		return "", err
	}
	if _, err := d.run(ctx, nil, "cp", probeBin, n+":"+probePath); err != nil {
		return "", err
	}
	return n, nil
}
