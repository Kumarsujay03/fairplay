// FairPlay: watch videos together, perfectly in sync.
//
//	fairplay -media ~/Movies
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
	"syscall"
	"time"

	"github.com/Kumarsujay03/fairplay/internal/auth"
	"github.com/Kumarsujay03/fairplay/internal/hub"
	"github.com/Kumarsujay03/fairplay/internal/media"
	"github.com/Kumarsujay03/fairplay/internal/server"
	"github.com/Kumarsujay03/fairplay/internal/tunnel"
	"github.com/Kumarsujay03/fairplay/internal/webui"
)

var version = "dev"

func main() {
	home, _ := os.UserHomeDir()
	cfgDir, _ := os.UserConfigDir()
	cacheDir, _ := os.UserCacheDir()

	var (
		mediaDir  = flag.String("media", filepath.Join(home, "Videos"), "folder with your videos")
		port      = flag.Int("port", 8321, "port to listen on")
		lan       = flag.Bool("lan", true, "let devices on your Wi-Fi join directly (false = localhost and tunnel only)")
		tunnelP   = flag.String("tunnel", "cloudflared", "internet tunnel: cloudflared, ngrok or off")
		tunnelBin = flag.String("tunnel-bin", "", "path to the tunnel binary (default: look in PATH)")
		ffmpeg    = flag.String("ffmpeg", "ffmpeg", "path to ffmpeg")
		ffprobe   = flag.String("ffprobe", "ffprobe", "path to ffprobe")
		cache     = flag.String("cache", filepath.Join(cacheDir, "fairplay"), "transcode cache folder")
		dataDir   = flag.String("data", filepath.Join(cfgDir, "fairplay"), "folder for invites")
		roomName  = flag.String("name", "Movie night", "room name shown to guests")
		maxGuests = flag.Int("max", 12, "maximum number of guests")
		autoAdmit = flag.Bool("auto-admit", false, "admit first-time invite opens without the lobby (second devices always need approval)")
		showVer   = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()
	if *showVer {
		fmt.Println("fairplay", version)
		return
	}
	log.SetFlags(log.Ltime)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		log.Fatal(err)
	}
	store, err := auth.NewStore(filepath.Join(*dataDir, "invites.json"))
	if err != nil {
		log.Fatalf("invites: %v", err)
	}
	mm, err := media.NewManager(ctx, media.Options{Root: *mediaDir, Cache: *cache, FFmpeg: *ffmpeg, FFprobe: *ffprobe})
	if err != nil {
		log.Fatal(err)
	}
	h := hub.New(store, mm, hub.Settings{
		RoomName: *roomName, AutoAdmit: *autoAdmit, Reactions: true, MaxGuests: *maxGuests,
	})
	srv := server.New(store, h, mm, webui.FS())

	bind := "0.0.0.0"
	if !*lan {
		bind = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(*port)))
	if err != nil {
		log.Fatalf("listen: %v", err)
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
	if *lan {
		for _, ip := range lanIPs() {
			lanURLs = append(lanURLs, fmt.Sprintf("http://%s:%d", ip, *port))
		}
	}
	h.SetTunnel(hub.TunnelInfo{Provider: *tunnelP, State: "starting", LAN: lanURLs})

	go h.Run(ctx)
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
	go tunnel.Run(ctx, *tunnelP, *tunnelBin, *port, func(st tunnel.Status) {
		h.SetTunnel(hub.TunnelInfo{Provider: st.Provider, State: st.State, URL: st.URL, Error: st.Error, LAN: lanURLs})
		switch st.State {
		case "up":
			log.Printf("internet link ready: %s", st.URL)
		case "error":
			log.Printf("tunnel: %s", st.Error)
		}
	})
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	fmt.Printf(`
  FairPlay %s
  media    %s
  encoder  %s

  Host console (keep this link private, it is your password):
    http://127.0.0.1:%d/host#k=%s

`, version, mm.Root(), mm.EncoderName(), *port, store.HostKey())
	for _, u := range lanURLs {
		fmt.Printf("  On your Wi-Fi: %s\n", u)
	}
	fmt.Printf("  Internet tunnel: %s (the public link shows up in the host console)\n\n  Ctrl+C to stop. Stopping ends the room for everyone.\n\n", *tunnelP)

	<-ctx.Done()
	log.Print("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(sctx)
	mm.Stop()
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

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
