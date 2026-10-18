//go:build darwin || freebsd

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// stdinIsTerminal reports whether a person can answer a question on stdin.
func stdinIsTerminal() bool {
	var t syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), syscall.TIOCGETA, uintptr(unsafe.Pointer(&t)))
	return e == 0
}
