package emoji

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestIndexAndDefaults(t *testing.T) {
	if !Valid("1f525") || !Valid("2764_fe0f") || !Valid("1f600") {
		t.Fatal("common emoji must be valid")
	}
	for _, bad := range []string{"", "🔥", "../x", "1F525", "zzzz", "1f525_", "ffffff"} {
		if Valid(bad) {
			t.Errorf("%q must be rejected", bad)
		}
	}
	for _, d := range Defaults {
		if b, ok := Bundled(d); !ok || len(b) < 1000 || string(b[8:12]) != "WEBP" {
			t.Errorf("default %s is not bundled as WebP", d)
		}
	}
	if len(IndexJSON()) < 1000 {
		t.Fatal("index is empty")
	}
}

func TestStoreDownloadsOnceAndCaches(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	gif := filepath.Join(t.TempDir(), "a.gif")
	if out, err := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=64x64:rate=10", "-t", "1", gif).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	data, _ := os.ReadFile(gif)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if !strings.HasSuffix(r.URL.Path, "/1f600/512.gif") {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	defer srv.Close()
	old := CDN
	CDN = srv.URL
	defer func() { CDN = old }()

	s := NewStore(t.TempDir(), ff)
	for i := 0; i < 3; i++ {
		b, err := s.Get(context.Background(), "1f600")
		if err != nil || string(b[8:12]) != "WEBP" {
			t.Fatalf("get: %v", err)
		}
	}
	if hits != 1 {
		t.Fatalf("downloaded %d times, want once", hits)
	}
	if _, err := s.Get(context.Background(), "nope"); err == nil {
		t.Fatal("unknown emoji must fail without downloading")
	}
	if b, _ := s.Get(context.Background(), "1f525"); len(b) == 0 || hits != 1 {
		t.Fatal("bundled emoji must not be downloaded")
	}
}
