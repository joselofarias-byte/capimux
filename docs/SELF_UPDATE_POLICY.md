# Fork self-update policy

This fork carries a validated Fabric control-plane delta on top of upstream `luqman-v1/9router-go`. A fork binary must therefore never silently replace itself with an upstream-only release.

## Defaults

- `DefaultUpdateURL` points to the `version.json` asset of this fork's latest GitHub release (`https://github.com/joselofarias-byte/capimux/releases/latest/download/version.json`). The older raw `main/version.json` URLs (both repository names) are mapped to it, so binaries configured with them follow the release manifest.
- `DefaultGitHubRepo` points to `joselofarias-byte/capimux` (the fork was renamed from `joselofarias-byte/9router-go`; an `UPDATE_REPO`/`UPDATE_URL` that still names the old repository is mapped to the new one instead of relying on GitHub's rename redirect).
- Until the fork publishes a release, that URL and the GitHub releases API both answer 404, which the updater treats as "up to date": nothing is downloaded.

## Release manifest

Every tagged release (`release.yml`) builds all `CROSS_TARGETS` (including `android-arm64` for Termux), verifies `SHA256SUMS.txt`, and generates `version.json` with `go run ./cmd/release-manifest`:

```json
{
  "latestVersion": "1.9.7",
  "downloadUrls": { "linux_arm64": "https://github.com/joselofarias-byte/capimux/releases/download/v1.9.7/9router-go-linux-arm64", "...": "..." },
  "sha256s":      { "linux_arm64": "<64 hex>", "...": "..." }
}
```

The updater installs only when it finds a valid SHA-256 for its own `GOOS_GOARCH` and the download matches it. The generator fails the release (nothing is published) for a non-tag version, a non-https base URL, an unrecognised file, two binaries for one platform, or any hash that disagrees with `SHA256SUMS.txt`.
- `AUTO_UPDATE` remains off by default.

This is fail-closed behavior: an update can be detected without allowing an untrusted or wrong-channel binary to replace the running Fabric build.

## Explicit overrides

Operators may deliberately choose another trusted channel with:

- `UPDATE_URL`
- `UPDATE_REPO`

Those overrides are explicit operator decisions and are not applied automatically.

## Termux deployment

For Termux-native Android arm64, use the CI-validated `9router-go-termux-arm64-*` artifact produced from successful pushes to this fork's `main` branch until first-party fork release assets are available. From the first release built with this workflow on, the `9router-go-android-arm64` release asset is published with its SHA-256 and `9router-go update` can install it.
