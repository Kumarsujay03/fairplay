//go:build !(linux || darwin || freebsd)

package media

func lowerPriority(pid int) {}
