package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func releaseDir(t *testing.T) (dir string, files []string) {
	t.Helper()
	dir = t.TempDir()
	for _, n := range []string{"9router-go-linux-arm64", "9router-go-android-arm64"} {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("bin "+n), 0o755); err != nil {
			t.Fatal(err)
		}
		files = append(files, p)
	}
	return dir, files
}

func signedOptions(dir string, files []string) options {
	return options{
		version:   "v1.9.7",
		baseURL:   "https://github.com/joselofarias-byte/capimux/releases/download/v1.9.7",
		out:       filepath.Join(dir, "version.json"),
		signedOut: filepath.Join(dir, "version.signed.json"),
		validity:  24 * time.Hour,
		files:     files,
	}
}

func assertNothingWritten(t *testing.T, o options) {
	t.Helper()
	for _, p := range []string{o.out, o.signedOut} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was written although signing failed", filepath.Base(p))
		}
	}
}

// Without the CAPIMUX_MANIFEST_SIGNING_KEY secret the release must fail
// explicitly and publish neither manifest.
func TestSignedOutWithoutSecretFailsExplicitly(t *testing.T) {
	dir, files := releaseDir(t)
	o := signedOptions(dir, files)
	err := run(o, func(string) string { return "" }, time.Now())
	if err == nil || !strings.Contains(err.Error(), "CAPIMUX_MANIFEST_SIGNING_KEY") {
		t.Fatalf("err = %v, want an explicit missing-secret error", err)
	}
	assertNothingWritten(t, o)
}

// A key that no public key embedded in this commit matches (today: none is
// embedded) must not produce a manifest, and the error never echoes the key.
func TestSignedOutWithUnembeddedKeyFails(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	dir, files := releaseDir(t)
	o := signedOptions(dir, files)
	err = run(o, func(k string) string {
		if k == "CAPIMUX_MANIFEST_SIGNING_KEY" {
			return keyPEM
		}
		return ""
	}, time.Now())
	if err == nil {
		t.Fatal("signed with a key that no embedded public key matches")
	}
	if !strings.Contains(err.Error(), "embebida") {
		t.Fatalf("unexpected error: %v", err)
	}
	body := strings.Split(keyPEM, "\n")[1]
	if strings.Contains(err.Error(), body[:20]) {
		t.Fatal("error message leaks the private key")
	}
	assertNothingWritten(t, o)
}

// The unsigned path keeps working for local use (no -signed-out).
func TestUnsignedManifestStillWritten(t *testing.T) {
	dir, files := releaseDir(t)
	o := signedOptions(dir, files)
	o.signedOut = ""
	if err := run(o, func(string) string { return "" }, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(o.out); err != nil {
		t.Fatal(err)
	}
}
