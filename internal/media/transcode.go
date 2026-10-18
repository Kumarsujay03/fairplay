package media

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type rung struct {
	Name    string
	Long    int  // long side in pixels: 1920 for "1080p"
	Bitrate int  // kbps cap for a full 16:9 picture (for a copied rung: the source bitrate)
	Copy    bool // the original video stream, not re-encoded
}

var ladder = []rung{
	{"1080p", 1920, 5000, false},
	{"720p", 1280, 2800, false},
	{"480p", 854, 1400, false},
	{"360p", 640, 800, false},
}

// Versions decides how many quality versions are prepared.
const (
	VersionsOriginal = "original" // one version: the original quality (smallest on disk)
	VersionsBalanced = "balanced" // original quality plus one smaller version (default)
	VersionsFull     = "full"     // original quality plus two smaller versions
)

// NormalizeVersions maps unknown values to the default.
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
			out = append(out, rung{r.Name, r.Long, int(float64(r.Bitrate) * area), false})
		}
		if len(out) == 3 {
			break
		}
	}
	if len(out) == 0 {
		out = []rung{{fmt.Sprintf("%dp", short&^1), long &^ 1, 600, false}}
	}
	return out
}

// canCopy reports whether the source video can be served as-is: browsers play
// 8-bit H.264 natively, so re-encoding it would only cost time, disk and quality.
func canCopy(v *VideoInfo) bool {
	if v == nil || v.Codec != "h264" || v.HDR || v.Interlaced {
		return false
	}
	if v.PixFmt != "yuv420p" && v.PixFmt != "yuvj420p" {
		return false // 10-bit or 4:2:2/4:4:4 H.264 does not play in most browsers
	}
	if v.SAR != "" && v.SAR != "1:1" && v.SAR != "0:1" && v.SAR != "N/A" {
		return false // non-square pixels need rescaling
	}
	long := max(v.Width, v.Height)
	if long > 1920+1920/20 {
		return false // 4K: serve a 1080p version instead
	}
	return v.Kbps > 0 && v.Kbps <= 10000 // a 40 Mbps Blu-ray remux would choke guests
}

// planVersions chooses what to prepare for this file.
func planVersions(v *VideoInfo, versions string) []rung {
	full := planLadder(v.Width, v.Height)
	extra := map[string]int{VersionsOriginal: 0, VersionsBalanced: 1, VersionsFull: 2}[NormalizeVersions(versions)]
	top := full[0]
	if canCopy(v) {
		top.Copy = true
		top.Bitrate = v.Kbps
	}
	out := []rung{top}
	for i := 1; i < len(full) && len(out) <= extra; i++ {
		out = append(out, full[i])
	}
	return out
}

// estimateBytes predicts the disk space a plan will use. Capped-quality
// encodes typically land around 60% of their cap.
func estimateBytes(info Info, rungs []rung, audioTracks int) int64 {
	kbps := 0
	for _, r := range rungs {
		if r.Copy {
			kbps += r.Bitrate
		} else {
			kbps += r.Bitrate * 6 / 10
		}
	}
	kbps += audioTracks * 128
	return int64(info.Duration * float64(kbps) * 1000 / 8)
}

// Encoder is a video encoder ffmpeg can use on this machine.
type Encoder struct {
	Name string
	Args []string
}

var softwareEncoder = Encoder{"libx264", []string{"-c:v", "libx264", "-preset", "veryfast", "-profile:v", "high", "-pix_fmt", "yuv420p", "-sc_threshold", "0"}}

var hwCandidates = []Encoder{
	{"h264_nvenc", []string{"-c:v", "h264_nvenc", "-preset", "p4", "-profile:v", "high", "-pix_fmt", "yuv420p", "-forced-idr", "1"}},
	{"h264_qsv", []string{"-c:v", "h264_qsv", "-preset", "veryfast", "-pix_fmt", "nv12"}},
	{"h264_videotoolbox", []string{"-c:v", "h264_videotoolbox", "-pix_fmt", "yuv420p", "-allow_sw", "0"}},
}

// DetectEncoder tries each hardware encoder with a two-frame test encode and
// falls back to libx264.
func DetectEncoder(ctx context.Context, ffmpeg string) Encoder {
	for _, c := range hwCandidates {
		tctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		args := append([]string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=black:s=640x360:d=0.2", "-frames:v", "2"}, c.Args...)
		args = append(args, "-f", "null", "-")
		err := exec.CommandContext(tctx, ffmpeg, args...).Run()
		cancel()
		if err == nil {
			return c
		}
	}
	return softwareEncoder
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

// buildArgs returns the ffmpeg arguments for one transcode into dir.
func buildArgs(input string, info Info, enc Encoder, tonemap bool, versions string) ([]string, []rung, []AudioInfo) {
	rungs := planVersions(info.Video, versions)
	portrait := info.Video.Height > info.Video.Width
	audio := info.Audio
	if len(audio) > maxAudioTracks {
		audio = audio[:maxAudioTracks]
	}
	copyTop := rungs[0].Copy
	encoded := rungs
	if copyTop {
		encoded = rungs[1:]
	}

	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y", "-i", input}
	if len(encoded) > 0 {
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
		fc := "[0:v:0]" + strings.Join(pre, ",") + fmt.Sprintf(",split=%d", len(encoded))
		for i := range encoded {
			fc += fmt.Sprintf("[s%d]", i)
		}
		for i, r := range encoded {
			// Scale the long side down to the rung, never up, keeping the shape.
			if portrait {
				fc += fmt.Sprintf(";[s%d]scale=-2:'min(%d,ih)':flags=bicubic[v%d]", i, r.Long, i)
			} else {
				fc += fmt.Sprintf(";[s%d]scale='min(%d,iw)':-2:flags=bicubic[v%d]", i, r.Long, i)
			}
		}
		args = append(args, "-filter_complex", fc)
	}
	if copyTop {
		args = append(args, "-map", "0:v:0")
	}
	for i := range encoded {
		args = append(args, "-map", fmt.Sprintf("[v%d]", i))
	}
	for _, a := range audio {
		args = append(args, "-map", fmt.Sprintf("0:a:%d", a.Index))
	}

	// Per video stream: copy the original, or encode with a quality cap.
	soft := enc.Name == softwareEncoder.Name
	for i, r := range rungs {
		if r.Copy {
			args = append(args, fmt.Sprintf("-c:v:%d", i), "copy")
			continue
		}
		opts := enc.Args
		if copyTop && soft {
			// Only the smaller versions are encoded, so spend the spare CPU on quality.
			opts = append([]string(nil), opts...)
			for k := range opts {
				if opts[k] == "veryfast" {
					opts[k] = "fast"
				}
			}
		}
		args = append(args, streamOpts(opts, i)...)
		if copyTop {
			// Keyframes exactly where the original has them, so all versions
			// cut into the same 4-second segments and switching is seamless.
			args = append(args, fmt.Sprintf("-force_key_frames:v:%d", i), "source", fmt.Sprintf("-g:v:%d", i), "100000")
		} else {
			args = append(args, fmt.Sprintf("-force_key_frames:v:%d", i), "expr:gte(t,n_forced*4)")
		}
		if soft {
			// Capped quality: simple scenes use far fewer bits than the cap.
			crf := 21 + i
			args = append(args, fmt.Sprintf("-crf:v:%d", i), fmt.Sprint(crf))
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
			args = append(args, fmt.Sprintf("-c:a:%d", j), "aac", fmt.Sprintf("-b:a:%d", j), "128k",
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
		"-f", "hls", "-hls_time", "4", "-hls_playlist_type", "event",
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

var bandwidthRe = regexp.MustCompile(`BANDWIDTH=[0-9]+`)

// fixMaster corrects the BANDWIDTH ffmpeg writes into master.m3u8 when an
// encode starts. ffmpeg can't know the bitrate of a copied stream yet and
// advertises only a few hundred kbps for it, which would make players pick
// qualities on wrong numbers. Peak is taken as 1.5× the average for copies
// and the encode cap for the rest, plus the stereo AAC track. Values that
// are already plausible are kept: when the encode finishes ffmpeg rewrites
// the playlist with measured numbers, which are better than estimates.
func fixMaster(path string, rungs []rung) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	changed := false
	for i := 1; i < len(lines); i++ {
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(lines[i]), "s_v%d/index.m3u8", &n); err != nil || n < 0 || n >= len(rungs) {
			continue
		}
		if !strings.HasPrefix(lines[i-1], "#EXT-X-STREAM-INF:") {
			continue
		}
		kbps := rungs[n].Bitrate
		if rungs[n].Copy {
			kbps = kbps * 3 / 2
		}
		want := (kbps + 128) * 1000
		var have int
		if m := bandwidthRe.FindString(lines[i-1]); m != "" {
			have, _ = strconv.Atoi(strings.TrimPrefix(m, "BANDWIDTH="))
		}
		if have*2 >= want {
			continue
		}
		lines[i-1] = bandwidthRe.ReplaceAllString(lines[i-1], fmt.Sprintf("BANDWIDTH=%d", want))
		changed = true
	}
	if !changed {
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
