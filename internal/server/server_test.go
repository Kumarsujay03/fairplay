package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"github.com/Kumarsujay03/fairplay/internal/auth"
	"github.com/Kumarsujay03/fairplay/internal/hub"
	"github.com/Kumarsujay03/fairplay/internal/media"
)

// ---- test client ----

type tc struct {
	t    *testing.T
	base string
	hc   *http.Client
	ip   string // simulated visitor IP, sent the way the tunnel does
}

func newClient(t *testing.T, base string) *tc {
	jar, _ := cookiejar.New(nil)
	return &tc{t: t, base: base, hc: &http.Client{Jar: jar}}
}

func (c *tc) do(method, path string, body any, origin bool) (int, map[string]any) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if origin {
		req.Header.Set("Origin", c.base)
	}
	if c.ip != "" {
		req.Header.Set("CF-Connecting-IP", c.ip)
	}
	res, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(res.Body)
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func (c *tc) proof() map[string]string {
	_, ch := c.do("GET", "/api/challenge", nil, false)
	salt, bits := ch["salt"].(string), int(ch["bits"].(float64))
	for n := 0; ; n++ {
		h := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", salt, n)))
		if leadingZeroBits(h) >= bits {
			return map[string]string{"id": ch["id"].(string), "nonce": fmt.Sprint(n)}
		}
	}
}

type wsc struct {
	t    *testing.T
	conn *websocket.Conn
	in   chan map[string]any
}

func (c *tc) ws() *wsc {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := http.Header{"Origin": {c.base}}
	conn, res, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(c.base, "http")+"/ws", &websocket.DialOptions{HTTPClient: c.hc, HTTPHeader: h})
	if err != nil {
		code := 0
		if res != nil {
			code = res.StatusCode
		}
		c.t.Fatalf("ws dial: %v (%d)", err, code)
	}
	w := &wsc{t: c.t, conn: conn, in: make(chan map[string]any, 512)}
	go func() {
		for {
			_, b, err := conn.Read(context.Background())
			if err != nil {
				close(w.in)
				return
			}
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			w.in <- m
		}
	}()
	return w
}

func (w *wsc) send(m map[string]any) {
	b, _ := json.Marshal(m)
	if err := w.conn.Write(context.Background(), websocket.MessageText, b); err != nil {
		w.t.Fatal(err)
	}
}

// wait returns the first message of type t matching ok, failing after 20 s.
func (w *wsc) wait(t string, ok func(map[string]any) bool) map[string]any {
	w.t.Helper()
	deadline := time.After(20 * time.Second)
	for {
		select {
		case m, open := <-w.in:
			if !open {
				w.t.Fatalf("socket closed while waiting for %s", t)
			}
			if m["t"] == t && (ok == nil || ok(m)) {
				return m
			}
		case <-deadline:
			w.t.Fatalf("timed out waiting for %s", t)
		}
	}
}

// ---- fixture ----

type fixture struct {
	srv   *httptest.Server
	store *auth.Store
	hub   *hub.Hub
	mm    *media.Manager
	ids   []string
}

func setup(t *testing.T) *fixture {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	root := t.TempDir()
	for i, name := range []string{"A Movie.mkv", "B Movie.mp4"} {
		gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
			"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24", "-f", "lavfi", "-i", "sine=frequency=440",
			"-t", fmt.Sprint(6+i), "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", filepath.Join(root, name))
		if out, err := gen.CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	store, err := auth.NewStore(filepath.Join(t.TempDir(), "inv.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mm, err := media.NewManager(ctx, media.Options{Root: root, Cache: t.TempDir(), FFmpeg: "ffmpeg", FFprobe: "ffprobe"})
	if err != nil {
		t.Fatal(err)
	}
	items, _, _ := mm.List()
	h := hub.New(store, mm, hub.Settings{RoomName: "Test night", Reactions: true, MaxGuests: 5})
	go h.Run(ctx)
	web := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>FairPlay</title>")}, "assets/app.js": {Data: []byte("x")}}
	s := New(store, h, mm, web)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	t.Cleanup(mm.Stop)
	f := &fixture{srv: srv, store: store, hub: h, mm: mm}
	for _, it := range items {
		f.ids = append(f.ids, it.ID)
	}
	return f
}

func hostLogin(t *testing.T, f *fixture) *tc {
	c := newClient(t, f.srv.URL)
	code, out := c.do("POST", "/api/host/login", map[string]any{"key": f.store.HostKey(), "pow": c.proof()}, true)
	if code != 200 {
		t.Fatalf("host login %d %v", code, out)
	}
	return c
}

func invite(t *testing.T, host *tc, label string) (id, token string) {
	code, out := host.do("POST", "/api/host/invites", map[string]any{"label": label}, true)
	if code != 200 {
		t.Fatalf("invite %d %v", code, out)
	}
	return out["invite"].(map[string]any)["id"].(string), out["token"].(string)
}

func join(c *tc, id, token, name string) (int, map[string]any) {
	return c.do("POST", "/api/join", map[string]any{"id": id, "token": token, "name": name, "pow": c.proof()}, true)
}

// ---- tests ----

func TestFullRoomFlow(t *testing.T) {
	f := setup(t)
	host := hostLogin(t, f)
	hw := host.ws()
	hello := hw.wait("hello", nil)
	if hello["you"].(map[string]any)["role"] != "host" {
		t.Fatal("host socket should be host")
	}

	// Invite, peek, join → lobby → admit.
	id, token := invite(t, host, "Priya")
	guest := newClient(t, f.srv.URL)
	code, peek := guest.do("POST", "/api/peek", map[string]any{"id": id, "token": token, "pow": guest.proof()}, true)
	if code != 200 || peek["label"] != "Priya" || peek["roomName"] != "Test night" {
		t.Fatalf("peek %d %v", code, peek)
	}
	code, res := join(guest, id, token, "Priya P")
	if code != 200 || res["status"] != "pending" {
		t.Fatalf("join %d %v", code, res)
	}
	lob := hw.wait("lobby", func(m map[string]any) bool { return len(m["pending"].([]any)) == 1 })
	pending := lob["pending"].([]any)[0].(map[string]any)
	if pending["name"] != "Priya P" || pending["secondDevice"] != false {
		t.Fatalf("lobby entry %v", pending)
	}
	// Pending guests cannot open the socket or the media.
	if code, _ := guest.do("GET", "/ws", nil, false); code != http.StatusUnauthorized {
		t.Fatalf("pending ws should be refused, got %d", code)
	}
	hw.send(map[string]any{"t": "host", "op": "admit", "sid": pending["id"]})
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, me := guest.do("GET", "/api/me", nil, false)
		if me["status"] == "admitted" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("guest never admitted")
		}
		time.Sleep(50 * time.Millisecond)
	}
	gw := guest.ws()
	ghello := gw.wait("hello", nil)
	if ghello["you"].(map[string]any)["control"] != false {
		t.Fatal("guest must not control playback by default")
	}

	// Ping returns server time.
	gw.send(map[string]any{"t": "ping", "c": 123.5})
	pong := gw.wait("pong", nil)
	if pong["c"].(float64) != 123.5 || pong["s"].(float64) < float64(time.Now().Add(-time.Minute).UnixMilli()) {
		t.Fatalf("bad pong %v", pong)
	}

	// Host loads a video; it becomes playable while encoding.
	hw.send(map[string]any{"t": "host", "op": "load", "media": f.ids[0]})
	gw.wait("media", func(m map[string]any) bool { mm := m["media"].(map[string]any); return mm["playable"] == true })
	gw.wait("state", func(m map[string]any) bool { return m["state"].(map[string]any)["reason"] == "host" })

	// The host sets the room's audio, subtitles and quality; guests receive it but cannot set it.
	hw.send(map[string]any{"t": "host", "op": "prefs", "prefs": map[string]any{"audio": 1, "sub": "sub_0", "quality": "1080p"}})
	pm := gw.wait("prefs", func(m map[string]any) bool { return m["prefs"].(map[string]any)["quality"] == "1080p" })["prefs"].(map[string]any)
	if pm["audio"].(float64) != 1 || pm["sub"] != "sub_0" {
		t.Fatalf("prefs %v", pm)
	}
	gw.send(map[string]any{"t": "host", "op": "prefs", "prefs": map[string]any{"audio": 0, "sub": "", "quality": "auto"}})
	gw.wait("err", func(m map[string]any) bool { return strings.Contains(m["text"].(string), "only the host") })
	hw.send(map[string]any{"t": "host", "op": "prefs", "prefs": map[string]any{"audio": 99, "sub": "../x", "quality": "huge"}})
	clean := gw.wait("prefs", func(m map[string]any) bool { return m["prefs"].(map[string]any)["quality"] == "auto" })["prefs"].(map[string]any)
	if clean["audio"].(float64) != -1 || clean["sub"] != "" {
		t.Fatalf("bad prefs must be cleaned, got %v", clean)
	}

	// Guest can stream the current file but nothing else.
	if code, _ := guest.do("GET", "/hls/"+f.ids[0]+"/master.m3u8", nil, false); code != 200 {
		t.Fatalf("guest hls current %d", code)
	}
	if code, _ := guest.do("GET", "/hls/"+f.ids[1]+"/master.m3u8", nil, false); code != 404 {
		t.Fatalf("guest must not reach other files, got %d", code)
	}
	if code, _ := newClient(t, f.srv.URL).do("GET", "/hls/"+f.ids[0]+"/master.m3u8", nil, false); code != 404 {
		t.Fatalf("anonymous hls must 404, got %d", code)
	}

	// Guest cannot control; host plays with a lead so everyone starts together.
	gw.send(map[string]any{"t": "cmd", "op": "play"})
	gw.wait("err", nil)
	before := time.Now().UnixMilli()
	hw.send(map[string]any{"t": "cmd", "op": "play"})
	st := gw.wait("state", func(m map[string]any) bool { return m["state"].(map[string]any)["playing"] == true })["state"].(map[string]any)
	if anchor := int64(st["anchor"].(float64)); anchor < before+700 {
		t.Fatalf("play should be scheduled at least 700 ms ahead, lead was %d", anchor-before)
	}

	// Co-host permission.
	hw.send(map[string]any{"t": "host", "op": "cohost", "sid": pending["id"], "on": true})
	gw.wait("you", func(m map[string]any) bool { return m["you"].(map[string]any)["control"] == true })
	gw.send(map[string]any{"t": "cmd", "op": "pause"})
	gw.wait("state", func(m map[string]any) bool {
		s := m["state"].(map[string]any)
		return s["playing"] == false && s["by"] == "Priya P"
	})

	// Intermission length is a host setting that guests' players follow, clamped to 1..120 minutes.
	hw.send(map[string]any{"t": "host", "op": "settings", "settings": map[string]any{
		"roomName": "Test night", "reactions": true, "maxGuests": 5, "interMinutes": 12}})
	gw.wait("settings", func(m map[string]any) bool { return m["settings"].(map[string]any)["interMinutes"].(float64) == 12 })
	hw.send(map[string]any{"t": "host", "op": "settings", "settings": map[string]any{
		"roomName": "Test night", "reactions": true, "maxGuests": 5, "interMinutes": 500}})
	gw.wait("settings", func(m map[string]any) bool { return m["settings"].(map[string]any)["interMinutes"].(float64) == 120 })

	// Intermission with countdown.
	hw.send(map[string]any{"t": "cmd", "op": "inter", "sec": 60})
	gw.wait("state", func(m map[string]any) bool {
		s := m["state"].(map[string]any)
		return s["reason"] == "intermission" && s["interUntil"].(float64) > 0
	})

	// Chat, reactions and the chat rate limit.
	gw.send(map[string]any{"t": "chat", "text": "  hello <b>room</b>  "})
	cm := hw.wait("chat", func(m map[string]any) bool { return m["m"].(map[string]any)["sys"] == nil })
	if cm["m"].(map[string]any)["text"] != "hello <b>room</b>" || cm["m"].(map[string]any)["name"] != "Priya P" {
		t.Fatalf("chat %v", cm)
	}
	gw.send(map[string]any{"t": "react", "e": "🔥"})
	hw.wait("react", func(m map[string]any) bool { return m["e"] == "🔥" })
	for i := 0; i < 8; i++ {
		gw.send(map[string]any{"t": "chat", "text": fmt.Sprint("spam ", i)})
	}
	gw.wait("err", func(m map[string]any) bool { return strings.Contains(m["text"].(string), "slow down") })

	// Host hides chat from Priya: she stops receiving and cannot post.
	hw.send(map[string]any{"t": "host", "op": "chat_hide", "sid": pending["id"], "on": true})
	gw.wait("you", func(m map[string]any) bool { return m["you"].(map[string]any)["chat"] == false })
	hw.send(map[string]any{"t": "chat", "text": "secret plan"})
	hw.wait("chat", func(m map[string]any) bool { return m["m"].(map[string]any)["text"] == "secret plan" })
	time.Sleep(1500 * time.Millisecond) // let her rate limit refill too
	gw.send(map[string]any{"t": "chat", "text": "can I talk?"})
	gw.wait("err", func(m map[string]any) bool { return strings.Contains(m["text"].(string), "turned off chat") })
	drainForChat := func() bool {
		for {
			select {
			case m := <-gw.in:
				if m["t"] == "chat" {
					return true
				}
			default:
				return false
			}
		}
	}
	if drainForChat() {
		t.Fatal("a viewer with chat hidden must not receive chat messages")
	}
	hw.send(map[string]any{"t": "host", "op": "chat_hide", "sid": pending["id"], "on": false})
	gw.wait("you", func(m map[string]any) bool { return m["you"].(map[string]any)["chat"] == true })
	hist := gw.wait("chathist", nil)
	found := false
	for _, c := range hist["chat"].([]any) {
		if c.(map[string]any)["text"] == "secret plan" {
			found = true
		}
	}
	if !found {
		t.Fatal("showing chat again should restore the history")
	}

	// Peers carry ping.
	gw.send(map[string]any{"t": "status", "mode": "solo", "rtt": 212, "jit": 9, "pos": 1.5})
	hw.wait("peers", func(m map[string]any) bool {
		for _, p := range m["peers"].([]any) {
			pm := p.(map[string]any)
			if pm["name"] == "Priya P" && pm["rtt"].(float64) == 212 && pm["mode"] == "solo" {
				return true
			}
		}
		return false
	})

	// Queue: add both files, remove one, clear the rest.
	hw.send(map[string]any{"t": "host", "op": "queue_add", "media": f.ids[1]})
	hw.wait("queue", func(m map[string]any) bool { return len(m["queue"].([]any)) == 1 })
	hw.send(map[string]any{"t": "host", "op": "queue_add", "media": f.ids[0]})
	hw.wait("queue", func(m map[string]any) bool { return len(m["queue"].([]any)) == 2 })
	hw.send(map[string]any{"t": "host", "op": "queue_remove", "index": 0})
	hw.wait("queue", func(m map[string]any) bool { return len(m["queue"].([]any)) == 1 })
	hw.send(map[string]any{"t": "host", "op": "queue_clear"})
	hw.wait("queue", func(m map[string]any) bool { return len(m["queue"].([]any)) == 0 })

	// Stop and clear: no video for anyone, and guests lose access to its stream.
	hw.send(map[string]any{"t": "host", "op": "clear"})
	gw.wait("state", func(m map[string]any) bool { return m["state"].(map[string]any)["media"] == "" })
	if code, _ := guest.do("GET", "/hls/"+f.ids[0]+"/master.m3u8", nil, false); code != 404 {
		t.Fatalf("after clearing, guests must not reach the old stream, got %d", code)
	}
	hw.send(map[string]any{"t": "host", "op": "load", "media": f.ids[0]})
	gw.wait("state", func(m map[string]any) bool { return m["state"].(map[string]any)["media"] == f.ids[0] })

	// Forwarded link: another device gets the lobby, flagged, and does not kick Priya.
	stranger := newClient(t, f.srv.URL)
	code, res = join(stranger, id, token, "Totally Priya")
	if code != 200 || res["status"] != "pending" || res["secondDevice"] != true {
		t.Fatalf("forwarded link %d %v", code, res)
	}
	hw.wait("lobby", func(m map[string]any) bool {
		for _, p := range m["pending"].([]any) {
			if p.(map[string]any)["secondDevice"] == true {
				return true
			}
		}
		return false
	})

	// Kick ends the guest's socket with a reason.
	hw.send(map[string]any{"t": "host", "op": "kick", "sid": pending["id"]})
	k := gw.wait("kicked", nil)
	if !strings.Contains(k["reason"].(string), "removed") {
		t.Fatalf("kick reason %v", k)
	}
}

func TestShieldAndHeaders(t *testing.T) {
	f := setup(t)
	host := hostLogin(t, f)
	id, _ := invite(t, host, "Aman")

	// Security headers on every page.
	res, err := http.Get(f.srv.URL + "/j/" + id)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy", "X-Robots-Tag", "X-Frame-Options"} {
		if res.Header.Get(h) == "" {
			t.Errorf("missing header %s", h)
		}
	}
	if !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Error("CSP should forbid framing")
	}

	// Scanners get 404 for anything that is not a page.
	for _, p := range []string{"/wp-admin", "/.env", "/api/host/invites", "/assets/../../etc/passwd", "/hls/x/../../status.json"} {
		r, _ := http.Get(f.srv.URL + p)
		if r.StatusCode != 404 {
			t.Errorf("%s: want 404 got %d", p, r.StatusCode)
		}
		r.Body.Close()
	}

	// CSRF: no Origin or a foreign Origin is refused.
	c := newClient(t, f.srv.URL)
	if code, _ := c.do("POST", "/api/join", map[string]any{"id": id}, false); code != 403 {
		t.Errorf("join without Origin: %d", code)
	}
	req, _ := http.NewRequest("POST", f.srv.URL+"/api/host/invites", strings.NewReader(`{"label":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	r, _ := host.hc.Do(req)
	if r.StatusCode != 403 {
		t.Errorf("foreign origin: %d", r.StatusCode)
	}
	r.Body.Close()

	// Missing or forged proof of work is refused and counts as a strike.
	bot := newClient(t, f.srv.URL)
	code, _ := bot.do("POST", "/api/join", map[string]any{"id": id, "token": "x", "name": "bot", "pow": map[string]string{"id": "nope", "nonce": "1"}}, true)
	if code != 403 {
		t.Errorf("forged pow: %d", code)
	}
	// Wrong tokens with valid proofs: five strikes and the IP is banned.
	for i := 0; i < 4; i++ {
		join(bot, id, "wrong-token", "bot")
	}
	code, out := bot.do("GET", "/api/challenge", nil, false)
	if code != http.StatusTooManyRequests {
		t.Fatalf("expected ban after strikes, got %d %v", code, out)
	}
}

func TestLobbyFloodCap(t *testing.T) {
	f := setup(t)
	host := hostLogin(t, f)
	for i := 0; i < maxPending; i++ {
		id, token := invite(t, host, fmt.Sprint("Guest", i))
		g := newClient(t, f.srv.URL)
		g.ip = fmt.Sprintf("203.0.113.%d", i+1)
		if code, out := join(g, id, token, "x"); code != 200 {
			t.Fatalf("join %d: %d %v", i, code, out)
		}
	}
	id, token := invite(t, host, "One more")
	g := newClient(t, f.srv.URL)
	g.ip = "198.51.100.7"
	if code, out := join(g, id, token, "x"); code != http.StatusServiceUnavailable {
		t.Fatalf("lobby should be full, got %d %v", code, out)
	}
}

func TestRateLimitPerIP(t *testing.T) {
	f := setup(t)
	c := newClient(t, f.srv.URL)
	c.ip = "192.0.2.50"
	limited := false
	for i := 0; i < 20; i++ {
		if code, _ := c.do("GET", "/api/challenge", nil, false); code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("20 rapid challenges from one IP should hit the limit")
	}
	other := newClient(t, f.srv.URL)
	other.ip = "192.0.2.51"
	if code, _ := other.do("GET", "/api/challenge", nil, false); code != 200 {
		t.Fatalf("a different visitor must not be affected, got %d", code)
	}
}

// The console treats these fields as lists; null instead of [] once crashed it.
func TestHostAPIListsAreNeverNull(t *testing.T) {
	f := setup(t)
	host := hostLogin(t, f)
	empty := t.TempDir()
	if err := f.mm.SetRoot(empty); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ path, field string }{
		{"/api/host/library", "items"},
		{"/api/host/storage", "items"},
	} {
		req, _ := http.NewRequest("GET", f.srv.URL+c.path, nil)
		res, err := host.hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]json.RawMessage
		_ = json.NewDecoder(res.Body).Decode(&raw)
		res.Body.Close()
		if v := strings.TrimSpace(string(raw[c.field])); v == "null" || v == "" {
			t.Errorf("%s: %q must be a list, got %s", c.path, c.field, v)
		}
	}
	code, out := host.do("POST", "/api/host/storage/clear", map[string]any{}, true)
	if code != 200 || out["items"] == nil {
		t.Errorf("storage/clear: %d items=%v", code, out["items"])
	}
}
