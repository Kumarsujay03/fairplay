//go:build !windows

package media

import "syscall"

// suspendProcess freezes a process: it stops using the CPU and the GPU at
// once and keeps everything in memory, so resuming is instant.
func suspendProcess(pid int) error { return syscall.Kill(pid, syscall.SIGSTOP) }

// resumeProcess lets a frozen process continue.
func resumeProcess(pid int) error { return syscall.Kill(pid, syscall.SIGCONT) }
