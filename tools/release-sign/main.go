// Command release-sign creates the release signing key and signs SHA256SUMS.
//
//	go run ./tools/release-sign keygen <private-key-file>   # prints the public key
//	FAIRPLAY_SIGNING_KEY=<base64 seed> go run ./tools/release-sign sign dist/SHA256SUMS
//	go run ./tools/release-sign verify dist/SHA256SUMS      # checks against the embedded key
//
// The signature is written next to the file as <file>.sig (base64 Ed25519).
// The public key is compiled into the updater (internal/update), so a release
// is only installed if it was signed with the matching private key.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/Kumarsujay03/fairplay/internal/update"
)

func main() {
	if len(os.Args) < 3 {
		fail("usage: release-sign keygen <private-key-file> | sign <file>")
	}
	switch os.Args[1] {
	case "keygen":
		if _, err := os.Stat(os.Args[2]); err == nil {
			fail("refusing to overwrite existing key " + os.Args[2])
		}
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		check(err)
		check(os.WriteFile(os.Args[2], []byte(base64.StdEncoding.EncodeToString(priv.Seed())+"\n"), 0o600))
		fmt.Println(base64.StdEncoding.EncodeToString(pub))
	case "sign":
		seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("FAIRPLAY_SIGNING_KEY")))
		if err != nil || len(seed) != ed25519.SeedSize {
			fail("FAIRPLAY_SIGNING_KEY must hold the base64 Ed25519 seed")
		}
		data, err := os.ReadFile(os.Args[2])
		check(err)
		sig := ed25519.Sign(ed25519.NewKeyFromSeed(seed), data)
		check(os.WriteFile(os.Args[2]+".sig", []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644))
		fmt.Println("signed", os.Args[2])
	case "verify":
		pub, _ := base64.StdEncoding.DecodeString(update.PublicKey)
		data, err := os.ReadFile(os.Args[2])
		check(err)
		sigText, err := os.ReadFile(os.Args[2] + ".sig")
		check(err)
		sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigText)))
		if err != nil || !ed25519.Verify(ed25519.PublicKey(pub), data, sig) {
			fail("signature does NOT match the public key in internal/update")
		}
		fmt.Println("signature OK")
	default:
		fail("unknown command " + os.Args[1])
	}
}

func check(err error) {
	if err != nil {
		fail(err.Error())
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
