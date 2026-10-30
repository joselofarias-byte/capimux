package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"9router/proxy/internal/auth"
	"9router/proxy/internal/db"
)

// credRouter builds the real server router over a temp DB with login
// enforced, one client API key and one owner connection. settings are merged
// into the stored settings. All values are synthetic; no handler under test
// makes an outbound request (the guarded upsert routes are rejected before
// their handler runs, and the allowed ones are local inserts).
type credRouter struct {
	t    *testing.T
	repo *db.Repo
	r    *chi.Mux
}

func newCredRouter(t *testing.T, settings map[string]any) *credRouter {
	t.Helper()
	t.Setenv("JWT_SECRET", "client-credential-import-test-secret")
	t.Setenv("DATA_DIR", t.TempDir())
	database, cleanup := setupTestDB(t)
	t.Cleanup(cleanup)
	if err := db.EnsureCoreSchema(database); err != nil {
		t.Fatalf("ensure core schema: %v", err)
	}
	repo := db.NewRepo(database)
	const ts = "2026-01-01T00:00:00Z"
	for _, q := range []string{
		`INSERT INTO apiKeys (id, key, name, isActive, createdAt) VALUES ('cred-client', 'cred-engine-key', 'workbuddy', 1, '` + ts + `')`,
		`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES ('conn-owner', 'openai', 'apikey', 'owner', 1, 1, '{"apiKey":"owner-upstream-key"}', '` + ts + `', '` + ts + `')`,
	} {
		if _, err := database.Exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	all := map[string]any{"requireLogin": true}
	for k, v := range settings {
		all[k] = v
	}
	if err := repo.UpdateSettingsRaw(all); err != nil {
		t.Fatalf("settings: %v", err)
	}
	r := chi.NewRouter()
	SetupServerRouter(r, repo, nil)
	return &credRouter{t: t, repo: repo, r: r}
}

func (c *credRouter) do(method, path, body string, setAuth func(*http.Request)) *httptest.ResponseRecorder {
	c.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if setAuth != nil {
		setAuth(req)
	}
	rec := httptest.NewRecorder()
	c.r.ServeHTTP(rec, req)
	return rec
}

func (c *credRouter) ownerKey() string {
	c.t.Helper()
	conn, err := c.repo.GetProviderConnectionByID("conn-owner")
	if err != nil || conn == nil {
		c.t.Fatalf("read owner connection: %v", err)
	}
	m := map[string]any{}
	_ = json.Unmarshal([]byte(conn.Data), &m)
	s, _ := m["apiKey"].(string)
	return s
}

func (c *credRouter) countConns() int {
	c.t.Helper()
	var n int
	if err := c.repo.RawDB().QueryRow(`SELECT COUNT(*) FROM providerConnections`).Scan(&n); err != nil {
		c.t.Fatal(err)
	}
	return n
}

func engineKeyAuth(req *http.Request) { req.Header.Set("Authorization", "Bearer cred-engine-key") }
func cliTokenAuth(req *http.Request)  { req.Header.Set(auth.CLITokenHeader, auth.CLIToken()) }

// The exact request scripts/workbuddy-termux-bootstrap.sh sends.
const workbuddyImportBody = `{"apiKey":"wb-synthetic-key","name":"WorkBuddy International"}`

// Overwriting a stored credential with only a client API key: replacing the
// owner's upstream key by name, or rewriting it in place, swaps the account
// the owner's traffic is billed to (or breaks it). Never allowed for a key,
// whatever the import setting says.
func TestClientKeyCannotOverwriteCredentials(t *testing.T) {
	for _, setting := range []any{nil, false, true} {
		name := "setting absent"
		if setting != nil {
			name = "setting " + mustJSON(t, setting)
		}
		t.Run(name, func(t *testing.T) {
			settings := map[string]any{}
			if setting != nil {
				settings[clientCredentialImportSettingForTest] = setting
			}
			c := newCredRouter(t, settings)
			for _, tc := range []struct{ name, method, path, body string }{
				{"create allowOverwrite", http.MethodPost, "/api/connections", `{"provider":"openai","name":"owner","apiKey":"attacker-key","allowOverwrite":true}`},
				{"create legacy overwrite", http.MethodPost, "/api/connections", `{"provider":"openai","name":"owner","apiKey":"attacker-key","overwrite":true}`},
				{"update data apiKey", http.MethodPut, "/api/connections/conn-owner", `{"data":{"apiKey":"attacker-key"}}`},
				{"update data string apiKey", http.MethodPut, "/api/providers/conn-owner", `{"data":"{\"apiKey\":\"attacker-key\"}"}`},
				{"update psd token", http.MethodPut, "/api/connections/conn-owner", `{"providerSpecificData":{"accessToken":"attacker-token"}}`},
			} {
				rec := c.do(tc.method, tc.path, tc.body, engineKeyAuth)
				if rec.Code != http.StatusForbidden {
					t.Errorf("%s: expected 403, got %d: %s", tc.name, rec.Code, rec.Body.String())
				}
				if got := c.ownerKey(); got != "owner-upstream-key" {
					t.Fatalf("%s: owner credential overwritten", tc.name)
				}
			}
			// Account-matching OAuth flows update an existing row in place;
			// a client key never reaches them. Bodies are empty so a handler
			// that did run would fail locally, never call out.
			for _, path := range []string{
				"/api/oauth/kiro/import", "/api/oauth/kiro/api-key", "/api/oauth/kiro/import-cli-proxy",
				"/api/oauth/kiro/social-exchange", "/api/oauth/pkce/exchange", "/api/oauth/authcode/exchange",
				"/api/oauth/device/poll", "/api/oauth/cursor/import", "/api/oauth/gitlab/pat",
				"/api/oauth/iflow/cookie", "/api/oauth/antigravity/exchange", "/api/oauth/cline/exchange",
				"/api/oauth/trae/exchange", "/api/oauth/windsurf/exchange", "/api/oauth/zed/exchange",
				"/api/oauth/kimchi/exchange", "/api/oauth/xiaomi-mimo/exchange", "/api/oauth/freebuff/poll",
				"/api/oauth/codex/cli-login/start",
			} {
				if rec := c.do(http.MethodPost, path, `{}`, engineKeyAuth); rec.Code != http.StatusForbidden {
					t.Errorf("%s: expected 403 for a client key, got %d: %s", path, rec.Code, rec.Body.String())
				}
			}
			// The Codex loopback proxy stores the account when its callback
			// lands, outside any dashboard request, so starting it is guarded.
			if rec := c.do(http.MethodGet, "/api/oauth/codex/start-proxy", "", engineKeyAuth); rec.Code != http.StatusForbidden {
				t.Errorf("codex start-proxy: expected 403 for a client key, got %d: %s", rec.Code, rec.Body.String())
			}
			// Ordinary edits with the key keep working.
			if rec := c.do(http.MethodPut, "/api/connections/conn-owner", `{"name":"renamed","priority":1,"data":{"apiKey":"owner-upstream-key"}}`, engineKeyAuth); rec.Code != http.StatusOK {
				t.Errorf("rename with unchanged key: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// Creating credentials with a client key is opt-in. A new connection would
// put the caller's account (or, with a baseUrl, the caller's server) in the
// owner's rotation and receive the owner's prompts.
func TestClientKeyCredentialCreationNeedsAdminOptIn(t *testing.T) {
	newConn := `{"provider":"openai","name":"extra","apiKey":"new-synthetic-key"}`
	withDest := `{"provider":"openai","name":"evil","apiKey":"k","providerSpecificData":{"baseUrl":"https://attacker.example.invalid/v1"}}`

	for _, closed := range []struct {
		name     string
		settings map[string]any
	}{
		{"absent (default closed)", nil},
		{"false", map[string]any{clientCredentialImportSettingForTest: false}},
	} {
		t.Run(closed.name+": no import at all", func(t *testing.T) {
			testNoClientKeyImport(t, closed.settings, newConn, withDest)
		})
	}

	t.Run("true: new credentials yes, destinations and overwrites no", func(t *testing.T) {
		testOptInImport(t, withDest)
	})
}

// testNoClientKeyImport: with the opt-in absent (the default) or false, a
// client key adds no credential through any route, Workbuddy's request
// included.
func testNoClientKeyImport(t *testing.T, settings map[string]any, newConn, withDest string) {
	c := newCredRouter(t, settings)
	before := c.countConns()
	for _, tc := range []struct{ path, body string }{
		{"/api/oauth/codebuddy-intl/import", workbuddyImportBody},
		{"/api/oauth/codex/bulk-import", `{"tokens":[{"accessToken":"t1"}]}`},
		{"/api/connections", newConn},
		{"/api/connections", withDest},
	} {
		rec := c.do(http.MethodPost, tc.path, tc.body, engineKeyAuth)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: expected 403, got %d: %s", tc.path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), clientCredentialImportSettingForTest) {
			t.Errorf("%s: the refusal should name the opt-in setting: %s", tc.path, rec.Body.String())
		}
	}
	if got := c.countConns(); got != before {
		t.Fatalf("connections = %d, want %d", got, before)
	}
}

// testOptInImport: with the opt-in true, new credentials are accepted but
// destinations and overwrites are not.
func testOptInImport(t *testing.T, withDest string) {
	c := newCredRouter(t, map[string]any{clientCredentialImportSettingForTest: true})
	for _, tc := range []struct{ path, body string }{
		{"/api/oauth/codebuddy-intl/import", workbuddyImportBody},
		{"/api/oauth/codex/bulk-import", `{"tokens":[{"accessToken":"t1"}]}`},
		{"/api/connections", `{"provider":"openai","name":"extra","apiKey":"new-synthetic-key"}`},
		{"/api/connections", `{"provider":"openai","name":"blank-dest","apiKey":"k","providerSpecificData":{"baseUrl":""}}`},
	} {
		if rec := c.do(http.MethodPost, tc.path, tc.body, engineKeyAuth); rec.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d: %s", tc.path, rec.Code, rec.Body.String())
		}
	}
	for _, body := range []string{
		withDest,
		`{"provider":"openai","name":"evil2","apiKey":"k","data":{"baseUrl":"https://attacker.example.invalid/v1"}}`,
		`{"provider":"openai","name":"evil3","apiKey":"k","proxyPoolId":"pool-x"}`,
		`{"id":"conn-owner","provider":"openai","name":"owner","apiKey":"attacker-key"}`,
	} {
		if rec := c.do(http.MethodPost, "/api/connections", body, engineKeyAuth); rec.Code != http.StatusForbidden {
			t.Errorf("expected 403 for %s, got %d: %s", body, rec.Code, rec.Body.String())
		}
	}
	if got := c.ownerKey(); got != "owner-upstream-key" {
		t.Fatal("owner credential overwritten")
	}
}

// The owner keeps every capability, and only the owner can open the gate.
func TestAdminKeepsCredentialManagementAndOwnsTheSetting(t *testing.T) {
	c := newCredRouter(t, map[string]any{clientCredentialImportSettingForTest: false})
	session, err := auth.Sign(auth.Secret(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	withSession := func(req *http.Request) { req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session}) }

	if rec := c.do(http.MethodPost, "/api/oauth/codebuddy-intl/import", workbuddyImportBody, cliTokenAuth); rec.Code != http.StatusOK {
		t.Fatalf("CLI import: %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.do(http.MethodPost, "/api/connections", `{"provider":"openai","name":"compat","apiKey":"k","providerSpecificData":{"baseUrl":"https://api.owner.example/v1"}}`, withSession); rec.Code != http.StatusOK {
		t.Fatalf("admin create with baseUrl: %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.do(http.MethodPut, "/api/connections/conn-owner", `{"data":{"apiKey":"rotated-owner-key"}}`, withSession); rec.Code != http.StatusOK {
		t.Fatalf("admin key rotation: %d %s", rec.Code, rec.Body.String())
	}
	if got := c.ownerKey(); got != "rotated-owner-key" {
		t.Fatal("admin rotation not saved")
	}
	if rec := c.do(http.MethodPost, "/api/connections", `{"provider":"openai","name":"owner","apiKey":"rotated-again","allowOverwrite":true}`, cliTokenAuth); rec.Code != http.StatusOK {
		t.Fatalf("CLI overwrite by name: %d %s", rec.Code, rec.Body.String())
	}

	// A key cannot grant itself the opt-in; the owner can.
	if rec := c.do(http.MethodPatch, "/api/settings", `{"`+clientCredentialImportSettingForTest+`":true}`, engineKeyAuth); rec.Code != http.StatusForbidden {
		t.Fatalf("client key enabled the opt-in: %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.do(http.MethodPost, "/api/oauth/codebuddy-intl/import", workbuddyImportBody, engineKeyAuth); rec.Code != http.StatusForbidden {
		t.Fatalf("import after refused opt-in: %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.do(http.MethodPatch, "/api/settings", `{"`+clientCredentialImportSettingForTest+`":true}`, withSession); rec.Code != http.StatusOK {
		t.Fatalf("admin enabling the opt-in: %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.do(http.MethodPost, "/api/oauth/codebuddy-intl/import", workbuddyImportBody, engineKeyAuth); rec.Code != http.StatusOK {
		t.Fatalf("import after admin opt-in: %d %s", rec.Code, rec.Body.String())
	}
}

// requireLogin=false is the owner's explicit "open dashboard" choice; the
// guard does not change it.
func TestCredentialGuardLeavesOpenDashboardAlone(t *testing.T) {
	c := newCredRouter(t, map[string]any{"requireLogin": false, clientCredentialImportSettingForTest: false})
	if rec := c.do(http.MethodPost, "/api/connections", `{"provider":"openai","name":"open","apiKey":"k"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("open dashboard create: %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.do(http.MethodPost, "/api/oauth/codebuddy-intl/import", workbuddyImportBody, nil); rec.Code != http.StatusOK {
		t.Fatalf("open dashboard import: %d %s", rec.Code, rec.Body.String())
	}
}

const clientCredentialImportSettingForTest = "allowClientCredentialImport"
