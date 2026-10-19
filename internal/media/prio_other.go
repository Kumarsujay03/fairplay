//go:build !(linux || darwin || freebsd)

package media

import (
	"context"
	"os/exec"
)

func ffmpegCommand(ctx context.Context, ffmpeg string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, ffmpeg, args...)
}

func lowerPriority(pid int) {}
