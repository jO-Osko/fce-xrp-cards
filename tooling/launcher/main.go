// Command launcher is the image's PID 1. It runs tee-node as root and the
// extension as a non-root user, and exits when either of them exits.
//
// Usage:
//
//	launcher --node PATH --uid UID [--gid GID] [--env NAMES] [--grace DURATION] -- COMMAND [ARGS...]
//
// tee-node gets the launcher's whole environment. The extension gets
// EXTENSION_PORT, SIGN_PORT, LOG_LEVEL, CHAIN_ID, EXTENSION_ID and the --env
// names, each only when set, and runs with no capabilities, no supplementary
// groups and no_new_privs. Both children share the launcher's stdin, stdout
// and stderr.
//
// On SIGTERM or SIGINT, or when either child ends, the launcher passes the
// signal (SIGTERM when a child ended) to the children still running, and kills
// any left after the grace. It exits 0 only after a requested stop that both
// children obeyed, 1 otherwise, and 2 on a usage error.
package main

import (
	"errors"
	"flag"
	"log"
	"os"
	"runtime"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

const usage = "usage: launcher --node PATH --uid UID [--gid GID] [--env NAMES] [--grace DURATION] -- COMMAND [ARGS...]"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	l := log.New(os.Stderr, "launcher: ", 0)
	cfg, err := parse(args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		l.Print(usage)
		return exitOK
	case err != nil:
		l.Printf("%v\n%s", err, usage)
		return exitUsage
	}

	// no_new_privs is per thread: set it here and start both children from this thread
	runtime.LockOSThread()
	if err := noNewPrivs(); err != nil {
		l.Printf("setting no_new_privs: %v", err)
		return exitFailure
	}
	return supervise(cfg, l)
}
