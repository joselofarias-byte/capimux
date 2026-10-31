package updater

import (
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type workflowStep struct {
	Name            string            `yaml:"name"`
	Uses            string            `yaml:"uses"`
	If              string            `yaml:"if"`
	Run             string            `yaml:"run"`
	Env             map[string]string `yaml:"env"`
	With            map[string]any    `yaml:"with"`
	ContinueOnError any               `yaml:"continue-on-error"`
}

type workflowJob struct {
	Needs           any            `yaml:"needs"`
	If              string         `yaml:"if"`
	ContinueOnError any            `yaml:"continue-on-error"`
	Steps           []workflowStep `yaml:"steps"`
}

// Publication order (GROK-004 / OPS-01): a build that requires the signed
// manifest must never reach users without version.signed.json next to it.
// The release workflow must (1) fail before building when the signing secret
// is missing or the tag differs from VERSION, (2) sign after the binaries and their SHA256SUMS exist, (3)
// upload the signed manifest in the same draft release as the binaries, (4)
// publish the release only after checking the signed manifest is attached,
// and (5) build the Docker image only after all of that succeeded.
func TestReleaseWorkflowPublishesSignedManifestWithBinaries(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Skip("release.yml not available:", err)
	}
	var wf struct {
		Jobs map[string]workflowJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("release.yml: %v", err)
	}
	bin, ok := wf.Jobs["binaries"]
	if !ok {
		t.Fatal("release.yml has no binaries job")
	}
	if bin.If != "" || bin.ContinueOnError != nil {
		t.Fatal("the binaries job must not be conditional or allowed to fail")
	}
	index := func(pred func(workflowStep) bool) int {
		for i, s := range bin.Steps {
			if pred(s) {
				return i
			}
		}
		return -1
	}
	const secretRef = "${{ secrets.CAPIMUX_MANIFEST_SIGNING_KEY }}"
	guard := index(func(s workflowStep) bool { return s.Name == "Require manifest signing key" })
	versionCheck := index(func(s workflowStep) bool {
		return strings.Contains(s.Run, "< VERSION") && strings.Contains(s.Run, "GITHUB_REF_NAME")
	})
	build := index(func(s workflowStep) bool { return strings.Contains(s.Run, "make cross") })
	sign := index(func(s workflowStep) bool { return strings.Contains(s.Run, "-signed-out version.signed.json") })
	upload := index(func(s workflowStep) bool { return strings.HasPrefix(s.Uses, "softprops/action-gh-release") })
	publish := index(func(s workflowStep) bool { return strings.Contains(s.Run, "--draft=false") })
	if guard != 0 {
		t.Errorf("the signing-secret guard must be the first step, got index %d", guard)
	}
	if versionCheck < 0 || versionCheck > build {
		t.Error("the tag must be checked against VERSION before building, or installs would see the release as always newer")
	}
	if !(build >= 0 && build < sign && sign < upload && upload < publish) {
		t.Fatalf("step order build=%d sign=%d upload=%d publish=%d, want build < sign < upload < publish", build, sign, upload, publish)
	}
	for _, i := range []int{guard, sign} {
		s := bin.Steps[i]
		if s.Env["CAPIMUX_MANIFEST_SIGNING_KEY"] != secretRef {
			t.Errorf("step %q must take the key only from %s", s.Name, secretRef)
		}
		if !strings.Contains(s.Run, `if [ -z "${CAPIMUX_MANIFEST_SIGNING_KEY}" ]`) || !strings.Contains(s.Run, "exit 1") {
			t.Errorf("step %q must fail explicitly when the secret is empty", s.Name)
		}
		if strings.Contains(s.Run, "echo \"${CAPIMUX_MANIFEST_SIGNING_KEY") || strings.Contains(s.Run, "echo $CAPIMUX_MANIFEST_SIGNING_KEY") {
			t.Errorf("step %q prints the signing key", s.Name)
		}
	}
	for i, s := range bin.Steps {
		if s.If != "" || s.ContinueOnError != nil {
			t.Errorf("step %d (%q) is conditional or may fail silently", i, s.Name)
		}
	}
	if n := strings.Count(string(raw), "secrets.CAPIMUX_MANIFEST_SIGNING_KEY"); n != 2 {
		t.Errorf("the signing secret is referenced %d times, want exactly 2 (guard and sign steps)", n)
	}
	up := bin.Steps[upload].With
	files, _ := up["files"].(string)
	for _, want := range []string{"version.signed.json", "version.json", "SHA256SUMS.txt", "9router-go-android-arm64"} {
		if !strings.Contains(files, want) {
			t.Errorf("release upload lacks %s", want)
		}
	}
	if up["draft"] != true || up["fail_on_unmatched_files"] != true {
		t.Errorf("upload must create a draft and fail on missing files, got draft=%v fail_on_unmatched_files=%v", up["draft"], up["fail_on_unmatched_files"])
	}
	pub := bin.Steps[publish].Run
	if g, p := strings.Index(pub, "grep -qx version.signed.json"), strings.Index(pub, "--draft=false"); g < 0 || g > p {
		t.Error("the release must be published only after checking version.signed.json is attached")
	}
	docker, ok := wf.Jobs["docker"]
	if !ok {
		t.Fatal("release.yml has no docker job")
	}
	if needs, _ := docker.Needs.(string); needs != "binaries" {
		t.Errorf("docker job needs = %v, want binaries", docker.Needs)
	}
}
