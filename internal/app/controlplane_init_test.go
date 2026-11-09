package app

import (
	"database/sql"
	"errors"
	"testing"
)

func stubControlPlane(t *testing.T, regErr, syncErr error) *int {
	t.Helper()
	oldReg, oldSync := initRegistryFn, syncAccountsFn
	syncCalls := 0
	initRegistryFn = func(*sql.DB) error { return regErr }
	syncAccountsFn = func(*sql.DB) error { syncCalls++; return syncErr }
	t.Cleanup(func() { initRegistryFn, syncAccountsFn = oldReg, oldSync })
	return &syncCalls
}

func TestInitControlPlane_DefaultKeepsWarningOnly(t *testing.T) {
	t.Setenv(StrictControlPlaneInitEnvVar, "")
	calls := stubControlPlane(t, errors.New("boom"), errors.New("boom"))
	if err := initControlPlane(nil); err != nil {
		t.Fatalf("default mode must not fail startup, got %v", err)
	}
	if *calls != 0 {
		t.Fatalf("default mode must not add an account sync at startup, got %d calls", *calls)
	}
}

func TestInitControlPlane_StrictFailsOnRegistryError(t *testing.T) {
	t.Setenv(StrictControlPlaneInitEnvVar, "1")
	stubControlPlane(t, errors.New("registry down"), nil)
	if err := initControlPlane(nil); err == nil {
		t.Fatal("strict mode must fail when the registry cannot load")
	}
}

func TestInitControlPlane_StrictFailsWithoutAccounts(t *testing.T) {
	t.Setenv(StrictControlPlaneInitEnvVar, "true")
	calls := stubControlPlane(t, nil, errors.New("db is nil"))
	if err := initControlPlane(nil); err == nil {
		t.Fatal("strict mode must fail when the account sync fails")
	}
	if *calls != 1 {
		t.Fatalf("expected one account sync, got %d", *calls)
	}
}

func TestInitControlPlane_StrictOK(t *testing.T) {
	t.Setenv(StrictControlPlaneInitEnvVar, "on")
	calls := stubControlPlane(t, nil, nil)
	if err := initControlPlane(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("expected one account sync, got %d", *calls)
	}
}
