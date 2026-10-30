package updater

import (
	"strings"
	"testing"
)

// The fork was renamed to capimux. Defaults must not depend on GitHub's
// rename redirect: if a repository named 9router-go ever exists again under
// the account, the redirect stops and every binary would trust it instead.
func TestForkDefaultsNameTheCurrentRepository(t *testing.T) {
	for name, v := range map[string]string{"DefaultUpdateURL": DefaultUpdateURL, "DefaultGitHubRepo": DefaultGitHubRepo} {
		if strings.Contains(v, "9router-go") {
			t.Errorf("%s = %q still names the pre-rename repository", name, v)
		}
		if !strings.Contains(v, "joselofarias-byte/capimux") {
			t.Errorf("%s = %q, want the joselofarias-byte/capimux repository", name, v)
		}
	}
}

func TestLegacyForkOverridesMapToCurrentRepository(t *testing.T) {
	repoCases := map[string]string{
		"joselofarias-byte/9router-go":   forkUpdateGitHubRepo,
		" Joselofarias-Byte/9router-go/": forkUpdateGitHubRepo,
		"joselofarias-byte/capimux":      "joselofarias-byte/capimux",
		// Any other explicit operator choice is respected verbatim.
		"luqman-v1/9router-go": "luqman-v1/9router-go",
		"someone/fork":         "someone/fork",
	}
	for in, want := range repoCases {
		if got := canonicalUpdateRepo(in); got != want {
			t.Errorf("canonicalUpdateRepo(%q) = %q, want %q", in, got, want)
		}
	}
	urlCases := map[string]string{
		"https://raw.githubusercontent.com/joselofarias-byte/9router-go/main/version.json": forkUpdateManifestURL,
		"https://raw.githubusercontent.com/luqman-v1/9router-go/main/version.json":         "https://raw.githubusercontent.com/luqman-v1/9router-go/main/version.json",
		"http://127.0.0.1:9999/version.json":                                               "http://127.0.0.1:9999/version.json",
	}
	for in, want := range urlCases {
		if got := canonicalUpdateURL(in); got != want {
			t.Errorf("canonicalUpdateURL(%q) = %q, want %q", in, got, want)
		}
	}
}
