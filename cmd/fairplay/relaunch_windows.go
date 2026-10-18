//go:build windows

package main

import (
	"os"
	"os/exec"
)

// relaunch starts the freshly installed binary in this console and exits with
// its exit code once it stops. Windows has no exec(), and the old .exe file
// cannot be replaced while it runs (it was renamed to .old instead).
func relaunch(exe string) error {
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Wait()
	os.Exit(cmd.ProcessState.ExitCode())
	return nil
}
