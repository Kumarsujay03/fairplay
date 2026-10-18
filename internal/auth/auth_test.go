package auth

import (
	"path/filepath"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	s, err := NewStore(filepath.Join(t.TempDir(), "invites.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHostLogin(t *testing.T) {
	s := newStore(t)
	if _, _, err := s.HostLogin("wrong", "", ""); err != ErrBadKey {
		t.Fatalf("want ErrBadKey, got %v", err)
	}
	sess, cookie, err := s.HostLogin(s.HostKey(), "1.2.3.4", "")
	if err != nil || sess.Role != RoleHost {
		t.Fatal(err)
	}
	if got, ok := s.Lookup(cookie); !ok || got.ID != sess.ID {
		t.Fatal("cookie lookup failed")
	}
}

func TestFirstJoinGoesToLobbyThenRejoinSkipsIt(t *testing.T) {
	s := newStore(t)
	inv, token, _ := s.CreateInvite("Priya", 0)
	res, err := s.Join(JoinRequest{InviteID: inv.ID, Token: token, Name: "Priya"})
	if err != nil || res.Session.Status != StatusPending {
		t.Fatalf("want pending, got %+v %v", res.Session, err)
	}
	if len(s.Pending()) != 1 {
		t.Fatal("lobby should have one entry")
	}
	if _, err := s.Admit(res.Session.ID, 0); err != nil {
		t.Fatal(err)
	}
	again, err := s.Join(JoinRequest{InviteID: inv.ID, Token: token, DeviceKey: res.DeviceKey})
	if err != nil || again.Session.Status != StatusAdmitted {
		t.Fatalf("same device should skip lobby, got %+v %v", again.Session, err)
	}
	if again.Session.Name != "Priya" {
		t.Fatalf("empty name falls back to label, got %q", again.Session.Name)
	}
	if len(again.Replaced) != 1 || again.Replaced[0] != res.Session.ID {
		t.Fatal("old session should be replaced, one seat per invite")
	}
}

func TestForwardedLinkNeedsHostAndIsFlagged(t *testing.T) {
	s := newStore(t)
	inv, token, _ := s.CreateInvite("Priya", 0)
	first, _ := s.Join(JoinRequest{InviteID: inv.ID, Token: token, AutoAdmit: true})
	if first.Session.Status != StatusAdmitted {
		t.Fatal("auto admit should admit first device")
	}
	stranger, err := s.Join(JoinRequest{InviteID: inv.ID, Token: token, AutoAdmit: true, DeviceKey: "some-other-device-key-xxxxxx"})
	if err != nil {
		t.Fatal(err)
	}
	if stranger.Session.Status != StatusPending || !stranger.Session.SecondDevice {
		t.Fatalf("second device must wait in lobby flagged, got %+v", stranger.Session)
	}
	if _, ok := s.Lookup(first.Cookie); !ok {
		t.Fatal("pending second device must not kick the real guest")
	}
	if err := s.Deny(stranger.Session.ID); err != nil {
		t.Fatal(err)
	}
	if sess, _ := s.Lookup(stranger.Cookie); sess.Status != StatusDenied {
		t.Fatal("denied session should read as denied")
	}
}

func TestAdmittingSecondDeviceMovesTheSeat(t *testing.T) {
	s := newStore(t)
	inv, token, _ := s.CreateInvite("Priya", 0)
	first, _ := s.Join(JoinRequest{InviteID: inv.ID, Token: token, AutoAdmit: true})
	laptop, _ := s.Join(JoinRequest{InviteID: inv.ID, Token: token, DeviceKey: "laptop-device-key-xxxxxxxxxx"})
	kicked, err := s.Admit(laptop.Session.ID, 0)
	if err != nil || len(kicked) != 1 || kicked[0] != first.Session.ID {
		t.Fatalf("admit should replace old seat, kicked=%v err=%v", kicked, err)
	}
	if _, ok := s.Lookup(first.Cookie); ok {
		t.Fatal("old device session must be gone")
	}
	back, _ := s.Join(JoinRequest{InviteID: inv.ID, Token: token, DeviceKey: first.DeviceKey})
	if back.Session.Status != StatusPending || !back.Session.SecondDevice {
		t.Fatal("old device is now the second device")
	}
}

func TestDenyOnFirstOpenUnbindsLink(t *testing.T) {
	s := newStore(t)
	inv, token, _ := s.CreateInvite("Priya", 0)
	stranger, _ := s.Join(JoinRequest{InviteID: inv.ID, Token: token})
	_ = s.Deny(stranger.Session.ID)
	real, _ := s.Join(JoinRequest{InviteID: inv.ID, Token: token, DeviceKey: "real-priya-device-key-xxxxxx"})
	if real.Session.SecondDevice {
		t.Fatal("after denying the first opener the real guest is not a second device")
	}
}

func TestInvalidTokensAndRevocation(t *testing.T) {
	s := newStore(t)
	inv, token, _ := s.CreateInvite("Priya", 0)
	if _, err := s.Join(JoinRequest{InviteID: inv.ID, Token: "nope"}); err != ErrInvalid {
		t.Fatalf("wrong token: %v", err)
	}
	if _, err := s.Join(JoinRequest{InviteID: "missing", Token: token}); err != ErrInvalid {
		t.Fatalf("wrong id: %v", err)
	}
	res, _ := s.Join(JoinRequest{InviteID: inv.ID, Token: token, AutoAdmit: true})
	kicked, _ := s.RevokeInvite(inv.ID)
	if len(kicked) != 1 {
		t.Fatal("revoke should kick")
	}
	if _, ok := s.Lookup(res.Cookie); ok {
		t.Fatal("revoked session must not resolve")
	}
	if _, err := s.Join(JoinRequest{InviteID: inv.ID, Token: token}); err != ErrInvalid {
		t.Fatal("revoked link must fail")
	}
}

func TestExpiry(t *testing.T) {
	s := newStore(t)
	inv, token, _ := s.CreateInvite("Priya", time.Hour)
	s.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, err := s.Join(JoinRequest{InviteID: inv.ID, Token: token}); err != ErrInvalid {
		t.Fatalf("expired link must fail, got %v", err)
	}
}

func TestLockedAndFull(t *testing.T) {
	s := newStore(t)
	a, ta, _ := s.CreateInvite("A", 0)
	b, tb, _ := s.CreateInvite("B", 0)
	if _, err := s.Join(JoinRequest{InviteID: a.ID, Token: ta, Locked: true}); err != ErrLocked {
		t.Fatalf("locked: %v", err)
	}
	if _, err := s.Join(JoinRequest{InviteID: a.ID, Token: ta, AutoAdmit: true, MaxGuests: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Join(JoinRequest{InviteID: b.ID, Token: tb, AutoAdmit: true, MaxGuests: 1}); err != ErrFull {
		t.Fatalf("full: %v", err)
	}
}

func TestInvitesPersistAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invites.json")
	s1, _ := NewStore(path)
	inv, token, _ := s1.CreateInvite("Priya", 0)
	first, _ := s1.Join(JoinRequest{InviteID: inv.ID, Token: token, AutoAdmit: true})
	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if s2.HostKey() == s1.HostKey() {
		t.Fatal("host key must rotate on restart")
	}
	back, err := s2.Join(JoinRequest{InviteID: inv.ID, Token: token, DeviceKey: first.DeviceKey})
	if err != nil || back.Session.Status != StatusAdmitted {
		t.Fatalf("returning device after restart should skip lobby: %+v %v", back.Session, err)
	}
}

func TestCleanName(t *testing.T) {
	cases := map[string]string{
		"  Priya  ":                    "Priya",
		"a\u0000b":                     "ab",
		"<script>":                     "script",
		"two   spaces":                 "two spaces",
		"abcdefghijklmnopqrstuvwxyz12": "abcdefghijklmnopqrstuvwx",
	}
	for in, want := range cases {
		if got := CleanName(in); got != want {
			t.Errorf("CleanName(%q)=%q want %q", in, got, want)
		}
	}
}
