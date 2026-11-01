package updater

// GROK-004 / OPS-01: publisher authenticity for self-updates.
//
// A build that pins at least one Ed25519 public key in
// embeddedManifestPublicKeys only trusts the signed manifest: CheckUpdate
// never falls back to the unsigned version.json or to the GitHub Releases API,
// and a missing, malformed or non-verifying signature is an error, so nothing
// can be installed. The key set is compiled into the binary; nothing fetched
// over the network can add, replace or disable it.
//
// While the set is empty (the owner has not provisioned a release key yet)
// the legacy path keeps working unchanged, with the mandatory SHA-256 check.
// See docs/security/GROK-004-OPS-01.md for how to generate the key and sign.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/samber/lo"
)

// embeddedManifestPublicKeys maps a key ID to the standard-base64 encoding of a
// raw 32-byte Ed25519 public key. Only public keys belong here; the private key
// never enters the repository.
//
// The owner fills this in (docs/security/GROK-004-OPS-01.md, "Embeber la clave
// pública"); TestEmbeddedManifestPublicKeysWellFormed checks the format:
//
//	var embeddedManifestPublicKeys = map[string]string{
//		"capimux-release-2026": "<44 caracteres base64 = 32 bytes>",
//	}
//
// Key IDs are 3–64 characters of [a-z0-9._-], starting with a letter or digit.
var embeddedManifestPublicKeys = map[string]string{}

// DefaultSignedManifestURL is where a signed build looks for the signed
// manifest envelope: the asset the release workflow uploads next to the
// binaries of the latest stable release. UPDATE_SIGNED_MANIFEST_URL overrides
// it (HTTPS only).
var DefaultSignedManifestURL = "https://github.com/joselofarias-byte/capimux/releases/latest/download/version.signed.json"

var manifestKeyIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,63}$`)

// validateEmbeddedManifestKey rejects a key ID or public key that cannot be a
// real release key: a malformed ID, anything but standard base64 of exactly 32
// bytes, or an all-zero placeholder.
func validateEmbeddedManifestKey(id, encoded string) (ed25519.PublicKey, error) {
	if !manifestKeyIDPattern.MatchString(id) {
		return nil, fmt.Errorf("updater: embedded manifest key ID %q must be 3-64 chars of [a-z0-9._-]", id)
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("updater: embedded manifest key %q is not a base64 Ed25519 public key", id)
	}
	if bytes.Equal(raw, make([]byte, ed25519.PublicKeySize)) {
		return nil, fmt.Errorf("updater: embedded manifest key %q is an all-zero placeholder", id)
	}
	return ed25519.PublicKey(raw), nil
}

const (
	signedManifestProduct = "capimux"
	signedManifestChannel = "stable"
)

// signedManifestHTTPClient lets tests trust an httptest TLS server.
var signedManifestHTTPClient *http.Client

// trustedManifestKeys decodes the embedded key set. An empty set returns
// (nil, nil): signing is not provisioned. Any malformed entry is an error so a
// broken key set fails closed instead of silently disabling verification.
func trustedManifestKeys() (map[string]ed25519.PublicKey, error) {
	if len(embeddedManifestPublicKeys) == 0 {
		return nil, nil
	}
	keys := make(map[string]ed25519.PublicKey, len(embeddedManifestPublicKeys))
	for id, encoded := range embeddedManifestPublicKeys {
		if strings.TrimSpace(id) == "" {
			return nil, errors.New("updater: embedded manifest key with empty key ID")
		}
		pub, err := validateEmbeddedManifestKey(id, encoded)
		if err != nil {
			return nil, err
		}
		keys[id] = pub
	}
	return keys, nil
}

// checkSignedUpdate runs the signed-manifest check when this build pins keys.
// enforced=false means no key is embedded and the caller may use the legacy
// sources. With enforced=true the caller must return (info, err) as is and
// must not consult any unsigned source.
func checkSignedUpdate(ctx context.Context) (info *UpdateInfo, enforced bool, err error) {
	keys, err := trustedManifestKeys()
	if err != nil {
		return nil, true, err
	}
	if len(keys) == 0 {
		return nil, false, nil
	}
	manifestURL := lo.CoalesceOrEmpty(os.Getenv("UPDATE_SIGNED_MANIFEST_URL"), DefaultSignedManifestURL)
	policy := manifestPolicy{
		TrustedKeys:    keys,
		Product:        signedManifestProduct,
		Channel:        signedManifestChannel,
		CurrentVersion: CurrentVersion,
		Platform:       runtime.GOOS + "_" + runtime.GOARCH,
		Now:            time.Now().UTC(),
	}
	info, err = checkSignedManifestCandidate(ctx, manifestURL, policy, signedManifestHTTPClient)
	if errors.Is(err, errManifestRollback) {
		// Authentic but not newer (same release, or a replayed older one):
		// report "up to date" and never offer it for installation.
		return &UpdateInfo{
			CurrentVersion: CurrentVersion,
			LatestVersion:  strings.TrimPrefix(CurrentVersion, "v"),
			HasUpdate:      false,
			GoVersion:      runtime.Version(),
			OS:             runtime.GOOS,
			Arch:           runtime.GOARCH,
			CheckedAt:      time.Now().UTC().Format(time.RFC3339),
			Source:         "signed_manifest",
		}, true, nil
	}
	if err != nil {
		return nil, true, fmt.Errorf("check update failed: signed manifest: %w", err)
	}
	return info, true, nil
}
