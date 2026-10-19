package media

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Every version is encoded, none is copied: a copied original keeps the
// file's own keyframes (anything from one to ten seconds apart), so its
// segments are long and uneven, every player waits for a different one at
// the encoder's edge, and seeks land seconds late. Encoding all versions
// with a keyframe every 4 seconds gives identical 4-second segments in every
// version, instant quality switches and seeks, and an edge that moves
// steadily. The top version is encoded at well above the file's own bitrate,
// so nothing is visibly lost.
type rung struct {
	Name    string
	Long    int // long side in pixels: 1920 for "1080p"
	Bitrate int // kbps cap for a full 16:9 picture
}

var ladder = []rung{
	{"1080p", 1920, 5000},
	{"720p", 1280, 2800},
	{"480p", 854, 1400},
	{"360p", 640, 800},
}

// segmentSeconds is the keyframe interval and the HLS segment length.
const segmentSeconds = 4

// Versions decides how many quality versions are prepared.
const (
	VersionsOriginal = "original" // one version: the top quality only (smallest on disk)
	VersionsBalanced = "balanced" // top quality plus one smaller version (default)
	VersionsFull     = "full"     // top quality plus two smaller versions
)

// Preparing speed: how much of the CPU the encoder may use. On a GPU it
// hardly matters; on the CPU, cool keeps the computer quiet.
const (
	PaceCool = "cool" // a quarter of the CPU threads
	PaceFast = "fast" // half of them
)

func NormalizePace(p string) string {
	if p == PaceFast {
		return PaceFast
	}
	return PaceCool
}

func NormalizeVersions(v string) string {
	switch v {
	case VersionsOriginal, VersionsBalanced, VersionsFull:
		return v
	}
	return VersionsBalanced
}

// planLadder picks up to three rungs at or below the source size. Quality is
// judged by the long side, so a 1920×800 film is 1080p (not 720p) and a
// portrait phone video is classed by its height. Bitrates shrink with the
// picture area, so wide films use less bandwidth. 4K tops out at 1080p.
func planLadder(width, height int) []rung {
	long, short := width, height
	if height > width {
		long, short = height, width
	}
	if long <= 0 {
		long, short = 1280, 720
	}
	area := float64(short) / (float64(long) * 9 / 16) // 1.0 for 16:9, ~0.74 for 2.40:1
	if area > 1 {
		area = 1
	}
	if area < 0.5 {
		area = 0.5
	}
	var out []rung
	for _, r := range ladder {
		if r.Long <= long+long/20 {
			out = append(out, rung{r.Name, r.Long, int(float64(r.Bitrate) * area)})
		}
		if len(out) == 3 {
			break
		}
	}
	if len(out) == 0 {
		out = []rung{{fmt.Sprintf("%dp", short&^1), long &^ 1, 600}}
	}
	return out
}

// planVersions chooses what to prepare for this file. The top version's cap
// follows the source: 1.5× its bitrate, at least the ladder's figure, so a
// re-encode of a good file stays visibly identical, and at most 10 Mbps so a
// Blu-ray remux doesn't choke guests.
func planVersions(v *VideoInfo, versions string) []rung {
	full := planLadder(v.Width, v.Height)
	extra := map[string]int{VersionsOriginal: 0, VersionsBalanced: 1, VersionsFull: 2}[NormalizeVersions(versions)]
	top := full[0]
	if v.Kbps > 0 {
		top.Bitrate = min(10000, max(top.Bitrate, v.Kbps*3/2))
	}
	out := []rung{top}
	for i := 1; i < len(full) && len(out) <= extra; i++ {
		out = append(out, full[i])
	}
	return out
}

// estimateBytes predicts the disk space a plan will use. Capped encodes
// typically land around 70% of their cap.
func estimateBytes(info Info, rungs []rung, audioTracks int) int64 {
	kbps := 0
	for _, r := range rungs {
		kbps += r.Bitrate * 7 / 10
	}
	kbps += audioTracks * 128
	return int64(info.Duration * float64(kbps) * 1000 / 8)
}

// Encoder is a video encoder ffmpeg can use on this machine.
// Device is the GPU device for encoders that need one (VAAPI).
type Encoder struct {
	Name   string
	Args   []string
	Device string
}

var softwareEncoder = Encoder{Name: "libx264", Args: []string{"-c:v", "libx264", "-preset", "veryfast", "-profile:v", "high", "-pix_fmt", "yuv420p", "-sc_threshold", "0"}}

var hwCandidates = []Encoder{
	{Name: "h264_nvenc", Args: []string{"-c:v", "h264_nvenc", "-preset", "p4", "-profile:v", "high", "-pix_fmt", "yuv420p", "-forced-idr", "1"}},
	{Name: "h264_qsv", Args: []string{"-c:v", "h264_qsv", "-preset", "veryfast", "-pix_fmt", "nv12"}},
	{Name: "h264_videotoolbox", Args: []string{"-c:v", "h264_videotoolbox", "-pix_fmt", "yuv420p", "-allow_sw", "0"}},
}

// VAAPI (Intel and AMD GPUs on Linux) needs a device and runs its scaler on
// the GPU too, so a smaller version costs the CPU almost nothing.
const vaapiName = "h264_vaapi"

func vaapiEncoder(dev string) Encoder {
	return Encoder{
		Name:   vaapiName,
		Device: dev,
		Args:   []string{"-c:v", "h264_vaapi", "-profile:v", "high", "-rc_mode", "VBR"},
	}
}

// DetectEncoder tries each hardware encoder with a two-frame test encode and
// falls back to libx264. VAAPI is tried first on Linux: it is what Intel and
// AMD laptops have, and the Quick Sync path needs a runtime that is rarely
// installed.
func DetectEncoder(ctx context.Context, ffmpeg string) Encoder {
	try := func(global []string, c Encoder, filter string) bool {
		tctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		args := append([]string{"-hide_banner", "-loglevel", "error"}, global...)
		args = append(args, "-f", "lavfi", "-i", "color=c=black:s=640x360:d=0.2", "-frames:v", "2")
		if filter != "" {
			args = append(args, "-vf", filter)
		}
		args = append(args, c.Args...)
		// Rate-controlled encoders refuse to open without a bitrate.
		args = append(args, "-b:v", "1000k", "-maxrate", "1500k", "-f", "null", "-")
		return exec.CommandContext(tctx, ffmpeg, args...).Run() == nil
	}
	if runtime.GOOS == "linux" {
		for _, dev := range vaapiDevices() {
			c := vaapiEncoder(dev)
			if try(c.globalArgs(), c, "format=nv12,hwupload") {
				return c
			}
		}
	}
	for _, c := range hwCandidates {
		if try(nil, c, "") {
			return c
		}
	}
	return softwareEncoder
}

func vaapiDevices() []string {
	m, _ := filepath.Glob("/dev/dri/renderD*")
	return m
}

// Label is the encoder in plain words, for the start-up banner.
func (e Encoder) Label() string {
	switch e.Name {
	case vaapiName:
		return "GPU (VAAPI)"
	case "h264_nvenc":
		return "NVIDIA GPU (NVENC)"
	case "h264_qsv":
		return "Intel GPU (Quick Sync)"
	case "h264_videotoolbox":
		return "Apple GPU (VideoToolbox)"
	}
	return "CPU (libx264)"
}

// Hint says how to get GPU encoding when the machine seems to have a GPU
// that this ffmpeg can't use yet. Empty when there is nothing to suggest.
func (e Encoder) Hint() string {
	if e.Name != softwareEncoder.Name || runtime.GOOS != "linux" || len(vaapiDevices()) == 0 {
		return ""
	}
	return "Your GPU could prepare videos with almost no CPU. Install the VAAPI driver (Ubuntu/Debian: sudo apt install intel-media-va-driver-non-free for Intel, mesa-va-drivers for AMD) and start FairPlay again."
}

// globalArgs are the input-side options an encoder needs.
func (e Encoder) globalArgs() []string {
	if e.Name != vaapiName {
		return nil
	}
	return []string{"-init_hw_device", "vaapi=va:" + e.Device, "-filter_hw_device", "va"}
}

// HasFilter reports whether ffmpeg was built with a filter.
func HasFilter(ffmpeg, name string) bool {
	out, err := exec.Command(ffmpeg, "-hide_banner", "-filters").Output()
	if err != nil {
		return false
	}
	return regexp.MustCompile(`(?m)^\s*\S+\s+` + regexp.QuoteMeta(name) + `\s`).Match(out)
}

var langRe = regexp.MustCompile(`^[a-z]{2,3}$`)

func safeLang(l string) string {
	l = strings.ToLower(l)
	if langRe.MatchString(l) {
		return l
	}
	return "und"
}

const maxAudioTracks = 6

// streamOpts rewrites encoder options for one output stream: "-c:v" becomes
// "-c:v:2", "-preset" becomes "-preset:v:2", so a copied stream is untouched.
func streamOpts(opts []string, i int) []string {
	out := make([]string, len(opts))
	for k, o := range opts {
		switch {
		case !strings.HasPrefix(o, "-"):
			out[k] = o
		case strings.HasSuffix(o, ":v"):
			out[k] = fmt.Sprintf("%s:%d", o, i)
		default:
			out[k] = fmt.Sprintf("%s:v:%d", o, i)
		}
	}
	return out
}

// buildArgs returns the ffmpeg arguments for one transcode into dir: every
// version in one pass, keyframes every segmentSeconds in all of them.
func buildArgs(input string, info Info, enc Encoder, tonemap bool, versions, pace string) ([]string, []rung, []AudioInfo) {
	rungs := planVersions(info.Video, versions)
	portrait := info.Video.Height > info.Video.Width
	audio := info.Audio
	if len(audio) > maxAudioTracks {
		audio = audio[:maxAudioTracks]
	}

	// The host usually watches on this same computer, so the browser must
	// keep enough CPU to decode the picture smoothly.
	threads := encodeThreads(NormalizePace(pace) == PaceCool)
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y", "-threads", fmt.Sprint(threads)}
	args = append(args, enc.globalArgs()...)
	args = append(args, "-i", input)

	// Video chain: optional deinterlace and HDR tonemap, square pixels, split.
	var pre []string
	if info.Video.Interlaced {
		pre = append(pre, "yadif=mode=0:deint=1")
	}
	if info.Video.HDR && tonemap {
		pre = append(pre, "zscale=t=linear:npl=100", "format=gbrpf32le", "zscale=p=bt709",
			"tonemap=tonemap=hable:desat=0", "zscale=t=bt709:m=bt709:r=tv", "format=yuv420p")
	}
	pre = append(pre, "scale=trunc(iw*sar/2)*2:ih", "setsar=1")
	gpu := enc.Name == vaapiName
	if gpu {
		// Frames go to the GPU once; each version is scaled there.
		pre = append(pre, "format=nv12", "hwupload")
	}
	fc := "[0:v:0]" + strings.Join(pre, ",") + fmt.Sprintf(",split=%d", len(rungs))
	for i := range rungs {
		fc += fmt.Sprintf("[s%d]", i)
	}
	for i, r := range rungs {
		// Scale the long side down to the rung, never up, keeping the shape.
		switch {
		case gpu && portrait:
			fc += fmt.Sprintf(";[s%d]scale_vaapi=w=-2:h='min(%d,ih)'[v%d]", i, r.Long, i)
		case gpu:
			fc += fmt.Sprintf(";[s%d]scale_vaapi=w='min(%d,iw)':h=-2[v%d]", i, r.Long, i)
		case portrait:
			fc += fmt.Sprintf(";[s%d]scale=-2:'min(%d,ih)':flags=bicubic[v%d]", i, r.Long, i)
		default:
			fc += fmt.Sprintf(";[s%d]scale='min(%d,iw)':-2:flags=bicubic[v%d]", i, r.Long, i)
		}
	}
	args = append(args, "-filter_complex", fc, "-filter_complex_threads", fmt.Sprint(threads))
	for i := range rungs {
		args = append(args, "-map", fmt.Sprintf("[v%d]", i))
	}
	for _, a := range audio {
		args = append(args, "-map", fmt.Sprintf("0:a:%d", a.Index))
	}

	// Per version: capped quality, keyframes on the same 4-second marks.
	soft := enc.Name == softwareEncoder.Name
	fps := info.Video.FPS
	if fps <= 0 || fps > 120 {
		fps = 24
	}
	gop := int(fps*segmentSeconds + 0.5)
	for i, r := range rungs {
		args = append(args, streamOpts(enc.Args, i)...)
		if soft {
			args = append(args, fmt.Sprintf("-threads:v:%d", i), fmt.Sprint(max(2, threads/len(rungs))))
		}
		args = append(args,
			fmt.Sprintf("-force_key_frames:v:%d", i), fmt.Sprintf("expr:gte(t,n_forced*%d)", segmentSeconds),
			fmt.Sprintf("-g:v:%d", i), fmt.Sprint(gop),
		)
		if soft {
			// Capped quality: simple scenes use far fewer bits than the cap.
			args = append(args, fmt.Sprintf("-crf:v:%d", i), fmt.Sprint(20+i))
		} else {
			args = append(args, fmt.Sprintf("-b:v:%d", i), fmt.Sprintf("%dk", r.Bitrate*7/10))
		}
		args = append(args,
			fmt.Sprintf("-maxrate:v:%d", i), fmt.Sprintf("%dk", r.Bitrate),
			fmt.Sprintf("-bufsize:v:%d", i), fmt.Sprintf("%dk", r.Bitrate*2),
		)
	}
	// Audio: stereo AAC is copied; anything else becomes stereo AAC.
	for j, a := range audio {
		if a.Codec == "aac" && a.Channels > 0 && a.Channels <= 2 {
			args = append(args, fmt.Sprintf("-c:a:%d", j), "copy")
		} else {
			args = append(args, fmt.Sprintf("-c:a:%d", j), "aac", fmt.Sprintf("-b:a:%d", j), "160k",
				fmt.Sprintf("-ac:a:%d", j), "2", fmt.Sprintf("-ar:a:%d", j), "48000")
		}
	}

	var vsm []string
	def := 0
	for i, a := range audio {
		if a.Default {
			def = i
			break
		}
	}
	for i, a := range audio {
		d := "no"
		if i == def {
			d = "yes"
		}
		vsm = append(vsm, fmt.Sprintf("a:%d,agroup:aud,language:%s,name:a%d,default:%s", i, safeLang(a.Lang), i, d))
	}
	for i := range rungs {
		if len(audio) > 0 {
			vsm = append(vsm, fmt.Sprintf("v:%d,agroup:aud,name:v%d", i, i))
		} else {
			vsm = append(vsm, fmt.Sprintf("v:%d,name:v%d", i, i))
		}
	}

	args = append(args,
		"-f", "hls", "-hls_time", fmt.Sprint(segmentSeconds), "-hls_playlist_type", "event",
		"-hls_segment_type", "fmp4", "-hls_flags", "independent_segments+temp_file",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", "s_%v/seg_%05d.m4s",
		"-master_pl_name", "master.m3u8",
		"-var_stream_map", strings.Join(vsm, " "),
		"-progress", "pipe:1", "-nostats",
		"s_%v/index.m3u8",
	)
	return args, rungs, audio
}

// subtitleArgs extracts every text subtitle stream to WebVTT in one pass.
func subtitleArgs(input string, subs []SubInfo) ([]string, []SubInfo) {
	var keep []SubInfo
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y", "-i", input}
	for _, s := range subs {
		if !s.Text {
			continue
		}
		keep = append(keep, s)
		args = append(args, "-map", fmt.Sprintf("0:s:%d", s.Index), "-c:s", "webvtt", "-f", "webvtt", fmt.Sprintf("sub_%d.vtt", s.Index))
	}
	return args, keep
}

// encodeThreads is how many threads ffmpeg may use: a quarter of the logical
// CPUs when cool, half when fast, at least 2 either way.
func encodeThreads(cool bool) int {
	if cool {
		return max(2, runtime.NumCPU()/4)
	}
	return max(2, runtime.NumCPU()/2)
}
