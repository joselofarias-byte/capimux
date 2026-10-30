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

// A client (inference) API key must not be able to change where an existing
// stored credential is sent: repointing a connection's baseUrl/proxy, binding
// it to a proxy pool, repointing a provider node, or rewriting a proxy pool's
// URLs would deliver the owner's upstream key to the caller on the next
// request. All values are synthetic and no outbound request is made.
func TestSetupServerRouter_InferenceKeyCannotRedirectCredentials(t *testing.T) {
	t.Setenv("JWT_SECRET", "credential-destination-test-secret")
	t.Setenv("DATA_DIR", t.TempDir())
	database, cleanup := setupTestDB(t)
	defer cleanup()
	if err := db.EnsureCoreSchema(database); err != nil {
		t.Fatalf("ensure core schema: %v", err)
	}
	repo := db.NewRepo(database)

	const ts = "2026-01-01T00:00:00Z"
	seed := []string{
		`INSERT INTO apiKeys (id, key, name, isActive, createdAt) VALUES ('dest-client', 'dest-engine-key', 'chatbox', 1, '` + ts + `')`,
		`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES ('conn-1', 'openai', 'apikey', 'owner', 1, 1, '{"apiKey":"owner-upstream-key","providerSpecificData":{"baseUrl":"https://api.owner.example/v1"}}', '` + ts + `', '` + ts + `')`,
		`INSERT INTO providerNodes (id, type, name, data, createdAt, updatedAt) VALUES ('node-1', 'openai-compatible', 'Owner node', '{"prefix":"own","apiType":"chat","baseUrl":"https://node.owner.example/v1"}', '` + ts + `', '` + ts + `')`,
		`INSERT INTO proxyPools (id, isActive, testStatus, data, createdAt, updatedAt) VALUES ('pool-1', 1, 'unknown', '{"name":"owner pool","urls":["http://proxy.owner.example:8080"],"type":"http","strategy":"round-robin"}', '` + ts + `', '` + ts + `')`,
	}
	for _, q := range seed {
		if _, err := database.Exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
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
	engineKey := func(req *http.Request) { req.Header.Set("Authorization", "Bearer dest-engine-key") }
	connData := func() map[string]any {
		t.Helper()
		c, err := repo.GetProviderConnectionByID("conn-1")
		if err != nil || c == nil {
			t.Fatalf("read connection: %v", err)
		}
		m := map[string]any{}
		_ = json.Unmarshal([]byte(c.Data), &m)
		return m
	}
	connBaseURL := func() any {
		psd, _ := connData()["providerSpecificData"].(map[string]any)
		return psd["baseUrl"]
	}

	for _, tc := range []struct{ name, body string }{
		{"psd baseUrl", `{"providerSpecificData":{"baseUrl":"https://attacker.example.invalid/v1"}}`},
		{"data baseUrl", `{"data":{"providerSpecificData":{"baseUrl":"https://attacker.example.invalid/v1"}}}`},
		{"data string baseUrl", `{"data":"{\"baseUrl\":\"https://attacker.example.invalid/v1\"}"}`},
		{"connection proxy", `{"providerSpecificData":{"connectionProxyEnabled":true,"connectionProxyUrl":"http://attacker.example.invalid:3128"}}`},
		{"vercel relay", `{"providerSpecificData":{"vercelRelayUrl":"https://attacker.example.invalid"}}`},
		{"bind proxy pool", `{"proxyPoolId":"pool-1"}`},
	} {
		t.Run("connection "+tc.name, func(t *testing.T) {
			rec := do(http.MethodPut, "/api/connections/conn-1", tc.body, engineKey)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
			}
			if got := connBaseURL(); got != "https://api.owner.example/v1" {
				t.Fatalf("connection baseUrl changed to %#v", got)
			}
			d := connData()
			if _, ok := d["proxyPoolId"]; ok {
				t.Fatalf("proxy pool bound: %#v", d)
			}
			if _, ok := d["baseUrl"]; ok {
				t.Fatalf("top-level baseUrl written: %#v", d)
			}
		})
	}

	t.Run("provider node baseUrl", func(t *testing.T) {
		rec := do(http.MethodPut, "/api/provider-nodes/node-1", `{"name":"Owner node","prefix":"own","apiType":"chat","baseUrl":"https://attacker.example.invalid/v1"}`, engineKey)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
		}
		_, nd, _ := repo.GetProviderNodeByID("node-1")
		if nd == nil || nd.BaseURL != "https://node.owner.example/v1" {
			t.Fatalf("node baseUrl changed: %#v", nd)
		}
	})

	t.Run("proxy pool urls", func(t *testing.T) {
		rec := do(http.MethodPut, "/api/proxy-pools/pool-1", `{"urls":["http://attacker.example.invalid:3128"]}`, engineKey)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
		}
		pools, _ := repo.ListProxyPools()
		if len(pools) != 1 || !strings.Contains(mustJSON(t, pools[0]["urls"]), "proxy.owner.example") {
			t.Fatalf("pool urls changed: %#v", pools)
		}
	})

	// Compatibility: ordinary edits with the key keep working, including a
	// full-form save that re-sends the unchanged destination. Priority changes
	// and any provider node edit are admin-only (client_key_admin_ops_test.go).
	for _, tc := range []struct{ name, method, path, body string }{
		{"rename + toggle", http.MethodPut, "/api/connections/conn-1", `{"name":"renamed","isActive":true,"priority":1}`},
		{"same baseUrl resent", http.MethodPut, "/api/connections/conn-1", `{"providerSpecificData":{"baseUrl":"https://api.owner.example/v1"}}`},
		{"assigned model", http.MethodPut, "/api/connections/conn-1", `{"assignedModel":"gpt-x"}`},
		{"pool rename", http.MethodPut, "/api/proxy-pools/pool-1", `{"name":"renamed pool","urls":["http://proxy.owner.example:8080"],"type":"http"}`},
	} {
		t.Run("allowed "+tc.name, func(t *testing.T) {
			if rec := do(tc.method, tc.path, tc.body, engineKey); rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}

	// The owner (session or CLI token) can still repoint credentials.
	session, err := auth.Sign(auth.Secret(), time.Now())
	if err != nil {
		t.Fatalf("sign session: %v", err)
	}
	withSession := func(req *http.Request) { req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: session}) }
	if rec := do(http.MethodPut, "/api/connections/conn-1", `{"providerSpecificData":{"baseUrl":"https://api2.owner.example/v1"}}`, withSession); rec.Code != http.StatusOK {
		t.Fatalf("admin repoint: %d %s", rec.Code, rec.Body.String())
	}
	if got := connBaseURL(); got != "https://api2.owner.example/v1" {
		t.Fatalf("admin repoint not saved: %#v", got)
	}
	withCLI := func(req *http.Request) { req.Header.Set(auth.CLITokenHeader, auth.CLIToken()) }
	if rec := do(http.MethodPut, "/api/proxy-pools/pool-1", `{"urls":["http://proxy2.owner.example:8080"]}`, withCLI); rec.Code != http.StatusOK {
		t.Fatalf("CLI pool repoint: %d %s", rec.Code, rec.Body.String())
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
