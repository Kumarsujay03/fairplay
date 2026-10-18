//go:build !windows

package main

import "syscall"

// stopProcess asks FairPlay to shut down the same way Ctrl+C does, so it says
// goodbye to every viewer first.
func stopProcess(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}
