package updater

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The release workflow must publish the Docker image to the owner's GHCR
// package (lowercase, as GHCR requires) with the workflow token, never to the
// upstream Docker Hub repository or with personal registry secrets.
func TestReleaseWorkflowPublishesToOwnedGHCR(t *testing.T) {
	wf, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Skip("release.yml not available:", err)
	}
	workflow := string(wf)
	const image = "ghcr.io/joselofarias-byte/capimux"
	m := regexp.MustCompile(`(?m)^\s*REGISTRY_IMAGE:\s*(\S+)\s*$`).FindStringSubmatch(workflow)
	if m == nil || m[1] != image {
		t.Fatalf("REGISTRY_IMAGE = %v, want %s", m, image)
	}
	if m[1] != strings.ToLower(m[1]) {
		t.Fatalf("GHCR image names must be lowercase: %s", m[1])
	}
	for _, want := range []string{
		"packages: write",
		"registry: ghcr.io",
		"password: ${{ secrets.GITHUB_TOKEN }}",
		"images: ${{ env.REGISTRY_IMAGE }}",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("release.yml lacks %q", want)
		}
	}
	for _, banned := range []string{"luqmenul/9router-go", "DOCKERHUB_USERNAME", "DOCKERHUB_TOKEN"} {
		if strings.Contains(workflow, banned) {
			t.Errorf("release.yml still references %q", banned)
		}
	}
	readme, err := os.ReadFile("../../README.md")
	if err == nil && strings.Contains(string(readme), "luqmenul/9router-go") {
		t.Error("README still points users at the upstream Docker Hub image")
	}
}
