// FairPlay: watch videos together, perfectly in sync.
//
//	fairplay                 start a room (opens the host console in your browser)
//	fairplay -media ~/Movies start with a specific video folder
//	fairplay update          install the latest release now
//	fairplay check           show what FairPlay found on this computer
//	fairplay version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Kumarsujay03/fairplay/internal/auth"
	"github.com/Kumarsujay03/fairplay/internal/emoji"
	"github.com/Kumarsujay03/fairplay/internal/hub"
	"github.com/Kumarsujay03/fairplay/internal/media"
	"github.com/Kumarsujay03/fairplay/internal/server"
	"github.com/Kumarsujay03/fairplay/internal/tunnel"
	"github.com/Kumarsujay03/fairplay/internal/update"
	"github.com/Kumarsujay03/fairplay/internal/webui"
)

var version = "dev"

type options struct {
	media, tunnel, tunnelBin, ffmpeg, ffprobe, cache, data, name, update string
	port, maxGuests, interMinutes                                        int
	lan, autoAdmit, open                                                 bool
	mediaSet                                                             bool // -media was given explicitly
	cacheMaxGB                                                           float64
}

func parseFlags(name string, args []string) options {
	cfgDir, _ := os.UserConfigDir()
	cacheDir, _ := os.UserCacheDir()
	var o options
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.StringVar(&o.media, "media", "", "folder with your videos (default: your Videos, Movies or Downloads folder)")
	fs.IntVar(&o.port, "port", 8321, "port to listen on")
	fs.BoolVar(&o.lan, "lan", true, "let devices on your Wi-Fi join directly (false = this computer and the tunnel only)")
	fs.StringVar(&o.tunnel, "tunnel", "cloudflared", "internet tunnel: cloudflared, ngrok or off")
	fs.StringVar(&o.tunnelBin, "tunnel-bin", "", "path to the tunnel program (default: search)")
	fs.StringVar(&o.ffmpeg, "ffmpeg", "", "path to ffmpeg (default: search)")
	fs.StringVar(&o.ffprobe, "ffprobe", "", "path to ffprobe (default: search)")
	fs.StringVar(&o.cache, "cache", filepath.Join(cacheDir, "fairplay"), "folder for prepared videos (safe to delete)")
	fs.Float64Var(&o.cacheMaxGB, "cache-max", 40, "keep at most this many GB of prepared videos; older ones are removed (0 = no limit)")
	fs.StringVar(&o.data, "data", filepath.Join(cfgDir, "fairplay"), "folder for invites and settings")
	fs.StringVar(&o.name, "name", "Movie night", "room name guests see")
	fs.IntVar(&o.maxGuests, "max", 12, "maximum number of guests")
	fs.IntVar(&o.interMinutes, "intermission", 5, "default intermission length in minutes (the host can change it in the console)")
	fs.BoolVar(&o.autoAdmit, "auto-admit", false, "admit first-time invite opens without the lobby (second devices always need approval)")
	fs.BoolVar(&o.open, "open", true, "open the host console in your browser on start")
	fs.StringVar(&o.update, "update", envOr("FAIRPLAY_UPDATE", "auto"), "updates: auto (install on start), check (only tell me) or off")
	fs.Usage = usage
	_ = fs.Parse(args)
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "media" {
			o.mediaSet = true
		}
	})
	if fs.NArg() > 0 {
		fail("unexpected argument %q. Run `fairplay help` for usage.", fs.Arg(0))
	}
	return o
}

func usage() {
	fmt.Fprint(os.Stderr, `FairPlay: watch videos together, perfectly in sync.

Usage:
  fairplay [options]      start a room and open the host console
  fairplay update         install the latest release now
  fairplay check          show what FairPlay found on this computer
  fairplay firewall       let guests on your Wi-Fi through this computer's firewall
  fairplay cache          show how much space prepared videos use
  fairplay cache clear    delete prepared videos to free space
  fairplay version        print the version

Common options:
  -media <folder>         folder with your videos (you can also change it in the host console)
  -name "Movie night"     room name guests see
  -tunnel cloudflared     internet sharing: cloudflared, ngrok or off
  -open=false             don't open the browser
  -update auto            auto, check or off

All options: fairplay -h
`)
}

func main() {
	log.SetFlags(log.Ltime)
	args := os.Args[1:]
	cmd := "start"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "start", "serve", "run":
		start(parseFlags("fairplay", args))
	case "update", "upgrade":
		runUpdate()
	case "check", "doctor":
		runCheck(parseFlags("fairplay check", args))
	case "firewall":
		runFirewall(parseFlags("fairplay firewall", args))
	case "cache":
		sub := ""
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			sub, args = args[0], args[1:]
		}
		runCache(sub, parseFlags("fairplay cache", args))
	case "version", "--version", "-version":
		fmt.Println("fairplay", version)
	case "help", "-h", "--help":
		usage()
	default:
		fail("unknown command %q. Run `fairplay help` for usage.", cmd)
	}
}

func start(o options) {
	update.CleanupOld()
	updates := newUpdateState(o.update)
	if updates.startupUpdate() {
		return // replaced by the new version
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(o.data, 0o700); err != nil {
		fail("cannot create the settings folder %s: %v", o.data, err)
	}
	saved := loadSettings(o.data)

	ffmpeg, ffprobe := findTool("ffmpeg", o.ffmpeg), findTool("ffprobe", o.ffprobe)
	if ffmpeg == "" || ffprobe == "" {
		failHint("FairPlay needs ffmpeg to prepare videos, and it isn't installed.", installHint("ffmpeg"))
	}

	root, created, err := chooseMediaFolder(o, saved)
	if err != nil {
		fail("%v", err)
	}

	store, err := auth.NewStore(filepath.Join(o.data, "invites.json"))
	if err != nil {
		fail("cannot read invites: %v", err)
	}
	mm, err := media.NewManager(ctx, media.Options{Root: root, Cache: o.cache, FFmpeg: ffmpeg, FFprobe: ffprobe})
	if err != nil {
		fail("%v", err)
	}
	mm.MaxCache = int64(o.cacheMaxGB * 1e9)
	h := hub.New(store, mm, hub.Settings{RoomName: o.name, AutoAdmit: o.autoAdmit, Reactions: true, MaxGuests: o.maxGuests, InterMinutes: o.interMinutes})
	srv := server.New(store, h, mm, webui.FS())
	mm.Current = h.CurrentMedia
	if freed := mm.Prune(mm.MaxCache, ""); freed > 0 {
		log.Printf("freed %.1f GB of older prepared videos (limit %.0f GB, change with -cache-max)", float64(freed)/1e9, o.cacheMaxGB)
	}
	srv.Version = version
	srv.Emoji = emoji.NewStore(filepath.Join(o.cache, "emoji"), ffmpeg)
	srv.UpdateInfo = updates.info
	srv.OnMediaRoot = func(p string) {
		saved.Media = p
		if err := saveSettings(o.data, saved); err != nil {
			log.Printf("could not remember the folder: %v", err)
		}
		log.Printf("video folder is now %s", p)
	}

	bind := "0.0.0.0"
	if !o.lan {
		bind = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(o.port)))
	if err != nil {
		if isAddrInUse(err) {
			fail("port %d is already in use. Is FairPlay already running? Close it, or start with -port %d", o.port, o.port+1)
		}
		fail("cannot listen on port %d: %v", o.port, err)
	}
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second, // slow-header (slowloris) protection
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      0, // segments and sockets stream; per-write deadlines live in the hub
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          log.New(discard{}, "", 0),
	}

	var lanURLs []string
	var fw firewall
	if o.lan {
		for _, ip := range lanIPs() {
			lanURLs = append(lanURLs, fmt.Sprintf("http://%s:%d", ip, o.port))
		}
		exe, _ := os.Executable()
		fw = detectFirewall(o.port, lanNets(), exe)
		fw.Done = offerFirewallFix(fw, o.port, &saved, o.data)
	}
	srv.Firewall = func() any { return fw }
	srv.Shutdown = func() {
		log.Print("the host stopped FairPlay from the console")
		stop()
	}
	tunnelBin := ""
	if o.tunnel != "off" {
		tunnelBin = findTool(o.tunnel, o.tunnelBin)
	}
	h.SetTunnel(hub.TunnelInfo{Provider: o.tunnel, State: "starting", LAN: lanURLs})

	go h.Run(ctx)
	go updates.watch(ctx)
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				srv.Shield.Sweep()
			}
		}
	}()
	if o.tunnel != "off" && tunnelBin == "" {
		h.SetTunnel(hub.TunnelInfo{Provider: o.tunnel, State: "error",
			Error: o.tunnel + " is not installed, so only people on your Wi-Fi can join. Install it with: " + installHint(o.tunnel), LAN: lanURLs})
	} else {
		go tunnel.Run(ctx, o.tunnel, tunnelBin, o.port, func(st tunnel.Status) {
			h.SetTunnel(hub.TunnelInfo{Provider: st.Provider, State: st.State, URL: st.URL, Error: st.Error, LAN: lanURLs})
			switch st.State {
			case "up":
				log.Printf("internet link ready: %s", st.URL)
			case "error":
				log.Printf("tunnel: %s", st.Error)
			}
		})
	}
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fail("server stopped: %v", err)
		}
	}()

	hostURL := fmt.Sprintf("http://127.0.0.1:%d/host#k=%s", o.port, store.HostKey())
	opened := o.open && openBrowser(hostURL)
	printBanner(bannerInfo{
		hostURL: hostURL, opened: opened, root: mm.Root(), created: created, lan: lanURLs, fw: fw,
		tunnel: o.tunnel, tunnelMissing: o.tunnel != "off" && tunnelBin == "", encoder: mm.EncoderName(),
	})

	<-ctx.Done()
	fmt.Println()
	log.Print("stopping FairPlay, the room has ended")
	time.Sleep(400 * time.Millisecond) // let the hub say goodbye to every viewer
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(sctx)
	mm.Stop()
}

type bannerInfo struct {
	fw                             firewall
	hostURL, root, tunnel, encoder string
	lan                            []string
	opened, created, tunnelMissing bool
}

func printBanner(b bannerInfo) {
	fmt.Printf("\n  FairPlay %s is running.\n\n", version)
	if b.opened {
		fmt.Println("  The host console is opening in your browser. If it doesn't, open this link.")
	} else {
		fmt.Println("  Open this link to reach the host console.")
	}
	fmt.Println("  Keep it private, it works like a password:")
	fmt.Printf("    %s\n\n", b.hostURL)
	note := "change it in the host console"
	if b.created {
		note = "created for you, put videos here or pick another folder in the host console"
	}
	fmt.Printf("  Videos     %s (%s)\n", tildePath(b.root), note)
	for _, u := range b.lan {
		fmt.Printf("  Wi-Fi      %s\n", u)
	}
	if b.fw.Kind != "" && !b.fw.Done {
		fmt.Printf("             Your firewall (%s) may block Wi-Fi guests. Fix it once with: fairplay firewall\n", b.fw.Kind)
	}
	switch {
	case b.tunnel == "off":
		fmt.Println("  Internet   off (start without -tunnel off to share beyond your Wi-Fi)")
	case b.tunnelMissing:
		fmt.Printf("  Internet   %s is not installed, so only your Wi-Fi can join.\n             Install it with: %s\n", b.tunnel, installHint(b.tunnel))
	default:
		fmt.Printf("  Internet   starting %s, the public link appears in the host console\n", b.tunnel)
	}
	fmt.Printf("  Encoder    %s\n\n", b.encoder)
	fmt.Print("  Press Ctrl+C to stop. Stopping ends the room for everyone.\n\n")
}

func lanIPs() []string {
	var out []string
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || !ipn.IP.IsPrivate() {
				continue
			}
			out = append(out, ipn.IP.String())
		}
	}
	return out
}

func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE) || strings.Contains(strings.ToLower(err.Error()), "address already in use") ||
		strings.Contains(strings.ToLower(err.Error()), "only one usage of each socket address")
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
