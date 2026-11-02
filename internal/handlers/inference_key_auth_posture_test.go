package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"9router/proxy/internal/auth"
	"9router/proxy/internal/db"
)

// A client (inference) API key passes RequireDashboardAuth for ordinary
// configuration, but it must never be able to take over or weaken dashboard
// authentication: set the password, turn the login off, switch to SSO, let
// or let tunnel hosts in. All credentials are synthetic; no outbound call is
// made. Tailscale/Funnel enablement is covered with stubbed host commands in
// internal/handlers/dashboard (TestHandleTailscaleEnable_NonAdminNeverExecs):
// hitting it here through the real router would run the host's tailscale CLI
// whenever the guard regressed.
func TestSetupServerRouter_InferenceKeyCannotWeakenDashboardAuth(t *testing.T) {
	t.Setenv("JWT_SECRET", "auth-posture-test-secret")
	t.Setenv("DATA_DIR", t.TempDir())
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	if _, err := database.Exec(`INSERT INTO apiKeys (id, key, name, isActive, createdAt) VALUES ('posture-client', 'posture-engine-key', 'chatbox', 1, '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed inference API key: %v", err)
	}
	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": true}); err != nil {
		t.Fatalf("enable dashboard login: %v", err)
	}

	r := chi.NewRouter()
	SetupServerRouter(r, repo, nil)

	do := func(method, path, body string, setAuth func(*http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if setAuth != nil {
			setAuth(req)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	engineKey := func(req *http.Request) { req.Header.Set("Authorization", "Bearer posture-engine-key") }
	settings := func() map[string]any {
		t.Helper()
		raw, err := repo.GetSettingsRaw()
		if err != nil {
			t.Fatalf("read settings: %v", err)
		}
		return raw
	}

	for _, tc := range []struct {
		name, method, path, body string
	}{
		{"set password without current", http.MethodPut, "/api/settings", `{"newPassword":"attacker-pass"}`},
		{"disable login", http.MethodPatch, "/api/settings", `{"requireLogin":false}`},
		{"disable login legacy path", http.MethodPut, "/settings", `{"requireLogin":false}`},
		{"open tunnel dashboard", http.MethodPatch, "/api/settings", `{"tunnelDashboardAccess":true}`},
		{"switch to sso", http.MethodPatch, "/api/settings", `{"authMode":"sso","ssoType":"oidc","oidcIssuerUrl":"https://idp.example.invalid","oidcClientId":"x"}`},
		{"rewrite tailscale host", http.MethodPatch, "/api/settings", `{"tailscaleUrl":"https://other.example.invalid"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(tc.method, tc.path, tc.body, engineKey)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403 for inference key, got %d: %s", rec.Code, rec.Body.String())
			}
			s := settings()
			if v, ok := s["requireLogin"].(bool); !ok || !v {
				t.Fatalf("requireLogin changed: %#v", s["requireLogin"])
			}
			for _, k := range []string{"password", "authMode", "tunnelDashboardAccess", "tailscaleUrl", "oidcIssuerUrl"} {
				if _, ok := s[k]; ok {
					t.Fatalf("inference key wrote %s", k)
				}
			}
		})
	}

	// Ordinary configuration with the same key keeps working (compatibility),
	// including a full-form save that re-sends an unchanged requireLogin.
	if rec := do(http.MethodPatch, "/api/settings", `{"language":"es","requireLogin":true}`, engineKey); rec.Code != http.StatusOK {
		t.Fatalf("ordinary settings write with inference key: %d %s", rec.Code, rec.Body.String())
	}
	if got := settings()["language"]; got != "es" {
		t.Fatalf("language not saved: %#v", got)
	}

	// A real admin session may still change the posture.
	session, err := auth.Sign(auth.Secret(), time.Now())
	if err != nil {
		t.Fatalf("sign admin session: %v", err)
	}
	withSession := func(req *http.Request) { req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session}) }
	if rec := do(http.MethodPatch, "/api/settings", `{"tunnelDashboardAccess":true}`, withSession); rec.Code != http.StatusOK {
		t.Fatalf("admin session posture change: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodPut, "/api/settings", `{"newPassword":"owner-pass"}`, withSession); rec.Code != http.StatusOK {
		t.Fatalf("admin session password set: %d %s", rec.Code, rec.Body.String())
	}
	// So may the on-device CLI token.
	withCLI := func(req *http.Request) { req.Header.Set(auth.CLITokenHeader, auth.CLIToken()) }
	if rec := do(http.MethodPatch, "/api/settings", `{"requireLogin":false}`, withCLI); rec.Code != http.StatusOK {
		t.Fatalf("CLI token posture change: %d %s", rec.Code, rec.Body.String())
	}

	// With login intentionally off the dashboard is open by the owner's choice;
	// that existing behaviour is preserved.
	if rec := do(http.MethodPatch, "/api/settings", `{"requireLogin":true}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("open dashboard re-enabling login: %d %s", rec.Code, rec.Body.String())
	}
}
