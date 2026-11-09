//go:build integration

package integration

import (
	"net/http"
	"testing"
)

// Negative: a client API key cannot export or import (partial or full)
// backups, with login on or off.
func TestPartialBackupDeniedToAPIKey(t *testing.T) {
	for _, requireLogin := range []bool{false, true} {
		env, _ := newProviderEnv(t)
		if err := env.Repo.UpdateSettingsRaw(map[string]any{"requireLogin": requireLogin}); err != nil {
			t.Fatal(err)
		}
		if err := env.Repo.CreateCombo("combo-x", "existente", "llm", `["deepseek/deepseek-chat"]`, "fallback"); err != nil {
			t.Fatal(err)
		}
		hdr := WithHeader("x-9r-backup-passphrase", "frase-de-prueba-larga-123")
		res := env.Get(t, "/api/settings/database?sections=combos", hdr)
		t.Logf("login=%v export con API key → %d", requireLogin, res.Status)
		if res.Status == http.StatusOK {
			t.Errorf("login=%v: API key exportó un respaldo parcial", requireLogin)
		}
		body := map[string]any{"sections": []any{"combos"}, "combos": []any{}}
		res = env.Post(t, "/api/settings/database", body)
		t.Logf("login=%v import con API key → %d", requireLogin, res.Status)
		if res.Status == http.StatusOK {
			t.Errorf("login=%v: API key importó un respaldo parcial", requireLogin)
		}
		combos, err := env.Repo.GetCombos()
		if err != nil || len(combos) != 1 {
			t.Fatalf("login=%v: combos tras el intento = %d (%v), se esperaba 1 intacto", requireLogin, len(combos), err)
		}
	}
}
