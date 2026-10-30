// Command release-manifest writes the version.json that the release workflow
// publishes next to the binaries, with one download URL and one SHA-256 per
// platform, so the mandatory-hash updater can auto-update legitimate releases.
//
//	go run ./cmd/release-manifest -version v1.9.7 \
//	  -base-url https://github.com/OWNER/REPO/releases/download/v1.9.7 \
//	  -sums SHA256SUMS.txt -out version.json 9router-go-*
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"9router/proxy/internal/updater"
)

func main() {
	version := flag.String("version", "", "release version (tag), e.g. v1.9.7")
	baseURL := flag.String("base-url", "", "https URL the binaries are downloaded from")
	notes := flag.String("notes", "", "release notes")
	sums := flag.String("sums", "", "optional SHA256SUMS.txt to cross-check")
	out := flag.String("out", "version.json", "output path")
	flag.Parse()

	if err := run(*version, *notes, *baseURL, *sums, *out, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "release-manifest:", err)
		os.Exit(1)
	}
}

func run(version, notes, baseURL, sums, out string, files []string) error {
	m, err := updater.BuildReleaseManifest(version, notes, baseURL, files)
	if err != nil {
		return err
	}
	if sums != "" {
		if err := m.VerifyAgainstSHA256SUMS(sums); err != nil {
			return err
		}
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(out, append(raw, '\n'), 0o644)
}
