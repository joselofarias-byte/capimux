package updater

// GROK-004 / OPS-01, publishing side: builds and signs the version.signed.json
// envelope that verifySignedManifest accepts. The release workflow runs it
// through cmd/release-manifest with the private key taken only from the
// CAPIMUX_MANIFEST_SIGNING_KEY Actions secret (or a key file on the owner's
// machine for an offline re-sign). Nothing here generates, stores or prints a
// private key.

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	json "encoding/json/v2"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ManifestSigningKeyEnv names the GitHub Actions secret (exposed as an
// environment variable of the signing step only) that holds the PKCS#8 PEM
// Ed25519 private key.
const ManifestSigningKeyEnv = "CAPIMUX_MANIFEST_SIGNING_KEY"

// DefaultManifestValidity is how long a published signed manifest stays
// valid: the verifier's maximum. After it expires, signed builds refuse it
// (fail closed) until the owner publishes a new release or re-signs.
const DefaultManifestValidity = maxManifestValidity

// BuildSignedManifestPayload hashes every binary and returns the exact payload
// bytes to sign: schema, product, channel, strict semver version, issue and
// expiry instants, and one HTTPS URL, SHA-256 and size per platform. It reuses
// BuildReleaseManifest so the signed and unsigned manifests can never point at
// different files or hashes.
func BuildSignedManifestPayload(version, notes, baseURL string, files []string, issuedAt time.Time, validity time.Duration) ([]byte, error) {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if !strictSemver.MatchString(v) {
		return nil, fmt.Errorf("manifest firmado: la versión %q no es semver estable (X.Y.Z); el updater firmado no acepta pre-releases", version)
	}
	if validity <= 0 || validity > maxManifestValidity {
		return nil, fmt.Errorf("manifest firmado: la vigencia %s debe ser mayor que 0 y como máximo %s", validity, maxManifestValidity)
	}
	if issuedAt.IsZero() {
		return nil, errors.New("manifest firmado: falta la fecha de emisión")
	}
	rm, err := BuildReleaseManifest(v, notes, baseURL, files)
	if err != nil {
		return nil, err
	}
	issued := issuedAt.UTC().Truncate(time.Second)
	m := updateManifest{
		Schema:    manifestSchemaVersion,
		Product:   signedManifestProduct,
		Channel:   signedManifestChannel,
		Version:   v,
		IssuedAt:  issued,
		ExpiresAt: issued.Add(validity),
		Notes:     notes,
		Assets:    map[string]manifestAsset{},
	}
	for _, f := range files {
		platform, _ := ReleaseAssetPlatform(f)
		st, err := os.Stat(f)
		if err != nil {
			return nil, err
		}
		if st.Size() <= 0 || st.Size() > 150<<20 {
			return nil, fmt.Errorf("manifest firmado: tamaño inválido para %s", filepath.Base(f))
		}
		m.Assets[platform] = manifestAsset{URL: rm.DownloadURLs[platform], SHA256: rm.SHA256s[platform], Size: st.Size()}
	}
	return json.Marshal(m, json.Deterministic(true))
}

// ParseManifestSigningKey decodes the PKCS#8 PEM Ed25519 private key that
// `openssl genpkey -algorithm ed25519` writes. Errors never echo key material.
func ParseManifestSigningKey(pemBytes []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("manifest firmado: la clave de firma no es un PEM PKCS#8 (\"-----BEGIN PRIVATE KEY-----\")")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("manifest firmado: no se pudo leer la clave PKCS#8")
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("manifest firmado: la clave de firma no es Ed25519")
	}
	return priv, nil
}

// SignManifestPayload signs payload with priv and returns the
// version.signed.json envelope. The signing key must correspond to a public key
// embedded in this build (the same commit the release binaries are built
// from), and the envelope is verified for every platform before it is
// returned, so a release can never publish a manifest its own binaries reject.
func SignManifestPayload(payload []byte, priv ed25519.PrivateKey, now time.Time) (envelope []byte, keyID string, err error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, "", errors.New("manifest firmado: clave privada Ed25519 inválida")
	}
	keys, err := trustedManifestKeys()
	if err != nil {
		return nil, "", err
	}
	if len(keys) == 0 {
		return nil, "", errors.New("manifest firmado: este build no tiene ninguna clave pública embebida (embeddedManifestPublicKeys); embebé la clave pública antes de publicar un release firmado")
	}
	pub := priv.Public().(ed25519.PublicKey)
	ids := make([]string, 0, len(keys))
	for id := range keys {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if keys[id].Equal(pub) {
			keyID = id
			break
		}
	}
	if keyID == "" {
		return nil, "", errors.New("manifest firmado: la clave de firma no corresponde a ninguna clave pública embebida en este build")
	}
	sig := ed25519.Sign(priv, append([]byte(manifestSigningContext), payload...))
	envelope, err = json.Marshal(signedManifestEnvelope{
		Payload:    base64.StdEncoding.EncodeToString(payload),
		Signatures: []manifestSignature{{KeyID: keyID, Sig: base64.StdEncoding.EncodeToString(sig)}},
	})
	if err != nil {
		return nil, "", err
	}
	if err := VerifySignedManifestForRelease(envelope, now); err != nil {
		return nil, "", err
	}
	return envelope, keyID, nil
}

// VerifySignedManifestForRelease checks a version.signed.json envelope the way
// a freshly installed older build would: against the embedded keys, with every
// platform it lists, at instant now. It is the release workflow's last gate
// before publishing.
func VerifySignedManifestForRelease(envelope []byte, now time.Time) error {
	keys, err := trustedManifestKeys()
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return errors.New("manifest firmado: no hay claves públicas embebidas para verificar")
	}
	var outer signedManifestEnvelope
	if err := json.Unmarshal(envelope, &outer); err != nil {
		return fmt.Errorf("manifest firmado: sobre inválido: %w", err)
	}
	raw, err := base64.StdEncoding.DecodeString(outer.Payload)
	if err != nil {
		return errors.New("manifest firmado: payload inválido")
	}
	var m updateManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("manifest firmado: payload inválido: %w", err)
	}
	if len(m.Assets) == 0 {
		return errors.New("manifest firmado: no lista ningún binario")
	}
	for platform := range m.Assets {
		if _, err := verifySignedManifest(envelope, manifestPolicy{
			TrustedKeys:    keys,
			Product:        signedManifestProduct,
			Channel:        signedManifestChannel,
			CurrentVersion: "0.0.0",
			Platform:       platform,
			Now:            now.UTC(),
		}); err != nil {
			return fmt.Errorf("manifest firmado: no verifica para %s: %w", platform, err)
		}
	}
	return nil
}
