package dashboard

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Regression guard for the 2026-10-29 incident: a test reached the handler
// and ran the host's real tailscale (`funnel --bg reset`). The default seams
// must never resolve or execute a real tailscale inside a test binary.
func TestDefaultTailscaleSeams_NeverTouchHostInTests(t *testing.T) {
	t.Setenv(FakeTailscaleEnv, "")
	if got := defaultTailscaleBin(); got != "" {
		t.Fatalf("test binary resolved a host tailscale: %q", got)
	}
	for _, bin := range []string{"/usr/local/bin/tailscale", "/usr/bin/tailscale", "tailscale"} {
		func() {
			defer func() {
				r := recover()
				if r == nil || !strings.Contains(r.(string), "tailscale real bloqueado") {
					t.Fatalf("exec of %q in a test did not panic with the guard: %v", bin, r)
				}
			}()
			_, _ = defaultTailscaleExec(context.Background(), bin, "funnel", "--bg", "reset")
		}()
	}
}

// writeFakeTailscale installs a fake CLI that only records its arguments.
func writeFakeTailscale(t *testing.T, statusJSON string) (bin, logPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake tailscale script needs a POSIX shell")
	}
	dir := t.TempDir()
	logPath = filepath.Join(dir, "calls.log")
	bin = filepath.Join(dir, "tailscale")
	script := "#!/bin/sh\necho \"$*\" >> '" + logPath + "'\n" +
		"if [ \"$1\" = status ]; then printf '%s' '" + statusJSON + "'; fi\n" +
		"if [ \"$1\" = funnel ] && [ \"$3\" != reset ]; then echo 'Available on the internet: https://fake.tailnet.example.ts.net/ available at'; fi\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, logPath
}

// The configured fake runs through the real exec path, and only the fake.
func TestDefaultTailscaleExec_RunsOnlyConfiguredFake(t *testing.T) {
	bin, logPath := writeFakeTailscale(t, `{"BackendState":"Running"}`)
	t.Setenv(FakeTailscaleEnv, bin)
	if got := defaultTailscaleBin(); got != bin {
		t.Fatalf("expected fake bin, got %q", got)
	}
	if _, err := defaultTailscaleExec(context.Background(), bin, "status", "--json"); err != nil {
		t.Fatalf("fake exec: %v", err)
	}
	raw, _ := os.ReadFile(logPath)
	if strings.TrimSpace(string(raw)) != "status --json" {
		t.Fatalf("fake log = %q", raw)
	}
}
