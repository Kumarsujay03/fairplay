package media

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

type rung struct {
	Name    string
	Long    int // long side in pixels: 1920 for "1080p"
	Bitrate int // kbps for a full 16:9 picture
}

var ladder = []rung{
	{"1080p", 1920, 5000},
	{"720p", 1280, 2800},
	{"480p", 854, 1400},
	{"360p", 640, 800},
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

// buildArgs returns the ffmpeg arguments for one ladder transcode into dir.
func buildArgs(input string, info Info, enc Encoder, tonemap bool) ([]string, []rung, []AudioInfo) {
	rungs := planLadder(info.Video.Width, info.Video.Height)
	portrait := info.Video.Height > info.Video.Width
	audio := info.Audio
	if len(audio) > maxAudioTracks {
		audio = audio[:maxAudioTracks]
	}

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
	fc := "[0:v:0]" + strings.Join(pre, ",") + fmt.Sprintf(",split=%d", len(rungs))
	for i := range rungs {
		fc += fmt.Sprintf("[s%d]", i)
	}
	for i, r := range rungs {
		// Scale the long side down to the rung, never up, keeping the shape.
		if portrait {
			fc += fmt.Sprintf(";[s%d]scale=-2:'min(%d,ih)':flags=bicubic[v%d]", i, r.Long, i)
		} else {
			fc += fmt.Sprintf(";[s%d]scale='min(%d,iw)':-2:flags=bicubic[v%d]", i, r.Long, i)
		}
	}

	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-y", "-i", input, "-filter_complex", fc}
	for i := range rungs {
		args = append(args, "-map", fmt.Sprintf("[v%d]", i))
	}
	for _, a := range audio {
		args = append(args, "-map", fmt.Sprintf("0:a:%d", a.Index))
	}
	args = append(args, enc.Args...)
	args = append(args, "-force_key_frames", "expr:gte(t,n_forced*4)")
	for i, r := range rungs {
		args = append(args,
			fmt.Sprintf("-b:v:%d", i), fmt.Sprintf("%dk", r.Bitrate),
			fmt.Sprintf("-maxrate:v:%d", i), fmt.Sprintf("%dk", r.Bitrate*11/10),
			fmt.Sprintf("-bufsize:v:%d", i), fmt.Sprintf("%dk", r.Bitrate*2),
		)
	}
	if len(audio) > 0 {
		args = append(args, "-c:a", "aac", "-b:a", "128k", "-ac", "2", "-ar", "48000")
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
