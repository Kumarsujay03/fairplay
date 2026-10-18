package server

// The shield protects the public URL from bots and spam without bothering
// real guests:
//
//   - Proof of work: /api/peek, /api/join and /api/host/login need a solved
//     challenge. The join page solves it in the background (~0.2 s on a phone)
//     while the guest types a name. A bot pays that CPU cost on every attempt,
//     and the cost doubles per recent strike from its network.
//   - Strikes and bans: wrong tokens, wrong host keys, bad proofs and
//     unauthenticated socket attempts count as strikes. Five strikes in ten
//     minutes ban the IP for fifteen minutes.
//   - Rate limits per IP and globally on the join endpoints.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"math/bits"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	powBaseBits  = 16
	powMaxBits   = 22
	strikeLimit  = 5
	strikeWindow = 10 * time.Minute
	banFor       = 15 * time.Minute
)

type ipState struct {
	strikes  []time.Time
	banUntil time.Time
	bucket   float64
	last     time.Time
}

type challenge struct {
	salt    string
	bits    int
	expires time.Time
}

type Shield struct {
	mu         sync.Mutex
	ips        map[string]*ipState
	challenges map[string]challenge
	global     float64
	globalLast time.Time
	now        func() time.Time
}

func NewShield() *Shield {
	return &Shield{ips: map[string]*ipState{}, challenges: map[string]challenge{}, now: time.Now, global: 30, globalLast: time.Now()}
}

func (s *Shield) stateLocked(ip string) *ipState {
	st, ok := s.ips[ip]
	if !ok {
		st = &ipState{bucket: 10, last: s.now()}
		s.ips[ip] = st
	}
	return st
}

// Banned reports whether ip is currently banned.
func (s *Shield) Banned(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.ips[ip]
	return ok && s.now().Before(st.banUntil)
}

// Strike records a failed or abusive request.
func (s *Shield) Strike(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stateLocked(ip)
	now := s.now()
	st.strikes = append(pruneOld(st.strikes, now), now)
	if len(st.strikes) >= strikeLimit {
		st.banUntil = now.Add(banFor)
		st.strikes = nil
	}
}

// Allow is a per-IP token bucket (burst 10, 1 every 3 s) plus a global
// bucket (burst 30, 1 per second) for the expensive endpoints.
func (s *Shield) Allow(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	st := s.stateLocked(ip)
	if now.Before(st.banUntil) {
		return false
	}
	st.bucket = minf(10, st.bucket+now.Sub(st.last).Seconds()/3)
	st.last = now
	s.global = minf(30, s.global+now.Sub(s.globalLast).Seconds())
	s.globalLast = now
	if st.bucket < 1 || s.global < 1 {
		return false
	}
	st.bucket--
	s.global--
	return true
}

// Challenge issues a proof-of-work puzzle sized to this IP's recent strikes.
func (s *Shield) Challenge(ip string) (id, salt string, difficulty int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, c := range s.challenges {
		if now.After(c.expires) {
			delete(s.challenges, k)
		}
	}
	st := s.stateLocked(ip)
	st.strikes = pruneOld(st.strikes, now)
	difficulty = powBaseBits + 2*len(st.strikes)
	if difficulty > powMaxBits {
		difficulty = powMaxBits
	}
	if len(s.challenges) > 10000 {
		difficulty = powMaxBits // under a flood, everyone pays more
	}
	id, salt = rnd(12), rnd(16)
	s.challenges[id] = challenge{salt: salt, bits: difficulty, expires: now.Add(2 * time.Minute)}
	return id, salt, difficulty
}

// Verify consumes a challenge. sha256(salt + ":" + nonce) must start with the
// required number of zero bits.
func (s *Shield) Verify(id, nonce string) bool {
	if len(nonce) == 0 || len(nonce) > 32 {
		return false
	}
	s.mu.Lock()
	c, ok := s.challenges[id]
	delete(s.challenges, id)
	s.mu.Unlock()
	if !ok || s.now().After(c.expires) {
		return false
	}
	return leadingZeroBits(sha256.Sum256([]byte(c.salt+":"+nonce))) >= c.bits
}

// Sweep forgets idle IPs.
func (s *Shield) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for ip, st := range s.ips {
		if now.After(st.banUntil) && len(pruneOld(st.strikes, now)) == 0 && now.Sub(st.last) > 30*time.Minute {
			delete(s.ips, ip)
		}
	}
}

func leadingZeroBits(h [32]byte) int {
	n := 0
	for _, b := range h {
		if b == 0 {
			n += 8
			continue
		}
		return n + bits.LeadingZeros8(b)
	}
	return n
}

func pruneOld(ts []time.Time, now time.Time) []time.Time {
	out := ts[:0]
	for _, t := range ts {
		if now.Sub(t) < strikeWindow {
			out = append(out, t)
		}
	}
	return out
}

func rnd(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// clientIP returns the real visitor address. Proxy headers are trusted only
// when the request came from loopback, which is where the tunnel connects.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		if v := r.Header.Get("CF-Connecting-IP"); v != "" && net.ParseIP(strings.TrimSpace(v)) != nil {
			return strings.TrimSpace(v)
		}
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			first := strings.TrimSpace(strings.Split(v, ",")[0])
			if net.ParseIP(first) != nil {
				return first
			}
		}
	}
	return host
}

// isHTTPS reports whether the visitor's connection is TLS (directly or at the
// tunnel edge).
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	}
	return false
}
