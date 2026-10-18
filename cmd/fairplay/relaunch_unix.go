//go:build !windows

package main

import (
	"os"
	"syscall"
)

// relaunch replaces this process with the freshly installed binary.
func relaunch(exe string) error {
	return syscall.Exec(exe, os.Args, os.Environ())
}
