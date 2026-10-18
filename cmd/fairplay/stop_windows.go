//go:build windows

package main

import "os"

// stopProcess ends FairPlay. Windows can't send Ctrl+C to a process in
// another console, so it stops at once; viewers see the room end when their
// connection drops.
func stopProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
