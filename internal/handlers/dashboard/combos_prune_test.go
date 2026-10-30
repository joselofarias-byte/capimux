package dashboard

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"9router/proxy/internal/db"
)

// Owner decision 3 (cycle 9): generated combos left with fewer than two
// valid, available models are deleted automatically; the user's own combos
// are never deleted, only flagged for review.

// generateGeminiFamily seeds a gemini connection, runs auto-family and returns
// one generated family combo id with its members.
func generateGeminiFamily(t *testing.T, repo *db.Repo, router http.Handler) (string, []string) {
	t.Helper()
	seedConnection(t, repo.RawDB(), "gemini")
	rec := postJSON(t, router, "/api/combos/auto-family", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("auto-family: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Created []string `json:"created"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || len(resp.Created) == 0 {
		t.Fatalf("auto-family created nothing: %v %s", err, rec.Body.String())
	}
	id := autoFamilyComboID(resp.Created[0])
	members, err := comboModels(mustCombo(t, repo, id).Models)
	if err != nil || len(members) < 2 {
		t.Fatalf("family %s members %v err %v", id, members, err)
	}
	return id, members
}

// disableAllButOne disables every member except the first on the gemini
// provider page, leaving the combo with a single valid model.
func disableAllButOne(t *testing.T, repo *db.Repo, members []string) {
	t.Helper()
	var off []string
	for _, m := range members[1:] {
		off = append(off, strings.TrimPrefix(m, "gemini/"))
	}
	b, _ := json.Marshal(off)
	if err := repo.SetKV("disabledModels", "gemini", string(b)); err != nil {
		t.Fatal(err)
	}
}

func getCombos(t *testing.T, router http.Handler) map[string]map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/combos", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/combos: %d %s", rec.Code, rec.Body.String())
	}
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("GET /api/combos body: %v", err)
	}
	out := map[string]map[string]any{}
	for _, c := range list {
		out[c["id"].(string)] = c
	}
	return out
}

func prunedFrom(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var resp struct {
		Pruned []string `json:"pruned"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode pruned: %v %s", err, rec.Body.String())
	}
	return resp.Pruned
}

func TestAutoFamilyPrunesGeneratedComboBelowTwoModels(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)
	id, members := generateGeminiFamily(t, repo, router)

	// Untouched generated combo: GET reports it as generated and healthy.
	c := getCombos(t, router)[id]
	if c["generated"] != true || c["needsReview"] != false {
		t.Fatalf("fresh generated combo review fields: %v", c)
	}

	disableAllButOne(t, repo, members)
	rec := postJSON(t, router, "/api/combos/auto-family", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("regenerate: %d %s", rec.Code, rec.Body.String())
	}
	if pruned := prunedFrom(t, rec); !slices.Contains(pruned, id) {
		t.Errorf("pruned = %v, want it to contain %s", pruned, id)
	}
	if combo, _ := repo.GetComboById(id); combo != nil {
		t.Fatalf("generated combo with one valid model survived: %+v", combo)
	}
	if kv, _ := repo.GetKVScope(generatedCombosScope); kv[id] != "" {
		t.Errorf("marker of pruned combo was left behind")
	}
}

func TestAutoFamilyKeepsEditedComboAndFlagsIt(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)
	id, members := generateGeminiFamily(t, repo, router)

	// The user renames it: from now on it is the user's configuration.
	if rec := putJSON(t, router, "/api/combos/"+id, map[string]any{"name": "Mi combo"}); rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body.String())
	}
	disableAllButOne(t, repo, members)
	rec := postJSON(t, router, "/api/combos/auto-family", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("regenerate: %d %s", rec.Code, rec.Body.String())
	}
	if pruned := prunedFrom(t, rec); slices.Contains(pruned, id) {
		t.Fatalf("edited combo was pruned: %v", pruned)
	}
	if combo := mustCombo(t, repo, id); combo.Name != "Mi combo" {
		t.Fatalf("edited combo changed: %+v", combo)
	}
	c := getCombos(t, router)[id]
	if c["generated"] != false || c["needsReview"] != true {
		t.Fatalf("edited combo review fields: %v", c)
	}
	if reason, _ := c["reviewReason"].(string); !strings.Contains(reason, "No se borra automáticamente") {
		t.Errorf("reviewReason = %q", reason)
	}
	if un, _ := c["unavailableModels"].([]any); len(un) != len(members)-1 {
		t.Errorf("unavailableModels = %v, want %d entries", un, len(members)-1)
	}
}

func TestAutoFamilyKeepsLegacyComboWithoutMarker(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)
	id, members := generateGeminiFamily(t, repo, router)

	// A combo generated before markers existed has kind auto-family but no
	// record: it cannot be told apart from a user edit, so it is kept.
	if err := repo.DeleteKV(generatedCombosScope, id); err != nil {
		t.Fatal(err)
	}
	disableAllButOne(t, repo, members)
	if rec := postJSON(t, router, "/api/combos/auto-family", nil); rec.Code != http.StatusOK {
		t.Fatalf("regenerate: %d %s", rec.Code, rec.Body.String())
	}
	mustCombo(t, repo, id)
	if c := getCombos(t, router)[id]; c["needsReview"] != true || c["generated"] != false {
		t.Errorf("legacy combo review fields: %v", c)
	}
}

func TestCustomComboIsNeverPrunedButFlagged(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)
	seedConnection(t, repo.RawDB(), "opencode")

	// A user combo pointing at a provider with no connection, plus a combo
	// that is healthy (two reachable models), plus one nesting another combo.
	for _, body := range []map[string]any{
		{"id": "mine-broken", "name": "roto", "models": []string{"openrouter/x", "oc/muse-spark-1.3-contributor-free"}},
		{"id": "mine-ok", "name": "sano", "models": ocFreeModels},
		{"id": "mine-nested", "name": "anidado", "models": []string{"sano", "oc/muse-spark-1.2-contributor-free"}},
	} {
		if rec := postJSON(t, router, "/api/combos", body); rec.Code/100 != 2 {
			t.Fatalf("create %v: %d %s", body["id"], rec.Code, rec.Body.String())
		}
	}
	// Both generators run; neither may delete a user combo.
	postJSON(t, router, "/api/combos/auto-family", nil)
	postJSON(t, router, "/api/combos/auto-free", nil)
	for _, id := range []string{"mine-broken", "mine-ok", "mine-nested"} {
		mustCombo(t, repo, id)
	}
	got := getCombos(t, router)
	if c := got["mine-broken"]; c["needsReview"] != true || c["generated"] != false {
		t.Errorf("broken custom combo: %v", c)
	} else if un, _ := c["unavailableModels"].([]any); len(un) != 1 || un[0] != "openrouter/x" {
		t.Errorf("unavailableModels = %v", un)
	}
	for _, id := range []string{"mine-ok", "mine-nested"} {
		if c := got[id]; c["needsReview"] != false {
			t.Errorf("%s flagged without reason: %v", id, c)
		}
	}
}

func TestAutoFreePrunesGeneratedComboBelowTwoModels(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)
	seedConnection(t, repo.RawDB(), "opencode")
	if rec := postJSON(t, router, "/api/combos/auto-free", nil); rec.Code != http.StatusOK {
		t.Fatalf("auto-free: %d %s", rec.Code, rec.Body.String())
	}
	if err := repo.SetKV("disabledModels", "oc", `["muse-spark-1.2-contributor-free"]`); err != nil {
		t.Fatal(err)
	}
	rec := postJSON(t, router, "/api/combos/auto-free", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("one free model: expected 409, got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "al menos dos modelos gratuitos") {
		t.Errorf("409 message must explain the minimum in Spanish: %s", rec.Body.String())
	}
	if pruned := prunedFrom(t, rec); !slices.Equal(pruned, []string{AutoFreeComboID}) {
		t.Errorf("pruned = %v", pruned)
	}
	if combo, _ := repo.GetComboById(AutoFreeComboID); combo != nil {
		t.Fatalf("generated auto-free combo with one model survived: %+v", combo)
	}
}

func TestAutoFreeKeepsEditedCombo(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)
	seedConnection(t, repo.RawDB(), "opencode")
	if rec := postJSON(t, router, "/api/combos/auto-free", nil); rec.Code != http.StatusOK {
		t.Fatalf("auto-free: %d %s", rec.Code, rec.Body.String())
	}
	if rec := putJSON(t, router, "/api/combos/"+AutoFreeComboID, map[string]any{"name": "Mis gratis"}); rec.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", rec.Code, rec.Body.String())
	}
	if err := repo.SetKV("disabledModels", "oc", `["muse-spark-1.2-contributor-free"]`); err != nil {
		t.Fatal(err)
	}
	rec := postJSON(t, router, "/api/combos/auto-free", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d %s", rec.Code, rec.Body.String())
	}
	if pruned := prunedFrom(t, rec); len(pruned) != 0 {
		t.Fatalf("edited combo pruned: %v", pruned)
	}
	mustCombo(t, repo, AutoFreeComboID)
	if c := getCombos(t, router)[AutoFreeComboID]; c["needsReview"] != true {
		t.Errorf("edited auto-free combo not flagged: %v", c)
	}
}

func TestDeleteComboRemovesGeneratedMarker(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)
	seedConnection(t, repo.RawDB(), "opencode")
	if rec := postJSON(t, router, "/api/combos/auto-free", nil); rec.Code != http.StatusOK {
		t.Fatalf("auto-free: %d %s", rec.Code, rec.Body.String())
	}
	if kv, _ := repo.GetKVScope(generatedCombosScope); kv[AutoFreeComboID] == "" {
		t.Fatal("generator did not record its marker")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/combos/"+AutoFreeComboID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if kv, _ := repo.GetKVScope(generatedCombosScope); kv[AutoFreeComboID] != "" {
		t.Error("marker survived the delete")
	}
}
