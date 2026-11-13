package dbtest

import (
	"database/sql"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"9router/proxy/internal/db"
)

func TestSchemaStatements(t *testing.T) {
	stmts := SchemaStatements()
	want := []string{
		"apiKeys",
		"providerConnections",
		"kv",
		"combos",
		"settings",
		"providerNodes",
		"usageHistory",
		"usageDaily",
		"requestDetails",
	}
	if len(stmts) != len(want) {
		t.Fatalf("SchemaStatements() len = %d, want %d", len(stmts), len(want))
	}
	for i, name := range want {
		stmt := stmts[i]
		if !strings.Contains(stmt, "CREATE TABLE") {
			t.Errorf("statement %d for %s does not create a table", i, name)
		}
		if !strings.Contains(stmt, name) {
			t.Errorf("statement %d = %q, want table %s", i, stmt, name)
		}
	}
}

func TestCreateTablesBuildsWritableFixture(t *testing.T) {
	database := openTempDB(t)
	if err := CreateTables(database); err != nil {
		t.Fatalf("CreateTables: %v", err)
	}

	inserts := []string{
		`INSERT INTO apiKeys (id, key, isActive, createdAt) VALUES ('k1', 'client-key', 1, '2026-11-13T00:00:00Z')`,
		`INSERT INTO providerConnections (id, provider, authType, isActive, data, lastUsedAt, consecutiveUseCount, createdAt, updatedAt)
			VALUES ('c1', 'deepseek', 'apikey', 1, '{}', '2026-11-13T00:00:00Z', 2, '2026-11-13T00:00:00Z', '2026-11-13T00:00:00Z')`,
		`INSERT INTO kv (scope, key, value) VALUES ('modelAliases', 'fast-model', '"deepseek/deepseek-chat"')`,
		`INSERT INTO combos (id, name, models, createdAt, updatedAt) VALUES ('combo-1', 'fast', '[]', '2026-11-13T00:00:00Z', '2026-11-13T00:00:00Z')`,
		`INSERT INTO settings (id, data) VALUES (1, '{}')`,
		`INSERT INTO providerNodes (id, data, createdAt, updatedAt) VALUES ('n1', '{}', '2026-11-13T00:00:00Z', '2026-11-13T00:00:00Z')`,
		`INSERT INTO usageHistory (timestamp, provider, model, promptTokens, completionTokens, status)
			VALUES ('2026-11-13T00:00:00Z', 'deepseek', 'deepseek-chat', 3, 4, 'ok')`,
		`INSERT INTO usageDaily (dateKey, data) VALUES ('2026-11-13', '{}')`,
		`INSERT INTO requestDetails (id, timestamp, provider, model, status, data)
			VALUES ('r1', '2026-11-13T00:00:00Z', 'deepseek', 'deepseek-chat', 'ok', '{}')`,
	}
	for _, stmt := range inserts {
		if _, err := database.Exec(stmt); err != nil {
			t.Errorf("insert failed: %v\nSQL: %s", err, stmt)
		}
	}

	var provider string
	var uses int
	err := database.QueryRow(
		`SELECT provider, consecutiveUseCount FROM providerConnections WHERE id = 'c1'`,
	).Scan(&provider, &uses)
	if err != nil {
		t.Fatalf("read provider connection: %v", err)
	}
	if provider != "deepseek" || uses != 2 {
		t.Fatalf("provider connection = %s uses=%d, want deepseek uses=2", provider, uses)
	}

	var model string
	err = database.QueryRow(`SELECT provider, model FROM usageHistory`).Scan(&provider, &model)
	if err != nil {
		t.Fatalf("read usage history: %v", err)
	}
	if provider != "deepseek" || model != "deepseek-chat" {
		t.Fatalf("usage history = %s/%s, want deepseek/deepseek-chat", provider, model)
	}

	cols := tableColumns(t, database)
	for _, col := range []string{"lastUsedAt", "consecutiveUseCount"} {
		if _, ok := cols["providerConnections"][col]; !ok {
			t.Errorf("providerConnections missing additive column %s", col)
		}
	}
}

func TestCreateTablesClosedDB(t *testing.T) {
	database := openTempDB(t)
	if err := database.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	err := CreateTables(database)
	if err == nil {
		t.Fatal("CreateTables on a closed database succeeded")
	}
	if !strings.Contains(err.Error(), "create table") {
		t.Fatalf("CreateTables error = %v, want wrapped create table error", err)
	}
	if !strings.Contains(err.Error(), "apiKeys") {
		t.Fatalf("CreateTables error = %v, want the apiKeys statement that failed", err)
	}
}

// TestCreateTablesMatchesCoreSchema records that the fixture does not match
// the schema EnsureCoreSchema builds for a real database.
//
// Reproduction: build one database with CreateTables and another with
// EnsureCoreSchema, then compare sqlite table and column names.
// internal/dbtest/dbtest.go:12-88 omits _meta and proxyPools (declared in
// internal/db/schema.go:32-86) and omits usageHistory.id (schema.go:128-131).
// Removing the skip fails this test. Production code is unchanged.
func TestCreateTablesMatchesCoreSchema(t *testing.T) {
	t.Skip("bug: dbtest.SchemaStatements is missing _meta, proxyPools and usageHistory.id that db.EnsureCoreSchema creates; see dbtest.go:12-88 and schema.go:32-86,128-131")

	fixture := openTempDB(t)
	if err := CreateTables(fixture); err != nil {
		t.Fatalf("CreateTables: %v", err)
	}
	core := openTempDB(t)
	if err := db.EnsureCoreSchema(core); err != nil {
		t.Fatalf("EnsureCoreSchema: %v", err)
	}

	got := tableColumns(t, fixture)
	want := tableColumns(t, core)
	var missingTables []string
	for name := range want {
		if _, ok := got[name]; !ok {
			missingTables = append(missingTables, name)
		}
	}
	sort.Strings(missingTables)
	if len(missingTables) > 0 {
		t.Errorf("CreateTables missing tables: %s", strings.Join(missingTables, ", "))
	}
	for name, cols := range want {
		have, ok := got[name]
		if !ok {
			continue
		}
		var missingCols []string
		for col := range cols {
			if _, ok := have[col]; !ok {
				missingCols = append(missingCols, col)
			}
		}
		sort.Strings(missingCols)
		if len(missingCols) > 0 {
			t.Errorf("table %s missing columns: %s", name, strings.Join(missingCols, ", "))
		}
	}
}

func openTempDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.OpenDatabase(filepath.Join(t.TempDir(), "fixture.sqlite"))
	if err != nil {
		t.Fatalf("OpenDatabase: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func tableColumns(t *testing.T, database *sql.DB) map[string]map[string]struct{} {
	t.Helper()
	rows, err := database.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read tables: %v", err)
	}

	out := make(map[string]map[string]struct{}, len(names))
	for _, name := range names {
		cols := make(map[string]struct{})
		info, err := database.Query("PRAGMA table_info(" + quoteIdent(name) + ")")
		if err != nil {
			t.Fatalf("table_info %s: %v", name, err)
		}
		for info.Next() {
			var cid, notNull, pk int
			var colName, colType string
			var dflt sql.NullString
			if err := info.Scan(&cid, &colName, &colType, &notNull, &dflt, &pk); err != nil {
				info.Close()
				t.Fatalf("scan column %s: %v", name, err)
			}
			cols[colName] = struct{}{}
		}
		if err := info.Err(); err != nil {
			info.Close()
			t.Fatalf("read columns %s: %v", name, err)
		}
		info.Close()
		out[name] = cols
	}
	return out
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
