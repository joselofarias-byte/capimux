package dashboard

import (
	"net/http"
	"slices"
	"testing"
)

// The combo recommenders (auto-family and auto-free) must only suggest models
// the user can actually call: the provider has an active connection and the
// model is not disabled on the provider page. These tests pin the cases where
// main suggested unusable models.

func setCustomModel(t *testing.T, repo interface {
	SetKV(scope, key, value string) error
}, alias, id string) {
	t.Helper()
	if err := repo.SetKV("customModels", alias+"|"+id+"|llm", `{"providerAlias":"`+alias+`","id":"`+id+`","type":"llm"}`); err != nil {
		t.Fatalf("set custom model: %v", err)
	}
}

func TestAutoFamilySkipsDisabledAndUnconnectedCustomModels(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	seedConnection(t, repo.RawDB(), "gemini")
	// Custom models joining the gemini-flash family:
	setCustomModel(t, repo, "gemini", "gemini-9-flash")     // usable
	setCustomModel(t, repo, "gemini", "gemini-9.1-flash")   // disabled below
	setCustomModel(t, repo, "openrouter", "gemini-8-flash") // no openrouter connection
	if err := repo.SetKV("disabledModels", "gemini", `["gemini-9.1-flash","gemini-3.8-flash"]`); err != nil {
		t.Fatal(err)
	}

	if rec := postJSON(t, router, "/api/combos/auto-family", nil); rec.Code != http.StatusOK {
		t.Fatalf("auto-family: %d %s", rec.Code, rec.Body.String())
	}
	members, err := comboModels(mustCombo(t, repo, autoFamilyComboID("gemini-flash")).Models)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(members, "gemini/gemini-9-flash") {
		t.Errorf("usable custom model missing from %v", members)
	}
	for _, bad := range []string{"gemini/gemini-9.1-flash", "gemini/gemini-3.8-flash", "openrouter/gemini-8-flash"} {
		if slices.Contains(members, bad) {
			t.Errorf("%s is disabled or has no active connection but was suggested: %v", bad, members)
		}
	}
}

func TestAutoFamilyCustomModelFollowsConnectionAlias(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	// bazaarlink's alias is "bzl": custom models stored under either the alias
	// or the provider id belong to the connected provider.
	seedConnection(t, repo.RawDB(), "bazaarlink")
	setCustomModel(t, repo, "bzl", "kimi-k9")
	setCustomModel(t, repo, "bazaarlink", "kimi-k8")
	if rec := postJSON(t, router, "/api/combos/auto-family", nil); rec.Code != http.StatusOK {
		t.Fatalf("auto-family: %d %s", rec.Code, rec.Body.String())
	}
	members, err := comboModels(mustCombo(t, repo, autoFamilyComboID("kimi")).Models)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bzl/kimi-k9", "bazaarlink/kimi-k8", "bzl/kimi-k2.6"} {
		if !slices.Contains(members, want) {
			t.Errorf("%s missing from %v", want, members)
		}
	}
}

func TestAutoFamilySkipsInactiveConnections(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	seedConnection(t, repo.RawDB(), "gemini")
	if _, err := repo.RawDB().Exec(`UPDATE providerConnections SET isActive = 0 WHERE provider = 'gemini'`); err != nil {
		t.Fatal(err)
	}
	setCustomModel(t, repo, "gemini", "gemini-9-flash")
	setCustomModel(t, repo, "gemini", "gemini-9.1-flash")
	if rec := postJSON(t, router, "/api/combos/auto-family", nil); rec.Code != http.StatusOK {
		t.Fatalf("auto-family: %d %s", rec.Code, rec.Body.String())
	}
	if c, err := repo.GetComboById(autoFamilyComboID("gemini-flash")); err != nil || c != nil {
		t.Fatalf("a provider whose only connection is disabled must not get a combo (combo=%v err=%v)", c, err)
	}
}

func TestAutoFreeComboSkipsInactiveConnectionsAndDisabledModels(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	// Only connection disabled: nothing reachable.
	seedConnection(t, repo.RawDB(), "opencode")
	if _, err := repo.RawDB().Exec(`UPDATE providerConnections SET isActive = 0`); err != nil {
		t.Fatal(err)
	}
	if rec := postJSON(t, router, "/api/combos/auto-free", nil); rec.Code != http.StatusConflict {
		t.Fatalf("inactive-only connection: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	// Active again, but one of the two free models is disabled on the
	// provider page: one model is not a fallback chain.
	if _, err := repo.RawDB().Exec(`UPDATE providerConnections SET isActive = 1`); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetKV("disabledModels", "oc", `["muse-spark-1.2-contributor-free"]`); err != nil {
		t.Fatal(err)
	}
	if rec := postJSON(t, router, "/api/combos/auto-free", nil); rec.Code != http.StatusConflict {
		t.Fatalf("one free model left: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	// Re-enabled: the combo is built again.
	if err := repo.SetKV("disabledModels", "oc", `[]`); err != nil {
		t.Fatal(err)
	}
	if rec := postJSON(t, router, "/api/combos/auto-free", nil); rec.Code != http.StatusOK {
		t.Fatalf("re-enabled: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	members, err := comboModels(mustCombo(t, repo, AutoFreeComboID).Models)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 || !slices.Contains(members, ocFreeModels[0]) || !slices.Contains(members, ocFreeModels[1]) {
		t.Errorf("members = %v, want %v", members, ocFreeModels)
	}
}
