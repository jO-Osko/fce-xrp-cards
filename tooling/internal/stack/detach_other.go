//go:build !unix

package stack

import "os/exec"

func detach(*exec.Cmd) {}
