package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"v0.10.0", "v0.9.9", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.2.0", false},
		{"v0.2.0", "0.1.0-dev", false}, // dev builds never auto-update
		{"garbage", "v0.1.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

type fakeRelease struct {
	version string
	files   map[string][]byte
}

func newServer(t *testing.T, rel *fakeRelease) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases/latest" {
			http.Redirect(w, r, "/releases/tag/"+rel.version, http.StatusFound)
			return
		}
		prefix := "/releases/download/" + rel.version + "/"
		if b, ok := rel.files[strings.TrimPrefix(r.URL.Path, prefix)]; ok && strings.HasPrefix(r.URL.Path, prefix) {
			w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
}

func signedRelease(t *testing.T, priv ed25519.PrivateKey, version, goos, goarch string, bin []byte) *fakeRelease {
	asset := AssetName(version, goos, goarch)
	sum := sha256.Sum256(bin)
	sums := []byte(fmt.Sprintf("%x  %s\n%x  other-file\n", sum, asset, sha256.Sum256(nil)))
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sums))
	return &fakeRelease{version: version, files: map[string][]byte{
		asset: bin, "SHA256SUMS": sums, "SHA256SUMS.sig": []byte(sig + "\n")}}
}

func setup(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, string) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	exe := filepath.Join(t.TempDir(), "fairplay")
	os.WriteFile(exe, []byte("old binary"), 0o755)
	return pub, priv, exe
}

func updaterFor(srv *httptest.Server, pub ed25519.PublicKey) *Updater {
	return &Updater{Current: "v0.1.0", BaseURL: srv.URL, PubKey: pub, Client: srv.Client(), GOOS: "linux", GOARCH: "amd64"}
}

func TestInstallVerifiedRelease(t *testing.T) {
	pub, priv, exe := setup(t)
	srv := newServer(t, signedRelease(t, priv, "v0.2.0", "linux", "amd64", []byte("new binary")))
	defer srv.Close()
	u := updaterFor(srv, pub)

	latest, err := u.Latest(context.Background())
	if err != nil || latest != "v0.2.0" {
		t.Fatalf("Latest = %q, %v", latest, err)
	}
	if err := u.Install(context.Background(), latest, exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new binary" {
		t.Fatalf("exe not replaced: %q", b)
	}
	if fi, _ := os.Stat(exe); runtime.GOOS != "windows" && fi.Mode().Perm()&0o100 == 0 {
		t.Fatal("new binary is not executable")
	}
}

func TestRejectsWrongKey(t *testing.T) {
	pub, _, exe := setup(t)
	_, attacker, _ := ed25519.GenerateKey(rand.Reader)
	srv := newServer(t, signedRelease(t, attacker, "v0.2.0", "linux", "amd64", []byte("evil")))
	defer srv.Close()
	err := updaterFor(srv, pub).Install(context.Background(), "v0.2.0", exe)
	if err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("expected signature error, got %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "old binary" {
		t.Fatal("binary must stay untouched")
	}
}

func TestRejectsTamperedBinary(t *testing.T) {
	pub, priv, exe := setup(t)
	rel := signedRelease(t, priv, "v0.2.0", "linux", "amd64", []byte("good"))
	rel.files[AssetName("v0.2.0", "linux", "amd64")] = []byte("swapped by attacker")
	srv := newServer(t, rel)
	defer srv.Close()
	err := updaterFor(srv, pub).Install(context.Background(), "v0.2.0", exe)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum error, got %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "old binary" {
		t.Fatal("binary must stay untouched")
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".fairplay-update-*"))
	if len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestRejectsUnsignedRelease(t *testing.T) {
	pub, priv, exe := setup(t)
	rel := signedRelease(t, priv, "v0.2.0", "linux", "amd64", []byte("x"))
	delete(rel.files, "SHA256SUMS.sig")
	srv := newServer(t, rel)
	defer srv.Close()
	if err := updaterFor(srv, pub).Install(context.Background(), "v0.2.0", exe); err == nil {
		t.Fatal("unsigned release must be refused")
	}
}

func TestEmbeddedKeyIsValid(t *testing.T) {
	if len(New("v0.1.0").PubKey) != ed25519.PublicKeySize {
		t.Fatal("embedded public key is malformed")
	}
}
