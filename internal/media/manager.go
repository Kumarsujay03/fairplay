package media

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

func init() {
	_ = mime.AddExtensionType(".m3u8", "application/vnd.apple.mpegurl")
	_ = mime.AddExtensionType(".m4s", "video/iso.segment")
	_ = mime.AddExtensionType(".vtt", "text/vtt; charset=utf-8")
}

const (
	StateIdle     = "idle"
	StateProbing  = "probing"
	StateEncoding = "encoding"
	StateReady    = "ready"
	StateError    = "error"
)

type AudioTrack struct {
	Index int    `json:"index"`
	Label string `json:"label"`
	Lang  string `json:"lang"`
}

type SubTrack struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Lang  string `json:"lang"`
	URL   string `json:"url"`
}

// Status is everything the room and host console need about one file.
type Status struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	State    string       `json:"state"`
	Playable bool         `json:"playable"`
	Encoded  float64      `json:"encoded"` // seconds encoded so far
	Duration float64      `json:"duration"`
	Speed    float64      `json:"speed"` // encoder speed vs realtime
	Error    string       `json:"error,omitempty"`
	Encoder  string       `json:"encoder,omitempty"`
	Rungs    []string     `json:"rungs,omitempty"`
	Audio    []AudioTrack `json:"audio,omitempty"`
	Subs     []SubTrack   `json:"subs,omitempty"`
	Notes    []string     `json:"notes,omitempty"`
	Width    int          `json:"width,omitempty"`
	Height   int          `json:"height,omitempty"`
	Bitrate  int          `json:"bitrate,omitempty"` // top rung kbps, for the upload estimate
	key      string
}

type Manager struct {
	root, realRoot string
	cache          string
	ffmpeg         string
	ffprobe        string
	enc            Encoder
	tonemap        bool

	mu       sync.Mutex
	items    map[string]Item
	statuses map[string]*Status
	running  *job
	OnUpdate func(Status)
}

type job struct {
	id     string
	cancel context.CancelFunc
	done   chan struct{}
}

type Options struct {
	Root, Cache, FFmpeg, FFprobe string
}

func NewManager(ctx context.Context, o Options) (*Manager, error) {
	realRoot, err := filepath.EvalSymlinks(o.Root)
	if err != nil {
		return nil, fmt.Errorf("media folder: %w", err)
	}
	if err := os.MkdirAll(o.Cache, 0o700); err != nil {
		return nil, err
	}
	for _, bin := range []string{o.FFmpeg, o.FFprobe} {
		if _, err := exec.LookPath(bin); err != nil {
			return nil, fmt.Errorf("%s not found: install ffmpeg or pass -ffmpeg/-ffprobe", bin)
		}
	}
	m := &Manager{
		root: o.Root, realRoot: realRoot, cache: o.Cache, ffmpeg: o.FFmpeg, ffprobe: o.FFprobe,
		items: map[string]Item{}, statuses: map[string]*Status{},
	}
	m.enc = DetectEncoder(ctx, o.FFmpeg)
	m.tonemap = HasFilter(o.FFmpeg, "zscale") && HasFilter(o.FFmpeg, "tonemap")
	return m, nil
}

func (m *Manager) EncoderName() string { return m.enc.Name }

// Root is the folder videos are listed from.
func (m *Manager) Root() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.realRoot
}

// SetRoot switches the library to another folder. Finished encodes stay in
// the cache and are found again if the old folder is chosen later.
func (m *Manager) SetRoot(dir string) error {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("folder not found: %s", dir)
	}
	st, err := os.Stat(real)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("not a folder: %s", dir)
	}
	m.mu.Lock()
	m.root, m.realRoot = dir, real
	m.items = map[string]Item{}
	m.mu.Unlock()
	return nil
}

// List rescans the folder and returns items with their current status.
func (m *Manager) List() ([]Item, map[string]Status, error) {
	items, err := scan(m.Root())
	if err != nil {
		return nil, nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = map[string]Item{}
	sts := map[string]Status{}
	for _, it := range items {
		m.items[it.ID] = it
		if st, ok := m.statuses[it.ID]; ok {
			sts[it.ID] = *st
		} else if st, ok := m.loadCachedLocked(it); ok {
			sts[it.ID] = st
		}
	}
	return items, sts, nil
}

func (m *Manager) Item(id string) (Item, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.items[id]
	if !ok {
		m.mu.Unlock()
		_, _, _ = m.List()
		m.mu.Lock()
		it, ok = m.items[id]
	}
	return it, ok
}

func (m *Manager) Status(id string) (Status, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.statuses[id]
	if !ok {
		return Status{}, false
	}
	return *st, true
}

// Prepare makes sure id is (being) transcoded. Only one encode runs at a time;
// preparing a different file cancels the running one so the room always gets
// the CPU.
func (m *Manager) Prepare(id string) error {
	it, ok := m.Item(id)
	if !ok {
		return errors.New("file not found")
	}
	m.mu.Lock()
	if st, ok := m.statuses[id]; ok && (st.State == StateReady || st.State == StateEncoding || st.State == StateProbing) {
		m.mu.Unlock()
		return nil
	}
	if st, ok := m.loadCachedLocked(it); ok && st.State == StateReady {
		m.mu.Unlock()
		m.emit(st)
		return nil
	}
	prev := m.running
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{id: id, cancel: cancel, done: make(chan struct{})}
	m.running = j
	st := &Status{ID: id, Name: it.Name, State: StateProbing, Encoder: m.enc.Name, key: cacheKey(it)}
	m.statuses[id] = st
	m.mu.Unlock()

	if prev != nil {
		prev.cancel()
		<-prev.done
	}
	m.emit(*st)
	go m.run(ctx, j, it)
	return nil
}

// Stop cancels any running encode (used on shutdown).
func (m *Manager) Stop() {
	m.mu.Lock()
	j := m.running
	m.mu.Unlock()
	if j != nil {
		j.cancel()
		<-j.done
	}
}

func (m *Manager) run(ctx context.Context, j *job, it Item) {
	defer close(j.done)
	defer func() {
		m.mu.Lock()
		if m.running == j {
			m.running = nil
		}
		m.mu.Unlock()
	}()
	fail := func(err error) {
		if ctx.Err() != nil {
			m.update(it.ID, func(s *Status) { s.State = StateIdle; s.Playable = false })
			return
		}
		log.Printf("media: %s: %v", it.RelPath, err)
		m.update(it.ID, func(s *Status) { s.State = StateError; s.Error = err.Error(); s.Playable = false })
	}

	info, err := probe(ctx, m.ffprobe, it.abs)
	if err != nil {
		fail(err)
		return
	}
	dir := filepath.Join(m.cache, cacheKey(it))
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fail(err)
		return
	}

	args, rungs, audio := buildArgs(it.abs, info, m.enc, m.tonemap)
	var notes []string
	if info.Video.HDR && !m.tonemap {
		notes = append(notes, "HDR source: this ffmpeg has no tonemap filter, colours may look washed out")
	}
	if len(info.Audio) > maxAudioTracks {
		notes = append(notes, fmt.Sprintf("only the first %d audio tracks are included", maxAudioTracks))
	}
	for _, s := range info.Subs {
		if !s.Text {
			notes = append(notes, fmt.Sprintf("subtitle track %d (%s) is a picture format and cannot be shown", s.Index+1, s.Codec))
		}
	}
	var at []AudioTrack
	for i, a := range audio {
		label := a.Title
		if label == "" {
			label = langName(a.Lang)
		}
		if label == "Unknown" {
			label = fmt.Sprintf("Track %d", i+1)
		}
		at = append(at, AudioTrack{Index: i, Label: label, Lang: safeLang(a.Lang)})
	}
	var rn []string
	for _, r := range rungs {
		rn = append(rn, r.Name)
	}
	m.update(it.ID, func(s *Status) {
		s.State, s.Duration, s.Rungs, s.Audio, s.Notes = StateEncoding, info.Duration, rn, at, notes
		s.Width, s.Height, s.Bitrate = info.Video.Width, info.Video.Height, rungs[0].Bitrate+128
	})

	// Subtitles run alongside the video encode.
	subDone := make(chan []SubTrack, 1)
	go func() { subDone <- m.extractSubs(ctx, dir, it, info) }()

	cmd := exec.CommandContext(ctx, m.ffmpeg, args...)
	cmd.Dir = dir
	stdout, _ := cmd.StdoutPipe()
	var stderr strings.Builder
	cmd.Stderr = &limitWriter{w: &stderr, n: 8192}
	if err := cmd.Start(); err != nil {
		fail(err)
		return
	}
	lowerPriority(cmd.Process.Pid)

	master := filepath.Join(dir, "master.m3u8")
	sc := bufio.NewScanner(stdout)
	var lastEmit time.Time
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch k {
		case "out_time_us", "out_time_ms":
			us, err := strconv.ParseInt(v, 10, 64)
			if err != nil || us < 0 {
				continue
			}
			sec := float64(us) / 1e6
			playable := sec >= minf(12, info.Duration-0.5) && fileExists(master)
			if time.Since(lastEmit) > 700*time.Millisecond {
				lastEmit = time.Now()
				m.update(it.ID, func(s *Status) { s.Encoded = sec; s.Playable = s.Playable || playable })
			}
		case "speed":
			f, _ := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(v), "x"), 64)
			m.mu.Lock()
			if s, ok := m.statuses[it.ID]; ok {
				s.Speed = f
			}
			m.mu.Unlock()
		}
	}
	err = cmd.Wait()
	subs := <-subDone
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		_ = os.RemoveAll(dir)
		fail(errors.New(lastLine(msg)))
		return
	}
	m.update(it.ID, func(s *Status) {
		s.State, s.Playable, s.Encoded, s.Subs = StateReady, true, info.Duration, subs
	})
	m.mu.Lock()
	final := *m.statuses[it.ID]
	m.mu.Unlock()
	if b, err := json.Marshal(final); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "status.json"), b, 0o600)
		_ = os.WriteFile(filepath.Join(dir, ".done"), nil, 0o600)
	}
}

func (m *Manager) extractSubs(ctx context.Context, dir string, it Item, info Info) []SubTrack {
	var out []SubTrack
	args, keep := subtitleArgs(it.abs, info.Subs)
	if len(keep) > 0 {
		cmd := exec.CommandContext(ctx, m.ffmpeg, args...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			log.Printf("media: subtitles for %s: %v", it.RelPath, err)
		} else {
			for _, s := range keep {
				label := s.Title
				if label == "" {
					label = langName(s.Lang)
				}
				id := fmt.Sprintf("sub_%d", s.Index)
				out = append(out, SubTrack{ID: id, Label: label, Lang: safeLang(s.Lang), URL: id + ".vtt"})
			}
		}
	}
	for i, side := range sidecarSubs(m.Root(), it.abs) {
		id := fmt.Sprintf("ext_%d", i)
		cmd := exec.CommandContext(ctx, m.ffmpeg, "-hide_banner", "-nostdin", "-loglevel", "error", "-y",
			"-i", side, "-c:s", "webvtt", "-f", "webvtt", id+".vtt")
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			log.Printf("media: sidecar %s: %v", side, err)
			continue
		}
		label, lang := subLabel(it.abs, side)
		out = append(out, SubTrack{ID: id, Label: label, Lang: safeLang(lang), URL: id + ".vtt"})
	}
	if len(out) > 0 {
		m.update(it.ID, func(s *Status) { s.Subs = out })
	}
	return out
}

func (m *Manager) loadCachedLocked(it Item) (Status, bool) {
	dir := filepath.Join(m.cache, cacheKey(it))
	if !fileExists(filepath.Join(dir, ".done")) {
		return Status{}, false
	}
	b, err := os.ReadFile(filepath.Join(dir, "status.json"))
	if err != nil {
		return Status{}, false
	}
	var st Status
	if json.Unmarshal(b, &st) != nil {
		return Status{}, false
	}
	st.ID, st.Name, st.key = it.ID, it.Name, cacheKey(it)
	m.statuses[it.ID] = &st
	return st, true
}

func (m *Manager) update(id string, f func(*Status)) {
	m.mu.Lock()
	st, ok := m.statuses[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	f(st)
	cp := *st
	m.mu.Unlock()
	m.emit(cp)
}

func (m *Manager) emit(st Status) {
	if m.OnUpdate != nil {
		m.OnUpdate(st)
	}
}

var hlsFileRe = regexp.MustCompile(`^(master\.m3u8|s_[av][0-9]{1,2}/(index\.m3u8|init(_[0-9]{1,2})?\.mp4|seg_[0-9]{5}\.m4s)|(sub|ext)_[0-9]{1,3}\.vtt)$`)

// ServeHLS serves one file of a transcode. Only the exact file names ffmpeg
// produces are allowed, so nothing else in the cache can be reached.
func (m *Manager) ServeHLS(w http.ResponseWriter, r *http.Request, id, file string) {
	if !hlsFileRe.MatchString(file) {
		http.NotFound(w, r)
		return
	}
	m.mu.Lock()
	st, ok := m.statuses[id]
	var key string
	if ok && st.Playable {
		key = st.key
	}
	m.mu.Unlock()
	if key == "" {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(file, ".m3u8") {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "private, max-age=86400")
	}
	http.ServeFile(w, r, filepath.Join(m.cache, key, filepath.FromSlash(file)))
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

type limitWriter struct {
	w *strings.Builder
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.w.Len() < l.n {
		rest := l.n - l.w.Len()
		if len(p) > rest {
			l.w.Write(p[:rest])
		} else {
			l.w.Write(p)
		}
	}
	return len(p), nil
}
