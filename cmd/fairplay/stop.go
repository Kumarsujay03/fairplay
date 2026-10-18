package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// running.json in the settings folder says which process serves which port,
// so `fairplay stop` can end a room started in another terminal.
type runningInfo struct {
	PID  int `json:"pid"`
	Port int `json:"port"`
}

func runningPath(data string) string { return filepath.Join(data, "running.json") }

func writeRunning(data string, port int) {
	b, _ := json.Marshal(runningInfo{PID: os.Getpid(), Port: port})
	_ = os.WriteFile(runningPath(data), b, 0o600)
}

func clearRunning(data string) {
	var r runningInfo
	if b, err := os.ReadFile(runningPath(data)); err == nil && json.Unmarshal(b, &r) == nil && r.PID == os.Getpid() {
		_ = os.Remove(runningPath(data))
	}
}

func runStop(o options) {
	var r runningInfo
	b, err := os.ReadFile(runningPath(o.data))
	if err != nil || json.Unmarshal(b, &r) != nil || r.PID <= 0 || r.PID == os.Getpid() {
		fmt.Println("FairPlay isn't running.")
		return
	}
	// A leftover file from a crash could name a PID that now belongs to
	// another program; only act while FairPlay's port is still in use.
	if !portBusy(r.Port) {
		_ = os.Remove(runningPath(o.data))
		fmt.Println("FairPlay isn't running.")
		return
	}
	if err := stopProcess(r.PID); err != nil {
		fail("could not stop FairPlay (process %d): %v", r.PID, err)
	}
	for i := 0; i < 50 && portBusy(r.Port); i++ {
		time.Sleep(200 * time.Millisecond)
	}
	if portBusy(r.Port) {
		fail("FairPlay (process %d) didn't stop in 10 seconds", r.PID)
	}
	fmt.Println("FairPlay stopped. The room has ended for everyone.")
}
