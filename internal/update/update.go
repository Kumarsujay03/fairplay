// Package update implements FairPlay's self-updater.
//
// Releases are published on GitHub with a SHA256SUMS file and an Ed25519
// signature of it (SHA256SUMS.sig). The updater:
//
//  1. finds the latest release tag from the github.com/<repo>/releases/latest redirect
//     (no API token, no rate limit);
//  2. downloads SHA256SUMS and SHA256SUMS.sig and verifies the signature with the
//     public key compiled into this binary;
//  3. downloads the binary for this OS/architecture and checks its SHA-256;
//  4. atomically replaces the running executable (on Windows the old file is
//     renamed to .old and removed on next start).
//
// A tampered download, a release signed with another key, or a compromised
// download host cannot get code installed.
package update

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Repo is the GitHub repository releases come from.
const Repo = "Kumarsujay03/fairplay"

// PublicKey verifies release signatures (base64 Ed25519). Its private half is
// the FAIRPLAY_SIGNING_KEY secret used by the release workflow.
const PublicKey = "kE4wYjJ3JSVL1UpesyK6tSrlo1WgIESA6oxHPRhyys8="

const maxBinary = 256 << 20

type Updater struct {
	Current string // running version, e.g. "v0.2.0"
	BaseURL string // https://github.com/<repo>
	PubKey  ed25519.PublicKey
	Client  *http.Client
	GOOS    string
	GOARCH  string
}

// New returns an updater for the official repository. FAIRPLAY_UPDATE_URL
// overrides the base URL (used for testing against a local server).
func New(current string) *Updater {
	key, _ := base64.StdEncoding.DecodeString(PublicKey)
	base := "https://github.com/" + Repo
	if v := os.Getenv("FAIRPLAY_UPDATE_URL"); v != "" {
		base = strings.TrimRight(v, "/")
	}
	return &Updater{Current: current, BaseURL: base, PubKey: ed25519.PublicKey(key),
		Client: &http.Client{Timeout: 10 * time.Minute}, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
}

var semverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

// IsRelease reports whether v is a plain release version (not a dev build).
func IsRelease(v string) bool { return semverRe.MatchString(v) }

// Newer reports whether a is a newer release than b. Non-release versions are never newer.
func Newer(a, b string) bool {
	pa, pb := parse(a), parse(b)
	if pa == nil || pb == nil {
		return false
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parse(v string) []int {
	m := semverRe.FindStringSubmatch(v)
	if m == nil {
		return nil
	}
	out := make([]int, 3)
	for i := range out {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out
}

// AssetName is the release file for a version and platform.
func AssetName(version, goos, goarch string) string {
	name := fmt.Sprintf("fairplay-%s-%s-%s", version, goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// Latest returns the newest release tag.
func (u *Updater) Latest(ctx context.Context) (string, error) {
	c := *u.Client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, _ := http.NewRequestWithContext(ctx, "GET", u.BaseURL+"/releases/latest", nil)
	resp, err := c.Do(req)
	if err != nil {
		return "", fmt.Errorf("check for updates: %w", err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	i := strings.LastIndex(loc, "/releases/tag/")
	if resp.StatusCode/100 != 3 || i < 0 {
		return "", fmt.Errorf("check for updates: unexpected reply %s", resp.Status)
	}
	tag := loc[i+len("/releases/tag/"):]
	if !IsRelease(tag) {
		return "", fmt.Errorf("check for updates: unexpected tag %q", tag)
	}
	return tag, nil
}

func (u *Updater) fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := u.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// expectedHash downloads and verifies the signed checksum list and returns
// the hash for the given asset.
func (u *Updater) expectedHash(ctx context.Context, version, asset string) ([]byte, error) {
	base := u.BaseURL + "/releases/download/" + version + "/"
	sums, err := u.fetch(ctx, base+"SHA256SUMS", 64<<10)
	if err != nil {
		return nil, err
	}
	sigText, err := u.fetch(ctx, base+"SHA256SUMS.sig", 4<<10)
	if err != nil {
		return nil, fmt.Errorf("release is not signed: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigText)))
	if err != nil || !ed25519.Verify(u.PubKey, sums, sig) {
		return nil, errors.New("release signature is invalid; refusing to update")
	}
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset {
			return hex.DecodeString(f[0])
		}
	}
	return nil, fmt.Errorf("no %s in this release", asset)
}

// Executable returns the real path of the running binary.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// Install downloads version, verifies it, and replaces the executable at exe.
func (u *Updater) Install(ctx context.Context, version, exe string) error {
	asset := AssetName(version, u.GOOS, u.GOARCH)
	want, err := u.expectedHash(ctx, version, asset)
	if err != nil {
		return err
	}
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".fairplay-update-*")
	if err != nil {
		return fmt.Errorf("cannot write next to %s (%w); move the binary to a folder you own, e.g. ~/.local/bin", exe, err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename

	req, _ := http.NewRequestWithContext(ctx, "GET", u.BaseURL+"/releases/download/"+version+"/"+asset, nil)
	resp, err := u.Client.Do(req)
	if err != nil {
		tmp.Close()
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		tmp.Close()
		return fmt.Errorf("download %s: %s", asset, resp.Status)
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, maxBinary))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(h.Sum(nil), want) {
		return errors.New("downloaded file does not match the signed checksum; refusing to update")
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return replace(exe, tmp.Name())
}

// replace swaps in the new binary. Windows cannot overwrite a running .exe but
// can rename it, so the old one is moved aside and deleted on next start.
func replace(exe, newPath string) error {
	if runtime.GOOS != "windows" {
		return os.Rename(newPath, exe)
	}
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := os.Rename(newPath, exe); err != nil {
		os.Rename(old, exe)
		return err
	}
	return nil
}

// CleanupOld removes a binary left behind by a previous Windows update.
func CleanupOld() {
	if exe, err := Executable(); err == nil {
		os.Remove(exe + ".old")
	}
}
