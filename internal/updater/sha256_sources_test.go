package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

// These tests never call PerformSelfUpdate on a success path: that would
// replace the running test binary. The success contract is exercised through
// verifyDownloadedUpdate, which PerformSelfUpdate uses before touching disk.

func shaSourcesTarGz(t *testing.T, name string, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestManifestPerPlatformSHA256(t *testing.T) {
	platform := runtime.GOOS + "_" + runtime.GOARCH
	mine := strings.Repeat("a", 64)
	other := strings.Repeat("b", 64)
	cases := []struct {
		name     string
		manifest map[string]any
		want     string
	}{
		{"per-platform wins over legacy single", map[string]any{
			"latestVersion": "99.0.0",
			"downloadUrls":  map[string]string{platform: "https://example.invalid/mine", "plan9_mips": "https://example.invalid/other"},
			"sha256":        other,
			"sha256s":       map[string]string{platform: mine, "plan9_mips": other},
		}, mine},
		{"default entry", map[string]any{
			"latestVersion": "99.0.0",
			"downloadUrl":   "https://example.invalid/any",
			"sha256s":       map[string]string{"default": mine},
		}, mine},
		{"legacy single sha256 still honoured", map[string]any{
			"latestVersion": "99.0.0",
			"downloadUrl":   "https://example.invalid/any",
			"sha256":        mine,
		}, mine},
		{"no digest stays empty (and PerformSelfUpdate refuses it)", map[string]any{
			"latestVersion": "99.0.0",
			"downloadUrl":   "https://example.invalid/any",
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.MarshalWrite(w, tc.manifest)
			}))
			defer srv.Close()
			t.Setenv("UPDATE_URL", srv.URL)
			info, err := CheckUpdate(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if info.SHA256 != tc.want {
				t.Fatalf("SHA256 = %q, want %q", info.SHA256, tc.want)
			}
		})
	}
}

func TestGitHubReleasesFallbackUsesAssetDigest(t *testing.T) {
	platformAsset := "capimux-" + runtime.GOOS + "-" + runtime.GOARCH
	digest := strings.Repeat("c", 64)
	cases := []struct {
		name   string
		digest string
		want   string
	}{
		{"sha256 digest", "sha256:" + digest, digest},
		{"uppercase prefix and hex", "SHA256:" + strings.ToUpper(digest), digest},
		{"non-sha256 digest ignored", "md5:" + digest, ""},
		{"missing digest", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.MarshalWrite(w, map[string]any{
					"tag_name": "v99.0.0",
					"assets": []map[string]any{
						{"name": "capimux-plan9-mips", "browser_download_url": "https://example.invalid/other", "digest": "sha256:" + strings.Repeat("d", 64)},
						{"name": platformAsset, "browser_download_url": "https://example.invalid/mine", "digest": tc.digest},
					},
				})
			}))
			defer srv.Close()
			info, err := checkGitHubReleases(context.Background(), srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			if info.DownloadURL != "https://example.invalid/mine" {
				t.Fatalf("DownloadURL = %q", info.DownloadURL)
			}
			if info.SHA256 != tc.want {
				t.Fatalf("SHA256 = %q, want %q (must never borrow another asset's digest)", info.SHA256, tc.want)
			}
		})
	}
}

func TestVerifyDownloadedUpdate(t *testing.T) {
	exe := append([]byte{0x7f, 'E', 'L', 'F'}, bytes.Repeat([]byte("capimux"), 512)...)
	archive := shaSourcesTarGz(t, "capimux", exe)
	exeSHA := ComputeSHA256(exe)
	archiveSHA := ComputeSHA256(archive)

	accept := []struct {
		name, url, sha string
		raw            []byte
		archive        bool
	}{
		{"raw binary, executable digest", "https://x/capimux-linux-amd64", exeSHA, exe, true},
		{"archive, executable digest (manifest contract)", "https://x/capimux.tar.gz", exeSHA, archive, false},
		{"archive, archive digest (GitHub asset digest)", "https://x/capimux.tar.gz", archiveSHA, archive, true},
	}
	for _, tc := range accept {
		t.Run("accept/"+tc.name, func(t *testing.T) {
			got, archiveVerified, err := verifyDownloadedUpdate(tc.raw, tc.url, tc.sha)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, exe) || archiveVerified != tc.archive {
				t.Fatalf("got %d bytes archiveVerified=%v", len(got), archiveVerified)
			}
		})
	}

	reject := []struct{ name, sha string }{
		{"missing", ""},
		{"short", "abc"},
		{"non-hex", strings.Repeat("z", 64)},
		{"mismatch", strings.Repeat("0", 64)},
		{"digest of another platform", ComputeSHA256([]byte("other platform"))},
	}
	for _, tc := range reject {
		t.Run("reject/"+tc.name, func(t *testing.T) {
			got, _, err := verifyDownloadedUpdate(archive, "https://x/capimux.tar.gz", tc.sha)
			if err == nil || got != nil {
				t.Fatalf("accepted update with %s checksum", tc.name)
			}
		})
	}
}
