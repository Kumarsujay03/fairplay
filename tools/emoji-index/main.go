// Command emoji-index refreshes FairPlay's animated emoji data from Google's
// Noto Emoji Animation project (CC BY 4.0):
//
//	go run ./tools/emoji-index
//
// It writes internal/emoji/index.json (code, name, category for every
// animated emoji, skin-tone variants left out) and the bundled defaults in
// internal/emoji/default/<code>.webp, shrunk with ffmpeg. Other emoji are
// fetched by the host's FairPlay on first use, see internal/emoji.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Kumarsujay03/fairplay/internal/emoji"
)

const api = "https://googlefonts.github.io/noto-emoji-animation/data/api.json"

func main() {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(api)
	check(err)
	defer resp.Body.Close()
	var data struct {
		Icons []struct {
			Codepoint  string   `json:"codepoint"`
			Tags       []string `json:"tags"`
			Categories []string `json:"categories"`
			Popularity int      `json:"popularity"`
		} `json:"icons"`
	}
	check(json.NewDecoder(resp.Body).Decode(&data))

	var out []emoji.Entry
	for _, ic := range data.Icons {
		if isSkinTone(ic.Codepoint) || len(ic.Categories) == 0 {
			continue
		}
		name := ""
		if len(ic.Tags) > 0 {
			name = strings.Trim(ic.Tags[0], ":")
		}
		out = append(out, emoji.Entry{Code: ic.Codepoint, Name: name, Cat: ic.Categories[0], Pop: ic.Popularity})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Pop > out[j].Pop })
	b, _ := json.Marshal(out)
	check(os.WriteFile("internal/emoji/index.json", b, 0o644))
	fmt.Printf("wrote internal/emoji/index.json: %d emoji, %d bytes\n", len(out), len(b))

	for _, code := range emoji.Defaults {
		dst := filepath.Join("internal/emoji/default", code+".webp")
		check(emoji.Convert(client, "ffmpeg", code, dst))
		st, _ := os.Stat(dst)
		fmt.Printf("  %s  %d KB\n", dst, st.Size()/1024)
	}
}

func isSkinTone(code string) bool {
	for _, t := range []string{"1f3fb", "1f3fc", "1f3fd", "1f3fe", "1f3ff"} {
		if strings.Contains(code, t) {
			return true
		}
	}
	return false
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
