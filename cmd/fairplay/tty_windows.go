//go:build windows

package main

import (
	"os"
	"syscall"
)

// stdinIsTerminal reports whether a person can answer a question on stdin.
func stdinIsTerminal() bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(os.Stdin.Fd()), &mode) == nil
}
