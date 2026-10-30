package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// seedConn adds an openai/codex connection with a fixed priority (nil = NULL)
// and an old updatedAt, so a fresh row would sort first among NULLs.
func (c *credRouter) seedConn(id, provider string, priority *int) {
	c.t.Helper()
	if _, err := c.repo.RawDB().Exec(
		`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES (?, ?, 'apikey', ?, ?, 1, '{"apiKey":"seed"}', '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z')`,
		id, provider, id, priority,
	); err != nil {
		c.t.Fatalf("seed %s: %v", id, err)
	}
}

func (c *credRouter) rotation(provider string) []string {
	c.t.Helper()
	conns, err := c.repo.GetProviderConnections(provider, false)
	if err != nil {
		c.t.Fatal(err)
	}
	ids := make([]string, 0, len(conns))
	for _, conn := range conns {
		ids = append(ids, conn.ID)
	}
	return ids
}

func (c *credRouter) createNode(setAuth func(*http.Request)) string {
	c.t.Helper()
	rec := c.do(http.MethodPost, "/api/provider-nodes", `{"name":"Local","prefix":"local","apiType":"chat","baseUrl":"http://127.0.0.1:1/v1"}`, setAuth)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		c.t.Fatalf("admin create node: %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if id, _ := body["id"].(string); id != "" {
		return id
	}
	if node, _ := body["node"].(map[string]any); node != nil {
		if id, _ := node["id"].(string); id != "" {
			return id
		}
	}
	c.t.Fatalf("no node id in %s", rec.Body.String())
	return ""
}

func intp(v int) *int { return &v }

// A normal client API key cannot delete connections, change priorities or the
// rotation order, or touch provider nodes, whatever allowClientCredentialImport
// says. Each operation is checked to have left the stored state unchanged.
func TestClientKeyCannotRunAdminOperations(t *testing.T) {
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
			c.seedConn("conn-second", "openai", intp(2))
			nodeID := c.createNode(cliTokenAuth)
			before := c.rotation("openai")

			for _, tc := range []struct{ name, method, path, body string }{
				{"delete connection", http.MethodDelete, "/api/connections/conn-owner", ""},
				{"delete provider alias", http.MethodDelete, "/api/providers/conn-owner", ""},
				{"reorder connection", http.MethodPost, "/api/connections/conn-second/reorder", `{"direction":"up"}`},
				{"reorder provider alias", http.MethodPost, "/api/providers/conn-second/reorder", `{"direction":"up"}`},
				{"PUT priority", http.MethodPut, "/api/connections/conn-second", `{"priority":0}`},
				{"PUT provider priority", http.MethodPut, "/api/providers/conn-owner", `{"priority":5}`},
				{"create node", http.MethodPost, "/api/provider-nodes", `{"name":"Evil","prefix":"evil","apiType":"chat","baseUrl":"https://attacker.example.invalid/v1"}`},
				{"update node", http.MethodPut, "/api/provider-nodes/" + nodeID, `{"name":"Evil","baseUrl":"https://attacker.example.invalid/v1"}`},
				{"delete node", http.MethodDelete, "/api/provider-nodes/" + nodeID, ""},
			} {
				rec := c.do(tc.method, tc.path, tc.body, engineKeyAuth)
				if rec.Code != http.StatusForbidden {
					t.Errorf("%s: expected 403, got %d: %s", tc.name, rec.Code, rec.Body.String())
				}
				if !strings.Contains(rec.Body.String(), "No autorizado") {
					t.Errorf("%s: expected the Spanish message, got %s", tc.name, rec.Body.String())
				}
			}
			if got := c.rotation("openai"); strings.Join(got, ",") != strings.Join(before, ",") {
				t.Fatalf("rotation changed: %v -> %v", before, got)
			}
			nodes, err := c.repo.GetProviderNodes()
			if err != nil {
				t.Fatal(err)
			}
			if len(nodes) != 1 || nodes[0].ID != nodeID {
				t.Fatalf("provider nodes changed: %+v", nodes)
			}

			// Since cycle 9 every other PUT is admin-only as well; see
			// TestClientKeyCannotChangeConnectionConfigOrState.
		})
	}
}

// The admin token keeps every one of those operations.
func TestAdminTokenRunsAdminOperations(t *testing.T) {
	c := newCredRouter(t, nil)
	c.seedConn("conn-second", "openai", intp(2))
	if rec := c.do(http.MethodPost, "/api/connections/conn-second/reorder", `{"direction":"up"}`, cliTokenAuth); rec.Code != http.StatusOK {
		t.Fatalf("admin reorder: %d %s", rec.Code, rec.Body.String())
	}
	if got := c.rotation("openai"); got[0] != "conn-second" {
		t.Fatalf("admin reorder not applied: %v", got)
	}
	if rec := c.do(http.MethodPut, "/api/connections/conn-owner", `{"priority":7}`, cliTokenAuth); rec.Code != http.StatusOK {
		t.Fatalf("admin PUT priority: %d %s", rec.Code, rec.Body.String())
	}
	nodeID := c.createNode(cliTokenAuth)
	if rec := c.do(http.MethodPut, "/api/provider-nodes/"+nodeID, `{"name":"Local 2","prefix":"local","apiType":"chat","baseUrl":"http://127.0.0.1:2/v1"}`, cliTokenAuth); rec.Code != http.StatusOK {
		t.Fatalf("admin update node: %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.do(http.MethodDelete, "/api/provider-nodes/"+nodeID, "", cliTokenAuth); rec.Code != http.StatusOK {
		t.Fatalf("admin delete node: %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.do(http.MethodDelete, "/api/connections/conn-owner", "", cliTokenAuth); rec.Code != http.StatusOK {
		t.Fatalf("admin delete connection: %d %s", rec.Code, rec.Body.String())
	}
	if got := c.rotation("openai"); len(got) != 1 || got[0] != "conn-second" {
		t.Fatalf("after admin delete: %v", got)
	}
}

// With the opt-in on, what a client key creates lands after every existing
// connection, whatever priority it asks for; the existing order is untouched.
// NULL-priority rows (sorted as 999999) and older rows are included because a
// plain max+1 or a NULL insert would jump ahead of them.
func TestClientKeyConnectionsGoLastInRotation(t *testing.T) {
	c := newCredRouter(t, map[string]any{clientCredentialImportSettingForTest: true})
	c.seedConn("conn-second", "openai", intp(2))
	c.seedConn("conn-null", "openai", nil)
	c.seedConn("codex-a", "codex", intp(1))
	c.seedConn("codex-null", "codex", nil)
	openaiBefore, codexBefore := c.rotation("openai"), c.rotation("codex")

	created := map[string]string{}
	for _, tc := range []struct{ label, path, body, provider string }{
		{"create", "/api/connections", `{"provider":"openai","name":"by-key","apiKey":"k","priority":1}`, "openai"},
		{"import", "/api/oauth/openai/import", `{"apiKey":"k2","name":"imported"}`, "openai"},
		{"bulk", "/api/oauth/codex/bulk-import", `{"tokens":[{"accessToken":"t1"},{"accessToken":"t2"}]}`, "codex"},
	} {
		rec := c.do(http.MethodPost, tc.path, tc.body, engineKeyAuth)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", tc.label, rec.Code, rec.Body.String())
		}
		created[tc.label] = rec.Body.String()
	}

	openaiAfter := c.rotation("openai")
	if len(openaiAfter) != len(openaiBefore)+2 || strings.Join(openaiAfter[:len(openaiBefore)], ",") != strings.Join(openaiBefore, ",") {
		t.Fatalf("openai rotation: before %v, after %v (existing order must stay first and intact)", openaiBefore, openaiAfter)
	}
	for _, id := range openaiAfter[len(openaiBefore):] {
		if !strings.Contains(created["create"]+created["import"], id) {
			t.Fatalf("unexpected row %s at the end of %v", id, openaiAfter)
		}
	}
	codexAfter := c.rotation("codex")
	if len(codexAfter) != len(codexBefore)+2 || strings.Join(codexAfter[:len(codexBefore)], ",") != strings.Join(codexBefore, ",") {
		t.Fatalf("codex rotation: before %v, after %v", codexBefore, codexAfter)
	}

	// An admin still chooses the priority.
	rec := c.do(http.MethodPost, "/api/connections", `{"provider":"openai","name":"by-admin","apiKey":"k3","priority":1}`, cliTokenAuth)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin create: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct{ ID string }
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	conn, err := c.repo.GetProviderConnectionByID(resp.ID)
	if err != nil || conn == nil || conn.Priority == nil || *conn.Priority != 1 {
		t.Fatalf("admin priority not kept: %+v %v", conn, err)
	}
}
