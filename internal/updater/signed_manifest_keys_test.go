package updater

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

// The owner embeds the release public key by hand. This test is the format
// check for that edit: every entry must be a valid key ID and a standard
// base64 encoding of exactly 32 bytes, so a typo fails CI instead of shipping a
// build whose updater refuses every manifest. An empty set is the current,
// legacy state and passes.
func TestEmbeddedManifestPublicKeysWellFormed(t *testing.T) {
	if len(embeddedManifestPublicKeys) == 0 {
		t.Log("no release public key embedded yet: the updater uses version.json with mandatory SHA-256")
	}
	for id, encoded := range embeddedManifestPublicKeys {
		if _, err := validateEmbeddedManifestKey(id, encoded); err != nil {
			t.Errorf("embeddedManifestPublicKeys[%q]: %v", id, err)
		}
		if strings.TrimSpace(encoded) != encoded || len(encoded) != 44 {
			t.Errorf("embeddedManifestPublicKeys[%q] must be exactly 44 base64 characters, no spaces", id)
		}
	}
	if _, err := trustedManifestKeys(); err != nil {
		t.Errorf("trustedManifestKeys: %v", err)
	}
}

func TestValidateEmbeddedManifestKey(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	good := base64.StdEncoding.EncodeToString(pub)
	if _, err := validateEmbeddedManifestKey("capimux-release-2026", good); err != nil {
		t.Fatalf("valid key refused: %v", err)
	}
	bad := map[string][2]string{
		"empty ID":              {"", good},
		"uppercase ID":          {"Capimux-Release", good},
		"ID with spaces":        {"capimux release", good},
		"too short ID":          {"ab", good},
		"URL-safe base64":       {"capimux-release-2026", base64.URLEncoding.EncodeToString(append([]byte{0xfb, 0xff}, pub[2:]...))},
		"unpadded base64":       {"capimux-release-2026", strings.TrimRight(good, "=")},
		"31 bytes":              {"capimux-release-2026", base64.StdEncoding.EncodeToString(pub[:31])},
		"33 bytes":              {"capimux-release-2026", base64.StdEncoding.EncodeToString(append(pub, 0))},
		"all-zero placeholder":  {"capimux-release-2026", base64.StdEncoding.EncodeToString(make([]byte, 32))},
		"PEM instead of raw":    {"capimux-release-2026", "-----BEGIN PUBLIC KEY-----"},
		"64-byte private key":   {"capimux-release-2026", base64.StdEncoding.EncodeToString(make([]byte, 64))},
		"hex instead of base64": {"capimux-release-2026", strings.Repeat("ab", 32)},
	}
	for name, c := range bad {
		if _, err := validateEmbeddedManifestKey(c[0], c[1]); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// The default signed manifest is the asset of the latest stable release, and
// the only cross-host redirect a fetch may follow is GitHub's own one from
// github.com to its release-asset CDN.
func TestSignedManifestDefaultURLAndRedirectPolicy(t *testing.T) {
	const want = "https://github.com/joselofarias-byte/capimux/releases/latest/download/version.signed.json"
	if DefaultSignedManifestURL != want {
		t.Fatalf("DefaultSignedManifestURL = %q, want %q", DefaultSignedManifestURL, want)
	}
	if err := validateSignedManifestSource(DefaultSignedManifestURL); err != nil {
		t.Fatal(err)
	}
	allowed := [][2]string{
		{"github.com", "github.com"},
		{"github.com", "release-assets.githubusercontent.com"},
		{"github.com", "objects.githubusercontent.com"},
		{"example.com:8443", "EXAMPLE.com:8443"},
	}
	for _, c := range allowed {
		if !signedManifestHostAllowed(c[0], c[1]) {
			t.Errorf("%s -> %s refused", c[0], c[1])
		}
	}
	refused := [][2]string{
		{"github.com", "attacker.example"},
		{"github.com", "raw.githubusercontent.com"},
		{"github.com", "release-assets.githubusercontent.com.attacker.example"},
		{"raw.githubusercontent.com", "release-assets.githubusercontent.com"},
		{"example.com", "release-assets.githubusercontent.com"},
	}
	for _, c := range refused {
		if signedManifestHostAllowed(c[0], c[1]) {
			t.Errorf("%s -> %s allowed", c[0], c[1])
		}
	}
}
