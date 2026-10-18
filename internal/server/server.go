// Package server wires HTTP routes, security headers, cookies and the bot
// shield around the auth store, the room hub and the media manager.
package server

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Kumarsujay03/fairplay/internal/auth"
	"github.com/Kumarsujay03/fairplay/internal/hub"
	"github.com/Kumarsujay03/fairplay/internal/media"
)

const (
	sessionCookie = "fp_s"
	deviceCookie  = "fp_d"
	maxPending    = 10
)

type Server struct {
	Auth   *auth.Store
	Hub    *hub.Hub
	Media  *media.Manager
	Shield *Shield
	Web    fs.FS // built frontend (index.html + assets/)
	mux    *http.ServeMux

	Version     string
	UpdateInfo  func() any        // current update status for the host console
	OnMediaRoot func(path string) // called after the host picks a new folder
	Firewall    func() any        // firewall that may block Wi-Fi guests, for the console
}

func New(a *auth.Store, h *hub.Hub, m *media.Manager, web fs.FS) *Server {
	s := &Server{Auth: a, Hub: h, Media: m, Shield: NewShield(), Web: web, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.secure(s.mux) }

func (s *Server) routes() {
	// Pages: only these paths return the app shell; everything else is a 404.
	for _, p := range []string{"GET /{$}", "GET /room", "GET /host", "GET /j/{id}"} {
		s.mux.HandleFunc(p, s.page)
	}
	s.mux.HandleFunc("GET /assets/", s.assets)
	s.mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "User-agent: *\nDisallow: /\n")
	})
	s.mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })

	s.mux.HandleFunc("GET /api/challenge", s.challenge)
	s.mux.HandleFunc("POST /api/peek", s.peek)
	s.mux.HandleFunc("POST /api/join", s.join)
	s.mux.HandleFunc("POST /api/host/login", s.hostLogin)
	s.mux.HandleFunc("GET /api/me", s.me)
	s.mux.HandleFunc("POST /api/logout", s.logout)

	s.mux.HandleFunc("GET /api/host/invites", s.host(s.listInvites))
	s.mux.HandleFunc("POST /api/host/invites", s.host(s.createInvite))
	s.mux.HandleFunc("POST /api/host/invites/{id}/regen", s.host(s.regenInvite))
	s.mux.HandleFunc("POST /api/host/invites/{id}/revoke", s.host(s.revokeInvite))
	s.mux.HandleFunc("DELETE /api/host/invites/{id}", s.host(s.deleteInvite))
	s.mux.HandleFunc("GET /api/host/library", s.host(s.library))
	s.mux.HandleFunc("GET /api/host/info", s.host(s.info))
	s.mux.HandleFunc("GET /api/host/browse", s.host(s.browse))
	s.mux.HandleFunc("POST /api/host/media", s.host(s.setMedia))
	s.mux.HandleFunc("GET /api/host/storage", s.host(s.storage))
	s.mux.HandleFunc("POST /api/host/storage/clear", s.host(s.clearStorage))

	s.mux.HandleFunc("GET /ws", s.ws)
	s.mux.HandleFunc("GET /hls/{id}/{file...}", s.hls)
}

// ---- middleware ----

func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		host := r.Host
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; "+
			"media-src 'self' blob:; worker-src 'self' blob:; connect-src 'self' ws://"+host+" wss://"+host+"; "+
			"font-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Robots-Tag", "noindex, nofollow, noarchive")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if isHTTPS(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}

		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/ws" {
			if s.Shield.Banned(clientIP(r)) {
				jsonErr(w, http.StatusTooManyRequests, "too many failed attempts from your network, try again in 15 minutes")
				return
			}
		}
		// CSRF: state-changing requests must come from our own pages.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !s.sameOrigin(r) {
				jsonErr(w, http.StatusForbidden, "cross-site request refused")
				return
			}
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") && r.ContentLength != 0 {
				jsonErr(w, http.StatusUnsupportedMediaType, "json only")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin accepts requests whose Origin matches the host they were sent to,
// or the public tunnel URL. Missing Origin is refused for unsafe methods.
func (s *Server) sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return false
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	if t := s.Hub.Tunnel(); t.URL != "" {
		if tu, err := url.Parse(t.URL); err == nil && strings.EqualFold(tu.Host, u.Host) {
			return true
		}
	}
	return false
}

func (s *Server) session(r *http.Request) (auth.Session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return auth.Session{}, false
	}
	return s.Auth.Lookup(c.Value)
}

func (s *Server) host(fn func(http.ResponseWriter, *http.Request, auth.Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.session(r)
		if !ok || sess.Role != auth.RoleHost {
			jsonErr(w, http.StatusNotFound, "not found")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		fn(w, r, sess)
	}
}

func (s *Server) setCookie(w http.ResponseWriter, r *http.Request, name, value string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", HttpOnly: true, Secure: isHTTPS(r),
		SameSite: http.SameSiteStrictMode, MaxAge: int(maxAge.Seconds()),
	})
}

// ---- pages and assets ----

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(s.Web, "index.html")
	if err != nil {
		http.Error(w, "frontend not built: run `make web`", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}

func (s *Server) assets(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if !strings.HasPrefix(p, "assets/") || strings.Contains(p, "..") {
		http.NotFound(w, r)
		return
	}
	if _, err := fs.Stat(s.Web, p); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFileFS(w, r, s.Web, p)
}

// ---- public API ----

type powProof struct {
	ID    string `json:"id"`
	Nonce string `json:"nonce"`
}

func (s *Server) challenge(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.Shield.Allow(ip) {
		jsonErr(w, http.StatusTooManyRequests, "slow down and try again in a moment")
		return
	}
	id, salt, bits := s.Shield.Challenge(ip)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"id": id, "salt": salt, "bits": bits})
}

func (s *Server) checkProof(w http.ResponseWriter, r *http.Request, p powProof) bool {
	if !s.Shield.Verify(p.ID, p.Nonce) {
		s.Shield.Strike(clientIP(r))
		jsonErr(w, http.StatusForbidden, "browser check failed, reload the page")
		return false
	}
	return true
}

func (s *Server) peek(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID    string   `json:"id"`
		Token string   `json:"token"`
		Pow   powProof `json:"pow"`
	}
	if !readJSON(w, r, &body) || !s.checkProof(w, r, body.Pow) {
		return
	}
	label, err := s.Auth.Peek(body.ID, body.Token)
	if err != nil {
		s.Shield.Strike(clientIP(r))
		jsonErr(w, http.StatusForbidden, err.Error())
		return
	}
	set := s.Hub.Settings()
	writeJSON(w, map[string]any{"label": label, "roomName": set.RoomName, "online": s.Hub.OnlineCount(), "locked": set.Locked})
}

func (s *Server) join(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.Shield.Allow(ip) {
		jsonErr(w, http.StatusTooManyRequests, "slow down and try again in a moment")
		return
	}
	var body struct {
		ID    string   `json:"id"`
		Token string   `json:"token"`
		Name  string   `json:"name"`
		Pow   powProof `json:"pow"`
	}
	if !readJSON(w, r, &body) || !s.checkProof(w, r, body.Pow) {
		return
	}
	if len(s.Auth.Pending()) >= maxPending {
		jsonErr(w, http.StatusServiceUnavailable, "the host has many join requests waiting, try again in a minute")
		return
	}
	dev := ""
	if c, err := r.Cookie(deviceCookie); err == nil {
		dev = c.Value
	}
	set := s.Hub.Settings()
	res, err := s.Auth.Join(auth.JoinRequest{
		InviteID: body.ID, Token: body.Token, Name: body.Name, DeviceKey: dev, IP: ip,
		UserAgent: r.UserAgent(), AutoAdmit: set.AutoAdmit, Locked: set.Locked, MaxGuests: set.MaxGuests,
	})
	if err != nil {
		code := http.StatusForbidden
		if errors.Is(err, auth.ErrInvalid) {
			s.Shield.Strike(ip)
		} else {
			code = http.StatusConflict
		}
		jsonErr(w, code, err.Error())
		return
	}
	s.Hub.Kick(res.Replaced, "you joined from another tab or device")
	s.setCookie(w, r, deviceCookie, res.DeviceKey, 400*24*time.Hour)
	s.setCookie(w, r, sessionCookie, res.Cookie, 7*24*time.Hour)
	writeJSON(w, map[string]any{"status": res.Session.Status, "name": res.Session.Name, "secondDevice": res.Session.SecondDevice})
}

func (s *Server) hostLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.Shield.Allow(ip) {
		jsonErr(w, http.StatusTooManyRequests, "slow down and try again in a moment")
		return
	}
	var body struct {
		Key string   `json:"key"`
		Pow powProof `json:"pow"`
	}
	if !readJSON(w, r, &body) || !s.checkProof(w, r, body.Pow) {
		return
	}
	_, cookie, err := s.Auth.HostLogin(body.Key, ip, r.UserAgent())
	if err != nil {
		s.Shield.Strike(ip)
		jsonErr(w, http.StatusForbidden, err.Error())
		return
	}
	s.setCookie(w, r, sessionCookie, cookie, 12*time.Hour)
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	sess, ok := s.session(r)
	if !ok {
		writeJSON(w, map[string]any{"status": "none"})
		return
	}
	writeJSON(w, map[string]any{
		"status": sess.Status, "role": sess.Role, "name": sess.Name, "id": sess.ID,
		"roomName": s.Hub.Settings().RoomName,
	})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if sess, ok := s.session(r); ok && sess.Role == auth.RoleGuest {
		_ = s.Auth.Kick(sess.ID)
		s.Hub.Kick([]string{sess.ID}, "you left the room")
	}
	s.setCookie(w, r, sessionCookie, "", -time.Second)
	writeJSON(w, map[string]any{"ok": true})
}

// ---- host API ----

func (s *Server) listInvites(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	writeJSON(w, s.Auth.Invites())
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	var body struct {
		Label string `json:"label"`
		Hours int    `json:"hours"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Hours <= 0 || body.Hours > 24*14 {
		body.Hours = 24
	}
	inv, token, err := s.Auth.CreateInvite(body.Label, time.Duration(body.Hours)*time.Hour)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"invite": inv, "token": token})
}

func (s *Server) regenInvite(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	token, kicked, err := s.Auth.RegenerateInvite(r.PathValue("id"))
	if err != nil {
		jsonErr(w, http.StatusNotFound, err.Error())
		return
	}
	s.Hub.Kick(kicked, "your invite link was replaced by the host")
	writeJSON(w, map[string]any{"token": token})
}

func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	kicked, err := s.Auth.RevokeInvite(r.PathValue("id"))
	if err != nil {
		jsonErr(w, http.StatusNotFound, err.Error())
		return
	}
	s.Hub.Kick(kicked, "the host revoked your invite")
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) deleteInvite(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	kicked, err := s.Auth.DeleteInvite(r.PathValue("id"))
	if err != nil {
		jsonErr(w, http.StatusNotFound, err.Error())
		return
	}
	s.Hub.Kick(kicked, "the host revoked your invite")
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) library(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	items, sts, err := s.Media.List()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"items": items, "status": sts, "root": s.Media.Root()})
}

func (s *Server) info(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	var upd any
	if s.UpdateInfo != nil {
		upd = s.UpdateInfo()
	}
	writeJSON(w, map[string]any{
		"tunnel": s.Hub.Tunnel(), "encoder": s.Media.EncoderName(), "settings": s.Hub.Settings(),
		"version": s.Version, "update": upd, "root": s.Media.Root(), "firewall": fwInfo(s.Firewall),
	})
}

func fwInfo(f func() any) any {
	if f == nil {
		return nil
	}
	return f()
}

// expandHome turns "~/Movies" into an absolute path.
func expandHome(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimLeft(p[1:], `/\`))
		}
	}
	return p
}

func (s *Server) browse(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	dir := expandHome(r.URL.Query().Get("path"))
	if dir == "" {
		dir = s.Media.Root()
	}
	l, err := media.Browse(dir)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "cannot open that folder")
		return
	}
	home, _ := os.UserHomeDir()
	writeJSON(w, map[string]any{"listing": l, "home": home})
}

func (s *Server) storage(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	list, total, dir := s.Media.Usage(s.Hub.CurrentMedia())
	writeJSON(w, map[string]any{"path": dir, "bytes": total, "items": list, "max": s.Media.MaxCache})
}

func (s *Server) clearStorage(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	freed := s.Media.ClearCache(s.Hub.CurrentMedia())
	list, total, dir := s.Media.Usage(s.Hub.CurrentMedia())
	writeJSON(w, map[string]any{"freed": freed, "path": dir, "bytes": total, "items": list, "max": s.Media.MaxCache})
}

func (s *Server) setMedia(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	var body struct {
		Path string `json:"path"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	dir := expandHome(body.Path)
	if err := s.Media.SetRoot(dir); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.Hub.ClearQueue()
	if s.OnMediaRoot != nil {
		s.OnMediaRoot(s.Media.Root())
	}
	s.library(w, r, auth.Session{})
}

// ---- realtime and media ----

func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(r)
	if !ok || sess.Status != auth.StatusAdmitted {
		s.Shield.Strike(clientIP(r))
		jsonErr(w, http.StatusUnauthorized, "not admitted")
		return
	}
	if !s.sameOrigin(r) {
		jsonErr(w, http.StatusForbidden, "bad origin")
		return
	}
	s.Hub.Serve(w, r, sess)
}

func (s *Server) hls(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.session(r)
	if !ok || sess.Status != auth.StatusAdmitted {
		http.NotFound(w, r)
		return
	}
	id := r.PathValue("id")
	if sess.Role != auth.RoleHost && id != s.Hub.CurrentMedia() {
		http.NotFound(w, r) // guests only ever reach the file the room is showing
		return
	}
	s.Media.ServeHLS(w, r, id, r.PathValue("file"))
}

// ---- helpers ----

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad request")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
