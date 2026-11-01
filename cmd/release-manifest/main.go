// Command release-manifest writes the version.json that the release workflow
// publishes next to the binaries, with one download URL and one SHA-256 per
// platform, so the mandatory-hash updater can auto-update legitimate releases.
//
//	go run ./cmd/release-manifest -version v1.9.7 \
//	  -base-url https://github.com/OWNER/REPO/releases/download/v1.9.7 \
//	  -sums SHA256SUMS.txt -out version.json 9router-go-*
//
// With -signed-out it also writes version.signed.json (GROK-004 / OPS-01): the
// Ed25519-signed manifest that builds with an embedded public key require. The
// private key is read from the CAPIMUX_MANIFEST_SIGNING_KEY environment
// variable (the Actions secret of the same name) or from -signing-key-file for
// an offline re-sign; it is never printed. Without a key, or when the key does
// not match a public key embedded in this commit, it fails and writes nothing.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"9router/proxy/internal/updater"
)

type options struct {
	version, notes, baseURL, sums, out string
	signedOut, signingKeyFile          string
	validity                           time.Duration
	files                              []string
}

func main() {
	var o options
	flag.StringVar(&o.version, "version", "", "release version (tag), e.g. v1.9.7")
	flag.StringVar(&o.baseURL, "base-url", "", "https URL the binaries are downloaded from")
	flag.StringVar(&o.notes, "notes", "", "release notes")
	flag.StringVar(&o.sums, "sums", "", "optional SHA256SUMS.txt to cross-check")
	flag.StringVar(&o.out, "out", "version.json", "unsigned manifest output path (empty: skip)")
	flag.StringVar(&o.signedOut, "signed-out", "", "signed manifest output path, e.g. version.signed.json")
	flag.StringVar(&o.signingKeyFile, "signing-key-file", "", "PKCS#8 PEM Ed25519 key file (default: $"+updater.ManifestSigningKeyEnv+")")
	flag.DurationVar(&o.validity, "validity", updater.DefaultManifestValidity, "signed manifest validity (max 720h)")
	flag.Parse()
	o.files = flag.Args()

	if err := run(o, os.Getenv, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "release-manifest:", err)
		os.Exit(1)
	}
}

func run(o options, getenv func(string) string, now time.Time) error {
	m, err := updater.BuildReleaseManifest(o.version, o.notes, o.baseURL, o.files)
	if err != nil {
		return err
	}
	if o.sums != "" {
		if err := m.VerifyAgainstSHA256SUMS(o.sums); err != nil {
			return err
		}
	}
	var envelope []byte
	if o.signedOut != "" {
		envelope, err = signedEnvelope(o, getenv, now)
		if err != nil {
			return err
		}
	}
	if o.out != "" {
		raw, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(o.out, append(raw, '\n'), 0o644); err != nil {
			return err
		}
	}
	if envelope != nil {
		return os.WriteFile(o.signedOut, append(envelope, '\n'), 0o644)
	}
	return nil
}

func signedEnvelope(o options, getenv func(string) string, now time.Time) ([]byte, error) {
	keyPEM, err := signingKey(o.signingKeyFile, getenv)
	if err != nil {
		return nil, err
	}
	priv, err := updater.ParseManifestSigningKey(keyPEM)
	clear(keyPEM)
	if err != nil {
		return nil, err
	}
	payload, err := updater.BuildSignedManifestPayload(o.version, o.notes, o.baseURL, o.files, now, o.validity)
	if err != nil {
		return nil, err
	}
	envelope, keyID, err := updater.SignManifestPayload(payload, priv, now)
	clear(priv)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "release-manifest: %s firmado con la clave %q y verificado contra las claves embebidas\n", o.signedOut, keyID)
	return envelope, nil
}

func signingKey(file string, getenv func(string) string) ([]byte, error) {
	if file != "" {
		f, err := os.Open(file)
		if err != nil {
			return nil, errors.New("no se pudo abrir -signing-key-file")
		}
		defer f.Close()
		return io.ReadAll(io.LimitReader(f, 16<<10))
	}
	if v := getenv(updater.ManifestSigningKeyEnv); v != "" {
		return []byte(v), nil
	}
	return nil, fmt.Errorf("falta la clave de firma: configurá el secreto %s en GitHub Actions (o pasá -signing-key-file); no se publica un release sin manifest firmado", updater.ManifestSigningKeyEnv)
}
