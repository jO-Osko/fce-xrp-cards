package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// child is a started process; ended once reaped, with its status.
type child struct {
	name   string
	proc   *os.Process
	ended  bool
	status syscall.WaitStatus
}

// supervisor runs the children and decides the launcher's exit code.
type supervisor struct {
	log      *log.Logger
	grace    time.Duration
	children []*child

	stopping  bool
	requested bool           // a signal asked for the stop; no child caused it
	sent      syscall.Signal // the signal the children were asked to stop with
}

// supervise starts tee-node, then the extension, and returns the exit code once both have ended.
func supervise(cfg config, l *log.Logger) int {
	// before the first start, so no exit goes unnoticed
	sigs := make(chan os.Signal, 16)
	signal.Notify(sigs, syscall.SIGCHLD, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigs)

	s := &supervisor{log: l, grace: cfg.grace}
	if err := s.start("tee-node", []string{cfg.node}, os.Environ(), nil); err != nil {
		l.Print(err)
		return exitFailure
	}
	cred := &syscall.Credential{
		Uid:    cfg.uid,
		Gid:    cfg.gid,
		Groups: []uint32{},
		// only root can drop supplementary groups; a local run keeps the caller's
		NoSetGroups: os.Geteuid() != 0,
	}
	if err := s.start("extension", cfg.command, extensionEnv(os.LookupEnv, cfg.env), cred); err != nil {
		l.Print(err)
		s.stop(syscall.SIGTERM, false, "the extension didn't start")
	}
	s.wait(sigs)
	return s.code()
}

func (s *supervisor) start(name string, argv, env []string, cred *syscall.Credential) error {
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("starting %s: %w", name, err)
	}
	p, err := os.StartProcess(path, argv, &os.ProcAttr{
		Env:   env,
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
		Sys:   &syscall.SysProcAttr{Credential: cred},
	})
	if err != nil {
		return fmt.Errorf("starting %s: %w", name, err)
	}
	s.children = append(s.children, &child{name: name, proc: p})
	return nil
}

// wait returns once every child has ended. The first child to end, or the
// first stop signal, stops the rest; the grace then bounds the stop.
func (s *supervisor) wait(sigs <-chan os.Signal) {
	var asked syscall.Signal // a stop signal not yet acted on
	var kill <-chan time.Time
	for {
		s.reap()
		if !s.stopping {
			if c := s.ended(); c != nil {
				s.stop(syscall.SIGTERM, false, c.name+" "+describe(c.status))
			} else if asked != 0 {
				s.stop(asked, true, "received "+asked.String())
			}
		}
		if s.allEnded() {
			return
		}
		if s.stopping && kill == nil {
			kill = time.After(s.grace)
		}

		select {
		case sig := <-sigs:
			if sig, ok := sig.(syscall.Signal); ok && sig != syscall.SIGCHLD && asked == 0 {
				asked = sig
			}
		case <-kill:
			s.kill()
			kill = make(chan time.Time) // never fires: kill once
		}
	}
}

// reap collects every ended process, orphans included, without blocking.
// Only this loop waits: a second waiter would race it for the statuses.
func (s *supervisor) reap() {
	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil || pid <= 0 {
			return
		}
		for _, c := range s.children {
			// a reaped orphan may reuse an ended child's pid
			if !c.ended && c.proc.Pid == pid {
				c.ended, c.status = true, ws
				_ = c.proc.Release()
				s.log.Printf("%s %s", c.name, describe(ws))
			}
		}
	}
}

// stop asks every running child to stop with sig.
func (s *supervisor) stop(sig syscall.Signal, requested bool, reason string) {
	s.stopping, s.requested, s.sent = true, requested, sig
	s.log.Printf("stopping: %s", reason)
	for _, c := range s.children {
		if !c.ended {
			// fails only for a child that ended unreaped; the next reap records it
			_ = c.proc.Signal(sig)
		}
	}
}

func (s *supervisor) kill() {
	for _, c := range s.children {
		if !c.ended {
			s.log.Printf("killing %s: still running %s after the stop", c.name, s.grace)
			_ = c.proc.Kill()
		}
	}
}

// ended returns the first child that has ended, or nil.
func (s *supervisor) ended() *child {
	for _, c := range s.children {
		if c.ended {
			return c
		}
	}
	return nil
}

func (s *supervisor) allEnded() bool {
	for _, c := range s.children {
		if !c.ended {
			return false
		}
	}
	return true
}

// code is exitOK only after a requested stop that every child obeyed.
func (s *supervisor) code() int {
	if !s.requested {
		return exitFailure
	}
	for _, c := range s.children {
		if !obeyed(c.status, s.sent) {
			return exitFailure
		}
	}
	return exitOK
}

// obeyed reports whether a child stopped as asked: with exit code 0, or ended
// by the signal itself, as tee-node v0.0.29 does, having no signal handler.
func obeyed(ws syscall.WaitStatus, sent syscall.Signal) bool {
	return (ws.Exited() && ws.ExitStatus() == 0) || (ws.Signaled() && ws.Signal() == sent)
}

func describe(ws syscall.WaitStatus) string {
	if ws.Signaled() {
		return "ended by signal: " + ws.Signal().String()
	}
	return fmt.Sprintf("exited with code %d", ws.ExitStatus())
}
