package db

import (
	"path/filepath"
	"testing"
)

// openMigratedRepo opens (or reopens) a real on-disk database with the
// production schema (the same RunMigrations + EnsureCoreSchema startup
// runs), so the test exercises the same tables a running server
// uses rather than a hand-written test schema.
func openMigratedRepo(t *testing.T, path string) (*Repo, func()) {
	t.Helper()
	database, err := OpenDatabase(path)
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	if err := RunMigrations(database); err != nil {
		database.Close()
		t.Fatalf("RunMigrations: %v", err)
	}
	if err := EnsureCoreSchema(database); err != nil {
		database.Close()
		t.Fatalf("EnsureCoreSchema: %v", err)
	}
	return NewRepo(database), func() { database.Close() }
}

func comboStrategyByID(t *testing.T, repo *Repo, id string) string {
	t.Helper()
	c, err := repo.GetComboById(id)
	if err != nil || c == nil {
		t.Fatalf("GetComboById(%s): combo=%v err=%v", id, c, err)
	}
	return c.Strategy
}

// TestComboStrategyPersistsAcrossReopen: the strategy given to CreateCombo and
// UpdateCombo is what GetComboById, GetComboByName, GetCombos and the router's
// settings read return, also after the database is closed and reopened.
// Before the fix the write went to a strategy column the schema does not have
// and the error was discarded, so every read came back "fallback".
func TestComboStrategyPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capimux.sqlite")

	repo, closeDB := openMigratedRepo(t, path)
	if err := repo.CreateCombo("c1", "mi-combo", "", `["a/x","b/y"]`, "round-robin"); err != nil {
		t.Fatalf("CreateCombo: %v", err)
	}
	if got := comboStrategyByID(t, repo, "c1"); got != "round-robin" {
		t.Fatalf("after create: strategy = %q, want round-robin", got)
	}
	if err := repo.UpdateCombo("c1", "mi-combo", "", `["a/x","b/y"]`, "sticky"); err != nil {
		t.Fatalf("UpdateCombo: %v", err)
	}
	if got := comboStrategyByID(t, repo, "c1"); got != "sticky" {
		t.Fatalf("after update: strategy = %q, want sticky", got)
	}
	closeDB()

	repo, closeDB = openMigratedRepo(t, path)
	defer closeDB()
	if got := comboStrategyByID(t, repo, "c1"); got != "sticky" {
		t.Fatalf("after reopen: GetComboById strategy = %q, want sticky", got)
	}
	byName, err := repo.GetComboByName("mi-combo")
	if err != nil || byName == nil || byName.Strategy != "sticky" {
		t.Fatalf("after reopen: GetComboByName = %+v err=%v, want sticky", byName, err)
	}
	all, err := repo.GetCombos()
	if err != nil || len(all) != 1 || all[0].Strategy != "sticky" {
		t.Fatalf("after reopen: GetCombos = %+v err=%v, want one combo with sticky", all, err)
	}
	s, err := repo.GetSettings()
	if err != nil || s.ComboStrategies["mi-combo"].Strategy != "sticky" {
		t.Fatalf("after reopen: settings.comboStrategies[mi-combo] = %+v err=%v, want sticky (the router reads it here)", s.ComboStrategies["mi-combo"], err)
	}
}

// TestComboStrategyUntouchedByOtherEdits: an UpdateCombo with an empty strategy
// (editing models, kind or name only) keeps the saved strategy, sticky limit
// and judge model, and a rename moves them to the new name.
func TestComboStrategyUntouchedByOtherEdits(t *testing.T) {
	repo, closeDB := openMigratedRepo(t, filepath.Join(t.TempDir(), "capimux.sqlite"))
	defer closeDB()

	if err := repo.CreateCombo("c1", "panel", "", `["a/x","b/y"]`, "fusion"); err != nil {
		t.Fatalf("CreateCombo: %v", err)
	}
	if err := repo.SetComboStrategy("panel", ComboStrategy{StickyLimit: 3, JudgeModel: "a/judge"}); err != nil {
		t.Fatalf("SetComboStrategy: %v", err)
	}

	if err := repo.UpdateCombo("c1", "panel", "llm", `["a/x","b/y","c/z"]`, ""); err != nil {
		t.Fatalf("UpdateCombo models: %v", err)
	}
	if got := comboStrategyByID(t, repo, "c1"); got != "fusion" {
		t.Fatalf("after models edit: strategy = %q, want fusion", got)
	}

	if err := repo.UpdateCombo("c1", "panel-v2", "llm", `["a/x","b/y","c/z"]`, ""); err != nil {
		t.Fatalf("UpdateCombo rename: %v", err)
	}
	if got := comboStrategyByID(t, repo, "c1"); got != "fusion" {
		t.Fatalf("after rename: strategy = %q, want fusion", got)
	}
	s, err := repo.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings: %v", err)
	}
	if _, stale := s.ComboStrategies["panel"]; stale {
		t.Errorf("rename left a stale strategy entry under the old name: %+v", s.ComboStrategies)
	}
	if cs := s.ComboStrategies["panel-v2"]; cs.Strategy != "fusion" || cs.StickyLimit != 3 || cs.JudgeModel != "a/judge" {
		t.Errorf("rename lost strategy settings: %+v", cs)
	}
}

// TestComboStrategyFallbackReadsBackWithGlobalDefault: saving "fallback" on a
// combo reads back as fallback even when a global comboStrategy is set.
func TestComboStrategyFallbackReadsBackWithGlobalDefault(t *testing.T) {
	repo, closeDB := openMigratedRepo(t, filepath.Join(t.TempDir(), "capimux.sqlite"))
	defer closeDB()

	if err := repo.UpdateSettingsRaw(map[string]any{"comboStrategy": "round-robin"}); err != nil {
		t.Fatalf("UpdateSettingsRaw: %v", err)
	}
	if err := repo.CreateCombo("c1", "orden", "", `["a/x","b/y"]`, "sticky"); err != nil {
		t.Fatalf("CreateCombo: %v", err)
	}
	if err := repo.UpdateCombo("c1", "orden", "", `["a/x","b/y"]`, "fallback"); err != nil {
		t.Fatalf("UpdateCombo: %v", err)
	}
	if got := comboStrategyByID(t, repo, "c1"); got != "fallback" {
		t.Fatalf("strategy = %q, want fallback", got)
	}
}
