// Package eventlog writes one JSON line per event to a file, for the whole
// time FairPlay runs: every room state change, every viewer's status report,
// every command and host action, chat and reactions, encoder progress, each
// HLS request, and the events players send from their browsers (stalls,
// seeks, quality switches, hls.js errors). When a showing stutters, the log
// says who was where, what the room clock said, what the encoder was doing
// and what the player saw, second by second.
//
// It is always on. Nothing in the hot path waits for disk: lines go to a
// buffered writer that is flushed once a second and on Close. A failed open
// or write is ignored, so logging never stops a showing.
package eventlog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// keepFiles is how many old log files are kept in the folder.
const keepFiles = 30

// Fields are the extra keys of one event.
type Fields map[string]any

type Logger struct {
	mu   sync.Mutex
	f    *os.File
	w    *bufio.Writer
	path string
	stop chan struct{}
	done chan struct{}
}

var (
	stdMu sync.RWMutex
	std   *Logger
)

// Open starts a new log file in dir (created if needed) named after the
// current time and makes it the default logger. Older files beyond keepFiles
// are removed.
func Open(dir string) (*Logger, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	name := "fairplay-" + time.Now().Format("20060102-150405") + ".jsonl"
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	l := &Logger{f: f, w: bufio.NewWriterSize(f, 64<<10), path: path, stop: make(chan struct{}), done: make(chan struct{})}
	go l.flusher()
	prune(dir)
	stdMu.Lock()
	std = l
	stdMu.Unlock()
	return l, nil
}

// Path is the file this logger writes to.
func (l *Logger) Path() string { return l.path }

func (l *Logger) flusher() {
	defer close(l.done)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-t.C:
			l.mu.Lock()
			_ = l.w.Flush()
			l.mu.Unlock()
		}
	}
}

// Log writes one event. kind is a short dotted name such as "room.state".
func (l *Logger) Log(kind string, f Fields) {
	if l == nil {
		return
	}
	rec := make(map[string]any, len(f)+3)
	for k, v := range f {
		rec[k] = v
	}
	now := time.Now()
	rec["ts"] = now.Format("15:04:05.000")
	rec["t"] = now.UnixMilli()
	rec["ev"] = kind
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	l.mu.Lock()
	_, _ = l.w.Write(b)
	_ = l.w.WriteByte('\n')
	l.mu.Unlock()
}

// Close flushes and closes the file.
func (l *Logger) Close() {
	if l == nil {
		return
	}
	close(l.stop)
	<-l.done
	l.mu.Lock()
	_ = l.w.Flush()
	_ = l.f.Close()
	l.mu.Unlock()
	stdMu.Lock()
	if std == l {
		std = nil
	}
	stdMu.Unlock()
}

// Log writes to the default logger; a no-op before Open.
func Log(kind string, f Fields) {
	stdMu.RLock()
	l := std
	stdMu.RUnlock()
	l.Log(kind, f)
}

// Path is the default logger's file, or "".
func Path() string {
	stdMu.RLock()
	defer stdMu.RUnlock()
	if std == nil {
		return ""
	}
	return std.path
}

// Files lists the log files in dir, newest first.
func Files(dir string) []string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "fairplay-") && strings.HasSuffix(e.Name(), ".jsonl") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

func prune(dir string) {
	files := Files(dir)
	for i := keepFiles; i < len(files); i++ {
		_ = os.Remove(files[i])
	}
}
