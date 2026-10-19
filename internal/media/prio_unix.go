//go:build linux || darwin || freebsd

package media

import "syscall"

// lowerPriority keeps the encoder from making the host's own playback stutter.
func lowerPriority(pid int) { _ = syscall.Setpriority(syscall.PRIO_PROCESS, pid, 10) }
