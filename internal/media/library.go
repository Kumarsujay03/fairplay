// Package media finds videos in the host's folder, probes them, transcodes
// them into an adaptive HLS ladder and serves the result.
package media

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

// ladderVersion changes whenever the encoding ladder changes, so files
// prepared by an older FairPlay are prepared again.
const ladderVersion = "ladder-v5"

func cacheKey(it Item, versions string) string {
	h := sha1.New()
	h.Write([]byte(ladderVersion))
	h.Write([]byte(NormalizeVersions(versions)))
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
	"en":    "English",
	"eng":   "English",
	"hi":    "Hindi",
	"hin":   "Hindi",
	"es":    "Spanish",
	"spa":   "Spanish",
	"fr":    "French",
	"fre":   "French",
	"fra":   "French",
	"de":    "German",
	"ger":   "German",
	"deu":   "German",
	"it":    "Italian",
	"ita":   "Italian",
	"pt":    "Portuguese",
	"por":   "Portuguese",
	"pt-br": "Brazilian Portuguese",
	"pob":   "Brazilian Portuguese",
	"ru":    "Russian",
	"rus":   "Russian",
	"ja":    "Japanese",
	"jpn":   "Japanese",
	"ko":    "Korean",
	"kor":   "Korean",
	"zh":    "Chinese",
	"chi":   "Chinese",
	"zho":   "Chinese",
	"yue":   "Cantonese",
	"cmn":   "Mandarin",
	"ar":    "Arabic",
	"ara":   "Arabic",
	"bn":    "Bengali",
	"ben":   "Bengali",
	"ta":    "Tamil",
	"tam":   "Tamil",
	"te":    "Telugu",
	"tel":   "Telugu",
	"mr":    "Marathi",
	"mar":   "Marathi",
	"ml":    "Malayalam",
	"mal":   "Malayalam",
	"kn":    "Kannada",
	"kan":   "Kannada",
	"pa":    "Punjabi",
	"pan":   "Punjabi",
	"gu":    "Gujarati",
	"guj":   "Gujarati",
	"or":    "Odia",
	"ori":   "Odia",
	"as":    "Assamese",
	"asm":   "Assamese",
	"ur":    "Urdu",
	"urd":   "Urdu",
	"ne":    "Nepali",
	"nep":   "Nepali",
	"si":    "Sinhala",
	"sin":   "Sinhala",
	"sa":    "Sanskrit",
	"san":   "Sanskrit",
	"id":    "Indonesian",
	"ind":   "Indonesian",
	"ms":    "Malay",
	"may":   "Malay",
	"msa":   "Malay",
	"fil":   "Filipino",
	"tl":    "Tagalog",
	"tgl":   "Tagalog",
	"th":    "Thai",
	"tha":   "Thai",
	"vi":    "Vietnamese",
	"vie":   "Vietnamese",
	"my":    "Burmese",
	"bur":   "Burmese",
	"mya":   "Burmese",
	"km":    "Khmer",
	"khm":   "Khmer",
	"lo":    "Lao",
	"lao":   "Lao",
	"tr":    "Turkish",
	"tur":   "Turkish",
	"fa":    "Persian",
	"per":   "Persian",
	"fas":   "Persian",
	"he":    "Hebrew",
	"heb":   "Hebrew",
	"iw":    "Hebrew",
	"el":    "Greek",
	"gre":   "Greek",
	"ell":   "Greek",
	"nl":    "Dutch",
	"dut":   "Dutch",
	"nld":   "Dutch",
	"vls":   "Flemish",
	"sv":    "Swedish",
	"swe":   "Swedish",
	"no":    "Norwegian",
	"nor":   "Norwegian",
	"nb":    "Norwegian Bokmål",
	"nob":   "Norwegian Bokmål",
	"nn":    "Norwegian Nynorsk",
	"nno":   "Norwegian Nynorsk",
	"da":    "Danish",
	"dan":   "Danish",
	"fi":    "Finnish",
	"fin":   "Finnish",
	"is":    "Icelandic",
	"ice":   "Icelandic",
	"isl":   "Icelandic",
	"pl":    "Polish",
	"pol":   "Polish",
	"cs":    "Czech",
	"cze":   "Czech",
	"ces":   "Czech",
	"sk":    "Slovak",
	"slo":   "Slovak",
	"slk":   "Slovak",
	"hu":    "Hungarian",
	"hun":   "Hungarian",
	"ro":    "Romanian",
	"rum":   "Romanian",
	"ron":   "Romanian",
	"bg":    "Bulgarian",
	"bul":   "Bulgarian",
	"sr":    "Serbian",
	"srp":   "Serbian",
	"scc":   "Serbian",
	"hr":    "Croatian",
	"hrv":   "Croatian",
	"scr":   "Croatian",
	"bs":    "Bosnian",
	"bos":   "Bosnian",
	"sl":    "Slovenian",
	"slv":   "Slovenian",
	"mk":    "Macedonian",
	"mac":   "Macedonian",
	"mkd":   "Macedonian",
	"sq":    "Albanian",
	"alb":   "Albanian",
	"sqi":   "Albanian",
	"uk":    "Ukrainian",
	"ukr":   "Ukrainian",
	"be":    "Belarusian",
	"bel":   "Belarusian",
	"lt":    "Lithuanian",
	"lit":   "Lithuanian",
	"lv":    "Latvian",
	"lav":   "Latvian",
	"et":    "Estonian",
	"est":   "Estonian",
	"ka":    "Georgian",
	"geo":   "Georgian",
	"kat":   "Georgian",
	"hy":    "Armenian",
	"arm":   "Armenian",
	"hye":   "Armenian",
	"az":    "Azerbaijani",
	"aze":   "Azerbaijani",
	"kk":    "Kazakh",
	"kaz":   "Kazakh",
	"uz":    "Uzbek",
	"uzb":   "Uzbek",
	"mn":    "Mongolian",
	"mon":   "Mongolian",
	"ca":    "Catalan",
	"cat":   "Catalan",
	"eu":    "Basque",
	"baq":   "Basque",
	"eus":   "Basque",
	"gl":    "Galician",
	"glg":   "Galician",
	"cy":    "Welsh",
	"wel":   "Welsh",
	"cym":   "Welsh",
	"ga":    "Irish",
	"gle":   "Irish",
	"gd":    "Scottish Gaelic",
	"gla":   "Scottish Gaelic",
	"sw":    "Swahili",
	"swa":   "Swahili",
	"am":    "Amharic",
	"amh":   "Amharic",
	"so":    "Somali",
	"som":   "Somali",
	"yo":    "Yoruba",
	"yor":   "Yoruba",
	"ha":    "Hausa",
	"hau":   "Hausa",
	"ig":    "Igbo",
	"ibo":   "Igbo",
	"zu":    "Zulu",
	"zul":   "Zulu",
	"xh":    "Xhosa",
	"xho":   "Xhosa",
	"af":    "Afrikaans",
	"afr":   "Afrikaans",
	"mg":    "Malagasy",
	"mlg":   "Malagasy",
	"la":    "Latin",
	"lat":   "Latin",
	"eo":    "Esperanto",
	"epo":   "Esperanto",
	"lb":    "Luxembourgish",
	"ltz":   "Luxembourgish",
	"mt":    "Maltese",
	"mlt":   "Maltese",
	"fo":    "Faroese",
	"fao":   "Faroese",
	"ps":    "Pashto",
	"pus":   "Pashto",
	"ku":    "Kurdish",
	"kur":   "Kurdish",
	"bo":    "Tibetan",
	"tib":   "Tibetan",
	"bod":   "Tibetan",
	"ug":    "Uyghur",
	"uig":   "Uyghur",
	"ht":    "Haitian Creole",
	"hat":   "Haitian Creole",
	"sgn":   "Sign language",
	"zxx":   "No language",
	"mul":   "Multiple languages",
	"und":   "Unknown",
}

func langName(code string) string {
	c := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(code, "_", "-")))
	if n, ok := langNames[c]; ok {
		return n
	}
	if base, _, ok := strings.Cut(c, "-"); ok {
		if n, ok := langNames[base]; ok {
			return n
		}
	}
	// Some files spell the language out ("English", "Hindi").
	for _, n := range langNames {
		if strings.EqualFold(n, c) {
			return n
		}
	}
	if code == "" {
		return "Unknown"
	}
	return strings.ToUpper(code)
}

var junkTitleRe = regexp.MustCompile(`(?i)(https?://|www\.|@|\.(com|in|net|org|io|me|co|to|cc|tv|xyz|site|info|club|link|ws|lol|ai)\b|t\.me/|telegram)`)

// trackLabel names an audio or subtitle track for people: "English",
// "Hindi · Commentary", "English · SDH". Track titles are kept only when they
// add something; release-group tags such as "MoviesMod.ai.in", or one title
// repeated on every track, are dropped in favour of the language.
func trackLabel(lang, title string, allTitles []string, fallback string, extra ...string) string {
	name := ""
	if l := strings.ToLower(strings.TrimSpace(lang)); l != "" && l != "und" {
		name = langName(l)
	}
	t := strings.TrimSpace(title)
	same := 0
	for _, o := range allTitles {
		if strings.EqualFold(strings.TrimSpace(o), t) {
			same++
		}
	}
	useful := t != "" && !junkTitleRe.MatchString(t) && !(same > 1 && len(allTitles) > 1) && len([]rune(t)) <= 40
	if useful && name != "" && (strings.EqualFold(t, name) || strings.EqualFold(t, lang)) {
		useful = false // "English" titled "English" or "eng"
	}
	switch {
	case name == "" && useful:
		name = t
	case name == "":
		name = fallback
	case useful:
		extra = append([]string{t}, extra...)
	}
	for _, e := range extra {
		if !strings.Contains(strings.ToLower(name), strings.ToLower(e)) {
			name += " · " + e
		}
	}
	return name
}

// dedupeLabels turns ["English", "English"] into ["English", "English 2"].
func dedupeLabels(in []string) []string {
	count := map[string]int{}
	for _, l := range in {
		count[l]++
	}
	seen := map[string]int{}
	out := make([]string, len(in))
	for i, l := range in {
		if count[l] > 1 {
			seen[l]++
			if seen[l] > 1 {
				out[i] = fmt.Sprintf("%s %d", l, seen[l])
				continue
			}
		}
		out[i] = l
	}
	return out
}
