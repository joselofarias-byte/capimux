package app

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"

	"9router/proxy/internal/controlplane/registry"
	cpsync "9router/proxy/internal/controlplane/sync"
)

// StrictControlPlaneInitEnvVar makes startup fail closed when the Fabric
// registry or the DB-authoritative account sync cannot be loaded (audit
// phase 1, #103). Off by default: startup keeps logging a warning.
const StrictControlPlaneInitEnvVar = "CAPIMUX_STRICT_CONTROLPLANE_INIT"

func strictControlPlaneInitEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(StrictControlPlaneInitEnvVar))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Seams for tests.
var (
	initRegistryFn = registry.InitRegistry
	syncAccountsFn = cpsync.SyncAccountsFromDB
)

// initControlPlane loads the registry before the first request. In strict
// mode it also refreshes accounts from providerConnections right away, so no
// endpoint sees the snapshot's stale accounts, and returns any error so the
// server does not start serving with an unknown account state.
func initControlPlane(conn *sql.DB) error {
	if !strictControlPlaneInitEnabled() {
		if err := initRegistryFn(conn); err != nil {
			log.Printf("[config] registry init warning: %v", err)
		}
		return nil
	}
	if err := initRegistryFn(conn); err != nil {
		return fmt.Errorf("control plane init: registry: %w", err)
	}
	if err := syncAccountsFn(conn); err != nil {
		return fmt.Errorf("control plane init: account sync: %w", err)
	}
	return nil
}
