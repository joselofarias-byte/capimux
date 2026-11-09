package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// connSnapshot is the part of a connection row a client key must not change.
type connSnapshot struct {
	Name     string
	IsActive bool
	Priority string
	Data     string
}

func (c *credRouter) snapshot(id string) connSnapshot {
	c.t.Helper()
	var s connSnapshot
	var prio any
	if err := c.repo.RawDB().QueryRow(`SELECT name, isActive, priority, data FROM providerConnections WHERE id = ?`, id).Scan(&s.Name, &s.IsActive, &prio, &s.Data); err != nil {
		c.t.Fatalf("snapshot %s: %v", id, err)
	}
	b, _ := json.Marshal(prio)
	s.Priority = string(b)
	return s
}

func (c *credRouter) proxyPools() string {
	c.t.Helper()
	pools, err := c.repo.ListProxyPools()
	if err != nil {
		c.t.Fatal(err)
	}
	b, _ := json.Marshal(pools)
	return string(b)
}

func (c *credRouter) createProxyPool(setAuth func(*http.Request)) string {
	c.t.Helper()
	rec := c.do(http.MethodPost, "/api/proxy-pools", `{"name":"local","proxyUrl":"http://127.0.0.1:1","isActive":true}`, setAuth)
	if rec.Code != http.StatusOK {
		c.t.Fatalf("admin create proxy pool: %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	id, _ := body["id"].(string)
	if id == "" {
		c.t.Fatalf("no proxy pool id in %s", rec.Body.String())
	}
	return id
}

// A client API key uses connections but never configures them: renaming,
// enabling/disabling, assigning a model, testing (which writes the result
// back), proxy pools, Codex reset credits and the Freebuff seat all answer
// 403, whatever allowClientCredentialImport says, and leave the stored state
// unchanged.
func TestClientKeyCannotChangeConnectionConfigOrState(t *testing.T) {
	for _, setting := range []any{nil, false, true} {
		name := "setting absent"
		if setting != nil {
			name = "setting " + mustJSON(t, setting)
		}
		t.Run(name, func(t *testing.T) {
			// The Kiro/Cursor auto-imports read the caller's home directory.
			t.Setenv("HOME", t.TempDir())
			settings := map[string]any{}
			if setting != nil {
				settings[clientCredentialImportSettingForTest] = setting
			}
			c := newCredRouter(t, settings)
			c.seedConn("conn-second", "openai", intp(2))
			poolID := c.createProxyPool(cliTokenAuth)
			before := c.snapshot("conn-second")
			ownerBefore := c.snapshot("conn-owner")
			poolsBefore := c.proxyPools()

			for _, tc := range []struct{ name, method, path, body string }{
				{"rename", http.MethodPut, "/api/connections/conn-second", `{"name":"renamed","priority":2}`},
				{"rename provider alias", http.MethodPut, "/api/providers/conn-second", `{"name":"renamed"}`},
				{"deactivate", http.MethodPut, "/api/connections/conn-second", `{"isActive":false}`},
				{"activate provider alias", http.MethodPut, "/api/providers/conn-owner", `{"isActive":true}`},
				{"assign model", http.MethodPut, "/api/connections/conn-second", `{"assignedModel":"gpt-4o-mini"}`},
				{"test connection", http.MethodPost, "/api/connections/no-such-conn/test", ""},
				{"test provider alias", http.MethodPost, "/api/providers/no-such-conn/test", ""},
				{"consume codex reset credit", http.MethodPost, "/api/usage/no-such-conn/reset-credits/consume", `{}`},
				{"create proxy pool", http.MethodPost, "/api/proxy-pools", `{"name":"evil","proxyUrl":"http://attacker.example.invalid:8080","isActive":true}`},
				{"update proxy pool", http.MethodPut, "/api/proxy-pools/" + poolID, `{"name":"evil","proxyUrl":"http://attacker.example.invalid:8080"}`},
				{"delete proxy pool", http.MethodDelete, "/api/proxy-pools/" + poolID, ""},
				{"test proxy pool", http.MethodPost, "/api/proxy-pools/" + poolID + "/test", ""},
				{"freebuff session switch", http.MethodPost, "/api/oauth/freebuff/session/switch", `{"connectionId":"no-such-conn","model":"m"}`},
			} {
				rec := c.do(tc.method, tc.path, tc.body, engineKeyAuth)
				if rec.Code != http.StatusForbidden {
					t.Errorf("%s: expected 403, got %d: %s", tc.name, rec.Code, rec.Body.String())
				}
				if !strings.Contains(rec.Body.String(), "No autorizado") {
					t.Errorf("%s: expected the Spanish message, got %s", tc.name, rec.Body.String())
				}
			}
			// Already admin-only in the dashboard guard (local credentials); kept
			// here as a regression check.
			for _, path := range []string{"/api/oauth/kiro/auto-import", "/api/oauth/cursor/auto-import"} {
				if rec := c.do(http.MethodGet, path, "", engineKeyAuth); rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
					t.Errorf("%s with a client key: expected 401/403, got %d: %s", path, rec.Code, rec.Body.String())
				}
			}
			if got := c.snapshot("conn-second"); got != before {
				t.Fatalf("conn-second changed: %+v -> %+v", before, got)
			}
			if got := c.snapshot("conn-owner"); got != ownerBefore {
				t.Fatalf("conn-owner changed: %+v -> %+v", ownerBefore, got)
			}
			if got := c.proxyPools(); got != poolsBefore {
				t.Fatalf("proxy pools changed:\n%s\n->\n%s", poolsBefore, got)
			}

			// Using the connections is still allowed: the key reads them.
			if rec := c.do(http.MethodGet, "/api/connections", "", engineKeyAuth); rec.Code != http.StatusOK {
				t.Errorf("client key listing connections: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// The admin token (and so the panel session) keeps renaming, enabling and
// disabling, assigning a model and managing proxy pools.
func TestAdminTokenChangesConnectionConfigAndState(t *testing.T) {
	c := newCredRouter(t, nil)
	c.seedConn("conn-second", "openai", intp(2))
	for _, body := range []string{`{"name":"renamed"}`, `{"isActive":false}`, `{"assignedModel":"gpt-4o-mini"}`} {
		if rec := c.do(http.MethodPut, "/api/connections/conn-second", body, cliTokenAuth); rec.Code != http.StatusOK {
			t.Fatalf("admin PUT %s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	got := c.snapshot("conn-second")
	if got.Name != "renamed" || got.IsActive || !strings.Contains(got.Data, "gpt-4o-mini") {
		t.Fatalf("admin edits not applied: %+v", got)
	}
	if rec := c.do(http.MethodPut, "/api/providers/conn-second", `{"isActive":true}`, cliTokenAuth); rec.Code != http.StatusOK {
		t.Fatalf("admin re-enable: %d %s", rec.Code, rec.Body.String())
	}
	if !c.snapshot("conn-second").IsActive {
		t.Fatal("admin re-enable not applied")
	}

	poolID := c.createProxyPool(cliTokenAuth)
	if rec := c.do(http.MethodPut, "/api/proxy-pools/"+poolID, `{"name":"local 2","proxyUrl":"http://127.0.0.1:2"}`, cliTokenAuth); rec.Code != http.StatusOK {
		t.Fatalf("admin update proxy pool: %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.do(http.MethodDelete, "/api/proxy-pools/"+poolID, "", cliTokenAuth); rec.Code != http.StatusOK {
		t.Fatalf("admin delete proxy pool: %d %s", rec.Code, rec.Body.String())
	}
}
