package media

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPlanLadder(t *testing.T) {
	cases := map[int]string{2160: "1080p,720p,480p", 1080: "1080p,720p,480p", 800: "720p,480p,360p", 480: "480p,360p", 240: "240p"}
	for h, want := range cases {
		var got []string
		for _, r := range planLadder(h) {
			got = append(got, r.Name)
		}
		if strings.Join(got, ",") != want {
			t.Errorf("planLadder(%d)=%v want %s", h, got, want)
		}
	}
}

func TestHLSFileAllowlist(t *testing.T) {
	ok := []string{"master.m3u8", "s_v0/index.m3u8", "s_a1/init_3.mp4", "s_v2/seg_00012.m4s", "sub_0.vtt", "ext_1.vtt"}
	bad := []string{"../x", "status.json", ".done", "s_v0/../../etc/passwd", "/etc/passwd", "s_v0/seg_1.m4s", "master.m3u8/x"}
	for _, f := range ok {
		if !hlsFileRe.MatchString(f) {
			t.Errorf("%q should be allowed", f)
		}
	}
	for _, f := range bad {
		if hlsFileRe.MatchString(f) {
			t.Errorf("%q must be rejected", f)
		}
	}
}

func TestScanRefusesSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.mp4"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(root, "ok.mkv"), []byte("x"), 0o600)
	os.Symlink(filepath.Join(outside, "secret.mp4"), filepath.Join(root, "link.mp4"))
	items, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].RelPath != "ok.mkv" {
		t.Fatalf("got %+v", items)
	}
}

func TestSubLabel(t *testing.T) {
	if l, lang := subLabel("/m/Movie.mkv", "/m/Movie.en.srt"); l != "English" || lang != "en" {
		t.Errorf("got %s %s", l, lang)
	}
	if l, _ := subLabel("/m/Movie.mkv", "/m/Movie.srt"); l != "External" {
		t.Errorf("got %s", l)
	}
}

// TestTranscodeEndToEnd runs a real 8 second encode when ffmpeg is installed.
func TestTranscodeEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if testing.Short() {
		t.Skip("short mode")
	}
	root := t.TempDir()
	src := filepath.Join(root, "Clip.mkv")
	srt := filepath.Join(root, "Clip.en.srt")
	os.WriteFile(srt, []byte("1\n00:00:01,000 --> 00:00:03,000\nhello\n"), 0o600)
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24", "-f", "lavfi", "-i", "sine=frequency=440",
		"-t", "8", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "ac3", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate: %v %s", err, out)
	}
	ctx := context.Background()
	m, err := NewManager(ctx, Options{Root: root, Cache: t.TempDir(), FFmpeg: "ffmpeg", FFprobe: "ffprobe"})
	if err != nil {
		t.Fatal(err)
	}
	items, _, err := m.List()
	if err != nil || len(items) != 1 {
		t.Fatalf("list: %v %v", items, err)
	}
	id := items[0].ID
	if err := m.Prepare(id); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(90 * time.Second)
	var st Status
	for time.Now().Before(deadline) {
		st, _ = m.Status(id)
		if st.State == StateReady || st.State == StateError {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if st.State != StateReady {
		t.Fatalf("state %s: %s", st.State, st.Error)
	}
	if strings.Join(st.Rungs, ",") != "360p" || len(st.Audio) != 1 || len(st.Subs) != 1 {
		t.Fatalf("unexpected status %+v", st)
	}
	for _, f := range []string{"master.m3u8", "s_v0/index.m3u8", st.Subs[0].URL} {
		rec := httptest.NewRecorder()
		m.ServeHLS(rec, httptest.NewRequest("GET", "/", nil), id, f)
		if rec.Code != 200 {
			t.Errorf("%s: %d", f, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	m.ServeHLS(rec, httptest.NewRequest("GET", "/", nil), id, "status.json")
	if rec.Code != 404 {
		t.Error("status.json must not be served")
	}
	// A second manager finds the finished encode in the cache.
	m2, _ := NewManager(ctx, Options{Root: root, Cache: m.cache, FFmpeg: "ffmpeg", FFprobe: "ffprobe"})
	_, sts, _ := m2.List()
	if sts[id].State != StateReady {
		t.Fatalf("cache not reused: %+v", sts[id])
	}
}

func TestBrowseAndSetRoot(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "Shows"), 0o755)
	os.Mkdir(filepath.Join(root, ".hidden"), 0o755)
	os.WriteFile(filepath.Join(root, "a.mkv"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o600)
	l, err := Browse(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Dirs) != 1 || l.Dirs[0] != "Shows" || l.Videos != 1 || l.Parent == "" {
		t.Fatalf("got %+v", l)
	}
	if items, err := scan(t.TempDir()); err != nil || items == nil {
		t.Fatal("an empty folder must list as an empty slice, not nil")
	}
	if _, err := Browse(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing folder should error")
	}

	other := t.TempDir()
	os.WriteFile(filepath.Join(other, "a.mkv"), []byte("x"), 0o600)
	m := &Manager{items: map[string]Item{}, statuses: map[string]*Status{}}
	if err := m.SetRoot(root); err != nil {
		t.Fatal(err)
	}
	first, _, _ := m.List()
	if err := m.SetRoot(other); err != nil {
		t.Fatal(err)
	}
	second, _, _ := m.List()
	if len(first) != 1 || len(second) != 1 || first[0].ID == second[0].ID {
		t.Fatal("same file name in two folders must get different IDs")
	}
	if err := m.SetRoot(filepath.Join(root, "a.mkv")); err == nil {
		t.Fatal("a file is not a folder")
	}
}
