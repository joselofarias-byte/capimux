package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"9router/proxy/internal/config"
	"9router/proxy/internal/deepseekweb"
)

// DeepSeek Web session credentials (and their encryption key) live in
// DATA_DIR/deepseek-web, outside the database: the dashboard export (download
// and Google Drive, which share exportDatabase) must never contain them.
func TestDatabaseExportNeverIncludesDeepSeekWebSessions(t *testing.T) {
	repo, cleanup := setupSettingsTestDB(t)
	defer cleanup()

	const fakeCred = "fake-dsh-web-session-NOT-REAL-export" // test only
	dir := filepath.Join(config.ResolveDataDir(), deepseekweb.DirName)
	m, err := deepseekweb.Open(deepseekweb.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Create(deepseekweb.CreateInput{Label: "export-test", Credential: []byte(fakeCred)})
	if err != nil {
		t.Fatal(err)
	}
	key, _ := os.ReadFile(filepath.Join(dir, "secret.key"))
	state, _ := os.ReadFile(filepath.Join(dir, "sessions.json"))
	var st struct {
		Sessions []struct {
			Sealed string `json:"sealedCredential"`
		} `json:"sessions"`
	}
	_ = json.Unmarshal(state, &st)
	if len(st.Sessions) != 1 || st.Sessions[0].Sealed == "" || len(key) == 0 {
		t.Fatal("fixture not written")
	}

	h := &DashboardHandler{Repo: repo}
	full, err := h.exportDatabase()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(full)
	exported := string(raw)
	for name, forbidden := range map[string]string{
		"credential":     fakeCred,
		"ciphertext":     st.Sessions[0].Sealed,
		"encryption key": strings.TrimSpace(string(key)),
		"session id":     s.ID,
	} {
		if strings.Contains(exported, forbidden) {
			t.Fatalf("database export contains the DeepSeek Web %s", name)
		}
	}
}
