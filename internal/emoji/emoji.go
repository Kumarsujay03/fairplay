// Package emoji serves animated reactions. The animations are Google's Noto
// Emoji Animation (CC BY 4.0, https://googlefonts.github.io/noto-emoji-animation/).
//
// The most used reactions are bundled in the binary. Any other emoji in the
// index is downloaded by the host's FairPlay the first time someone sends it,
// shrunk with ffmpeg and kept in the cache folder, so guests only ever talk to
// the host. Without internet the player falls back to the normal emoji glyph.
package emoji

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// Entry is one animated emoji in the picker.
type Entry struct {
	Code string `json:"c"` // codepoints joined by "_", e.g. "2764_fe0f"
	Name string `json:"n"`
	Cat  string `json:"k"`
	Pop  int    `json:"-"`
}

// CDN is where animations are downloaded from (overridable in tests).
var CDN = "https://fonts.gstatic.com/s/e/notoemoji/latest"

// Defaults are bundled so the reaction bar works offline.
var Defaults = []string{
	"1f525", "1f602", "1f62d", "1f44f", "2764_fe0f", "1f37f",
	"1f92f", "1f62e", "1f440", "1f480", "1f64c", "1f60d",
}

//go:embed index.json
var indexJSON []byte

//go:embed default/*.webp
var defaults embed.FS

var (
	codeRe  = regexp.MustCompile(`^[0-9a-f]{4,6}(_[0-9a-f]{4,6}){0,7}$`)
	known   map[string]bool
	knownMu sync.Once
)

// IndexJSON is the picker data, served to admitted viewers.
func IndexJSON() []byte { return indexJSON }

// Valid reports whether code is an animated emoji FairPlay knows.
func Valid(code string) bool {
	if !codeRe.MatchString(code) {
		return false
	}
	knownMu.Do(func() {
		var list []Entry
		_ = json.Unmarshal(indexJSON, &list)
		known = make(map[string]bool, len(list)+len(Defaults))
		for _, e := range list {
			known[e.Code] = true
		}
		for _, d := range Defaults {
			known[d] = true
		}
	})
	return known[code]
}

// Store finds or prepares the small animated WebP for an emoji.
type Store struct {
	Dir    string // cache folder for downloaded emoji
	FFmpeg string
	Client *http.Client

	mu       sync.Mutex
	inflight map[string]*call
	slots    chan struct{}
}

type call struct {
	done chan struct{}
	err  error
}

func NewStore(dir, ffmpeg string) *Store {
	return &Store{Dir: dir, FFmpeg: ffmpeg, Client: &http.Client{Timeout: 30 * time.Second},
		inflight: map[string]*call{}, slots: make(chan struct{}, 2)}
}

// Bundled returns the embedded animation for a default emoji.
func Bundled(code string) ([]byte, bool) {
	b, err := defaults.ReadFile("default/" + code + ".webp")
	return b, err == nil
}

// Get returns the animation bytes for code, preparing it if needed.
func (s *Store) Get(ctx context.Context, code string) ([]byte, error) {
	if !Valid(code) {
		return nil, errors.New("unknown emoji")
	}
	if b, ok := Bundled(code); ok {
		return b, nil
	}
	path := filepath.Join(s.Dir, code+".webp")
	if b, err := os.ReadFile(path); err == nil {
		return b, nil
	}
	// One download per emoji at a time, at most two at once overall.
	s.mu.Lock()
	c, ok := s.inflight[code]
	if !ok {
		c = &call{done: make(chan struct{})}
		s.inflight[code] = c
		go func() {
			s.slots <- struct{}{}
			c.err = Convert(s.Client, s.FFmpeg, code, path)
			<-s.slots
			s.mu.Lock()
			delete(s.inflight, code)
			s.mu.Unlock()
			close(c.done)
		}()
	}
	s.mu.Unlock()
	select {
	case <-c.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if c.err != nil {
		return nil, c.err
	}
	return os.ReadFile(path)
}

// Convert downloads the animated GIF for code from Google's emoji CDN and
// writes a small looping animated WebP (96 px, 15 fps) to dst.
func Convert(client *http.Client, ffmpeg, code, dst string) error {
	if !codeRe.MatchString(code) {
		return errors.New("bad emoji code")
	}
	url := fmt.Sprintf("%s/%s/512.gif", CDN, code)
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("emoji %s: %s", code, resp.Status)
	}
	gif, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "gif", "-i", "pipe:0",
		"-vf", "fps=15,scale=96:96:flags=lanczos",
		"-c:v", "libwebp_anim", "-lossless", "0", "-q:v", "55", "-compression_level", "6", "-loop", "0", "-an",
		"-f", "webp", tmp)
	cmd.Stdin = bytes.NewReader(gif)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("ffmpeg: %v %s", err, bytes.TrimSpace(out))
	}
	return os.Rename(tmp, dst)
}
