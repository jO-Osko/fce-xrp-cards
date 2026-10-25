//go:build linux

package main

import "syscall"

// prSetNoNewPrivs is PR_SET_NO_NEW_PRIVS from linux/prctl.h.
const prSetNoNewPrivs = 38

// noNewPrivs sets no_new_privs on the calling thread; processes it starts inherit it.
func noNewPrivs() error {
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0); errno != 0 {
		return errno
	}
	return nil
}
