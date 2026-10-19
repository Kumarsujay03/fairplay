package media

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPlanLadder(t *testing.T) {
	cases := []struct {
		w, h int
		want string
	}{
		{3840, 2160, "1080p,720p,480p"},
		{1920, 1080, "1080p,720p,480p"},
		{1920, 800, "1080p,720p,480p"}, // 2.40:1 film is still 1080p
		{1920, 1040, "1080p,720p,480p"},
		{1280, 720, "720p,480p,360p"},
		{1280, 534, "720p,480p,360p"},
		{854, 480, "480p,360p"},
		{1080, 1920, "1080p,720p,480p"}, // portrait phone video
		{426, 240, "240p"},
	}
	for _, c := range cases {
		var got []string
		for _, r := range planLadder(c.w, c.h) {
			got = append(got, r.Name)
		}
		if strings.Join(got, ",") != c.want {
			t.Errorf("planLadder(%d,%d)=%v want %s", c.w, c.h, got, c.want)
		}
	}
	wide := planLadder(1920, 800)[0].Bitrate
	full := planLadder(1920, 1080)[0].Bitrate
	if !(wide < full && wide > full/2) {
		t.Errorf("wide film bitrate %d should be below 16:9 bitrate %d", wide, full)
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
	// A second manager (FairPlay restarted) finds the finished encode in the cache.
	m2, _ := NewManager(ctx, Options{Root: root, Cache: m.cache, FFmpeg: "ffmpeg", FFprobe: "ffprobe"})
	_, sts, _ := m2.List()
	if sts[id].State != StateReady {
		t.Fatalf("cache not reused: %+v", sts[id])
	}
	// Regression: playing a cached file after the library was listed must still
	// announce it as ready, or the room waits on "Preparing video…" forever.
	got := make(chan Status, 4)
	m2.OnUpdate = func(st Status) { got <- st }
	if err := m2.Prepare(id); err != nil {
		t.Fatal(err)
	}
	select {
	case st := <-got:
		if st.State != StateReady || !st.Playable {
			t.Fatalf("announced %+v", st)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a cached file was never announced as ready")
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

func TestTrackLabels(t *testing.T) {
	junk := []string{"MoviesMod.ai.in", "MoviesMod.ai.in"}
	if got := trackLabel("eng", "MoviesMod.ai.in", junk, "Track 1"); got != "English" {
		t.Errorf("junk title must fall back to the language, got %q", got)
	}
	if got := trackLabel("hin", "MoviesMod.ai.in", junk, "Track 2"); got != "Hindi" {
		t.Errorf("got %q", got)
	}
	if got := trackLabel("eng", "SDH", []string{"SDH", ""}, "x"); got != "English · SDH" {
		t.Errorf("useful title is kept, got %q", got)
	}
	if got := trackLabel("eng", "English", []string{"English"}, "x", "Forced"); got != "English · Forced" {
		t.Errorf("got %q", got)
	}
	if got := trackLabel("", "Director's commentary", []string{"Director's commentary"}, "Track 1"); got != "Director's commentary" {
		t.Errorf("no language: title is used, got %q", got)
	}
	if got := trackLabel("und", "www.site.com", []string{"www.site.com"}, "Track 3"); got != "Track 3" {
		t.Errorf("got %q", got)
	}
	if got := dedupeLabels([]string{"English", "English", "Hindi"}); strings.Join(got, ",") != "English,English 2,Hindi" {
		t.Errorf("got %v", got)
	}
}

func TestLangNames(t *testing.T) {
	cases := map[string]string{"ind": "Indonesian", "rum": "Romanian", "ron": "Romanian", "chi": "Chinese", "spa": "Spanish",
		"jpn": "Japanese", "hin": "Hindi", "en-US": "English", "pt_BR": "Brazilian Portuguese", "pt-PT": "Portuguese", "pob": "Brazilian Portuguese", "English": "English"}
	for code, want := range cases {
		if got := langName(code); got != want {
			t.Errorf("langName(%q)=%q want %q", code, got, want)
		}
	}
	// The real-world case: 12 subtitle tracks all titled with the uploader's site.
	langs := []string{"eng", "eng", "eng", "eng", "eng", "chi", "spa", "spa", "ind", "jpn", "jpn", "rum"}
	titles := make([]string, len(langs))
	for i := range titles {
		titles[i] = "MoviesMod.ai.in"
	}
	var labels []string
	for i, l := range langs {
		labels = append(labels, trackLabel(l, titles[i], titles, "x"))
	}
	got := strings.Join(dedupeLabels(labels), ", ")
	want := "English, English 2, English 3, English 4, English 5, Chinese, Spanish, Spanish 2, Indonesian, Japanese, Japanese 2, Romanian"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestCachePruneAndClear(t *testing.T) {
	cache := t.TempDir()
	mk := func(key, name string, mb int, age time.Duration) {
		d := filepath.Join(cache, key)
		os.MkdirAll(d, 0o700)
		os.WriteFile(filepath.Join(d, "seg.m4s"), make([]byte, mb<<20), 0o600)
		os.WriteFile(filepath.Join(d, "status.json"), []byte(`{"name":"`+name+`"}`), 0o600)
		os.WriteFile(filepath.Join(d, ".done"), nil, 0o600)
		ts := time.Now().Add(-age)
		os.Chtimes(d, ts, ts)
	}
	mk("old", "Old film", 3, 72*time.Hour)
	mk("mid", "Mid film", 3, 24*time.Hour)
	mk("cur", "Current film", 3, 96*time.Hour) // oldest, but playing right now
	m := &Manager{cache: cache, items: map[string]Item{}, statuses: map[string]*Status{"room": {ID: "room", key: "cur"}}}

	list, total := CacheUsage(cache)
	if len(list) != 3 || total < 9<<20 || list[0].Name != "Mid film" {
		t.Fatalf("usage %+v total %d", list, total)
	}
	freed := m.Prune(7<<20, "room") // 9 MB used, limit 7 MB: only the oldest unused film goes
	if freed < 3<<20 || fileExists(filepath.Join(cache, "old")) || !fileExists(filepath.Join(cache, "cur")) || !fileExists(filepath.Join(cache, "mid")) {
		t.Fatalf("prune freed %d; old gone=%v cur kept=%v", freed, !fileExists(filepath.Join(cache, "old")), fileExists(filepath.Join(cache, "cur")))
	}
	m.ClearCache("room")
	if fileExists(filepath.Join(cache, "mid")) || !fileExists(filepath.Join(cache, "cur")) {
		t.Fatal("clear must remove everything except the video in use")
	}
	if m.Prune(0, "") != 0 {
		t.Fatal("limit 0 means no pruning")
	}
}

func TestPlanVersions(t *testing.T) {
	h264 := &VideoInfo{Codec: "h264", Width: 1920, Height: 800, PixFmt: "yuv420p", Kbps: 2600}
	names := func(rs []rung) string {
		var s []string
		for _, r := range rs {
			s = append(s, r.Name)
		}
		return strings.Join(s, ",")
	}
	cases := map[string]string{
		VersionsOriginal: "1080p",
		VersionsBalanced: "1080p,720p",
		VersionsFull:     "1080p,720p,480p",
		"nonsense":       "1080p,720p",
	}
	for v, want := range cases {
		if got := names(planVersions(h264, v)); got != want {
			t.Errorf("%s: got %s want %s", v, got, want)
		}
	}
	// The top cap follows the source: 1.5× its bitrate, never below the ladder, never above 10 Mbps.
	if top := planVersions(h264, VersionsBalanced)[0].Bitrate; top != 3900 {
		t.Errorf("top cap for a 2.6 Mbps source = %d, want 3900", top)
	}
	if top := planVersions(&VideoInfo{Width: 1920, Height: 1080, Kbps: 35000}, VersionsBalanced)[0].Bitrate; top != 10000 {
		t.Errorf("remux cap = %d, want 10000", top)
	}
	if top := planVersions(&VideoInfo{Width: 1920, Height: 1080, Kbps: 900}, VersionsBalanced)[0].Bitrate; top != 5000 {
		t.Errorf("low source cap = %d, want the ladder's 5000", top)
	}
	if got := streamOpts([]string{"-c:v", "libx264", "-preset", "fast", "-profile:v", "high"}, 2); strings.Join(got, " ") != "-c:v:2 libx264 -preset:v:2 fast -profile:v:2 high" {
		t.Errorf("streamOpts: %v", got)
	}
	// 145 minutes, 1080p at 3.9 Mbps cap + 720p + 2 audio tracks: around 5.5 GB.
	est := estimateBytes(Info{Duration: 145 * 60}, planVersions(h264, VersionsBalanced), 2)
	if est < 4.5e9 || est > 6.5e9 {
		t.Errorf("estimate %.1f GB", float64(est)/1e9)
	}
}

// TestVersionsAreAligned encodes a real 720p H.264 file: both versions must
// be cut into the same number of 4-second segments with keyframes on the
// same marks, so quality switches and seeks are instant, and playback must
// be allowed only once the whole short file is prepared.
func TestVersionsAreAligned(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	root := t.TempDir()
	src := filepath.Join(root, "Clip.mp4")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=24", "-f", "lavfi", "-i", "sine=frequency=440",
		"-t", "12", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-g", "7",
		"-b:v", "2000k", "-c:a", "aac", "-ac", "2", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	m, err := NewManager(context.Background(), Options{Root: root, Cache: t.TempDir(), FFmpeg: "ffmpeg", FFprobe: "ffprobe"})
	if err != nil {
		t.Fatal(err)
	}
	m.SetVersions(VersionsBalanced)
	items, _, _ := m.List()
	id := items[0].ID
	m.Prepare(id)
	var st Status
	for i := 0; i < 300; i++ {
		if st, _ = m.Status(id); st.State == StateReady || st.State == StateError {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if st.State != StateReady || strings.Join(st.Rungs, ",") != "720p,480p" {
		t.Fatalf("status %+v", st)
	}
	dir := filepath.Join(m.cache, m.key(items[0]))
	segs := func(sub string) []float64 {
		b, _ := os.ReadFile(filepath.Join(dir, sub, "index.m3u8"))
		var out []float64
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "#EXTINF:") {
				var d float64
				fmt.Sscanf(strings.TrimPrefix(l, "#EXTINF:"), "%f", &d)
				out = append(out, d)
			}
		}
		return out
	}
	a, b := segs("s_v0"), segs("s_v1")
	if len(a) != 3 || len(b) != 3 {
		t.Fatalf("12 s must give 3 segments of 4 s in every version, got %v and %v", a, b)
	}
	for i := range a {
		if a[i] < 3.9 || a[i] > 4.1 || b[i] < 3.9 || b[i] > 4.1 {
			t.Fatalf("segments must be 4 s in every version, got %v and %v", a, b)
		}
	}
	master, _ := os.ReadFile(filepath.Join(dir, "master.m3u8"))
	if !strings.Contains(string(master), "RESOLUTION=1280x720") || !strings.Contains(string(master), "RESOLUTION=854x480") {
		t.Fatalf("master:\n%s", master)
	}
	if st.Disk <= 0 || st.EstBytes <= 0 {
		t.Fatalf("disk use not reported: %+v", st)
	}
}

func TestPaceAndKeyframeArgs(t *testing.T) {
	info := Info{Duration: 100, Video: &VideoInfo{Codec: "h264", Width: 1920, Height: 800, PixFmt: "yuv420p", Kbps: 3000, FPS: 23.976}}
	cool, _, _ := buildArgs("in.mkv", info, softwareEncoder, false, VersionsBalanced, PaceCool)
	joined := " " + strings.Join(cool, " ") + " "
	for _, want := range []string{"-force_key_frames:v:0 expr:gte(t,n_forced*4)", "-force_key_frames:v:1 expr:gte(t,n_forced*4)", "-g:v:0 96", "-g:v:1 96", "-hls_time 4", "-maxrate:v:0 4500k"} {
		if !strings.Contains(joined, " "+want+" ") {
			t.Errorf("missing %q in %v", want, cool)
		}
	}
	for _, no := range []string{"-readrate", "copy", "source"} {
		if strings.Contains(joined, " "+no+" ") {
			t.Errorf("%q must be gone: %v", no, cool)
		}
	}
}
