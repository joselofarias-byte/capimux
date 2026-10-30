package dashboard

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"9router/proxy/internal/db"
	"9router/proxy/internal/models"
)

// openComboStrategyDB opens (or reopens) an on-disk database with the
// production startup schema, so a "reload" is a real close + reopen.
func openComboStrategyDB(t *testing.T, path string) (*db.Repo, http.Handler, func()) {
	t.Helper()
	conn, err := db.OpenDatabase(path)
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	if err := db.RunMigrations(conn); err != nil {
		conn.Close()
		t.Fatalf("RunMigrations: %v", err)
	}
	if err := db.EnsureCoreSchema(conn); err != nil {
		conn.Close()
		t.Fatalf("EnsureCoreSchema: %v", err)
	}
	repo := db.NewRepo(conn)
	return repo, setupTestRouter(repo), func() { conn.Close() }
}

func getComboStrategy(t *testing.T, router http.Handler, id string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/combos", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/combos: %d %s", rec.Code, rec.Body.String())
	}
	var combos []*models.Combo
	if err := json.Unmarshal(rec.Body.Bytes(), &combos); err != nil {
		t.Fatalf("decode combos: %v", err)
	}
	for _, c := range combos {
		if c.ID == id {
			return c.Strategy
		}
	}
	t.Fatalf("combo %s not in GET /api/combos: %s", id, rec.Body.String())
	return ""
}

// TestUpdateComboStrategyPersists: PUT /api/combos/{id} with a new strategy is
// reflected by GET /api/combos and survives closing and reopening the
// database. Before the fix the PUT answered 200 but the strategy was dropped
// (written to a column the schema does not have), so GET kept "fallback".
func TestUpdateComboStrategyPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capimux.sqlite")
	_, router, closeDB := openComboStrategyDB(t, path)

	rec := postJSON(t, router, "/api/combos", map[string]any{
		"id": "c1", "name": "mi-combo", "models": []string{"a/x", "b/y"}, "strategy": "fallback",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if got := getComboStrategy(t, router, "c1"); got != "fallback" {
		t.Fatalf("after create: strategy = %q, want fallback", got)
	}

	rec = putJSON(t, router, "/api/combos/c1", map[string]any{"strategy": "round-robin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("update strategy: %d %s", rec.Code, rec.Body.String())
	}
	if got := getComboStrategy(t, router, "c1"); got != "round-robin" {
		t.Fatalf("after save: GET strategy = %q, want round-robin", got)
	}
	closeDB()

	_, router, closeDB = openComboStrategyDB(t, path)
	defer closeDB()
	if got := getComboStrategy(t, router, "c1"); got != "round-robin" {
		t.Fatalf("after reload: GET strategy = %q, want round-robin", got)
	}

	// Create with a non-default strategy persists too.
	rec = postJSON(t, router, "/api/combos", map[string]any{
		"id": "c2", "name": "otro", "models": []string{"a/x", "b/y"}, "strategy": "capacity",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create c2: %d %s", rec.Code, rec.Body.String())
	}
	if got := getComboStrategy(t, router, "c2"); got != "capacity" {
		t.Fatalf("create with capacity: GET strategy = %q, want capacity", got)
	}
}

// TestComboStrategyRejectsInvalidValue: an unknown strategy is a 400 on both
// create and update, and the saved strategy stays as it was.
func TestComboStrategyRejectsInvalidValue(t *testing.T) {
	_, router, closeDB := openComboStrategyDB(t, filepath.Join(t.TempDir(), "capimux.sqlite"))
	defer closeDB()

	rec := postJSON(t, router, "/api/combos", map[string]any{
		"id": "bad", "name": "malo", "models": []string{"a/x"}, "strategy": "aleatorio",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create with invalid strategy: expected 400, got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "estrategia de combo inválida") {
		t.Errorf("400 must explain the error in Spanish: %s", rec.Body.String())
	}

	rec = postJSON(t, router, "/api/combos", map[string]any{
		"id": "c1", "name": "mi-combo", "models": []string{"a/x", "b/y"}, "strategy": "sticky",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	rec = putJSON(t, router, "/api/combos/c1", map[string]any{"strategy": "weighted"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("update with invalid strategy: expected 400, got %d %s", rec.Code, rec.Body.String())
	}
	if got := getComboStrategy(t, router, "c1"); got != "sticky" {
		t.Fatalf("rejected update changed the strategy to %q, want sticky", got)
	}
}

// TestUpdateComboOtherFieldsKeepStrategy: editing name, models or kind
// without a strategy leaves the saved strategy alone, and a rename keeps it.
func TestUpdateComboOtherFieldsKeepStrategy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capimux.sqlite")
	repo, router, closeDB := openComboStrategyDB(t, path)

	rec := postJSON(t, router, "/api/combos", map[string]any{
		"id": "c1", "name": "mi-combo", "models": []string{"a/x", "b/y"}, "strategy": "round-robin",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}

	for _, body := range []map[string]any{
		{"models": []string{"a/x", "b/y", "c/z"}},
		{"kind": "llm"},
		{"name": "mi-combo-v2"},
	} {
		rec = putJSON(t, router, "/api/combos/c1", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("update %v: %d %s", body, rec.Code, rec.Body.String())
		}
		if got := getComboStrategy(t, router, "c1"); got != "round-robin" {
			t.Fatalf("after update %v: strategy = %q, want round-robin", body, got)
		}
	}
	s, err := repo.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if _, stale := s.ComboStrategies["mi-combo"]; stale {
		t.Errorf("rename left the strategy under the old name: %+v", s.ComboStrategies)
	}
	closeDB()

	_, router, closeDB = openComboStrategyDB(t, path)
	defer closeDB()
	if got := getComboStrategy(t, router, "c1"); got != "round-robin" {
		t.Fatalf("after reload: strategy = %q, want round-robin", got)
	}
}

// TestRegenerateKeepsGeneratedComboStrategy: regenerating auto-family combos
// rewrites their models but keeps the strategy the user picked on the card.
func TestRegenerateKeepsGeneratedComboStrategy(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)
	seedConnection(t, repo.RawDB(), "gemini")

	if rec := postJSON(t, router, "/api/combos/auto-family", nil); rec.Code != http.StatusOK {
		t.Fatalf("auto-family: %d %s", rec.Code, rec.Body.String())
	}
	id := autoFamilyComboID("gemini-flash")
	if rec := putJSON(t, router, "/api/combos/"+id, map[string]any{"strategy": "round-robin"}); rec.Code != http.StatusOK {
		t.Fatalf("set strategy: %d %s", rec.Code, rec.Body.String())
	}
	if rec := postJSON(t, router, "/api/combos/auto-family", nil); rec.Code != http.StatusOK {
		t.Fatalf("regenerate: %d %s", rec.Code, rec.Body.String())
	}
	if got := getComboStrategy(t, router, id); got != "round-robin" {
		t.Fatalf("after regenerate: strategy = %q, want round-robin", got)
	}
}
