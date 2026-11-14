package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"9router/proxy/internal/config"
	"9router/proxy/internal/entitlements"
)

// The per-installation Ed25519 proof key lives in DATA_DIR/license and must
// never be copied into general backups (download export or Google Drive
// upload, which share exportDatabase).
func TestDatabaseExportNeverIncludesInstallationProofKey(t *testing.T) {
	repo, cleanup := setupSettingsTestDB(t)
	defer cleanup()

	store := entitlements.NewRuntimeStore(config.ResolveDataDir())
	identity, err := store.InstallationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	seedFile, err := os.ReadFile(filepath.Join(store.Dir(), "installation-key"))
	if err != nil {
		t.Fatal(err)
	}
	seed := strings.TrimSpace(string(seedFile))
	if seed == "" {
		t.Fatal("installation key file is empty")
	}

	h := &DashboardHandler{Repo: repo}
	full, err := h.exportDatabase()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	exported := string(raw)
	for name, forbidden := range map[string]string{
		"proof key seed":   seed,
		"installation id":  identity.ID,
		"key file name":    "installation-key",
		"lease cache name": "lease.json",
	} {
		if strings.Contains(exported, forbidden) {
			t.Fatalf("database export contains the %s", name)
		}
	}
}
