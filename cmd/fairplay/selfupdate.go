package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/Kumarsujay03/fairplay/internal/update"
)

// updateState tracks auto-update for the banner and the host console.
//
// Updates are only ever installed when FairPlay starts, never during a
// show. "auto" installs and restarts, "check" only tells you, "off" does
// nothing. Dev builds (not from a release) never update.
type updateState struct {
	mode string

	mu        sync.Mutex
	latest    string
	checked   time.Time
	lastError string
}

func newUpdateState(mode string) *updateState {
	switch mode {
	case "auto", "check", "off":
	default:
		mode = "auto"
	}
	return &updateState{mode: mode}
}

func (u *updateState) info() any {
	u.mu.Lock()
	defer u.mu.Unlock()
	return map[string]any{
		"current": version, "latest": u.latest, "mode": u.mode, "devBuild": !update.IsRelease(version),
		"available": update.Newer(u.latest, version), "checked": u.checked, "error": u.lastError,
	}
}

func (u *updateState) check(ctx context.Context) (string, error) {
	latest, err := update.New(version).Latest(ctx)
	u.mu.Lock()
	defer u.mu.Unlock()
	u.checked = time.Now()
	if err != nil {
		u.lastError = err.Error()
		return "", err
	}
	u.latest, u.lastError = latest, ""
	return latest, nil
}

// startupUpdate installs a newer release before the room opens and restarts
// into it. It returns true if this process has been replaced.
func (u *updateState) startupUpdate() bool {
	if u.mode == "off" || !update.IsRelease(version) || os.Getenv("FAIRPLAY_UPDATED") != "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	latest, err := u.check(ctx)
	cancel()
	if err != nil || !update.Newer(latest, version) {
		return false // offline or up to date: start normally
	}
	if u.mode == "check" {
		fmt.Printf("\n  FairPlay %s is available (you have %s). Run `fairplay update` to install it.\n", latest, version)
		return false
	}
	fmt.Printf("\n  Updating FairPlay %s → %s ...\n", version, latest)
	exe, err := update.Executable()
	if err == nil {
		ictx, icancel := context.WithTimeout(context.Background(), 3*time.Minute)
		err = update.New(version).Install(ictx, latest, exe)
		icancel()
	}
	if err != nil {
		fmt.Printf("  Update skipped: %v\n  Starting %s instead.\n", err, version)
		return false
	}
	fmt.Printf("  Updated. Restarting into %s.\n", latest)
	os.Setenv("FAIRPLAY_UPDATED", "1")
	if err := relaunch(exe); err != nil {
		fmt.Printf("  Could not restart automatically (%v). Run FairPlay again to use %s.\n", err, latest)
		os.Exit(0)
	}
	return true
}

// watch checks again every 6 hours so a long-running host sees new releases
// in the console. It never installs while running.
func (u *updateState) watch(ctx context.Context) {
	if u.mode == "off" || !update.IsRelease(version) {
		return
	}
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if latest, err := u.check(cctx); err == nil && update.Newer(latest, version) {
				log.Printf("FairPlay %s is available; it installs the next time you start FairPlay", latest)
			}
			cancel()
		}
	}
}

// runUpdate is `fairplay update`: check and install right now.
func runUpdate() {
	update.CleanupOld()
	if !update.IsRelease(version) {
		fmt.Printf("This is a development build (%s). Download a release or build the latest source instead.\n", version)
		return
	}
	u := update.New(version)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	latest, err := u.Latest(ctx)
	if err != nil {
		fail("%v", err)
	}
	if !update.Newer(latest, version) {
		fmt.Printf("FairPlay %s is up to date.\n", version)
		return
	}
	exe, err := update.Executable()
	if err != nil {
		fail("%v", err)
	}
	fmt.Printf("Updating FairPlay %s → %s ...\n", version, latest)
	if err := u.Install(ctx, latest, exe); err != nil {
		fail("%v", err)
	}
	fmt.Printf("Done. FairPlay %s is installed at %s\n", latest, exe)
}
