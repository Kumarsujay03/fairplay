package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Kumarsujay03/fairplay/internal/eventlog"
)

// runLogs shows where the event logs are. Every start of FairPlay writes one
// file with a JSON object per line: room state changes, each viewer's status
// once a second, commands, chat, encoder progress, HLS requests and what the
// players saw in their browsers. Read it with any JSON tool, for example:
//
//	jq -c 'select(.ev=="status" and .name=="Host")' <file>
func runLogs(o options) {
	dir := filepath.Join(o.data, "logs")
	files := eventlog.Files(dir)
	fmt.Printf("Event logs are in %s\n", tildePath(dir))
	if len(files) == 0 {
		fmt.Println("No logs yet. One is written every time FairPlay runs.")
		return
	}
	fmt.Println()
	for i, f := range files {
		st, err := os.Stat(f)
		if err != nil {
			continue
		}
		mark := "  "
		if i == 0 {
			mark = "* "
		}
		fmt.Printf("%s%s  %6.1f MB  %s\n", mark, filepath.Base(f), float64(st.Size())/1e6, st.ModTime().Format("Jan 2 15:04"))
	}
	fmt.Println()
	fmt.Println("Each line is one JSON event. Useful filters (with jq):")
	fmt.Println("  room play/pause/seek:   jq -c 'select(.ev==\"room.state\")'")
	fmt.Println("  one viewer, per second: jq -c 'select(.ev==\"status\" and .name==\"Host\")'")
	fmt.Println("  stalls and errors:      jq -c 'select(.ev|test(\"wait|player.video.(waiting|stalled)|player.hls.error|encode.error\"))'")
	fmt.Println("  what the encoder ran:   jq -c 'select(.ev==\"encode.plan\")'")
}
