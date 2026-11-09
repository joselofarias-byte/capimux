package dashboard

import (
	"bytes"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"9router/proxy/internal/auth"
	"9router/proxy/internal/db"
)

func seedPartialBackupDB(t *testing.T, repo *db.Repo) {
	t.Helper()
	for _, stmt := range []string{
		`INSERT INTO providerConnections (id, provider, authType, name, email, priority, isActive, data, createdAt, updatedAt)
		 VALUES ('conn-1', 'openai', 'apikey', 'Primaria', '', 1, 1, '{"apiKey":"sk-prueba"}', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO combos (id, name, kind, models, createdAt, updatedAt)
		 VALUES ('combo-a', 'rapido', 'llm', '["openai/gpt"]', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		`INSERT INTO kv (scope, key, value) VALUES ('pricing', 'openai', '{"gpt":1.0}')`,
	} {
		if _, err := repo.RawDB().Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
}

func exportPartial(t *testing.T, router http.Handler, query string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/settings/database"+query, nil)
	req.Header.Set(cliTokenHeader, auth.CLIToken())
	req.Header.Set(backupPassphraseHeader, testBackupPassphrase)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return rec.Code, nil
	}
	plain, err := decryptBackup(rec.Body.Bytes(), testBackupPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(plain, &payload); err != nil {
		t.Fatal(err)
	}
	if cd := rec.Header().Get("Content-Disposition"); query != "" && !bytes.Contains([]byte(cd), []byte("backup-parcial")) {
		t.Errorf("nombre de archivo parcial esperado, got %q", cd)
	}
	return rec.Code, payload
}

func importJSON(t *testing.T, router http.Handler, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/settings/database", bytes.NewReader(body))
	req.Header.Set(cliTokenHeader, auth.CLIToken())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func countRows(t *testing.T, repo *db.Repo, q string) int {
	t.Helper()
	var n int
	if err := repo.RawDB().QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPartialExportOnlyListedSections(t *testing.T) {
	repo, cleanup := setupSettingsTestDB(t)
	defer cleanup()
	seedPartialBackupDB(t, repo)
	router := setupTestRouter(repo)

	code, payload := exportPartial(t, router, "?sections=combos,pricing")
	if code != http.StatusOK {
		t.Fatalf("export = %d", code)
	}
	var keys []string
	for k := range payload {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"combos", "pricing", "sections"}) {
		t.Fatalf("claves = %v", keys)
	}
	if _, leaked := payload["providerConnections"]; leaked {
		t.Fatal("una exportación parcial sin conexiones no debe incluir credenciales")
	}
}

func TestPartialExportRejectsUnknownSection(t *testing.T) {
	repo, cleanup := setupSettingsTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)
	if code, _ := exportPartial(t, router, "?sections=combos,secretos"); code != http.StatusBadRequest {
		t.Fatalf("sección desconocida = %d, se esperaba 400", code)
	}
}

// Restoring a partial backup replaces only its sections: combos go back to
// the exported state, connections and pricing are untouched.
func TestPartialImportTouchesOnlyItsSections(t *testing.T) {
	repo, cleanup := setupSettingsTestDB(t)
	defer cleanup()
	seedPartialBackupDB(t, repo)
	router := setupTestRouter(repo)

	_, payload := exportPartial(t, router, "?sections=combos")

	for _, stmt := range []string{
		`INSERT INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES ('combo-b', 'nuevo', 'llm', '[]', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`,
		`INSERT INTO providerConnections (id, provider, authType, name, email, priority, isActive, data, createdAt, updatedAt)
		 VALUES ('conn-2', 'groq', 'apikey', 'Segunda', '', 2, 1, '{"apiKey":"sk-dos"}', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z')`,
	} {
		if _, err := repo.RawDB().Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}

	if rec := importJSON(t, router, payload); rec.Code != http.StatusOK {
		t.Fatalf("import = %d %s", rec.Code, rec.Body.String())
	}
	if n := countRows(t, repo, `SELECT COUNT(*) FROM combos`); n != 1 {
		t.Fatalf("combos = %d, se esperaba 1 (el exportado)", n)
	}
	if n := countRows(t, repo, `SELECT COUNT(*) FROM providerConnections`); n != 2 {
		t.Fatalf("conexiones = %d, se esperaban 2 (intactas)", n)
	}
	if n := countRows(t, repo, `SELECT COUNT(*) FROM kv WHERE scope='pricing'`); n != 1 {
		t.Fatalf("pricing = %d, se esperaba 1 (intacto)", n)
	}
}

// Negative: a crafted partial file cannot write a section it does not list.
func TestPartialImportIgnoresUnlistedKeys(t *testing.T) {
	repo, cleanup := setupSettingsTestDB(t)
	defer cleanup()
	seedPartialBackupDB(t, repo)
	router := setupTestRouter(repo)

	payload := map[string]any{
		"sections": []any{"combos"},
		"combos":   []any{},
		"providerConnections": []any{map[string]any{
			"id": "conn-intrusa", "provider": "openai", "authType": "apikey", "priority": 9, "isActive": true, "apiKey": "sk-intrusa",
		}},
	}
	if rec := importJSON(t, router, payload); rec.Code != http.StatusOK {
		t.Fatalf("import = %d %s", rec.Code, rec.Body.String())
	}
	if n := countRows(t, repo, `SELECT COUNT(*) FROM providerConnections WHERE id='conn-intrusa'`); n != 0 {
		t.Fatal("se escribió una sección no listada")
	}
	if n := countRows(t, repo, `SELECT COUNT(*) FROM providerConnections`); n != 1 {
		t.Fatalf("conexiones = %d, se esperaba 1", n)
	}
}

func TestPartialImportRejectsMalformedSections(t *testing.T) {
	repo, cleanup := setupSettingsTestDB(t)
	defer cleanup()
	seedPartialBackupDB(t, repo)
	router := setupTestRouter(repo)
	for _, bad := range []any{"combos", []any{}, []any{"desconocida"}, []any{1}} {
		if rec := importJSON(t, router, map[string]any{"sections": bad, "combos": []any{}}); rec.Code != http.StatusBadRequest {
			t.Errorf("sections=%v → %d, se esperaba 400", bad, rec.Code)
		}
	}
	if n := countRows(t, repo, `SELECT COUNT(*) FROM combos`); n != 1 {
		t.Fatalf("un import rechazado no debe tocar datos: combos=%d", n)
	}
}

// Regression: a payload without "sections" is still a full restore.
func TestFullImportStillWipesEverything(t *testing.T) {
	repo, cleanup := setupSettingsTestDB(t)
	defer cleanup()
	seedPartialBackupDB(t, repo)
	router := setupTestRouter(repo)
	if rec := importJSON(t, router, map[string]any{"combos": []any{}}); rec.Code != http.StatusOK {
		t.Fatalf("import = %d", rec.Code)
	}
	if n := countRows(t, repo, `SELECT COUNT(*) FROM providerConnections`); n != 0 {
		t.Fatalf("restauración completa debe reemplazar conexiones: %d", n)
	}
}

// Negative: without the dashboard password or CLI token, a partial export is
// refused like a full one.
func TestPartialExportRequiresPassword(t *testing.T) {
	repo, cleanup := setupSettingsTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)
	req := httptest.NewRequest(http.MethodGet, "/api/settings/database?sections=combos", nil)
	req.Header.Set(backupPassphraseHeader, testBackupPassphrase)
	req.Header.Set("Authorization", "Bearer sk-cualquiera")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatal("exportación parcial sin contraseña permitida")
	}
}
