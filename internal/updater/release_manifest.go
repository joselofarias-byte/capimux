package updater

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ReleaseManifest is the version.json the release workflow publishes next to
// the binaries. checkManifest reads exactly these fields: one download URL and
// one SHA-256 per platform key ("linux_arm64", "android_arm64", …), so the
// mandatory-hash updater can verify whichever binary it downloads.
type ReleaseManifest struct {
	LatestVersion string            `json:"latestVersion"`
	ReleaseNotes  string            `json:"releaseNotes,omitempty"`
	DownloadURLs  map[string]string `json:"downloadUrls"`
	SHA256s       map[string]string `json:"sha256s"`
}

var (
	releaseVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+([-+][0-9A-Za-z.-]+)?$`)
	releaseOSes           = map[string]bool{"linux": true, "darwin": true, "windows": true, "android": true, "freebsd": true}
	releaseArches         = map[string]bool{"amd64": true, "arm64": true, "386": true, "arm": true}
)

// ReleaseAssetPlatform maps a release binary name such as
// "9router-go-linux-arm64" or "9router-go-windows-amd64.exe" to its updater
// platform key ("linux_arm64"). Checksums, notes and archives are not
// binaries and report false.
func ReleaseAssetPlatform(name string) (string, bool) {
	base := filepath.Base(name)
	if strings.HasSuffix(base, ".exe") {
		base = strings.TrimSuffix(base, ".exe")
	} else if strings.Contains(base, ".") {
		return "", false
	}
	parts := strings.Split(base, "-")
	if len(parts) < 3 {
		return "", false
	}
	goos, goarch := parts[len(parts)-2], parts[len(parts)-1]
	if !releaseOSes[goos] || !releaseArches[goarch] {
		return "", false
	}
	if goos == "windows" != strings.HasSuffix(name, ".exe") {
		return "", false
	}
	return goos + "_" + goarch, true
}

// BuildReleaseManifest hashes every binary and builds the manifest that points
// each platform at baseURL/<file name>. It refuses an empty version, a file it
// cannot classify, and two files for the same platform, so a broken release
// cannot publish a manifest that silently omits or mislabels a binary.
func BuildReleaseManifest(version, notes, baseURL string, files []string) (*ReleaseManifest, error) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if !releaseVersionPattern.MatchString(version) {
		return nil, fmt.Errorf("release manifest: version %q is not a release tag like v1.9.7", version)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("release manifest: no binaries")
	}
	baseURL = strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	if !strings.HasPrefix(baseURL, "https://") {
		return nil, fmt.Errorf("release manifest: base URL must be https, got %q", baseURL)
	}
	m := &ReleaseManifest{LatestVersion: version, ReleaseNotes: notes, DownloadURLs: map[string]string{}, SHA256s: map[string]string{}}
	for _, f := range files {
		platform, ok := ReleaseAssetPlatform(f)
		if !ok {
			return nil, fmt.Errorf("release manifest: %s is not a <name>-<os>-<arch> binary", f)
		}
		if _, dup := m.SHA256s[platform]; dup {
			return nil, fmt.Errorf("release manifest: two binaries for %s", platform)
		}
		sum, err := sha256File(f)
		if err != nil {
			return nil, err
		}
		m.DownloadURLs[platform] = baseURL + "/" + filepath.Base(f)
		m.SHA256s[platform] = sum
	}
	return m, nil
}

// VerifyAgainstSHA256SUMS cross-checks the manifest with the `sha256sum`
// output the release already publishes, so the two can never disagree.
func (m *ReleaseManifest) VerifyAgainstSHA256SUMS(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sums := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			continue
		}
		sums[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
	}
	if err := sc.Err(); err != nil {
		return err
	}
	platforms := make([]string, 0, len(m.DownloadURLs))
	for p := range m.DownloadURLs {
		platforms = append(platforms, p)
	}
	sort.Strings(platforms)
	for _, p := range platforms {
		name := filepath.Base(m.DownloadURLs[p])
		if got, ok := sums[name]; !ok {
			return fmt.Errorf("release manifest: %s missing from %s", name, filepath.Base(path))
		} else if got != m.SHA256s[p] {
			return fmt.Errorf("release manifest: %s hash differs from %s", name, filepath.Base(path))
		}
	}
	return nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
