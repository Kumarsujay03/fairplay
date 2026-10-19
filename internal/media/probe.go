package media

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type VideoInfo struct {
	Codec      string  `json:"codec"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	FPS        float64 `json:"fps"`
	PixFmt     string  `json:"pixFmt"`
	HDR        bool    `json:"hdr"`
	Interlaced bool    `json:"interlaced"`
}

type AudioInfo struct {
	Index    int    `json:"index"` // position among audio streams
	Codec    string `json:"codec"`
	Channels int    `json:"channels"`
	Lang     string `json:"lang"`
	Title    string `json:"title"`
	Default  bool   `json:"default"`
	Comment  bool   `json:"commentary,omitempty"`
}

type SubInfo struct {
	Index  int    `json:"index"` // position among subtitle streams
	Codec  string `json:"codec"`
	Lang   string `json:"lang"`
	Title  string `json:"title"`
	Text   bool   `json:"text"` // bitmap subtitles (PGS, VobSub) cannot be converted to WebVTT
	Forced bool   `json:"forced,omitempty"`
	SDH    bool   `json:"sdh,omitempty"`
}

type Info struct {
	Duration float64     `json:"duration"`
	Bitrate  int64       `json:"bitrate"`
	Video    *VideoInfo  `json:"video"`
	Audio    []AudioInfo `json:"audio"`
	Subs     []SubInfo   `json:"subs"`
}

var textSubCodecs = map[string]bool{
	"subrip": true, "srt": true, "ass": true, "ssa": true, "mov_text": true, "webvtt": true, "text": true,
}

func probe(ctx context.Context, ffprobe, path string) (Info, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-print_format", "json",
		"-show_format", "-show_streams", path).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return Info{}, fmt.Errorf("ffprobe: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return Info{}, fmt.Errorf("ffprobe: %w", err)
	}
	return parseProbe(out)
}

type probeJSON struct {
	Streams []struct {
		CodecType    string            `json:"codec_type"`
		CodecName    string            `json:"codec_name"`
		Width        int               `json:"width"`
		Height       int               `json:"height"`
		PixFmt       string            `json:"pix_fmt"`
		AvgFrameRate string            `json:"avg_frame_rate"`
		FieldOrder   string            `json:"field_order"`
		ColorTrans   string            `json:"color_transfer"`
		Channels     int               `json:"channels"`
		Tags         map[string]string `json:"tags"`
		Disposition  map[string]int    `json:"disposition"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
		BitRate  string `json:"bit_rate"`
	} `json:"format"`
}

func parseProbe(b []byte) (Info, error) {
	var p probeJSON
	if err := json.Unmarshal(b, &p); err != nil {
		return Info{}, err
	}
	var info Info
	info.Duration, _ = strconv.ParseFloat(p.Format.Duration, 64)
	info.Bitrate, _ = strconv.ParseInt(p.Format.BitRate, 10, 64)
	na, ns := 0, 0
	for _, s := range p.Streams {
		switch s.CodecType {
		case "video":
			if info.Video != nil || s.Disposition["attached_pic"] == 1 {
				continue // first real video stream only; skip cover art
			}
			info.Video = &VideoInfo{
				Codec: s.CodecName, Width: s.Width, Height: s.Height, PixFmt: s.PixFmt,
				FPS:        parseRate(s.AvgFrameRate),
				HDR:        s.ColorTrans == "smpte2084" || s.ColorTrans == "arib-std-b67",
				Interlaced: s.FieldOrder != "" && s.FieldOrder != "progressive" && s.FieldOrder != "unknown",
			}
		case "audio":
			info.Audio = append(info.Audio, AudioInfo{
				Index: na, Codec: s.CodecName, Channels: s.Channels,
				Lang: tag(s.Tags, "language"), Title: tag(s.Tags, "title"), Default: s.Disposition["default"] == 1,
				Comment: s.Disposition["comment"] == 1,
			})
			na++
		case "subtitle":
			info.Subs = append(info.Subs, SubInfo{
				Index: ns, Codec: s.CodecName, Lang: tag(s.Tags, "language"), Title: tag(s.Tags, "title"),
				Text: textSubCodecs[s.CodecName], Forced: s.Disposition["forced"] == 1,
				SDH: s.Disposition["hearing_impaired"] == 1,
			})
			ns++
		}
	}
	if info.Video == nil {
		return info, fmt.Errorf("no video stream found")
	}
	return info, nil
}

func tag(m map[string]string, k string) string {
	if v, ok := m[k]; ok {
		return v
	}
	return m[strings.ToUpper(k)]
}

func parseRate(r string) float64 {
	a, b, ok := strings.Cut(r, "/")
	if !ok {
		f, _ := strconv.ParseFloat(r, 64)
		return f
	}
	n, _ := strconv.ParseFloat(a, 64)
	d, _ := strconv.ParseFloat(b, 64)
	if d == 0 {
		return 0
	}
	return n / d
}
