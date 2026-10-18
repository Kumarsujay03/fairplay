package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChooseMediaFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("FAIRPLAY_MEDIA", "")

	// Nothing exists: ~/Videos is created.
	dir, created, err := chooseMediaFolder(options{}, settings{})
	if err != nil || !created || dir != filepath.Join(home, "Videos") || !isDir(dir) {
		t.Fatalf("got %q created=%v err=%v", dir, created, err)
	}

	// Movies wins when Videos is gone and Movies exists.
	os.Remove(dir)
	os.Mkdir(filepath.Join(home, "Movies"), 0o755)
	if dir, _, _ = chooseMediaFolder(options{}, settings{}); dir != filepath.Join(home, "Movies") {
		t.Fatalf("want Movies, got %q", dir)
	}

	// The folder picked in the console last time wins over the defaults.
	saved := filepath.Join(home, "Films")
	os.Mkdir(saved, 0o755)
	if dir, _, _ = chooseMediaFolder(options{}, settings{Media: saved}); dir != saved {
		t.Fatalf("want saved folder, got %q", dir)
	}

	// An explicit -media that does not exist is a clear error that suggests real folders.
	_, _, err = chooseMediaFolder(options{media: "~/Nope", mediaSet: true}, settings{})
	if err == nil || !strings.Contains(err.Error(), "does not exist") || !strings.Contains(err.Error(), "~/Movies") {
		t.Fatalf("want helpful error, got %v", err)
	}

	// ~ is expanded.
	if dir, _, err = chooseMediaFolder(options{media: "~/Films", mediaSet: true}, settings{}); err != nil || dir != saved {
		t.Fatalf("tilde: %q %v", dir, err)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	d := t.TempDir()
	if err := saveSettings(d, settings{Media: "/x"}); err != nil {
		t.Fatal(err)
	}
	if got := loadSettings(d); got.Media != "/x" {
		t.Fatalf("got %+v", got)
	}
}

func TestInstallHintIsACommand(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "cloudflared"} {
		if h := installHint(tool); h == "" || h == "see the FairPlay README" {
			t.Errorf("no hint for %s", tool)
		}
	}
}
