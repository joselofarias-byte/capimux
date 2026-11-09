package registry

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func openSnapshotDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "snap.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE registry_snapshots (version TEXT PRIMARY KEY, created_at TEXT NOT NULL, reason TEXT NOT NULL, checksum TEXT NOT NULL, status TEXT NOT NULL, payload TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func stateWithAccount(id string, active bool) *RegistryState {
	return &RegistryState{
		Providers:      map[string]*Provider{},
		Models:         map[string]*Model{},
		ProviderModels: map[string]map[string]*ProviderModel{},
		Accounts:       map[string]*Account{id: {ID: id, ProviderID: "p", IsActive: active}},
	}
}

// A discovery snapshot copied an account that was later disabled in the DB.
func activateStaleSnapshot(t *testing.T, preserve bool) *RegistryState {
	db := openSnapshotDB(t)
	first, err := CreateSnapshot(db, stateWithAccount("acc-1", true), "init")
	if err != nil {
		t.Fatal(err)
	}
	if err := ActivateSnapshot(db, first.Version); err != nil {
		t.Fatal(err)
	}
	stale, err := CreateSnapshot(db, stateWithAccount("acc-1", true), "discovery_sync")
	if err != nil {
		t.Fatal(err)
	}
	// DB-authoritative sync disables the account while discovery was running.
	UpdateAccounts(map[string]*Account{"acc-1": {ID: "acc-1", ProviderID: "p", IsActive: false}})
	if preserve {
		err = ActivateSnapshotPreservingAccounts(db, stale.Version)
	} else {
		err = ActivateSnapshot(db, stale.Version)
	}
	if err != nil {
		t.Fatal(err)
	}
	return GetActiveState()
}

func TestActivateSnapshotPreservingAccounts_KeepsDisabledAccount(t *testing.T) {
	st := activateStaleSnapshot(t, true)
	if acc := st.Accounts["acc-1"]; acc == nil || acc.IsActive {
		t.Fatalf("disabled account must stay disabled, got %+v", acc)
	}
}

// Regression: plain ActivateSnapshot keeps its historical behavior (the
// snapshot copy wins; the orchestrator re-syncs accounts afterwards).
func TestActivateSnapshot_DefaultBehaviorUnchanged(t *testing.T) {
	st := activateStaleSnapshot(t, false)
	if acc := st.Accounts["acc-1"]; acc == nil || !acc.IsActive {
		t.Fatalf("default activation must load the snapshot's accounts, got %+v", acc)
	}
}

func TestActivateSnapshotPreservingAccounts_CopiesAccounts(t *testing.T) {
	st := activateStaleSnapshot(t, true)
	UpdateAccounts(map[string]*Account{"acc-2": {ID: "acc-2"}})
	if _, ok := st.Accounts["acc-2"]; ok {
		t.Fatal("published state must not share its account map with later updates")
	}
}
