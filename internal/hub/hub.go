// Package hub runs the live room: one WebSocket per viewer, the global clock,
// ping, chat, reactions, the lobby feed and co-host permissions.
//
// Lock order: Hub.mu may call into auth and media. Neither of those ever calls
// back into the hub synchronously; they only raise a dirty flag.
package hub

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/Kumarsujay03/fairplay/internal/auth"
	"github.com/Kumarsujay03/fairplay/internal/emoji"
	"github.com/Kumarsujay03/fairplay/internal/media"
	"github.com/Kumarsujay03/fairplay/internal/room"
)

type Settings struct {
	RoomName   string `json:"roomName"`
	AutoAdmit  bool   `json:"autoAdmit"`
	Locked     bool   `json:"locked"`
	WaitForAll bool   `json:"waitForAll"`
	Reactions  bool   `json:"reactions"`
	MaxGuests  int    `json:"maxGuests"`
	// InterMinutes is the default intermission length the host's buttons use.
	InterMinutes int `json:"interMinutes"`
}

type TunnelInfo struct {
	Provider string   `json:"provider"`
	State    string   `json:"state"`
	URL      string   `json:"url,omitempty"`
	Error    string   `json:"error,omitempty"`
	LAN      []string `json:"lan,omitempty"`
}

// TrackPrefs is the host's choice of audio, subtitles and quality for the
// current video. Every viewer follows it unless they pick their own.
type TrackPrefs struct {
	Audio   int    `json:"audio"`   // audio track index, -1 = the file's default
	Sub     string `json:"sub"`     // subtitle track id, "" = off
	Quality string `json:"quality"` // "auto" or a rung such as "1080p"
}

func defaultPrefs() TrackPrefs { return TrackPrefs{Audio: -1, Quality: "auto"} }

var (
	subIDRe   = regexp.MustCompile(`^(sub|ext)_[0-9]{1,3}$`)
	qualityRe = regexp.MustCompile(`^[0-9]{3,4}p$`)
)

type ChatMsg struct {
	ID   int64  `json:"id"`
	SID  string `json:"sid"`
	Name string `json:"name"`
	Text string `json:"text"`
	TS   int64  `json:"ts"`
	Sys  bool   `json:"sys,omitempty"`
}

type Peer struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Role      string  `json:"role"`
	CoHost    bool    `json:"cohost,omitempty"`
	ChatOff   bool    `json:"chatOff,omitempty"`
	Mode      string  `json:"mode"`
	Buffering bool    `json:"buf"`
	RTT       int     `json:"rtt"`
	Jitter    int     `json:"jit"`
	Drift     int     `json:"drift"`
	Pos       float64 `json:"pos"`
	Quality   string  `json:"q,omitempty"`
	Device    string  `json:"device,omitempty"`
}

type QueueItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// maxAudioTracks matches the transcoder's limit.
const maxAudioTracks = 6

// maxChatRunes caps one chat message, so a wall of text can't swamp the overlay.
const maxChatRunes = 200

// chatHistory is how many messages a room keeps; late joiners and anyone
// scrolling back can read them.
const chatHistory = 200

type Hub struct {
	auth  *auth.Store
	media *media.Manager

	mu         sync.Mutex
	room       *room.Room
	settings   Settings
	clients    map[*client]struct{}
	chat       []ChatMsg
	chatSeq    int64
	queue      []string
	cohost     map[string]bool
	chatOff    map[string]bool // keyed by invite (or session) so it survives reconnects
	statuses   map[string]media.Status
	tunnel     TunnelInfo
	autoplay   bool
	prefs      TrackPrefs
	peersDirty bool
	bufClear   time.Time

	sigMu      sync.Mutex
	mediaQ     map[string]media.Status
	lobbyDirty bool
	signal     chan struct{}

	now func() int64
}

func New(a *auth.Store, m *media.Manager, s Settings) *Hub {
	if s.InterMinutes <= 0 {
		s.InterMinutes = 5
	}
	h := &Hub{
		auth: a, media: m, room: room.New(), settings: s,
		clients: map[*client]struct{}{}, cohost: map[string]bool{}, chatOff: map[string]bool{},
		statuses: map[string]media.Status{}, mediaQ: map[string]media.Status{},
		signal: make(chan struct{}, 1), prefs: defaultPrefs(),
		now: func() int64 { return time.Now().UnixMilli() },
	}
	a.OnChange = func() {
		h.sigMu.Lock()
		h.lobbyDirty = true
		h.sigMu.Unlock()
		h.poke()
	}
	if m != nil {
		m.OnUpdate = func(st media.Status) {
			h.sigMu.Lock()
			h.mediaQ[st.ID] = st
			h.sigMu.Unlock()
			h.poke()
		}
	}
	return h
}

func (h *Hub) poke() {
	select {
	case h.signal <- struct{}{}:
	default:
	}
}

// Run drives timers and async updates until ctx ends.
func (h *Hub) Run(ctx context.Context) {
	tick := time.NewTicker(250 * time.Millisecond)
	peers := time.NewTicker(time.Second)
	sweep := time.NewTicker(time.Minute)
	defer tick.Stop()
	defer peers.Stop()
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			// Tell everyone the room is over before the sockets close.
			h.mu.Lock()
			for c := range h.clients {
				c.writeLocked(msg{"t": "kicked", "reason": "The host closed FairPlay. The room has ended."})
				c.close("FairPlay stopped")
			}
			h.mu.Unlock()
			time.Sleep(300 * time.Millisecond) // let the goodbye reach browsers
			return
		case <-h.signal:
			h.drainSignals()
		case <-tick.C:
			h.mu.Lock()
			h.tickLocked()
			h.mu.Unlock()
		case <-peers.C:
			h.mu.Lock()
			if h.peersDirty {
				h.peersDirty = false
				h.broadcastPeersLocked()
			}
			h.mu.Unlock()
		case <-sweep.C:
			h.auth.Sweep()
		}
	}
}

func (h *Hub) drainSignals() {
	h.sigMu.Lock()
	q := h.mediaQ
	h.mediaQ = map[string]media.Status{}
	lobby := h.lobbyDirty
	h.lobbyDirty = false
	h.sigMu.Unlock()

	h.mu.Lock()
	defer h.mu.Unlock()
	for _, st := range q {
		h.onMediaLocked(st)
	}
	if lobby {
		h.broadcastLobbyLocked()
	}
}

func (h *Hub) onMediaLocked(st media.Status) {
	h.statuses[st.ID] = st
	h.toHostsLocked(msg{"t": "libst", "status": st})
	cur := h.room.State()
	if st.ID != cur.MediaID {
		return
	}
	limit := st.Encoded
	if st.State == media.StateReady {
		limit = -1
	}
	h.room.SetPlayable(st.Duration, limit)
	h.broadcastLocked(msg{"t": "media", "media": st})
	if st.Playable {
		if s, ok := h.room.Ready(h.now()); ok {
			h.broadcastStateLocked(s)
			if h.autoplay {
				h.autoplay = false
				if s, ok := h.room.Play(h.now(), h.leadLocked(), "Playlist"); ok {
					h.broadcastStateLocked(s)
				}
			}
		}
	}
	if st.State == media.StateReady && len(h.queue) > 0 {
		next := h.queue[0]
		if ns, ok := h.statuses[next]; !ok || ns.State == media.StateIdle || ns.State == media.StateError {
			go func() { _ = h.media.Prepare(next) }() // prefetch the next item
		}
	}
}

func (h *Hub) tickLocked() {
	now := h.now()
	lead := h.leadLocked()
	if s, ok := h.room.Tick(now, lead); ok {
		h.broadcastStateLocked(s)
		if s.Reason == room.ReasonEnded && len(h.queue) > 0 {
			h.loadLocked(h.queue[0], "Playlist", true)
			return
		}
	}
	if !h.settings.WaitForAll {
		return
	}
	st := h.room.State()
	stalled := h.stalledLocked(now)
	switch {
	case st.Playing && now > st.Anchor+1500 && stalled != "":
		if s, ok := h.room.Pause(now, room.ReasonBuffering, stalled); ok {
			h.broadcastStateLocked(s)
		}
		h.bufClear = time.Time{}
	case !st.Playing && st.Reason == room.ReasonBuffering && stalled == "":
		if h.bufClear.IsZero() {
			h.bufClear = time.Now()
		} else if time.Since(h.bufClear) > 500*time.Millisecond {
			h.bufClear = time.Time{}
			if s, ok := h.room.ResumeAfterBuffering(now, lead); ok {
				h.broadcastStateLocked(s)
			}
		}
	case stalled != "":
		h.bufClear = time.Time{}
	}
}

// stalledLocked returns the name of a following viewer who has been buffering
// for more than 700 ms, or "".
func (h *Hub) stalledLocked(now int64) string {
	for c := range h.clients {
		if c.mode == "follow" && c.buffering && !c.bufSince.IsZero() && time.Since(c.bufSince) > 700*time.Millisecond {
			return c.name
		}
	}
	return ""
}

// leadLocked derives the start delay from the worst round trip of anyone
// following the room.
func (h *Hub) leadLocked() int64 {
	var worst int64
	for c := range h.clients {
		if c.mode == "follow" && int64(c.rtt) > worst {
			worst = int64(c.rtt)
		}
	}
	return room.Lead(worst)
}

// ---- HTTP entry ----

// Serve upgrades an admitted session to a WebSocket. Origin must already
// have been checked by the caller.
func (h *Hub) Serve(w http.ResponseWriter, r *http.Request, sess auth.Session) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	conn.SetReadLimit(32 << 10)
	ctx, cancel := context.WithCancel(context.Background())
	c := &client{
		h: h, conn: conn, send: make(chan []byte, 256), ctx: ctx, cancel: cancel,
		sid: sess.ID, invite: sess.InviteID, role: sess.Role, name: sess.Name, device: sess.Device, mode: "follow",
		chatTB: newBucket(5, 1), reactTB: newBucket(6, 3),
	}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	c.writeLocked(h.helloLocked(c))
	if sess.Role == auth.RoleHost {
		c.writeLocked(h.lobbyMsgLocked())
		c.writeLocked(msg{"t": "tunnel", "tunnel": h.tunnel})
		for _, st := range h.statuses {
			c.writeLocked(msg{"t": "libst", "status": st})
		}
	}
	h.peersDirty = true
	h.mu.Unlock()

	go c.writer()
	c.reader()

	h.mu.Lock()
	delete(h.clients, c)
	h.peersDirty = true
	h.mu.Unlock()
	cancel()
	conn.CloseNow()
}

// Kick disconnects every socket of the given sessions.
func (h *Hub) Kick(sids []string, reason string) {
	if len(sids) == 0 {
		return
	}
	set := map[string]bool{}
	for _, s := range sids {
		set[s] = true
		h.mu.Lock()
		delete(h.cohost, s)
		h.mu.Unlock()
	}
	h.mu.Lock()
	for c := range h.clients {
		if set[c.sid] {
			c.writeLocked(msg{"t": "kicked", "reason": reason})
			c.close(reason)
		}
	}
	h.peersDirty = true
	h.mu.Unlock()
}

// ClearQueue empties the playlist, for example after the media folder changed.
func (h *Hub) ClearQueue() {
	h.mu.Lock()
	h.queue = nil
	h.broadcastQueueLocked()
	h.mu.Unlock()
}

func (h *Hub) SetTunnel(t TunnelInfo) {
	h.mu.Lock()
	h.tunnel = t
	h.toHostsLocked(msg{"t": "tunnel", "tunnel": t})
	h.mu.Unlock()
}

func (h *Hub) Settings() Settings {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.settings
}

// CurrentMedia is the file guests may stream.
func (h *Hub) CurrentMedia() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.room.State().MediaID
}

func (h *Hub) Tunnel() TunnelInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tunnel
}

// OnlineCount is the number of distinct admitted guests connected.
func (h *Hub) OnlineCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	seen := map[string]bool{}
	for c := range h.clients {
		if c.role == auth.RoleGuest {
			seen[c.sid] = true
		}
	}
	return len(seen)
}

// ---- message handling ----

type msg map[string]any

type inMsg struct {
	T        string      `json:"t"`
	C        float64     `json:"c"`
	Mode     string      `json:"mode"`
	Buf      bool        `json:"buf"`
	Pos      float64     `json:"pos"`
	RTT      int         `json:"rtt"`
	Jit      int         `json:"jit"`
	Drift    int         `json:"drift"`
	Q        string      `json:"q"`
	Text     string      `json:"text"`
	E        string      `json:"e"`
	Op       string      `json:"op"`
	Sec      int         `json:"sec"`
	Media    string      `json:"media"`
	SID      string      `json:"sid"`
	On       bool        `json:"on"`
	Name     string      `json:"name"`
	Index    int         `json:"index"`
	To       int         `json:"to"`
	Settings *Settings   `json:"settings"`
	Prefs    *TrackPrefs `json:"prefs"`
}

func (h *Hub) handle(c *client, m inMsg) {
	now := h.now()
	switch m.T {
	case "ping":
		// Answer immediately with server time; this is the clock source.
		c.write(msg{"t": "pong", "c": m.C, "s": now})
		return
	case "status":
		h.mu.Lock()
		h.onStatusLocked(c, m)
		h.mu.Unlock()
	case "chat":
		h.onChat(c, m.Text)
	case "react":
		h.onReact(c, m.E)
	case "rename":
		if name, err := h.auth.Rename(c.sid, m.Name); err == nil {
			h.mu.Lock()
			for cl := range h.clients {
				if cl.sid == c.sid {
					cl.name = name
					cl.writeLocked(msg{"t": "you", "you": h.youLocked(cl)})
				}
			}
			h.peersDirty = true
			h.mu.Unlock()
		}
	case "cmd":
		h.onCommand(c, m)
	case "host":
		if c.role != auth.RoleHost {
			c.write(msg{"t": "err", "text": "only the host can do that"})
			return
		}
		h.onHost(c, m)
	}
}

func (h *Hub) onStatusLocked(c *client, m inMsg) {
	mode := "follow"
	if m.Mode == "solo" {
		mode = "solo"
	}
	if m.Buf && !c.buffering {
		c.bufSince = time.Now()
	}
	if !m.Buf {
		c.bufSince = time.Time{}
	}
	changed := c.mode != mode || c.buffering != m.Buf || absInt(c.rtt-m.RTT) > 15 || c.q != m.Q || absInt(c.drift-m.Drift) > 40
	c.mode, c.buffering, c.pos, c.q = mode, m.Buf, m.Pos, cut(m.Q, 12)
	c.rtt, c.jitter, c.drift = clampInt(m.RTT, 0, 10000), clampInt(m.Jit, 0, 10000), clampInt(m.Drift, -600000, 600000)
	if changed {
		h.peersDirty = true
	}
}

func (h *Hub) canControl(c *client) bool {
	return c.role == auth.RoleHost || h.cohost[c.sid]
}

func (h *Hub) onCommand(c *client, m inMsg) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.canControl(c) {
		c.writeLocked(msg{"t": "err", "text": "the host controls playback for everyone"})
		return
	}
	now, lead := h.now(), h.leadLocked()
	var (
		s  room.State
		ok bool
	)
	switch m.Op {
	case "play":
		s, ok = h.room.Play(now, lead, c.name)
	case "pause":
		s, ok = h.room.Pause(now, room.ReasonHost, c.name)
	case "seek":
		s, ok = h.room.SeekTo(now, lead, m.Pos, c.name)
	case "inter":
		sec := clampInt(m.Sec, 0, 3600)
		s, ok = h.room.Intermission(now, sec, c.name)
		if ok {
			h.systemChatLocked(c.name + " started an intermission")
		}
	case "inter_extend":
		s, ok = h.room.ExtendIntermission(now, clampInt(m.Sec, 30, 1800))
	case "inter_end":
		if h.room.State().Reason == room.ReasonIntermission {
			s, ok = h.room.Play(now, lead, c.name)
		}
	}
	if ok {
		h.broadcastStateLocked(s)
	}
}

func (h *Hub) onHost(c *client, m inMsg) {
	switch m.Op {
	case "admit":
		kicked, err := h.auth.Admit(m.SID, h.Settings().MaxGuests)
		if err != nil {
			c.write(msg{"t": "err", "text": "could not admit: " + err.Error()})
			return
		}
		h.Kick(kicked, "you joined from another device")
	case "deny":
		_ = h.auth.Deny(m.SID)
	case "kick":
		if err := h.auth.Kick(m.SID); err == nil {
			h.Kick([]string{m.SID}, "the host removed you from the room")
		}
	case "cohost":
		h.mu.Lock()
		if m.On {
			h.cohost[m.SID] = true
		} else {
			delete(h.cohost, m.SID)
		}
		for cl := range h.clients {
			if cl.sid == m.SID {
				cl.writeLocked(msg{"t": "you", "you": h.youLocked(cl)})
			}
		}
		h.peersDirty = true
		h.mu.Unlock()
	case "chat_hide":
		h.mu.Lock()
		for cl := range h.clients {
			if cl.sid != m.SID || cl.role == auth.RoleHost {
				continue
			}
			if m.On {
				h.chatOff[cl.chatKey()] = true
			} else {
				delete(h.chatOff, cl.chatKey())
			}
		}
		for cl := range h.clients {
			if cl.sid == m.SID {
				cl.writeLocked(msg{"t": "you", "you": h.youLocked(cl)})
				if !m.On {
					cl.writeLocked(msg{"t": "chathist", "chat": append([]ChatMsg(nil), h.chat...)})
				}
			}
		}
		h.peersDirty = true
		h.mu.Unlock()
	case "settings":
		if m.Settings == nil {
			return
		}
		ns := *m.Settings
		ns.RoomName = cut(auth.CleanName(ns.RoomName), 40)
		if ns.RoomName == "" {
			ns.RoomName = "Movie night"
		}
		ns.MaxGuests = clampInt(ns.MaxGuests, 1, 50)
		if ns.InterMinutes <= 0 {
			ns.InterMinutes = 5
		}
		ns.InterMinutes = clampInt(ns.InterMinutes, 1, 120)
		h.mu.Lock()
		h.settings = ns
		h.broadcastLocked(msg{"t": "settings", "settings": h.publicSettingsLocked()})
		h.toHostsLocked(msg{"t": "hostsettings", "settings": ns})
		h.mu.Unlock()
	case "prefs":
		if m.Prefs == nil {
			return
		}
		p := *m.Prefs
		if p.Audio < -1 || p.Audio >= maxAudioTracks {
			p.Audio = -1
		}
		if p.Sub != "" && !subIDRe.MatchString(p.Sub) {
			p.Sub = ""
		}
		if p.Quality != "auto" && !qualityRe.MatchString(p.Quality) {
			p.Quality = "auto"
		}
		h.mu.Lock()
		h.prefs = p
		h.broadcastLocked(msg{"t": "prefs", "prefs": p})
		h.mu.Unlock()
	case "load":
		h.mu.Lock()
		h.loadLocked(m.Media, c.name, false)
		h.mu.Unlock()
	case "clear":
		// Stop the video for everyone: no media, no encode, a clean player.
		h.mu.Lock()
		cur := h.room.State().MediaID
		if cur == "" {
			h.mu.Unlock()
			return
		}
		s := h.room.Clear(h.now(), c.name)
		h.autoplay = false
		h.prefs = defaultPrefs()
		h.broadcastStateLocked(s)
		h.broadcastLocked(msg{"t": "media", "media": nil})
		h.broadcastLocked(msg{"t": "prefs", "prefs": h.prefs})
		h.systemChatLocked(c.name + " stopped the video")
		h.mu.Unlock()
		go h.media.Cancel(cur)
	case "queue_clear":
		h.mu.Lock()
		h.queue = nil
		h.broadcastQueueLocked()
		h.mu.Unlock()
	case "queue_add":
		if _, ok := h.media.Item(m.Media); !ok {
			return
		}
		h.mu.Lock()
		if len(h.queue) < 100 {
			h.queue = append(h.queue, m.Media)
		}
		h.broadcastQueueLocked()
		h.mu.Unlock()
	case "queue_remove":
		h.mu.Lock()
		if m.Index >= 0 && m.Index < len(h.queue) {
			h.queue = append(h.queue[:m.Index], h.queue[m.Index+1:]...)
		}
		h.broadcastQueueLocked()
		h.mu.Unlock()
	case "queue_move":
		h.mu.Lock()
		if m.Index >= 0 && m.Index < len(h.queue) && m.To >= 0 && m.To < len(h.queue) {
			it := h.queue[m.Index]
			h.queue = append(h.queue[:m.Index], h.queue[m.Index+1:]...)
			h.queue = append(h.queue[:m.To], append([]string{it}, h.queue[m.To:]...)...)
		}
		h.broadcastQueueLocked()
		h.mu.Unlock()
	case "next":
		h.mu.Lock()
		if len(h.queue) > 0 {
			h.loadLocked(h.queue[0], c.name, false)
		}
		h.mu.Unlock()
	case "end_room":
		kicked := h.auth.RevokeAll()
		h.mu.Lock()
		h.settings.Locked = true
		h.toHostsLocked(msg{"t": "hostsettings", "settings": h.settings})
		h.mu.Unlock()
		h.Kick(kicked, "the host ended the room")
	}
}

// loadLocked switches the room to a new file. If it came from the playlist
// head it is removed from the queue.
func (h *Hub) loadLocked(id, by string, autoplay bool) {
	it, ok := h.media.Item(id)
	if !ok {
		return
	}
	if len(h.queue) > 0 && h.queue[0] == id {
		h.queue = h.queue[1:]
	}
	dur := 0.0
	if st, ok := h.statuses[id]; ok {
		dur = st.Duration
	}
	s := h.room.Load(h.now(), id, dur, by)
	h.autoplay = autoplay
	h.prefs = defaultPrefs() // a new film starts on its own default tracks
	h.broadcastLocked(msg{"t": "prefs", "prefs": h.prefs})
	h.broadcastStateLocked(s)
	if st, ok := h.statuses[id]; ok {
		h.broadcastLocked(msg{"t": "media", "media": st})
	} else {
		h.broadcastLocked(msg{"t": "media", "media": media.Status{ID: id, Name: it.Name, State: media.StateProbing}})
	}
	h.broadcastQueueLocked()
	h.systemChatLocked("Now playing: " + it.Name)
	go func() {
		if err := h.media.Prepare(id); err != nil {
			log.Printf("hub: prepare %s: %v", id, err)
		}
	}()
}

func (h *Hub) onChat(c *client, text string) {
	text = cleanChat(text)
	if text == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.chatOff[c.chatKey()] {
		c.writeLocked(msg{"t": "err", "text": "the host has turned off chat for you"})
		return
	}
	if !c.chatTB.take() {
		c.writeLocked(msg{"t": "err", "text": "slow down a little"})
		return
	}
	h.chatSeq++
	m := ChatMsg{ID: h.chatSeq, SID: c.sid, Name: c.name, Text: text, TS: h.now()}
	h.appendChatLocked(m)
}

func (h *Hub) systemChatLocked(text string) {
	h.chatSeq++
	h.appendChatLocked(ChatMsg{ID: h.chatSeq, Name: "FairPlay", Text: text, TS: h.now(), Sys: true})
}

func (h *Hub) appendChatLocked(m ChatMsg) {
	h.chat = append(h.chat, m)
	if len(h.chat) > chatHistory {
		h.chat = h.chat[len(h.chat)-chatHistory:]
	}
	b, err := json.Marshal(msg{"t": "chat", "m": m})
	if err != nil {
		return
	}
	for c := range h.clients {
		if !h.chatOff[c.chatKey()] {
			c.sendLocked(b)
		}
	}
}

func (h *Hub) onReact(c *client, e string) {
	if !emoji.Valid(e) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.settings.Reactions || !c.reactTB.take() {
		return
	}
	h.broadcastLocked(msg{"t": "react", "e": e, "sid": c.sid, "name": c.name})
}

// ---- outgoing ----

func (h *Hub) helloLocked(c *client) msg {
	st := h.room.State()
	var cur any
	if s, ok := h.statuses[st.MediaID]; ok {
		cur = s
	}
	chat := []ChatMsg{}
	if !h.chatOff[c.chatKey()] {
		chat = append(chat, h.chat...)
	}
	return msg{
		"t": "hello", "you": h.youLocked(c), "state": st, "now": h.now(),
		"settings": h.publicSettingsLocked(), "chat": chat, "media": cur,
		"queue": h.queueItemsLocked(), "peers": h.peersLocked(), "prefs": h.prefs,
	}
}

func (h *Hub) youLocked(c *client) msg {
	return msg{"id": c.sid, "name": c.name, "role": string(c.role), "control": h.canControl(c), "chat": !h.chatOff[c.chatKey()]}
}

func (h *Hub) publicSettingsLocked() msg {
	return msg{"roomName": h.settings.RoomName, "reactions": h.settings.Reactions, "waitForAll": h.settings.WaitForAll, "interMinutes": h.settings.InterMinutes}
}

func (h *Hub) peersLocked() []Peer {
	seen := map[string]bool{}
	var out []Peer
	for c := range h.clients {
		if seen[c.sid] {
			continue
		}
		seen[c.sid] = true
		out = append(out, Peer{
			ID: c.sid, Name: c.name, Role: string(c.role), CoHost: h.cohost[c.sid], ChatOff: h.chatOff[c.chatKey()], Mode: c.mode,
			Buffering: c.buffering, RTT: c.rtt, Jitter: c.jitter, Drift: c.drift, Pos: c.pos, Quality: c.q, Device: c.device,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Role != out[j].Role {
			return out[i].Role == string(auth.RoleHost)
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func (h *Hub) broadcastPeersLocked() {
	h.broadcastLocked(msg{"t": "peers", "peers": h.peersLocked(), "lead": h.leadLocked()})
}

func (h *Hub) broadcastStateLocked(s room.State) {
	h.broadcastLocked(msg{"t": "state", "state": s, "now": h.now()})
}

func (h *Hub) queueItemsLocked() []QueueItem {
	out := []QueueItem{}
	for _, id := range h.queue {
		name := id
		if it, ok := h.media.Item(id); ok {
			name = it.Name
		}
		out = append(out, QueueItem{ID: id, Name: name})
	}
	return out
}

func (h *Hub) broadcastQueueLocked() {
	h.broadcastLocked(msg{"t": "queue", "queue": h.queueItemsLocked()})
}

func (h *Hub) lobbyMsgLocked() msg {
	type pending struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Label        string `json:"label"`
		Device       string `json:"device"`
		IP           string `json:"ip"`
		SecondDevice bool   `json:"secondDevice"`
		Since        int64  `json:"since"`
	}
	list := []pending{}
	for _, s := range h.auth.Pending() {
		list = append(list, pending{s.ID, s.Name, s.Label, s.Device, s.IP, s.SecondDevice, s.CreatedAt.UnixMilli()})
	}
	return msg{"t": "lobby", "pending": list, "invites": h.auth.Invites(), "settings": h.settings}
}

func (h *Hub) broadcastLobbyLocked() {
	h.toHostsLocked(h.lobbyMsgLocked())
	h.peersDirty = true
}

func (h *Hub) broadcastLocked(m msg) {
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	for c := range h.clients {
		c.sendLocked(b)
	}
}

func (h *Hub) toHostsLocked(m msg) {
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	for c := range h.clients {
		if c.role == auth.RoleHost {
			c.sendLocked(b)
		}
	}
}

// ---- client ----

type client struct {
	h      *Hub
	conn   *websocket.Conn
	send   chan []byte
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once

	sid, invite, name, device string
	role                      auth.Role

	// guarded by Hub.mu
	mode      string
	buffering bool
	bufSince  time.Time
	rtt       int
	jitter    int
	drift     int
	pos       float64
	q         string
	chatTB    *bucket
	reactTB   *bucket
	dead      bool
}

// chatKey identifies the person for chat visibility: their invite if they
// have one, so hiding chat survives reconnects and rejoins.
func (c *client) chatKey() string {
	if c.invite != "" {
		return "i:" + c.invite
	}
	return "s:" + c.sid
}

func (c *client) reader() {
	for {
		ctx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
		typ, data, err := c.conn.Read(ctx)
		cancel()
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var m inMsg
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		c.h.handle(c, m)
	}
}

func (c *client) writer() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case b, ok := <-c.send:
			if !ok {
				return
			}
			ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
			err := c.conn.Write(ctx, websocket.MessageText, b)
			cancel()
			if err != nil {
				c.cancel()
				c.conn.CloseNow()
				return
			}
		}
	}
}

// write is for code paths that do not hold Hub.mu.
func (c *client) write(m msg) {
	c.h.mu.Lock()
	c.writeLocked(m)
	c.h.mu.Unlock()
}

func (c *client) writeLocked(m msg) {
	b, err := json.Marshal(m)
	if err == nil {
		c.sendLocked(b)
	}
}

func (c *client) sendLocked(b []byte) {
	if c.dead {
		return
	}
	select {
	case c.send <- b:
	default:
		// A client that cannot keep up would block everyone; drop it and let
		// it reconnect.
		c.close("too slow")
	}
}

func (c *client) close(reason string) {
	c.once.Do(func() {
		c.dead = true
		go func() {
			// Give the writer a moment to flush a final "kicked" message.
			time.Sleep(200 * time.Millisecond)
			c.conn.Close(websocket.StatusPolicyViolation, cut(reason, 100))
			c.cancel()
		}()
	})
}

// ---- helpers ----

type bucket struct {
	tokens, max, rate float64
	last              time.Time
}

func newBucket(burst, perSec float64) *bucket {
	return &bucket{tokens: burst, max: burst, rate: perSec, last: time.Now()}
}

func (b *bucket) take() bool {
	now := time.Now()
	b.tokens += now.Sub(b.last).Seconds() * b.rate
	if b.tokens > b.max {
		b.tokens = b.max
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func cleanChat(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n >= maxChatRunes {
			break
		}
		if r == '\n' || r == '\t' {
			r = ' '
		}
		if !unicode.IsPrint(r) && r != ' ' && r != '‍' && r != '️' {
			continue
		}
		b.WriteRune(r)
		n++
	}
	out := strings.TrimSpace(b.String())
	if !utf8.ValidString(out) {
		return ""
	}
	return out
}

func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
