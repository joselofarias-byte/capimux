package updater

// GROK-004 / OPS-01 tampering tests: once a release public key is embedded,
// CheckUpdate must accept only an authentic, current, newer manifest, and must
// never fall back to the unsigned sources. Keys are generated per test; no
// real signing key exists in the repository.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const tamperKeyID = "capimux-release-test"

type tamperFixture struct {
	pub      ed25519.PublicKey
	priv     ed25519.PrivateKey
	binary   []byte
	digest   string
	serve    atomic.Value // []byte served as the signed envelope
	legacy   atomic.Int32 // hits on the unsigned sources
	tls      *httptest.Server
	unsigned *httptest.Server
}

func newTamperFixture(t *testing.T) *tamperFixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &tamperFixture{pub: pub, priv: priv, binary: []byte("\x7fELF-capimux-1.9.7-authentic")}
	sum := sha256.Sum256(f.binary)
	f.digest = hex.EncodeToString(sum[:])
	f.serve.Store([]byte{})
	f.tls = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(f.serve.Load().([]byte))
	}))
	t.Cleanup(f.tls.Close)
	// The unsigned sources offer a newer release with a valid digest: a build
	// that pins a key must never consult them.
	f.unsigned = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.legacy.Add(1)
		_, _ = w.Write([]byte(`{"version":"9.9.9","downloadUrl":"` + "http://" + r.Host + `/evil","sha256":"` + strings.Repeat("ab", 32) + `"}`))
	}))
	t.Cleanup(f.unsigned.Close)

	prevKeys, prevClient, prevVersion, prevURL := embeddedManifestPublicKeys, signedManifestHTTPClient, CurrentVersion, DefaultSignedManifestURL
	t.Cleanup(func() {
		embeddedManifestPublicKeys, signedManifestHTTPClient, CurrentVersion, DefaultSignedManifestURL = prevKeys, prevClient, prevVersion, prevURL
	})
	embeddedManifestPublicKeys = map[string]string{tamperKeyID: base64.StdEncoding.EncodeToString(pub)}
	signedManifestHTTPClient = f.tls.Client()
	CurrentVersion = "1.9.6"
	DefaultSignedManifestURL = f.tls.URL + "/version.signed.json"
	t.Setenv("UPDATE_SIGNED_MANIFEST_URL", "")
	t.Setenv("UPDATE_URL", f.unsigned.URL+"/version.json")
	t.Setenv("UPDATE_REPO", "attacker/repo")
	return f
}

func (f *tamperFixture) manifest(version string) updateManifest {
	now := time.Now().UTC()
	return updateManifest{
		Schema: 1, Product: "capimux", Channel: "stable", Version: version,
		IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(24 * time.Hour),
		Assets: map[string]manifestAsset{
			runtime.GOOS + "_" + runtime.GOARCH: {
				URL:    f.tls.URL + "/capimux-" + runtime.GOOS + "-" + runtime.GOARCH,
				SHA256: f.digest, Size: int64(len(f.binary)),
			},
		},
	}
}

func signManifest(t *testing.T, m updateManifest, priv ed25519.PrivateKey) (payload []byte, sig []byte) {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw, ed25519.Sign(priv, append([]byte(manifestSigningContext), raw...))
}

func envelopeJSON(t *testing.T, payload []byte, sigs []manifestSignature) []byte {
	t.Helper()
	b, err := json.Marshal(signedManifestEnvelope{Payload: base64.StdEncoding.EncodeToString(payload), Signatures: sigs})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sigEntry(id string, sig []byte) []manifestSignature {
	return []manifestSignature{{KeyID: id, Sig: base64.StdEncoding.EncodeToString(sig)}}
}

func (f *tamperFixture) mustReject(t *testing.T, envelope []byte, what string) {
	t.Helper()
	f.serve.Store(envelope)
	info, err := CheckUpdate(context.Background())
	if err == nil {
		t.Fatalf("%s accepted: %+v", what, info)
	}
	if info != nil {
		t.Fatalf("%s returned update info alongside the error: %+v", what, info)
	}
	if n := f.legacy.Load(); n != 0 {
		t.Fatalf("%s fell back to the unsigned sources (%d hits)", what, n)
	}
}

func TestSignedManifestAcceptsAuthenticNewerRelease(t *testing.T) {
	f := newTamperFixture(t)
	payload, sig := signManifest(t, f.manifest("1.9.7"), f.priv)
	f.serve.Store(envelopeJSON(t, payload, sigEntry(tamperKeyID, sig)))
	info, err := CheckUpdate(context.Background())
	if err != nil {
		t.Fatalf("authentic manifest rejected: %v", err)
	}
	if !info.HasUpdate || info.LatestVersion != "1.9.7" || info.SHA256 != f.digest || info.Source != "signed_manifest" {
		t.Fatalf("unexpected info: %+v", info)
	}
	if f.legacy.Load() != 0 {
		t.Fatal("signed build consulted the unsigned sources")
	}
}

func TestSignedManifestRejectsTampering(t *testing.T) {
	f := newTamperFixture(t)
	payload, sig := signManifest(t, f.manifest("1.9.7"), f.priv)

	t.Run("altered manifest payload", func(t *testing.T) {
		evil := f.manifest("1.9.7")
		a := evil.Assets[runtime.GOOS+"_"+runtime.GOARCH]
		a.SHA256 = strings.Repeat("cd", 32)
		a.URL = "https://attacker.example/capimux"
		evil.Assets[runtime.GOOS+"_"+runtime.GOARCH] = a
		evilPayload, _ := signManifest(t, evil, f.priv) // re-encoded, original signature kept
		f.mustReject(t, envelopeJSON(t, evilPayload, sigEntry(tamperKeyID, sig)), "altered payload")
	})
	t.Run("single flipped payload byte", func(t *testing.T) {
		mod := append([]byte(nil), payload...)
		mod[len(mod)/2] ^= 0x01
		f.mustReject(t, envelopeJSON(t, mod, sigEntry(tamperKeyID, sig)), "flipped payload byte")
	})
	t.Run("altered signature", func(t *testing.T) {
		bad := append([]byte(nil), sig...)
		bad[0] ^= 0xff
		f.mustReject(t, envelopeJSON(t, payload, sigEntry(tamperKeyID, bad)), "altered signature")
	})
	t.Run("truncated signature", func(t *testing.T) {
		f.mustReject(t, envelopeJSON(t, payload, sigEntry(tamperKeyID, sig[:32])), "truncated signature")
	})
	t.Run("signature from another key", func(t *testing.T) {
		_, other, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		_, otherSig := signManifest(t, f.manifest("1.9.7"), other)
		f.mustReject(t, envelopeJSON(t, payload, sigEntry(tamperKeyID, otherSig)), "foreign-key signature")
	})
	t.Run("valid signature under an unknown key id", func(t *testing.T) {
		f.mustReject(t, envelopeJSON(t, payload, sigEntry("someone-else", sig)), "unknown key id")
	})
	t.Run("missing signature", func(t *testing.T) {
		f.mustReject(t, envelopeJSON(t, payload, nil), "empty signature list")
		f.mustReject(t, []byte(`{"payload":"`+base64.StdEncoding.EncodeToString(payload)+`"}`), "absent signatures field")
	})
	t.Run("unsigned legacy manifest served at the signed URL", func(t *testing.T) {
		f.mustReject(t, []byte(`{"version":"1.9.7","downloadUrl":"https://x/y","sha256":"`+f.digest+`"}`), "unsigned manifest")
	})
	t.Run("signature without the domain-separation context", func(t *testing.T) {
		f.mustReject(t, envelopeJSON(t, payload, sigEntry(tamperKeyID, ed25519.Sign(f.priv, payload))), "context-less signature")
	})
	t.Run("expired manifest replay", func(t *testing.T) {
		m := f.manifest("1.9.7")
		m.IssuedAt, m.ExpiresAt = time.Now().Add(-48*time.Hour), time.Now().Add(-24*time.Hour)
		p, s := signManifest(t, m, f.priv)
		f.mustReject(t, envelopeJSON(t, p, sigEntry(tamperKeyID, s)), "expired manifest")
	})
	t.Run("plain HTTP manifest URL", func(t *testing.T) {
		t.Setenv("UPDATE_SIGNED_MANIFEST_URL", f.unsigned.URL+"/version.signed.json")
		f.mustReject(t, envelopeJSON(t, payload, sigEntry(tamperKeyID, sig)), "HTTP manifest URL")
	})
}

func TestSignedManifestDowngradeAndReplayAreNotInstallable(t *testing.T) {
	f := newTamperFixture(t)
	for _, version := range []string{"1.9.5", "1.9.6"} {
		p, s := signManifest(t, f.manifest(version), f.priv)
		f.serve.Store(envelopeJSON(t, p, sigEntry(tamperKeyID, s)))
		info, err := CheckUpdate(context.Background())
		if err != nil {
			t.Fatalf("authentic %s manifest errored: %v", version, err)
		}
		if info.HasUpdate || info.DownloadURL != "" || info.SHA256 != "" {
			t.Fatalf("signed %s manifest offered as an update to 1.9.6: %+v", version, info)
		}
	}
	if f.legacy.Load() != 0 {
		t.Fatal("downgrade fell back to the unsigned sources")
	}
}

func TestSignedManifestAlteredBinaryCannotBeInstalled(t *testing.T) {
	f := newTamperFixture(t)
	p, s := signManifest(t, f.manifest("1.9.7"), f.priv)
	f.serve.Store(envelopeJSON(t, p, sigEntry(tamperKeyID, s)))
	info, err := CheckUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyUpdateBinarySHA256(f.binary, info.SHA256); err != nil {
		t.Fatalf("authentic binary failed the signed digest: %v", err)
	}
	tampered := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("\x7fELF-capimux-1.9.7-backdoored"))
	}))
	defer tampered.Close()
	err = PerformSelfUpdate(tampered.URL+"/9router-go-"+runtime.GOOS+"-"+runtime.GOARCH, info.SHA256)
	if err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("binary that does not match the signed digest was not rejected for its hash: %v", err)
	}
}

func TestSignedManifestMalformedEmbeddedKeyFailsClosed(t *testing.T) {
	f := newTamperFixture(t)
	for name, key := range map[string]string{
		"not base64": "%%%",
		"short key":  base64.StdEncoding.EncodeToString(make([]byte, 16)),
		"empty":      "",
	} {
		embeddedManifestPublicKeys = map[string]string{tamperKeyID: key}
		p, s := signManifest(t, f.manifest("1.9.7"), f.priv)
		f.mustReject(t, envelopeJSON(t, p, sigEntry(tamperKeyID, s)), "malformed embedded key ("+name+")")
	}
	embeddedManifestPublicKeys = map[string]string{" ": base64.StdEncoding.EncodeToString(f.pub)}
	p, s := signManifest(t, f.manifest("1.9.7"), f.priv)
	f.mustReject(t, envelopeJSON(t, p, sigEntry(" ", s)), "blank key id")
}

func TestUnsignedBuildKeepsLegacyUpdatePath(t *testing.T) {
	f := newTamperFixture(t)
	embeddedManifestPublicKeys = map[string]string{}
	info, err := CheckUpdate(context.Background())
	if err != nil {
		t.Fatalf("legacy manifest path broken for builds without a pinned key: %v", err)
	}
	if info.Source != "manifest" || f.legacy.Load() != 1 {
		t.Fatalf("expected the legacy manifest to be used once, got source=%q hits=%d", info.Source, f.legacy.Load())
	}
}

func TestRepositoryShipsNoPlaceholderManifestKey(t *testing.T) {
	// Until the owner provisions a key, the set must be empty, never a dummy
	// or test key that someone holds the private half of.
	keys, err := trustedManifestKeys()
	if err != nil {
		t.Fatalf("embedded key set does not parse: %v", err)
	}
	for id := range keys {
		if strings.Contains(strings.ToLower(id), "test") || strings.Contains(strings.ToLower(id), "dummy") {
			t.Fatalf("placeholder key %q embedded", id)
		}
	}
}
