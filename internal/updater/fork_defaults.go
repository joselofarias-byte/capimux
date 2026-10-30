package updater

import "strings"

// Fork-safe update defaults prevent a Fabric-enabled build from silently
// replacing itself with an upstream-only binary. Operators can still override
// these values explicitly with UPDATE_URL and UPDATE_REPO.
//
// The fork was renamed from joselofarias-byte/9router-go to
// joselofarias-byte/capimux. GitHub redirects the old name today, but that
// redirect disappears the moment a repository with the old name exists again,
// and from then on every installed binary would trust whatever it publishes.
// Defaults therefore name the current repository, and an operator override
// that still names the old one is mapped to it.
const (
	forkUpdateManifestURL = "https://raw.githubusercontent.com/joselofarias-byte/capimux/main/version.json"
	forkUpdateGitHubRepo  = "joselofarias-byte/capimux"
	forkVersionSuffix     = "-fabric"

	legacyForkGitHubRepo  = "joselofarias-byte/9router-go"
	legacyForkManifestURL = "https://raw.githubusercontent.com/joselofarias-byte/9router-go/main/version.json"
)

// canonicalUpdateRepo maps the pre-rename fork name to the current one so an
// existing UPDATE_REPO keeps working without depending on GitHub's redirect.
func canonicalUpdateRepo(repo string) string {
	if strings.EqualFold(strings.Trim(strings.TrimSpace(repo), "/"), legacyForkGitHubRepo) {
		return forkUpdateGitHubRepo
	}
	return repo
}

// canonicalUpdateURL does the same for an UPDATE_URL that still points at the
// pre-rename manifest.
func canonicalUpdateURL(u string) string {
	if strings.EqualFold(strings.TrimSpace(u), legacyForkManifestURL) {
		return forkUpdateManifestURL
	}
	return u
}

func init() {
	DefaultUpdateURL = forkUpdateManifestURL
	DefaultGitHubRepo = forkUpdateGitHubRepo

	// Keep the upstream numeric version as the update-comparison base while
	// making the running fork unmistakable in logs and the `version` command.
	// The inherited semver parser intentionally ignores '-' suffixes.
	if !strings.Contains(CurrentVersion, forkVersionSuffix) {
		CurrentVersion += forkVersionSuffix
	}
}
