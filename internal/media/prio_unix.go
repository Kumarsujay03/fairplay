//go:build linux || darwin || freebsd

package media

import (
	"context"
	"os/exec"
	"syscall"
)

// ffmpegCommand starts ffmpeg at the lowest CPU priority, through nice(1)
// when it exists. Every thread ffmpeg starts inherits it, so the browser on
// this computer (usually the host's own player) always gets the CPU first.
func ffmpegCommand(ctx context.Context, ffmpeg string, args ...string) *exec.Cmd {
	if nice, err := exec.LookPath("nice"); err == nil {
		return exec.CommandContext(ctx, nice, append([]string{"-n", "19", ffmpeg}, args...)...)
	}
	return exec.CommandContext(ctx, ffmpeg, args...)
}

// lowerPriority is the fallback without nice(1). It only reaches the threads
// that exist at that moment, which is why ffmpegCommand is preferred.
func lowerPriority(pid int) { _ = syscall.Setpriority(syscall.PRIO_PROCESS, pid, 19) }
