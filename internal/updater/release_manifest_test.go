package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	stdjson "encoding/json"
)

func TestReleaseAssetPlatform(t *testing.T) {
	ok := map[string]string{
		"9router-go-linux-amd64":       "linux_amd64",
		"9router-go-linux-arm64":       "linux_arm64",
		"9router-go-darwin-arm64":      "darwin_arm64",
		"9router-go-windows-amd64.exe": "windows_amd64",
		"9router-go-android-arm64":     "android_arm64",
		"dist/capimux-linux-386":       "linux_386",
	}
	for name, want := range ok {
		if got, valid := ReleaseAssetPlatform(name); !valid || got != want {
			t.Errorf("ReleaseAssetPlatform(%q) = %q,%v want %q", name, got, valid, want)
		}
	}
	for _, name := range []string{
		"SHA256SUMS.txt", "version.json", "9router-go-linux-amd64.tar.gz", "9router-go-linux-amd64.sha256",
		"9router-go-plan9-amd64", "9router-go-linux-mips", "9router-go-windows-amd64", "9router-go-linux-amd64.exe", "linux-amd64",
	} {
		if got, valid := ReleaseAssetPlatform(name); valid {
			t.Errorf("ReleaseAssetPlatform(%q) accepted as %q", name, got)
		}
	}
}

func writeReleaseFiles(t *testing.T, names ...string) (string, []string, map[string][]byte) {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	content := map[string][]byte{}
	for _, n := range names {
		b := append([]byte{0x7f, 'E', 'L', 'F'}, bytes.Repeat([]byte(n), 400)...)
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, b, 0o755); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
		content[n] = b
	}
	return dir, paths, content
}

func TestBuildReleaseManifestRejectsBrokenReleases(t *testing.T) {
	_, good, _ := writeReleaseFiles(t, "9router-go-linux-amd64")
	_, dup, _ := writeReleaseFiles(t, "9router-go-linux-amd64", "capimux-linux-amd64")
	_, odd, _ := writeReleaseFiles(t, "9router-go-linux-amd64", "README.txt")
	cases := []struct {
		name, version, base string
		files               []string
	}{
		{"empty version", "", "https://x/dl", good},
		{"branch name instead of tag (workflow_dispatch)", "main", "https://x/dl", good},
		{"plain http base", "v1.2.3", "http://x/dl", good},
		{"no binaries", "v1.2.3", "https://x/dl", nil},
		{"two binaries for one platform", "v1.2.3", "https://x/dl", dup},
		{"unclassifiable file", "v1.2.3", "https://x/dl", odd},
		{"missing file", "v1.2.3", "https://x/dl", []string{filepath.Join(t.TempDir(), "9router-go-linux-arm64")}},
	}
	for _, tc := range cases {
		if m, err := BuildReleaseManifest(tc.version, "", tc.base, tc.files); err == nil {
			t.Errorf("%s: accepted, manifest %+v", tc.name, m)
		}
	}
}

func TestReleaseManifestVerifyAgainstSHA256SUMS(t *testing.T) {
	dir, paths, content := writeReleaseFiles(t, "9router-go-linux-amd64", "9router-go-windows-amd64.exe")
	m, err := BuildReleaseManifest("v1.2.3", "", "https://x/dl", paths)
	if err != nil {
		t.Fatal(err)
	}
	sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	write := func(body string) string {
		p := filepath.Join(dir, "SHA256SUMS-"+strings.ReplaceAll(t.Name(), "/", "_")+".txt")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := sum(content["9router-go-linux-amd64"]) + "  9router-go-linux-amd64\n" + sum(content["9router-go-windows-amd64.exe"]) + " *9router-go-windows-amd64.exe\n"
	if err := m.VerifyAgainstSHA256SUMS(write(good)); err != nil {
		t.Fatalf("matching sums refused: %v", err)
	}
	missing := sum(content["9router-go-linux-amd64"]) + "  9router-go-linux-amd64\n"
	if err := m.VerifyAgainstSHA256SUMS(write(missing)); err == nil {
		t.Fatal("sums file without the windows binary accepted")
	}
	wrong := strings.Repeat("0", 64) + "  9router-go-linux-amd64\n" + sum(content["9router-go-windows-amd64.exe"]) + "  9router-go-windows-amd64.exe\n"
	if err := m.VerifyAgainstSHA256SUMS(write(wrong)); err == nil {
		t.Fatal("sums file with a different hash accepted")
	}
}

// End to end without network: a manifest generated exactly like the release
// workflow does is served locally; the updater must pick this platform's URL
// and hash, and the downloaded binary must pass the mandatory verification.
// Then a tampered binary on the same URL must be refused.
func TestReleaseManifestRoundTripThroughUpdater(t *testing.T) {
	platform := runtime.GOOS + "-" + runtime.GOARCH
	names := []string{"9router-go-linux-amd64", "9router-go-linux-arm64", "9router-go-darwin-arm64", "9router-go-windows-amd64.exe", "9router-go-android-arm64"}
	own := "9router-go-" + platform
	if runtime.GOOS == "windows" {
		own += ".exe"
	}
	if !strings.Contains(strings.Join(names, " "), own) {
		names = append(names, own)
	}
	_, paths, content := writeReleaseFiles(t, names...)

	var served = content
	tampered := false
	mux := http.NewServeMux()
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	m, err := BuildReleaseManifest("v99.0.0", "notes", srv.URL+"/download/v99.0.0", paths)
	if err != nil {
		t.Fatal(err)
	}
	mux.HandleFunc("/latest/version.json", func(w http.ResponseWriter, r *http.Request) {
		_ = stdjson.NewEncoder(w).Encode(m)
	})
	mux.HandleFunc("/download/v99.0.0/", func(w http.ResponseWriter, r *http.Request) {
		b := served[filepath.Base(r.URL.Path)]
		if tampered {
			b = append(append([]byte{}, b...), "evil"...)
		}
		_, _ = w.Write(b)
	})

	t.Setenv("UPDATE_URL", srv.URL+"/latest/version.json")
	info, err := checkManifestWithClient(context.Background(), srv.URL+"/latest/version.json", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	key := runtime.GOOS + "_" + runtime.GOARCH
	if info.DownloadURL != m.DownloadURLs[key] || info.SHA256 != m.SHA256s[key] {
		t.Fatalf("updater picked %q/%q, want this platform's %q/%q", info.DownloadURL, info.SHA256, m.DownloadURLs[key], m.SHA256s[key])
	}
	fetch := func() []byte {
		resp, err := srv.Client().Get(info.DownloadURL)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return b
	}
	if _, _, err := verifyDownloadedUpdate(fetch(), info.DownloadURL, info.SHA256); err != nil {
		t.Fatalf("legit release refused: %v", err)
	}
	tampered = true
	if _, _, err := verifyDownloadedUpdate(fetch(), info.DownloadURL, info.SHA256); err == nil {
		t.Fatal("tampered binary accepted")
	}
}

func TestGitHubReleasesNoReleaseIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	info, err := checkGitHubReleases(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("404 releases/latest reported as failure: %v", err)
	}
	if info.HasUpdate || info.DownloadURL != "" || info.SHA256 != "" {
		t.Fatalf("no-release answer offered an update: %+v", info)
	}
	// Other failures stay errors.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer bad.Close()
	if _, err := checkGitHubReleases(context.Background(), bad.URL); err == nil {
		t.Fatal("500 from releases API was not an error")
	}
}

func TestForkManifestDefaultIsTheReleaseAsset(t *testing.T) {
	if DefaultUpdateURL != "https://github.com/joselofarias-byte/capimux/releases/latest/download/version.json" {
		t.Fatalf("DefaultUpdateURL = %q", DefaultUpdateURL)
	}
	for _, old := range []string{legacyForkManifestURL, forkRawManifestURL} {
		if got := canonicalUpdateURL(old); got != DefaultUpdateURL {
			t.Errorf("canonicalUpdateURL(%q) = %q, want the release manifest", old, got)
		}
	}
}

// The release workflow must hash and upload every binary `make cross` builds,
// and every one of them must map to an updater platform key; otherwise that
// platform silently stops auto-updating.
func TestReleaseWorkflowPublishesEveryCrossTarget(t *testing.T) {
	mk, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Skip("Makefile not available:", err)
	}
	wf, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Skip("release.yml not available:", err)
	}
	var targets []string
	for _, line := range strings.Split(string(mk), "\n") {
		if strings.HasPrefix(line, "CROSS_TARGETS") {
			_, v, _ := strings.Cut(line, "=")
			targets = strings.Fields(strings.TrimPrefix(v, "="))
		}
	}
	if len(targets) == 0 {
		t.Fatal("CROSS_TARGETS not found in Makefile")
	}
	workflow := string(wf)
	for _, want := range []string{"go run ./cmd/release-manifest", "sha256sum -c SHA256SUMS.txt", "version.json"} {
		if !strings.Contains(workflow, want) {
			t.Errorf("release.yml lacks %q", want)
		}
	}
	seen := map[string]bool{}
	for _, tgt := range targets {
		name := "9router-go-" + tgt
		if strings.HasPrefix(tgt, "windows-") {
			name += ".exe"
		}
		if strings.Count(workflow, name) < 2 {
			t.Errorf("release.yml must both hash and upload %s", name)
		}
		key, ok := ReleaseAssetPlatform(name)
		if !ok {
			t.Errorf("%s does not map to an updater platform", name)
		}
		seen[key] = true
	}
	if !seen["android_arm64"] {
		t.Error("Termux (android_arm64) is not built for releases")
	}
}
