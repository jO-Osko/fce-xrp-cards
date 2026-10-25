//go:build !linux

package main

import "errors"

// noNewPrivs fails: the launcher runs only in the Linux image.
func noNewPrivs() error {
	return errors.New("not supported outside Linux")
}
