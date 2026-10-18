package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Kumarsujay03/fairplay/internal/media"
)

// settings are remembered between runs in <data>/settings.json.
type settings struct {
	Media string `json:"media,omitempty"`
}

func loadSettings(dir string) settings {
	var s settings
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func saveSettings(dir string, s settings) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	tmp := filepath.Join(dir, "settings.json.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "settings.json"))
}

// chooseMediaFolder picks the video folder: -media, then $FAIRPLAY_MEDIA, then
// the folder chosen last time in the host console, then the usual Videos,
// Movies or Downloads folder. If none exists, ~/Videos is created.
func chooseMediaFolder(o options, saved settings) (dir string, created bool, err error) {
	if o.mediaSet {
		d := expandHome(o.media)
		if isDir(d) {
			return d, false, nil
		}
		msg := fmt.Sprintf("the video folder %s does not exist.", d)
		if c := existingCandidates(); len(c) > 0 {
			msg += "\n  Folders that do exist: " + strings.Join(c, ", ") + "\n  Or just run `fairplay` and pick a folder in the host console."
		}
		return "", false, fmt.Errorf("%s", msg)
	}
	if env := expandHome(os.Getenv("FAIRPLAY_MEDIA")); env != "" && isDir(env) {
		return env, false, nil
	}
	if saved.Media != "" && isDir(saved.Media) {
		return saved.Media, false, nil
	}
	if c := existingCandidates(); len(c) > 0 {
		return expandHome(c[0]), false, nil
	}
	home, _ := os.UserHomeDir()
	d := filepath.Join(home, "Videos")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", false, fmt.Errorf("could not create %s: %v. Start with -media <folder>", d, err)
	}
	return d, true, nil
}

func existingCandidates() []string {
	var out []string
	for _, c := range []string{"~/Videos", "~/Movies", "~/Downloads"} {
		if isDir(expandHome(c)) {
			out = append(out, c)
		}
	}
	return out
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func expandHome(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimLeft(p[1:], `/\`))
		}
	}
	return p
}

func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// findTool returns the path to an external program, or "" if it is missing.
// Besides PATH it looks in the places installers commonly use, so a fresh
// install works without opening a new terminal.
func findTool(name, explicit string) string {
	if explicit != "" {
		if p, err := exec.LookPath(expandHome(explicit)); err == nil {
			return p
		}
		return ""
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	exe := name
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	var dirs []string
	if self, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(self))
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		local := os.Getenv("LOCALAPPDATA")
		dirs = append(dirs, filepath.Join(local, "Microsoft", "WinGet", "Links"),
			filepath.Join(os.Getenv("ProgramFiles"), "ffmpeg", "bin"), `C:\ffmpeg\bin`,
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "cloudflared"))
		// winget unpacks portable packages under Packages\<id>\...\bin
		if m, _ := filepath.Glob(filepath.Join(local, "Microsoft", "WinGet", "Packages", "*", "*", "bin")); len(m) > 0 {
			dirs = append(dirs, m...)
		}
		if m, _ := filepath.Glob(filepath.Join(local, "Microsoft", "WinGet", "Packages", "*")); len(m) > 0 {
			dirs = append(dirs, m...)
		}
	case "darwin":
		dirs = append(dirs, "/opt/homebrew/bin", "/usr/local/bin", filepath.Join(home, ".local", "bin"))
	default:
		dirs = append(dirs, filepath.Join(home, ".local", "bin"), "/usr/local/bin", "/snap/bin", "/usr/bin")
	}
	for _, d := range dirs {
		p := filepath.Join(d, exe)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// installHint is the one command that installs a missing tool on this OS.
func installHint(tool string) string {
	switch runtime.GOOS {
	case "darwin":
		return "brew install " + tool
	case "windows":
		switch tool {
		case "ffmpeg", "ffprobe":
			return "winget install Gyan.FFmpeg"
		case "cloudflared":
			return "winget install Cloudflare.cloudflared"
		case "ngrok":
			return "winget install ngrok.ngrok"
		}
	default:
		switch tool {
		case "ffmpeg", "ffprobe":
			for _, pm := range [][2]string{{"apt-get", "sudo apt install ffmpeg"}, {"dnf", "sudo dnf install ffmpeg"},
				{"pacman", "sudo pacman -S ffmpeg"}, {"zypper", "sudo zypper install ffmpeg"}, {"apk", "sudo apk add ffmpeg"}} {
				if _, err := exec.LookPath(pm[0]); err == nil {
					return pm[1]
				}
			}
			return "your package manager, or https://ffmpeg.org/download.html"
		case "cloudflared":
			arch := runtime.GOARCH
			return fmt.Sprintf("mkdir -p ~/.local/bin && curl -fsSL -o ~/.local/bin/cloudflared https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-%s && chmod +x ~/.local/bin/cloudflared", arch)
		case "ngrok":
			return "see https://ngrok.com/download"
		}
	}
	return "see the FairPlay README"
}

// openBrowser opens url in the default browser. It returns false when there
// is no desktop to open it on.
func openBrowser(url string) bool {
	if os.Getenv("FAIRPLAY_NO_BROWSER") != "" {
		return false
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return false
		}
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := cmd.Start(); err != nil {
		return false
	}
	go func() { _ = cmd.Wait() }()
	return true
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

// fail prints a friendly error and exits. When FairPlay was started by
// double-clicking on Windows, it waits for Enter so the window doesn't vanish.
func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "\n  FairPlay can't start: "+format+"\n\n", a...)
	pauseIfDoubleClicked()
	os.Exit(1)
}

func failHint(problem, command string) {
	fmt.Fprintf(os.Stderr, "\n  %s\n\n  Install it with:\n    %s\n\n  Then run FairPlay again.\n\n", problem, command)
	pauseIfDoubleClicked()
	os.Exit(1)
}

func pauseIfDoubleClicked() {
	if runtime.GOOS == "windows" && len(os.Args) == 1 {
		fmt.Fprint(os.Stderr, "  Press Enter to close this window.")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
}

// runCheck prints what FairPlay found, for troubleshooting.
func runCheck(o options) {
	ok := func(b bool) string {
		if b {
			return "ok  "
		}
		return "MISSING"
	}
	fmt.Printf("FairPlay %s on %s/%s\n\n", version, runtime.GOOS, runtime.GOARCH)
	for _, t := range []string{"ffmpeg", "ffprobe", "cloudflared", "ngrok"} {
		explicit := ""
		switch t {
		case "ffmpeg":
			explicit = o.ffmpeg
		case "ffprobe":
			explicit = o.ffprobe
		}
		p := findTool(t, explicit)
		line := fmt.Sprintf("  %-12s %s", t, ok(p != ""))
		if p != "" {
			line += "  " + p
		} else {
			need := "needed"
			if t == "cloudflared" || t == "ngrok" {
				need = "optional, for internet sharing"
			}
			line += fmt.Sprintf("  (%s) install: %s", need, installHint(t))
		}
		fmt.Println(line)
	}
	saved := loadSettings(o.data)
	if root, created, err := chooseMediaFolder(o, saved); err != nil {
		fmt.Printf("  %-12s %s  %v\n", "videos", "MISSING", err)
	} else {
		n := 0
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && media.IsVideo(d.Name()) {
				n++
			}
			return nil
		})
		note := ""
		if created {
			note = " (just created)"
		}
		fmt.Printf("  %-12s ok    %s, %d video(s)%s\n", "videos", tildePath(root), n, note)
	}
	fmt.Printf("  %-12s       %s\n", "settings", tildePath(o.data))
	fmt.Printf("  %-12s       %s\n", "cache", tildePath(o.cache))
	for _, u := range lanIPs() {
		fmt.Printf("  %-12s       http://%s:%d\n", "wi-fi", u, o.port)
	}
	fmt.Printf("  %-12s       %s\n", "updates", o.update)
}
