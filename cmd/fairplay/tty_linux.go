//go:build linux

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// stdinIsTerminal reports whether a person can answer a question on stdin.
// /dev/null and pipes are not terminals.
func stdinIsTerminal() bool {
	var t syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t)))
	return e == 0
}
