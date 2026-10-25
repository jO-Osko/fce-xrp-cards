package imagecheck

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

// defaultGrace is the launcher's --grace default.
const defaultGrace = 10 * time.Second

// launch is the launcher's command line as the image's ENTRYPOINT gives it.
type launch struct {
	node     string
	uid, gid uint32
	env      []string // the extension's own variables
	grace    time.Duration
	command  []string
}

// parseLaunch reads the launcher's arguments as the launcher does (launcher/config.go): keep the two in step.
func parseLaunch(args []string) (launch, error) {
	l := launch{grace: defaultGrace}
	var uidSet, gidSet bool
	id := func(dst *uint32, set *bool) func(string) error {
		return func(s string) error {
			n, err := strconv.ParseUint(s, 10, 32)
			if err != nil || n == math.MaxUint32 {
				return fmt.Errorf("%q is not a user or group id", s)
			}
			*dst, *set = uint32(n), true
			return nil
		}
	}

	fs := flag.NewFlagSet("launcher", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&l.node, "node", "", "")
	fs.Func("uid", "", id(&l.uid, &uidSet))
	fs.Func("gid", "", id(&l.gid, &gidSet))
	fs.Func("env", "", func(s string) error {
		for name := range strings.SplitSeq(s, ",") {
			if !validName(name) {
				return fmt.Errorf("%q is not a variable name", name)
			}
			l.env = append(l.env, name)
		}
		return nil
	})
	fs.DurationVar(&l.grace, "grace", l.grace, "")
	if err := fs.Parse(args); err != nil {
		return l, err
	}
	l.command = fs.Args()
	if !gidSet {
		l.gid = l.uid
	}

	var errs []error
	if l.node == "" {
		errs = append(errs, errors.New("no --node"))
	}
	if len(l.command) == 0 {
		errs = append(errs, errors.New("no extension command after --"))
	}
	if !uidSet || l.uid == 0 || l.gid == 0 {
		errs = append(errs, errors.New("the extension's user and group must be set and not root"))
	}
	if l.grace <= 0 {
		errs = append(errs, errors.New("--grace must be positive"))
	}
	return l, errors.Join(errs...)
}

// validName accepts a portable variable name, as the launcher does.
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
