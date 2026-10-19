//go:build windows

package media

import "syscall"

// Windows has no SIGSTOP; NtSuspendProcess and NtResumeProcess do the same
// for every thread of the process.
var (
	ntdll     = syscall.NewLazyDLL("ntdll.dll")
	ntSuspend = ntdll.NewProc("NtSuspendProcess")
	ntResume  = ntdll.NewProc("NtResumeProcess")
)

func withProcess(pid int, fn *syscall.LazyProc) error {
	const processSuspendResume = 0x0800
	h, err := syscall.OpenProcess(processSuspendResume, false, uint32(pid))
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)
	if r, _, _ := fn.Call(uintptr(h)); r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

func suspendProcess(pid int) error { return withProcess(pid, ntSuspend) }
func resumeProcess(pid int) error  { return withProcess(pid, ntResume) }
