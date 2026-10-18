// Package media finds videos in the host's folder, probes them, transcodes
// them into an adaptive HLS ladder and serves the result.
package media

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var videoExt = map[string]bool{
	".mp4": true, ".m4v": true, ".mkv": true, ".mov": true, ".avi": true, ".webm": true,
	".ts": true, ".m2ts": true, ".mts": true, ".wmv": true, ".flv": true, ".mpg": true,
	".mpeg": true, ".ogv": true, ".3gp": true,
}

var subExt = map[string]bool{".srt": true, ".vtt": true, ".ass": true, ".ssa": true}

// Item is one playable file in the library.
type Item struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	RelPath string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	abs     string
}

var errOutside = errors.New("path escapes the media folder")

// scan walks root (max depth 4) and returns every video it finds. Symlinks are
// followed only when their target stays inside root.
func scan(root string) ([]Item, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	out := []Item{} // never nil, so an empty folder is [] in JSON
	err = filepath.WalkDir(realRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		rel, _ := filepath.Rel(realRoot, p)
		if d.IsDir() {
			if p != realRoot && (strings.HasPrefix(d.Name(), ".") || strings.Count(rel, string(filepath.Separator)) >= 4) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || !videoExt[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		abs, err := safeResolve(realRoot, p)
		if err != nil {
			return nil
		}
		st, err := os.Stat(abs)
		if err != nil || !st.Mode().IsRegular() {
			return nil
		}
		out = append(out, Item{
			ID: idFor(realRoot, rel), Name: prettyName(d.Name()), RelPath: filepath.ToSlash(rel),
			Size: st.Size(), ModTime: st.ModTime(), abs: abs,
		})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].RelPath) < strings.ToLower(out[j].RelPath) })
	return out, err
}

// safeResolve resolves symlinks and refuses anything outside root.
func safeResolve(realRoot, p string) (string, error) {
	abs, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(realRoot, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", errOutside
	}
	return abs, nil
}

// sidecarSubs finds "Movie.srt", "Movie.en.srt", "Movie.English.vtt" next to the video.
func sidecarSubs(realRoot, video string) []string {
	dir := filepath.Dir(video)
	base := strings.TrimSuffix(filepath.Base(video), filepath.Ext(video))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !subExt[strings.ToLower(filepath.Ext(n))] {
			continue
		}
		if n == base+filepath.Ext(n) || strings.HasPrefix(n, base+".") {
			if abs, err := safeResolve(realRoot, filepath.Join(dir, n)); err == nil {
				out = append(out, abs)
			}
		}
	}
	sort.Strings(out)
	return out
}

// idFor is stable for a file within a folder and differs between folders.
func idFor(root, rel string) string {
	h := sha1.Sum([]byte(root + "\x00" + filepath.ToSlash(rel)))
	return hex.EncodeToString(h[:6])
}

// IsVideo reports whether a file name looks like a video FairPlay can read.
func IsVideo(name string) bool { return videoExt[strings.ToLower(filepath.Ext(name))] }

// Listing is one folder in the host's folder picker.
type Listing struct {
	Path   string   `json:"path"`
	Parent string   `json:"parent,omitempty"`
	Dirs   []string `json:"dirs"`
	Videos int      `json:"videos"` // video files directly inside this folder
}

// Browse lists the sub-folders of dir for the host's folder picker. Hidden
// folders are skipped. Only the host can call this.
func Browse(dir string) (Listing, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Listing{}, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return Listing{}, err
	}
	l := Listing{Path: abs, Dirs: []string{}}
	if p := filepath.Dir(abs); p != abs {
		l.Parent = p
	}
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, ".") {
			continue
		}
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(abs, n)); err == nil {
				isDir = st.IsDir()
			}
		}
		if isDir {
			if len(l.Dirs) < 500 {
				l.Dirs = append(l.Dirs, n)
			}
		} else if IsVideo(n) {
			l.Videos++
		}
	}
	sort.Slice(l.Dirs, func(i, j int) bool { return strings.ToLower(l.Dirs[i]) < strings.ToLower(l.Dirs[j]) })
	return l, nil
}

func cacheKey(it Item) string {
	h := sha1.New()
	h.Write([]byte(it.abs))
	h.Write([]byte{0})
	h.Write([]byte(it.ModTime.UTC().Format(time.RFC3339Nano)))
	h.Write([]byte{0})
	h.Write([]byte(filepath.Base(it.abs)))
	var size [8]byte
	for i := 0; i < 8; i++ {
		size[i] = byte(it.Size >> (8 * i))
	}
	h.Write(size[:])
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// prettyName turns "Interstellar.2014.1080p.mkv" into "Interstellar 2014 1080p".
func prettyName(file string) string {
	n := strings.TrimSuffix(file, filepath.Ext(file))
	n = strings.NewReplacer(".", " ", "_", " ").Replace(n)
	return strings.Join(strings.Fields(n), " ")
}

// subLabel derives "English" or "en" from "Movie.en.srt".
func subLabel(video, sub string) (label, lang string) {
	base := strings.TrimSuffix(filepath.Base(video), filepath.Ext(video))
	n := strings.TrimSuffix(filepath.Base(sub), filepath.Ext(sub))
	rest := strings.TrimPrefix(strings.TrimPrefix(n, base), ".")
	if rest == "" {
		return "External", ""
	}
	parts := strings.Split(rest, ".")
	lang = strings.ToLower(parts[0])
	if len(lang) > 3 {
		return parts[0], ""
	}
	return langName(lang), lang
}

var langNames = map[string]string{
	"en": "English", "eng": "English", "hi": "Hindi", "hin": "Hindi", "es": "Spanish", "spa": "Spanish",
	"fr": "French", "fre": "French", "fra": "French", "de": "German", "ger": "German", "deu": "German",
	"it": "Italian", "ita": "Italian", "ja": "Japanese", "jpn": "Japanese", "ko": "Korean", "kor": "Korean",
	"zh": "Chinese", "chi": "Chinese", "zho": "Chinese", "ru": "Russian", "rus": "Russian", "pt": "Portuguese",
	"por": "Portuguese", "ar": "Arabic", "ara": "Arabic", "ta": "Tamil", "tam": "Tamil", "te": "Telugu",
	"tel": "Telugu", "bn": "Bengali", "ben": "Bengali", "mr": "Marathi", "mar": "Marathi", "ml": "Malayalam",
	"mal": "Malayalam", "kn": "Kannada", "kan": "Kannada", "pa": "Punjabi", "pan": "Punjabi", "ur": "Urdu",
	"urd": "Urdu", "gu": "Gujarati", "guj": "Gujarati", "tr": "Turkish", "tur": "Turkish", "nl": "Dutch",
	"dut": "Dutch", "nld": "Dutch", "pl": "Polish", "pol": "Polish", "sv": "Swedish", "swe": "Swedish",
	"und": "Unknown",
}

func langName(code string) string {
	if n, ok := langNames[strings.ToLower(code)]; ok {
		return n
	}
	if code == "" {
		return "Unknown"
	}
	return strings.ToUpper(code)
}
