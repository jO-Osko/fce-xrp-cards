// Command imageprobe runs inside the image checks' containers; fce builds it
// from source for linux/amd64 (imagecheck.BuildProbe) and copies it into
// containers made from the image under test. It uses only the standard library.
//
// Usage:
//
//	imageprobe procs [-children n] [-timeout d]
//	imageprobe dial [-timeout d] host:port...
//	imageprobe kill argv0
//
// Each prints its outcome as JSON on stdout and exits 1 on failure.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/flare-foundation/fce-tooling/imagecheck/internal/probe"
)

const procRoot = "/proc"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: imageprobe procs|dial|kill [flags] [args]")
		return 2
	}
	var (
		out any
		err error
	)
	switch args[0] {
	case "procs":
		out, err = procs(args[1:])
	case "dial":
		out, err = dial(args[1:])
	case "kill":
		out, err = kill(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "imageprobe: unknown command %q\n", args[0])
		return 2
	}
	if out != nil {
		if e := json.NewEncoder(os.Stdout).Encode(out); e != nil {
			err = errors.Join(err, e)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "imageprobe:", err)
		return 1
	}
	return 0
}

func procs(args []string) (any, error) {
	fs := flag.NewFlagSet("procs", flag.ContinueOnError)
	children := fs.Int("children", 0, "wait until PID 1 has this many started children")
	timeout := fs.Duration("timeout", 30*time.Second, "how long to wait")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	return probe.WaitChildren(ctx, procRoot, os.Getpid(), *children)
}

func dial(args []string) (any, error) {
	fs := flag.NewFlagSet("dial", flag.ContinueOnError)
	timeout := fs.Duration("timeout", 2*time.Second, "per address")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return probe.Dial(fs.Args(), *timeout), nil
}

func kill(args []string) (any, error) {
	if len(args) != 1 {
		return nil, errors.New("kill takes one argv0")
	}
	n, err := probe.Kill(procRoot, os.Getpid(), args[0])
	if err == nil && n == 0 {
		err = fmt.Errorf("no process runs %s", args[0])
	}
	return n, err
}
