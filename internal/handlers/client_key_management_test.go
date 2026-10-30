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

// One client API key must not be able to manage the others: minting a key it
// controls (surviving its own revocation), deleting or disabling the owner's
// keys. Only an admin session or the local CLI token may, unless an admin opts
// in with allowClientKeyManagement. All keys are synthetic.
func TestSetupServerRouter_ClientKeyCannotManageKeysWithoutOptIn(t *testing.T) {
	t.Setenv("JWT_SECRET", "key-management-test-secret")
	t.Setenv("DATA_DIR", t.TempDir())
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	for _, q := range []string{
		`INSERT INTO apiKeys (id, key, name, isActive, createdAt) VALUES ('key-a', 'sk-client-a-synthetic', 'chatbox', 1, '2026-01-01T00:00:00Z')`,
		`INSERT INTO apiKeys (id, key, name, isActive, createdAt) VALUES ('key-b', 'sk-client-b-synthetic', 'opencode', 1, '2026-01-01T00:00:00Z')`,
	} {
		if _, err := database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": true}); err != nil {
		t.Fatal(err)
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
	keyA := func(req *http.Request) { req.Header.Set("Authorization", "Bearer sk-client-a-synthetic") }
	keyCount := func() int {
		keys, err := repo.GetApiKeys()
		if err != nil {
			t.Fatal(err)
		}
		return len(keys)
	}
	keyBActive := func() bool {
		var active int
		if err := database.QueryRow(`SELECT isActive FROM apiKeys WHERE id='key-b'`).Scan(&active); err != nil {
			t.Fatalf("key-b gone: %v", err)
		}
		return active == 1
	}

	for _, tc := range []struct{ name, method, path, body string }{
		{"mint own replacement", http.MethodPost, "/api/keys", `{"name":"backdoor","key":"sk-attacker-chosen"}`},
		{"delete other key", http.MethodDelete, "/api/keys/key-b", ``},
		{"disable other key", http.MethodPut, "/api/keys/key-b/toggle", `{"isActive":false}`},
		{"grant itself the opt-in", http.MethodPatch, "/api/settings", `{"allowClientKeyManagement":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec := do(tc.method, tc.path, tc.body, keyA); rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
			}
			if n := keyCount(); n != 2 {
				t.Fatalf("key count changed to %d", n)
			}
			if !keyBActive() {
				t.Fatal("key-b was disabled")
			}
		})
	}

	// Listing stays available (masked) for CLI compatibility.
	if rec := do(http.MethodGet, "/api/keys", ``, keyA); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "sk-client-b-synthetic") {
		t.Fatalf("masked list: %d %s", rec.Code, rec.Body.String())
	}

	// The CLI token (Termux/APK) and an admin session keep full control.
	withCLI := func(req *http.Request) { req.Header.Set(auth.CLITokenHeader, auth.CLIToken()) }
	if rec := do(http.MethodPost, "/api/keys", `{"name":"via-cli"}`, withCLI); rec.Code != http.StatusOK {
		t.Fatalf("CLI create: %d %s", rec.Code, rec.Body.String())
	}
	session, err := auth.Sign(auth.Secret(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	withSession := func(req *http.Request) { req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session}) }

	// Explicit opt-in by the admin lets client keys manage keys again.
	if rec := do(http.MethodPatch, "/api/settings", `{"allowClientKeyManagement":true}`, withSession); rec.Code != http.StatusOK {
		t.Fatalf("admin opt-in: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodPut, "/api/keys/key-b/toggle", `{"isActive":false}`, keyA); rec.Code != http.StatusOK {
		t.Fatalf("opted-in toggle: %d %s", rec.Code, rec.Body.String())
	}
	if keyBActive() {
		t.Fatal("opted-in toggle did not apply")
	}
	if rec := do(http.MethodDelete, "/api/keys/key-b", ``, withSession); rec.Code != http.StatusOK {
		t.Fatalf("admin delete: %d %s", rec.Code, rec.Body.String())
	}
}
