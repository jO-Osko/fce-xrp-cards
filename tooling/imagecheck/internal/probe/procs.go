// Package probe is what imageprobe does inside the image checks' containers:
// read processes, dial ports and kill a process. Standard library only, so the
// probe stays a small static binary.
package probe

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Proc is a process as /proc shows it.
type Proc struct {
	PID  int      `json:"pid"`
	PPID int      `json:"ppid"`
	Argv []string `json:"argv"`
	// UID and GID are the real, effective, saved and filesystem ids.
	UID        []uint32 `json:"uid"`
	GID        []uint32 `json:"gid"`
	Groups     []uint32 `json:"groups"`
	CapEff     uint64   `json:"capEff"`
	NoNewPrivs int      `json:"noNewPrivs"`
}

// Procs reads every process under root (normally /proc) except self.
func Procs(root string, self int) ([]Proc, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var procs []Proc
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		p, err := readProc(filepath.Join(root, e.Name()), pid)
		if err != nil {
			continue // gone meanwhile
		}
		procs = append(procs, p)
	}
	return procs, nil
}

func readProc(dir string, pid int) (Proc, error) {
	status, err := os.ReadFile(filepath.Join(dir, "status"))
	if err != nil {
		return Proc{}, err
	}
	cmdline, err := os.ReadFile(filepath.Join(dir, "cmdline"))
	if err != nil {
		return Proc{}, err
	}
	p, err := parseStatus(status)
	if err != nil {
		return Proc{}, fmt.Errorf("reading %s: %w", dir, err)
	}
	p.PID = pid
	if c := bytes.TrimSuffix(cmdline, []byte{0}); len(c) > 0 {
		p.Argv = strings.Split(string(c), "\x00")
	}
	return p, nil
}

func parseStatus(b []byte) (Proc, error) {
	var p Proc
	ids := func(v string) ([]uint32, error) {
		out := []uint32{}
		for f := range strings.FieldsSeq(v) {
			n, err := strconv.ParseUint(f, 10, 32)
			if err != nil {
				return nil, err
			}
			out = append(out, uint32(n))
		}
		return out, nil
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		var err error
		switch k {
		case "PPid":
			p.PPID, err = strconv.Atoi(v)
		case "Uid":
			p.UID, err = ids(v)
		case "Gid":
			p.GID, err = ids(v)
		case "Groups":
			p.Groups, err = ids(v)
		case "CapEff":
			p.CapEff, err = strconv.ParseUint(v, 16, 64)
		case "NoNewPrivs":
			p.NoNewPrivs, err = strconv.Atoi(v)
		}
		if err != nil {
			return p, fmt.Errorf("parsing %s: %w", k, err)
		}
	}
	return p, sc.Err()
}

// WaitChildren polls until PID 1 has n children that have exec'd (argv differs from PID 1's), then returns every process.
func WaitChildren(ctx context.Context, root string, self, n int) ([]Proc, error) {
	for {
		procs, err := Procs(root, self)
		if err == nil && started(procs) >= n {
			return procs, nil
		}
		select {
		case <-ctx.Done():
			return procs, fmt.Errorf("waiting for %d started children of PID 1: %w", n, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func started(procs []Proc) int {
	var launcher []string
	for _, p := range procs {
		if p.PID == 1 {
			launcher = p.Argv
		}
	}
	n := 0
	for _, p := range procs {
		// a forked child shows its parent's argv until it execs
		if p.PPID == 1 && len(p.Argv) > 0 && (len(launcher) == 0 || p.Argv[0] != launcher[0]) {
			n++
		}
	}
	return n
}

// Kill sends SIGKILL to every process under root whose argv[0] is argv0, and returns how many it signalled.
func Kill(root string, self int, argv0 string) (int, error) {
	procs, err := Procs(root, self)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range procs {
		if len(p.Argv) > 0 && p.Argv[0] == argv0 {
			if err := syscall.Kill(p.PID, syscall.SIGKILL); err != nil {
				return n, fmt.Errorf("killing %d: %w", p.PID, err)
			}
			n++
		}
	}
	return n, nil
}
