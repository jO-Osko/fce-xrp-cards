package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// defaultGrace is how long the children get to stop before they are killed.
const defaultGrace = 10 * time.Second

// config is the launcher's command line.
type config struct {
	node    string
	command []string
	uid     uint32
	gid     uint32
	env     []string // the extension's own variables, by name
	grace   time.Duration
}

// id is a user or group id flag.
type id struct {
	v   uint32
	set bool
}

func (i *id) String() string { return strconv.FormatUint(uint64(i.v), 10) }

func (i *id) Set(s string) error {
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return errors.New("not a decimal id")
	}
	if n == math.MaxUint32 {
		return errors.New("4294967295 is -1 to the kernel, not an id")
	}
	i.v, i.set = uint32(n), true
	return nil
}

func parse(args []string) (config, error) {
	cfg := config{grace: defaultGrace}
	var uid, gid id

	fs := flag.NewFlagSet("launcher", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.node, "node", "", "tee-node's binary")
	fs.Var(&uid, "uid", "the extension's user id")
	fs.Var(&gid, "gid", "the extension's group id; the user id when unset")
	fs.Func("env", "the extension's own variables, comma-separated", func(s string) error {
		for name := range strings.SplitSeq(s, ",") {
			if !validName(name) {
				return fmt.Errorf("%q is not a variable name", name)
			}
			cfg.env = append(cfg.env, name)
		}
		return nil
	})
	fs.DurationVar(&cfg.grace, "grace", defaultGrace, "how long the children get to stop")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}

	cfg.command = fs.Args()
	if !gid.set {
		gid = uid
	}
	cfg.uid, cfg.gid = uid.v, gid.v

	var errs []error
	if cfg.node == "" {
		errs = append(errs, errors.New("--node is required"))
	}
	if len(cfg.command) == 0 {
		errs = append(errs, errors.New("the extension's command is required, after --"))
	}
	if !uid.set || cfg.uid == 0 || cfg.gid == 0 {
		errs = append(errs, errors.New("--uid is required, and the extension's user and group must not be root (0)"))
	}
	if cfg.grace <= 0 {
		errs = append(errs, errors.New("--grace must be positive"))
	}
	return cfg, errors.Join(errs...)
}

// validName accepts a portable variable name: a letter or underscore, then letters, digits or underscores.
func validName(s string) bool {
	if s == "" || (s[0] >= '0' && s[0] <= '9') {
		return false
	}
	for _, c := range s {
		if c != '_' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}
