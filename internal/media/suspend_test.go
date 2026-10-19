//go:build !windows

package media

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSuspendAndResume(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skip("no sleep:", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	state := func() string {
		b, _ := os.ReadFile("/proc/" + itoa(cmd.Process.Pid) + "/stat")
		f := strings.Fields(string(b))
		if len(f) > 2 {
			return f[2]
		}
		return "?"
	}
	if err := suspendProcess(cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if s := state(); s != "T" {
		t.Fatalf("after suspend state=%s, want T (stopped)", s)
	}
	if err := resumeProcess(cmd.Process.Pid); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if s := state(); s == "T" {
		t.Fatalf("after resume state=%s, still stopped", s)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
