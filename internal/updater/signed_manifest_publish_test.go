package updater

// End-to-end tests of the publishing side of GROK-004 / OPS-01: the manifest
// the release tool builds and signs must be accepted by a build that embeds
// the matching public key, and every alteration must be rejected. Keys are
// generated inside each test, kept in memory and never written to disk.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	json "encoding/json/v2"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const e2eKeyID = "capimux-release-e2e"

var releasePlatforms = []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64", "android-arm64"}

type releaseFixture struct {
	priv     ed25519.PrivateKey
	keyPEM   []byte
	dir      string
	files    []string
	baseURL  string
	tls      *httptest.Server
	mu       sync.Mutex
	envelope []byte
	assets   map[string][]byte // served bytes per file name
	legacy   atomic.Int32
}

func ephemeralSigningKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func newReleaseFixture(t *testing.T, embed bool) *releaseFixture {
	t.Helper()
	pub, priv, keyPEM := ephemeralSigningKey(t)
	f := &releaseFixture{priv: priv, keyPEM: keyPEM, dir: t.TempDir(), assets: map[string][]byte{}}
	for _, p := range releasePlatforms {
		name := "9router-go-" + p
		if strings.HasPrefix(p, "windows") {
			name += ".exe"
		}
		body := []byte("\x7fELF capimux 1.9.7 " + p + " authentic build")
		path := filepath.Join(f.dir, name)
		if err := os.WriteFile(path, body, 0o755); err != nil {
			t.Fatal(err)
		}
		f.files = append(f.files, path)
		f.assets[name] = body
	}
	f.tls = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path == "/version.signed.json" {
			_, _ = w.Write(f.envelope)
			return
		}
		if body, ok := f.assets[strings.TrimPrefix(r.URL.Path, "/download/")]; ok {
			_, _ = w.Write(body)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(f.tls.Close)
	f.baseURL = f.tls.URL + "/download"
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.legacy.Add(1)
		_, _ = w.Write([]byte(`{"version":"9.9.9","downloadUrl":"http://` + r.Host + `/evil","sha256":"` + strings.Repeat("ab", 32) + `"}`))
	}))
	t.Cleanup(legacy.Close)

	prevKeys, prevClient, prevVersion, prevURL := embeddedManifestPublicKeys, signedManifestHTTPClient, CurrentVersion, DefaultSignedManifestURL
	t.Cleanup(func() {
		embeddedManifestPublicKeys, signedManifestHTTPClient, CurrentVersion, DefaultSignedManifestURL = prevKeys, prevClient, prevVersion, prevURL
	})
	embeddedManifestPublicKeys = map[string]string{}
	if embed {
		embeddedManifestPublicKeys[e2eKeyID] = base64.StdEncoding.EncodeToString(pub)
	}
	signedManifestHTTPClient = f.tls.Client()
	CurrentVersion = "1.9.6"
	DefaultSignedManifestURL = f.tls.URL + "/version.signed.json"
	t.Setenv("UPDATE_SIGNED_MANIFEST_URL", "")
	t.Setenv("UPDATE_URL", legacy.URL+"/version.json")
	t.Setenv("UPDATE_REPO", "attacker/repo")
	return f
}

func (f *releaseFixture) publish(env []byte) {
	f.mu.Lock()
	f.envelope = env
	f.mu.Unlock()
}

// signRelease runs the same steps as `release-manifest -signed-out`.
func (f *releaseFixture) signRelease(t *testing.T, version string, issued time.Time, validity time.Duration) []byte {
	t.Helper()
	priv, err := ParseManifestSigningKey(f.keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := BuildSignedManifestPayload(version, "", f.baseURL, f.files, issued, validity)
	if err != nil {
		t.Fatal(err)
	}
	env, keyID, err := SignManifestPayload(payload, priv, issued)
	if err != nil {
		t.Fatalf("SignManifestPayload: %v", err)
	}
	if keyID != e2eKeyID {
		t.Fatalf("signed with key %q, want %q", keyID, e2eKeyID)
	}
	return env
}

func currentAssetName() string {
	name := "9router-go-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func (f *releaseFixture) download(t *testing.T, url string) []byte {
	t.Helper()
	resp, err := f.tls.Client().Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func decodeEnvelope(t *testing.T, env []byte) (signedManifestEnvelope, updateManifest, []byte) {
	t.Helper()
	var outer signedManifestEnvelope
	if err := json.Unmarshal(env, &outer); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(outer.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var m updateManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return outer, m, raw
}

func TestSignedReleaseEndToEnd(t *testing.T) {
	f := newReleaseFixture(t, true)
	now := time.Now().UTC()
	env := f.signRelease(t, "v1.9.7", now, 7*24*time.Hour)

	_, m, _ := decodeEnvelope(t, env)
	if m.Version != "1.9.7" || m.Product != "capimux" || m.Channel != "stable" || m.Schema != 1 {
		t.Fatalf("unexpected manifest header: %+v", m)
	}
	if got := m.ExpiresAt.Sub(m.IssuedAt); got != 7*24*time.Hour {
		t.Fatalf("validity = %s, want 168h", got)
	}
	if len(m.Assets) != len(releasePlatforms) {
		t.Fatalf("manifest lists %d assets, want %d", len(m.Assets), len(releasePlatforms))
	}
	for platform, a := range m.Assets {
		name := filepath.Base(a.URL)
		body := f.assets[name]
		sum := sha256.Sum256(body)
		if a.SHA256 != hex.EncodeToString(sum[:]) || a.Size != int64(len(body)) || !strings.HasPrefix(a.URL, "https://") {
			t.Errorf("%s: asset %+v does not match %s (%d bytes)", platform, a, name, len(body))
		}
	}

	f.publish(env)
	info, err := CheckUpdate(context.Background())
	if err != nil {
		t.Fatalf("published manifest rejected by a signed build: %v", err)
	}
	if !info.HasUpdate || info.LatestVersion != "1.9.7" || info.Source != "signed_manifest" ||
		info.DownloadURL != f.baseURL+"/"+currentAssetName() {
		t.Fatalf("unexpected update info: %+v", info)
	}
	body := f.download(t, info.DownloadURL)
	if _, _, err := verifyDownloadedUpdate(body, info.DownloadURL, info.SHA256); err != nil {
		t.Fatalf("authentic binary rejected: %v", err)
	}
	if n := f.legacy.Load(); n != 0 {
		t.Fatalf("signed build consulted the unsigned sources %d times", n)
	}
}

func TestSignedReleaseRejectsTampering(t *testing.T) {
	f := newReleaseFixture(t, true)
	now := time.Now().UTC()
	env := f.signRelease(t, "v1.9.7", now, 7*24*time.Hour)
	outer, m, raw := decodeEnvelope(t, env)

	reject := func(t *testing.T, served []byte, what string) {
		t.Helper()
		f.publish(served)
		info, err := CheckUpdate(context.Background())
		if err == nil || info != nil {
			t.Fatalf("%s accepted: info=%+v err=%v", what, info, err)
		}
		if n := f.legacy.Load(); n != 0 {
			t.Fatalf("%s fell back to the unsigned sources", what)
		}
		if err := VerifySignedManifestForRelease(served, now); err == nil {
			t.Fatalf("%s passed the release verification gate", what)
		}
	}
	reencode := func(t *testing.T, payload []byte, sig string) []byte {
		t.Helper()
		b, err := json.Marshal(signedManifestEnvelope{
			Payload:    base64.StdEncoding.EncodeToString(payload),
			Signatures: []manifestSignature{{KeyID: e2eKeyID, Sig: sig}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	t.Run("binary hash swapped in the payload", func(t *testing.T) {
		evil := m
		evil.Assets = map[string]manifestAsset{}
		for k, a := range m.Assets {
			a.SHA256 = strings.Repeat("cd", 32)
			evil.Assets[k] = a
		}
		p, err := json.Marshal(evil, json.Deterministic(true))
		if err != nil {
			t.Fatal(err)
		}
		reject(t, reencode(t, p, outer.Signatures[0].Sig), "swapped hash")
	})
	t.Run("one payload byte flipped", func(t *testing.T) {
		p := append([]byte(nil), raw...)
		p[len(p)/3] ^= 0x01
		reject(t, reencode(t, p, outer.Signatures[0].Sig), "flipped byte")
	})
	t.Run("expiry extended after signing", func(t *testing.T) {
		evil := m
		evil.ExpiresAt = evil.ExpiresAt.Add(time.Hour)
		p, _ := json.Marshal(evil, json.Deterministic(true))
		reject(t, reencode(t, p, outer.Signatures[0].Sig), "extended expiry")
	})
	t.Run("signature altered", func(t *testing.T) {
		sig, _ := base64.StdEncoding.DecodeString(outer.Signatures[0].Sig)
		sig[10] ^= 0x80
		reject(t, reencode(t, raw, base64.StdEncoding.EncodeToString(sig)), "altered signature")
	})
	t.Run("signed by a key that is not embedded", func(t *testing.T) {
		_, other, _ := ed25519GenerateForTest(t)
		sig := ed25519.Sign(other, append([]byte(manifestSigningContext), raw...))
		reject(t, reencode(t, raw, base64.StdEncoding.EncodeToString(sig)), "foreign key")
		if _, _, err := SignManifestPayload(raw, other, now); err == nil {
			t.Fatal("the release tool signed with a key no build trusts")
		}
	})
	t.Run("expired manifest", func(t *testing.T) {
		issued := now.Add(-25 * time.Hour)
		p, err := BuildSignedManifestPayload("v1.9.7", "", f.baseURL, f.files, issued, 24*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		// The tool's own gate refuses to publish it...
		if _, _, err := SignManifestPayload(p, f.priv, now); err == nil {
			t.Fatal("the release tool published an already expired manifest")
		}
		// ...and a hand-signed copy is refused by the updater.
		sig := ed25519.Sign(f.priv, append([]byte(manifestSigningContext), p...))
		reject(t, reencode(t, p, base64.StdEncoding.EncodeToString(sig)), "expired manifest")
	})
	t.Run("manifest expires at the end of its validity", func(t *testing.T) {
		if err := VerifySignedManifestForRelease(env, m.ExpiresAt); err == nil {
			t.Fatal("manifest still valid at its expiresAt instant")
		}
		if err := VerifySignedManifestForRelease(env, m.ExpiresAt.Add(-time.Second)); err != nil {
			t.Fatalf("manifest invalid one second before expiry: %v", err)
		}
	})
	t.Run("binary replaced on the server after signing", func(t *testing.T) {
		f.publish(env)
		info, err := CheckUpdate(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		f.assets[currentAssetName()] = []byte("\x7fELF capimux trojan")
		f.mu.Unlock()
		if _, _, err := verifyDownloadedUpdate(f.download(t, info.DownloadURL), info.DownloadURL, info.SHA256); err == nil {
			t.Fatal("a binary that differs from the signed SHA-256 was accepted")
		}
	})
}

func ed25519GenerateForTest(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey, error) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv, nil
}

// Publication order: while this commit embeds no public key (or a different
// one), the release tool refuses to sign, so the workflow cannot publish a
// release whose binaries would not accept its own manifest.
func TestSignedReleaseRequiresAnEmbeddedMatchingKey(t *testing.T) {
	f := newReleaseFixture(t, false)
	now := time.Now().UTC()
	payload, err := BuildSignedManifestPayload("v1.9.7", "", f.baseURL, f.files, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := SignManifestPayload(payload, f.priv, now); err == nil || !strings.Contains(err.Error(), "ninguna clave pública embebida") {
		t.Fatalf("signing without an embedded key: err = %v", err)
	}
	otherPub, _, _ := ed25519GenerateForTest(t)
	embeddedManifestPublicKeys = map[string]string{"capimux-release-other": base64.StdEncoding.EncodeToString(otherPub)}
	if _, _, err := SignManifestPayload(payload, f.priv, now); err == nil || !strings.Contains(err.Error(), "no corresponde") {
		t.Fatalf("signing with a key that is not embedded: err = %v", err)
	}
}

func TestBuildSignedManifestPayloadRefusesUnsafeInput(t *testing.T) {
	f := newReleaseFixture(t, true)
	now := time.Now().UTC()
	cases := []struct {
		name, version, base string
		validity            time.Duration
	}{
		{"pre-release version", "v1.9.7-rc.1", f.baseURL, time.Hour},
		{"non-semver version", "main", f.baseURL, time.Hour},
		{"HTTP base URL", "v1.9.7", "http://example.com/dl", time.Hour},
		{"validity above the verifier maximum", "v1.9.7", f.baseURL, maxManifestValidity + time.Second},
		{"zero validity", "v1.9.7", f.baseURL, 0},
	}
	for _, c := range cases {
		if _, err := BuildSignedManifestPayload(c.version, "", c.base, f.files, now, c.validity); err == nil {
			t.Errorf("%s accepted", c.name)
		}
	}
	if _, err := BuildSignedManifestPayload("v1.9.7", "", f.baseURL, f.files, now, maxManifestValidity); err != nil {
		t.Errorf("maximum validity refused: %v", err)
	}
}

func TestParseManifestSigningKeyNeverEchoesKeyMaterial(t *testing.T) {
	_, _, good := ephemeralSigningKey(t)
	if _, err := ParseManifestSigningKey(good); err != nil {
		t.Fatalf("PKCS#8 PEM refused: %v", err)
	}
	secret := strings.Repeat("S3CR3T", 8)
	for _, bad := range [][]byte{
		[]byte(secret),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte(secret)}),
		pem.EncodeToMemory(&pem.Block{Type: "OPENSSH PRIVATE KEY", Bytes: []byte(secret)}),
	} {
		_, err := ParseManifestSigningKey(bad)
		if err == nil {
			t.Fatal("malformed key accepted")
		}
		if strings.Contains(err.Error(), "S3CR3T") || strings.Contains(err.Error(), base64.StdEncoding.EncodeToString([]byte(secret))[:16]) {
			t.Fatalf("error leaks key material: %v", err)
		}
	}
}
