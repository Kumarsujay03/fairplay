// Package auth owns every secret in FairPlay: the host key, personal invites,
// device binding, the join lobby and browser sessions.
//
// Secrets are never stored in plain form. Invite tokens, session cookies and
// device keys are kept as SHA-256 hashes and compared in constant time.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

type Role string

const (
	RoleHost  Role = "host"
	RoleGuest Role = "guest"
)

type Status string

const (
	StatusPending  Status = "pending"
	StatusAdmitted Status = "admitted"
	StatusDenied   Status = "denied"
)

var (
	ErrInvalid  = errors.New("invite link is invalid or has expired")
	ErrLocked   = errors.New("the room is locked")
	ErrFull     = errors.New("the room is full")
	ErrBadKey   = errors.New("host key is wrong")
	ErrNotFound = errors.New("not found")
)

// Invite is one personal link for one person.
type Invite struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	TokenHash  string    `json:"tokenHash"`
	DeviceHash string    `json:"deviceHash,omitempty"`
	Admitted   bool      `json:"admitted"`
	Revoked    bool      `json:"revoked"`
	CreatedAt  time.Time `json:"createdAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	LastName   string    `json:"lastName,omitempty"`
}

// InviteView is what the host console sees. It never contains the token.
type InviteView struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	Bound     bool      `json:"bound"`
	Admitted  bool      `json:"admitted"`
	Revoked   bool      `json:"revoked"`
	Expired   bool      `json:"expired"`
	ExpiresAt time.Time `json:"expiresAt"`
	Name      string    `json:"name,omitempty"`
}

// Session is one browser that holds a FairPlay cookie.
type Session struct {
	ID           string    `json:"id"`
	Role         Role      `json:"role"`
	InviteID     string    `json:"inviteId,omitempty"`
	Label        string    `json:"label,omitempty"`
	Name         string    `json:"name"`
	Status       Status    `json:"status"`
	SecondDevice bool      `json:"secondDevice,omitempty"`
	Device       string    `json:"device"`
	IP           string    `json:"ip"`
	CreatedAt    time.Time `json:"createdAt"`

	cookieHash string
	deviceHash string
}

// JoinRequest carries everything the lobby needs to decide on a guest.
type JoinRequest struct {
	InviteID  string
	Token     string
	Name      string
	DeviceKey string // value of the device cookie, may be empty
	IP        string
	UserAgent string
	AutoAdmit bool
	Locked    bool
	MaxGuests int
}

// JoinResult is returned to the HTTP layer to set cookies.
type JoinResult struct {
	Session   Session
	Cookie    string
	DeviceKey string
	Replaced  []string // session IDs that lost their seat to this join
}

type Store struct {
	mu       sync.Mutex
	path     string
	hostKey  string
	invites  map[string]*Invite
	sessions map[string]*Session // keyed by cookie hash
	byID     map[string]*Session
	now      func() time.Time
	OnChange func() // lobby or session list changed; called without the lock held
}

// NewStore loads invites from path (created if missing). A fresh host key is
// generated on every start so an old terminal screenshot is useless.
func NewStore(path string) (*Store, error) {
	s := &Store{
		path:     path,
		hostKey:  randToken(24),
		invites:  map[string]*Invite{},
		sessions: map[string]*Session{},
		byID:     map[string]*Session{},
		now:      time.Now,
	}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(b) > 0 {
		var list []*Invite
		if err := json.Unmarshal(b, &list); err != nil {
			return nil, err
		}
		for _, inv := range list {
			s.invites[inv.ID] = inv
		}
	}
	return s, nil
}

func (s *Store) HostKey() string { return s.hostKey }

// HostLogin exchanges the host key for a host session cookie.
func (s *Store) HostLogin(key, ip, ua string) (Session, string, error) {
	if subtle.ConstantTimeCompare([]byte(key), []byte(s.hostKey)) != 1 {
		return Session{}, "", ErrBadKey
	}
	s.mu.Lock()
	cookie := randToken(32)
	sess := &Session{
		ID: randID(), Role: RoleHost, Name: "Host", Status: StatusAdmitted,
		Device: DeviceLabel(ua), IP: ip, CreatedAt: s.now(), cookieHash: hash(cookie),
	}
	s.addLocked(sess)
	s.mu.Unlock()
	s.changed()
	return *sess, cookie, nil
}

// CreateInvite makes a new personal link. The returned token is shown once.
func (s *Store) CreateInvite(label string, ttl time.Duration) (InviteView, string, error) {
	label = CleanName(label)
	if label == "" {
		return InviteView{}, "", errors.New("give the invite a name")
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	token := randToken(16)
	s.mu.Lock()
	inv := &Invite{
		ID: randID(), Label: label, TokenHash: hash(token),
		CreatedAt: s.now(), ExpiresAt: s.now().Add(ttl),
	}
	s.invites[inv.ID] = inv
	err := s.saveLocked()
	v := s.viewLocked(inv)
	s.mu.Unlock()
	return v, token, err
}

// RegenerateInvite issues a new token and clears the device binding.
// Sessions using the old link are returned so the caller can kick them.
func (s *Store) RegenerateInvite(id string) (string, []string, error) {
	s.mu.Lock()
	inv, ok := s.invites[id]
	if !ok {
		s.mu.Unlock()
		return "", nil, ErrNotFound
	}
	token := randToken(16)
	inv.TokenHash = hash(token)
	inv.DeviceHash = ""
	inv.Admitted = false
	inv.Revoked = false
	inv.ExpiresAt = s.now().Add(24 * time.Hour)
	kicked := s.dropInviteSessionsLocked(id)
	err := s.saveLocked()
	s.mu.Unlock()
	s.changed()
	return token, kicked, err
}

// RevokeInvite kills the link and every session that came through it.
func (s *Store) RevokeInvite(id string) ([]string, error) {
	s.mu.Lock()
	inv, ok := s.invites[id]
	if !ok {
		s.mu.Unlock()
		return nil, ErrNotFound
	}
	inv.Revoked = true
	kicked := s.dropInviteSessionsLocked(id)
	err := s.saveLocked()
	s.mu.Unlock()
	s.changed()
	return kicked, err
}

// DeleteInvite removes the invite entirely.
func (s *Store) DeleteInvite(id string) ([]string, error) {
	s.mu.Lock()
	if _, ok := s.invites[id]; !ok {
		s.mu.Unlock()
		return nil, ErrNotFound
	}
	kicked := s.dropInviteSessionsLocked(id)
	delete(s.invites, id)
	err := s.saveLocked()
	s.mu.Unlock()
	s.changed()
	return kicked, err
}

// RevokeAll ends the room: every invite and guest session is cut.
func (s *Store) RevokeAll() []string {
	s.mu.Lock()
	var kicked []string
	for id, inv := range s.invites {
		inv.Revoked = true
		kicked = append(kicked, s.dropInviteSessionsLocked(id)...)
	}
	_ = s.saveLocked()
	s.mu.Unlock()
	s.changed()
	return kicked
}

func (s *Store) Invites() []InviteView {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]InviteView, 0, len(s.invites))
	for _, inv := range s.invites {
		out = append(out, s.viewLocked(inv))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ExpiresAt.Before(out[j].ExpiresAt) })
	return out
}

// Peek checks a token without consuming it, so the join page can greet the
// guest by name. It reveals nothing unless the token is right.
func (s *Store) Peek(id, token string) (label string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, err := s.checkLocked(id, token)
	if err != nil {
		return "", err
	}
	return inv.Label, nil
}

// Join runs the lobby rules. See docs/PITCH.md section 6.
func (s *Store) Join(req JoinRequest) (JoinResult, error) {
	s.mu.Lock()
	inv, err := s.checkLocked(req.InviteID, req.Token)
	if err != nil {
		s.mu.Unlock()
		return JoinResult{}, err
	}
	deviceKey := req.DeviceKey
	if len(deviceKey) < 20 {
		deviceKey = randToken(24)
	}
	devHash := hash(deviceKey)

	status := StatusPending
	second := false
	switch {
	case inv.DeviceHash == "":
		if req.Locked {
			s.mu.Unlock()
			return JoinResult{}, ErrLocked
		}
		inv.DeviceHash = devHash
		if req.AutoAdmit {
			status = StatusAdmitted
			inv.Admitted = true
		}
	case ctEqual(inv.DeviceHash, devHash):
		if inv.Admitted {
			status = StatusAdmitted // returning device, skip the lobby
		} else if req.Locked {
			s.mu.Unlock()
			return JoinResult{}, ErrLocked
		}
	default:
		if req.Locked {
			s.mu.Unlock()
			return JoinResult{}, ErrLocked
		}
		second = true // forwarded or second device: always needs the host
	}

	if status == StatusAdmitted && req.MaxGuests > 0 && s.admittedGuestsLocked(inv.ID) >= req.MaxGuests {
		s.mu.Unlock()
		return JoinResult{}, ErrFull
	}

	name := CleanName(req.Name)
	if name == "" {
		name = inv.Label
	}
	cookie := randToken(32)
	sess := &Session{
		ID: randID(), Role: RoleGuest, InviteID: inv.ID, Label: inv.Label, Name: name,
		Status: status, SecondDevice: second, Device: DeviceLabel(req.UserAgent), IP: req.IP,
		CreatedAt: s.now(), cookieHash: hash(cookie), deviceHash: devHash,
	}
	// Only one pending request per invite and device at a time.
	for _, old := range s.byID {
		if old.InviteID == inv.ID && old.Status == StatusPending && old.deviceHash == devHash {
			s.removeLocked(old)
		}
	}
	var replaced []string
	if status == StatusAdmitted {
		replaced = s.dropAdmittedForInviteLocked(inv.ID)
		inv.LastName = name
	}
	s.addLocked(sess)
	err = s.saveLocked()
	res := JoinResult{Session: *sess, Cookie: cookie, DeviceKey: deviceKey, Replaced: replaced}
	s.mu.Unlock()
	s.changed()
	return res, err
}

// Admit lets a pending guest in. Admitting a second-device request moves the
// invite to that device and returns the old session so it can be kicked.
func (s *Store) Admit(sessionID string, maxGuests int) ([]string, error) {
	s.mu.Lock()
	sess, ok := s.byID[sessionID]
	if !ok || sess.Role != RoleGuest || sess.Status != StatusPending {
		s.mu.Unlock()
		return nil, ErrNotFound
	}
	inv, ok := s.invites[sess.InviteID]
	if !ok || inv.Revoked {
		s.mu.Unlock()
		return nil, ErrNotFound
	}
	if maxGuests > 0 && s.admittedGuestsLocked(inv.ID) >= maxGuests {
		s.mu.Unlock()
		return nil, ErrFull
	}
	replaced := s.dropAdmittedForInviteLocked(inv.ID)
	inv.DeviceHash = sess.deviceHash
	inv.Admitted = true
	inv.LastName = sess.Name
	sess.Status = StatusAdmitted
	sess.SecondDevice = false
	err := s.saveLocked()
	s.mu.Unlock()
	s.changed()
	return replaced, err
}

// Deny rejects a pending guest. Their browser sees "denied" on next poll.
func (s *Store) Deny(sessionID string) error {
	s.mu.Lock()
	sess, ok := s.byID[sessionID]
	if !ok || sess.Status != StatusPending {
		s.mu.Unlock()
		return ErrNotFound
	}
	sess.Status = StatusDenied
	// A denied first-time opener must not keep the link bound to their device.
	if inv, ok := s.invites[sess.InviteID]; ok && !inv.Admitted && ctEqual(inv.DeviceHash, sess.deviceHash) {
		inv.DeviceHash = ""
		_ = s.saveLocked()
	}
	s.mu.Unlock()
	s.changed()
	return nil
}

// Kick removes a session. The invite stays valid unless revoked separately.
func (s *Store) Kick(sessionID string) error {
	s.mu.Lock()
	sess, ok := s.byID[sessionID]
	if !ok || sess.Role == RoleHost {
		s.mu.Unlock()
		return ErrNotFound
	}
	if inv, ok := s.invites[sess.InviteID]; ok {
		inv.Admitted = false // next time they go through the lobby again
		_ = s.saveLocked()
	}
	s.removeLocked(sess)
	s.mu.Unlock()
	s.changed()
	return nil
}

// Lookup resolves a cookie to a session snapshot.
func (s *Store) Lookup(cookie string) (Session, bool) {
	if cookie == "" {
		return Session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[hash(cookie)]
	if !ok {
		return Session{}, false
	}
	if sess.Role == RoleGuest {
		inv, ok := s.invites[sess.InviteID]
		if !ok || inv.Revoked {
			return Session{}, false
		}
	}
	return *sess, true
}

// Pending lists the lobby, oldest first.
func (s *Store) Pending() []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Session
	for _, sess := range s.byID {
		if sess.Status == StatusPending {
			out = append(out, *sess)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Rename changes a guest's display name (from the room UI).
func (s *Store) Rename(sessionID, name string) (string, error) {
	name = CleanName(name)
	if name == "" {
		return "", errors.New("name is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byID[sessionID]
	if !ok {
		return "", ErrNotFound
	}
	sess.Name = name
	return name, nil
}

// Sweep drops denied and stale pending sessions. Call it periodically.
func (s *Store) Sweep() {
	s.mu.Lock()
	cut := s.now().Add(-10 * time.Minute)
	n := 0
	for _, sess := range s.byID {
		if sess.Status != StatusAdmitted && sess.CreatedAt.Before(cut) {
			s.removeLocked(sess)
			n++
		}
	}
	s.mu.Unlock()
	if n > 0 {
		s.changed()
	}
}

// ---- internals ----

func (s *Store) checkLocked(id, token string) (*Invite, error) {
	inv, ok := s.invites[id]
	// Hash even on a miss so a wrong ID and a wrong token take the same time.
	got := hash(token)
	if !ok {
		ctEqual(got, hash("x"))
		return nil, ErrInvalid
	}
	if !ctEqual(got, inv.TokenHash) || inv.Revoked || s.now().After(inv.ExpiresAt) {
		return nil, ErrInvalid
	}
	return inv, nil
}

func (s *Store) admittedGuestsLocked(exceptInvite string) int {
	n := 0
	for _, sess := range s.byID {
		if sess.Role == RoleGuest && sess.Status == StatusAdmitted && sess.InviteID != exceptInvite {
			n++
		}
	}
	return n
}

func (s *Store) dropAdmittedForInviteLocked(inviteID string) []string {
	var ids []string
	for _, sess := range s.byID {
		if sess.InviteID == inviteID && sess.Status == StatusAdmitted {
			ids = append(ids, sess.ID)
			s.removeLocked(sess)
		}
	}
	return ids
}

func (s *Store) dropInviteSessionsLocked(inviteID string) []string {
	var ids []string
	for _, sess := range s.byID {
		if sess.InviteID == inviteID {
			ids = append(ids, sess.ID)
			s.removeLocked(sess)
		}
	}
	return ids
}

func (s *Store) addLocked(sess *Session) {
	s.sessions[sess.cookieHash] = sess
	s.byID[sess.ID] = sess
}

func (s *Store) removeLocked(sess *Session) {
	delete(s.sessions, sess.cookieHash)
	delete(s.byID, sess.ID)
}

func (s *Store) viewLocked(inv *Invite) InviteView {
	return InviteView{
		ID: inv.ID, Label: inv.Label, Bound: inv.DeviceHash != "", Admitted: inv.Admitted,
		Revoked: inv.Revoked, Expired: s.now().After(inv.ExpiresAt), ExpiresAt: inv.ExpiresAt,
		Name: inv.LastName,
	}
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	list := make([]*Invite, 0, len(s.invites))
	for _, inv := range s.invites {
		list = append(list, inv)
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) changed() {
	if s.OnChange != nil {
		s.OnChange()
	}
}

// CleanName keeps printable characters, collapses spaces and caps at 24 runes.
func CleanName(in string) string {
	var b strings.Builder
	space := false
	n := 0
	for _, r := range strings.TrimSpace(in) {
		if n >= 24 {
			break
		}
		if unicode.IsSpace(r) {
			if !space {
				b.WriteRune(' ')
				n++
			}
			space = true
			continue
		}
		if !unicode.IsPrint(r) || r == '<' || r == '>' {
			continue
		}
		space = false
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

// DeviceLabel turns a user agent into "Android · Chrome" for the lobby.
func DeviceLabel(ua string) string {
	os := "Unknown device"
	switch {
	case strings.Contains(ua, "iPhone"):
		os = "iPhone"
	case strings.Contains(ua, "iPad"):
		os = "iPad"
	case strings.Contains(ua, "Android"):
		os = "Android"
	case strings.Contains(ua, "Windows"):
		os = "Windows"
	case strings.Contains(ua, "Mac OS X"):
		os = "Mac"
	case strings.Contains(ua, "CrOS"):
		os = "ChromeOS"
	case strings.Contains(ua, "Linux"):
		os = "Linux"
	}
	br := ""
	switch {
	case strings.Contains(ua, "Edg/"):
		br = "Edge"
	case strings.Contains(ua, "OPR/"):
		br = "Opera"
	case strings.Contains(ua, "SamsungBrowser"):
		br = "Samsung Internet"
	case strings.Contains(ua, "Firefox/"):
		br = "Firefox"
	case strings.Contains(ua, "Chrome/"):
		br = "Chrome"
	case strings.Contains(ua, "Safari/"):
		br = "Safari"
	}
	if br == "" {
		return os
	}
	return os + " · " + br
}

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func ctEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func randToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func randID() string {
	b := make([]byte, 9)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
