// Package tunnel starts and supervises cloudflared or ngrok so the room is
// reachable from the internet without opening router ports.
package tunnel

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Status struct {
	Provider string `json:"provider"`
	State    string `json:"state"` // off, starting, up, error
	URL      string `json:"url,omitempty"`
	Error    string `json:"error,omitempty"`
}

var (
	cfURL    = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)
	ngrokURL = regexp.MustCompile(`https://[a-zA-Z0-9.-]+\.ngrok(-free)?\.(app|dev|io)`)
)

// Run keeps the tunnel alive until ctx ends, restarting with backoff.
// provider is "cloudflared", "ngrok" or "off".
func Run(ctx context.Context, provider, bin string, port int, onChange func(Status)) {
	if provider == "off" || provider == "" {
		onChange(Status{Provider: "off", State: "off"})
		return
	}
	if bin == "" {
		bin = provider
	}
	if _, err := exec.LookPath(bin); err != nil {
		onChange(Status{Provider: provider, State: "error", Error: provider + " is not installed; guests can still join on your LAN"})
		return
	}
	backoff := 3 * time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := runOnce(ctx, provider, bin, port, onChange)
		if ctx.Err() != nil {
			return
		}
		msg := "tunnel stopped"
		if err != nil {
			msg = err.Error()
		}
		onChange(Status{Provider: provider, State: "error", Error: msg + "; retrying"})
		if time.Since(start) > time.Minute {
			backoff = 3 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

func runOnce(ctx context.Context, provider, bin string, port int, onChange func(Status)) error {
	onChange(Status{Provider: provider, State: "starting"})
	origin := fmt.Sprintf("http://127.0.0.1:%d", port)
	var cmd *exec.Cmd
	var re *regexp.Regexp
	switch provider {
	case "cloudflared":
		cmd = exec.CommandContext(ctx, bin, "tunnel", "--no-autoupdate", "--url", origin)
		re = cfURL
	case "ngrok":
		cmd = exec.CommandContext(ctx, bin, "http", fmt.Sprint(port), "--log", "stdout", "--log-format", "json")
		re = ngrokURL
	default:
		return fmt.Errorf("unknown tunnel provider %q", provider)
	}
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return err
	}
	var once sync.Once
	var lastErr string
	var mu sync.Mutex
	scan := func(r io.Reader) {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			line := sc.Text()
			if u := re.FindString(line); u != "" {
				once.Do(func() { onChange(Status{Provider: provider, State: "up", URL: u}) })
			}
			if provider == "ngrok" {
				var j struct {
					Lvl string `json:"lvl"`
					Err string `json:"err"`
				}
				if json.Unmarshal([]byte(line), &j) == nil && j.Err != "" && j.Err != "<nil>" {
					mu.Lock()
					lastErr = j.Err
					mu.Unlock()
				}
			} else if strings.Contains(line, " ERR ") {
				mu.Lock()
				lastErr = strings.TrimSpace(line[strings.Index(line, " ERR ")+5:])
				mu.Unlock()
			}
		}
	}
	go scan(stdout)
	go scan(stderr)
	err := cmd.Wait()
	mu.Lock()
	defer mu.Unlock()
	if lastErr != "" {
		return fmt.Errorf("%s", lastErr)
	}
	return err
}
