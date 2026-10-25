//go:build unix

package stack

import (
	"os/exec"
	"syscall"
)

// detach puts cmd in its own process group, so a terminal's Ctrl-C reaches fce, not cmd.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
