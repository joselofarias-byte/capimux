package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"9router/proxy/internal/auth"
	"9router/proxy/internal/db"
	"9router/proxy/internal/handlers/dashboard"
)

// Router-level Tailscale coverage that can never touch the host's tailnet: the
// only executable the handlers may run is a fake script that records its
// arguments (any other binary panics inside tests, see
// dashboard.TestDefaultTailscaleSeams_NeverTouchHostInTests).
func TestSetupServerRouter_TailscaleEnableDisable_FakeCLIOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake tailscale script needs a POSIX shell")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	fake := filepath.Join(dir, "tailscale")
	script := "#!/bin/sh\necho \"$*\" >> '" + logPath + "'\n" +
		"if [ \"$1\" = status ]; then printf '%s' '{\"BackendState\":\"Running\",\"Self\":{\"DNSName\":\"fake.tailnet.example.ts.net.\"}}'; fi\n" +
		"if [ \"$1\" = funnel ] && [ \"$3\" != reset ]; then echo 'available at https://fake.tailnet.example.ts.net/'; fi\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(dashboard.FakeTailscaleEnv, fake)
	t.Setenv("JWT_SECRET", "tailscale-fake-test-secret")
	t.Setenv("DATA_DIR", t.TempDir())

	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	if _, err := database.Exec(`INSERT INTO apiKeys (id, key, name, isActive, createdAt) VALUES ('ts-client', 'ts-engine-key', 'chatbox', 1, '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": true}); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	SetupServerRouter(r, repo, nil)
	do := func(path string, setAuth func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		setAuth(req)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	calls := func() string { b, _ := os.ReadFile(logPath); return string(b) }

	engineKey := func(req *http.Request) { req.Header.Set("Authorization", "Bearer ts-engine-key") }
	for _, p := range []string{"/api/tunnel/tailscale-enable", "/api/tunnel/tailscale-disable"} {
		if rec := do(p, engineKey); rec.Code != http.StatusForbidden {
			t.Fatalf("%s with inference key: %d %s", p, rec.Code, rec.Body.String())
		}
	}
	if c := calls(); c != "" {
		t.Fatalf("inference key reached the tailscale CLI: %q", c)
	}

	session, err := auth.Sign(auth.Secret(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	withSession := func(req *http.Request) { req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session}) }
	if rec := do("/api/tunnel/tailscale-enable", withSession); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "fake.tailnet.example.ts.net") {
		t.Fatalf("admin enable via fake CLI: %d %s", rec.Code, rec.Body.String())
	}
	if c := calls(); !strings.Contains(c, "funnel --bg reset") || !strings.Contains(c, "status --json") {
		t.Fatalf("fake CLI calls = %q", c)
	}
	if rec := do("/api/tunnel/tailscale-disable", withSession); rec.Code != http.StatusOK {
		t.Fatalf("admin disable via fake CLI: %d %s", rec.Code, rec.Body.String())
	}
}
