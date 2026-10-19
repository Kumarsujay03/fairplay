//go:build linux || darwin || freebsd

package media

import (
	"context"
	"os/exec"
	"syscall"
)

// ffmpegCommand starts ffmpeg at the lowest CPU priority, through nice(1)
// when it exists, so every thread ffmpeg creates inherits it and the
// browser on this computer (usually the host's own player) always gets the
// CPU first. Lowering the priority after the start only reached ffmpeg's
// first thread; the encoding threads kept normal priority.
func ffmpegCommand(ctx context.Context, ffmpeg string, args ...string) *exec.Cmd {
	if nice, err := exec.LookPath("nice"); err == nil {
		return exec.CommandContext(ctx, nice, append([]string{"-n", "19", ffmpeg}, args...)...)
	}
	return exec.CommandContext(ctx, ffmpeg, args...)
}

// lowerPriority is the fallback without nice(1).
func lowerPriority(pid int) { _ = syscall.Setpriority(syscall.PRIO_PROCESS, pid, 19) }
