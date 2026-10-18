//go:build !linux && !darwin && !freebsd && !windows

package main

func stdinIsTerminal() bool { return false }
