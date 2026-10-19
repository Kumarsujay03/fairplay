package media

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// partialFile marks a cache folder whose encode was interrupted (shutdown,
// Cancel, or another file taking its turn) and can be continued.
const partialFile = "partial.json"

// partial is what an interrupted encode had done and what it was making.
// The plan fields must match the next run exactly, or the segments on disk
// would not fit the ones the new run appends.
type partial struct {
	Ladder    string   `json:"ladder"`
	Versions  string   `json:"versions"`
	Encoder   string   `json:"encoder"`
	Rungs     []string `json:"rungs"`
	Audio     int      `json:"audio"`
	Segments  int      `json:"segments"`  // whole segments present in every version
	Completed float64  `json:"completed"` // seconds prepared: Segments * segmentSeconds
}

// samePlan reports whether two partials describe the same output.
func (p partial) samePlan(q partial) bool {
	return p.Ladder == q.Ladder && p.Versions == q.Versions && p.Encoder == q.Encoder &&
		p.Audio == q.Audio && strings.Join(p.Rungs, ",") == strings.Join(q.Rungs, ",")
}

// variantDirs are the per-version folders ffmpeg writes for a plan: the
// video versions first, then the audio tracks, in the order of -map.
func (p partial) variantDirs(dir string) []string {
	var out []string
	for i := range p.Rungs {
		out = append(out, filepath.Join(dir, fmt.Sprintf("s_v%d", i)))
	}
	for i := 0; i < p.Audio; i++ {
		out = append(out, filepath.Join(dir, fmt.Sprintf("s_a%d", i)))
	}
	return out
}

// playlistSegments returns the segment file names a variant playlist lists.
func playlistSegments(playlist string) ([]string, error) {
	b, err := os.ReadFile(playlist)
	if err != nil {
		return nil, err
	}
	var segs []string
	inf := false
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "#EXTINF:"):
			inf = true
		case l == "" || strings.HasPrefix(l, "#"):
		case inf:
			segs = append(segs, l)
			inf = false
		}
	}
	return segs, nil
}

// trimPlaylist cuts a variant playlist down to its first n segments and
// deletes the segment files after them, so every version stops at the same
// place before the encode is continued.
func trimPlaylist(playlist string, n int) error {
	b, err := os.ReadFile(playlist)
	if err != nil {
		return err
	}
	// The header is everything before the first segment; an entry is a
	// segment's tags (EXTINF, maybe a discontinuity) followed by its file.
	var header, cur []string
	var entries [][]string
	for _, l := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(l)
		if t == "" || t == "#EXT-X-ENDLIST" {
			continue
		}
		if len(entries) == 0 && len(cur) == 0 && t != "#EXT-X-DISCONTINUITY" && !strings.HasPrefix(t, "#EXTINF:") {
			header = append(header, t)
			continue
		}
		cur = append(cur, t)
		if !strings.HasPrefix(t, "#") {
			entries = append(entries, cur)
			cur = nil
		}
	}
	if n > len(entries) {
		return fmt.Errorf("%s lists %d segments, expected at least %d", filepath.Base(filepath.Dir(playlist)), len(entries), n)
	}
	for _, e := range entries[n:] {
		_ = os.Remove(filepath.Join(filepath.Dir(playlist), e[len(e)-1]))
	}
	keep := header
	for _, e := range entries[:n] {
		keep = append(keep, e...)
	}
	return os.WriteFile(playlist, []byte(strings.Join(keep, "\n")+"\n"), 0o600)
}

// savePartial records an interrupted encode in dir so the next run can
// continue it. encoded is the last output time ffmpeg reported. Every
// version is cut back to the same number of whole segments. It returns the
// seconds kept, or 0 when there is nothing worth keeping (the folder is
// then removed by the caller).
func savePartial(dir string, p partial, encoded float64) (float64, error) {
	n := int(math.Floor(encoded / segmentSeconds))
	dirs := p.variantDirs(dir)
	var lists [][]string
	for _, d := range dirs {
		segs, err := playlistSegments(filepath.Join(d, "index.m3u8"))
		if err != nil {
			return 0, err
		}
		lists = append(lists, segs)
		if len(segs) < n {
			n = len(segs)
		}
	}
	if n <= 0 {
		return 0, errors.New("no whole segment yet")
	}
	for i, d := range dirs {
		if len(lists[i]) > n {
			if err := trimPlaylist(filepath.Join(d, "index.m3u8"), n); err != nil {
				return 0, err
			}
		}
		for _, f := range lists[i][:n] {
			if !fileExists(filepath.Join(d, f)) {
				return 0, fmt.Errorf("%s is missing", f)
			}
		}
		removeTempFiles(d)
	}
	p.Segments, p.Completed = n, float64(n*segmentSeconds)
	b, err := json.Marshal(p)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(dir, partialFile), b, 0o600); err != nil {
		return 0, err
	}
	touch(dir)
	return p.Completed, nil
}

// loadPartial checks whether dir holds an interrupted encode of exactly the
// plan want and returns the seconds it may continue from. Anything that does
// not add up (another plan, a damaged playlist, a missing segment) returns 0
// and an explanation: the encode then starts over.
func loadPartial(dir string, want partial, duration float64) (float64, error) {
	b, err := os.ReadFile(filepath.Join(dir, partialFile))
	if err != nil {
		return 0, err
	}
	var p partial
	if err := json.Unmarshal(b, &p); err != nil {
		return 0, err
	}
	if !p.samePlan(want) {
		return 0, errors.New("the plan changed")
	}
	if p.Segments <= 0 || p.Completed != float64(p.Segments*segmentSeconds) {
		return 0, errors.New("bad segment count")
	}
	if p.Completed >= duration-0.5 {
		return 0, errors.New("nothing left to encode")
	}
	if !fileExists(filepath.Join(dir, "master.m3u8")) {
		return 0, errors.New("master.m3u8 is missing")
	}
	for _, d := range p.variantDirs(dir) {
		segs, err := playlistSegments(filepath.Join(d, "index.m3u8"))
		if err != nil {
			return 0, err
		}
		if len(segs) != p.Segments {
			return 0, fmt.Errorf("%s lists %d segments, expected %d", filepath.Base(d), len(segs), p.Segments)
		}
		for _, f := range segs {
			if !fileExists(filepath.Join(d, f)) {
				return 0, fmt.Errorf("%s/%s is missing", filepath.Base(d), f)
			}
		}
		if m, _ := filepath.Glob(filepath.Join(d, "init*.mp4")); len(m) == 0 {
			return 0, fmt.Errorf("%s has no init segment", filepath.Base(d))
		}
		removeTempFiles(d)
	}
	return p.Completed, nil
}

// removeTempFiles drops the segment ffmpeg was writing when it was stopped.
func removeTempFiles(d string) {
	m, _ := filepath.Glob(filepath.Join(d, "*.tmp"))
	for _, f := range m {
		_ = os.Remove(f)
	}
}
