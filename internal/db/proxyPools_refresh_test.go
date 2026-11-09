package db

import "testing"

// Ported from #130 (#101 audit).
func TestGetProxyPool_RefreshesChangedSharedDBConfig(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	proxyPoolCache.Delete("pool-refresh")
	defer proxyPoolCache.Delete("pool-refresh")

	if _, err := database.Exec(`CREATE TABLE IF NOT EXISTS proxyPools (
		id TEXT PRIMARY KEY,
		data TEXT,
		isActive INTEGER DEFAULT 1,
		testStatus TEXT,
		createdAt TEXT,
		updatedAt TEXT
	);`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	repo := NewRepo(database)
	initial := `{"name":"shared","proxyUrl":"https://old.example","type":"http","strictProxy":false}`
	if _, err := database.Exec(`INSERT INTO proxyPools (id, data, isActive) VALUES (?, ?, 1)`, "pool-refresh", initial); err != nil {
		t.Fatalf("insert: %v", err)
	}

	first, err := repo.GetProxyPool("pool-refresh")
	if err != nil {
		t.Fatalf("first GetProxyPool: %v", err)
	}
	if first.StrictProxy || first.URLs[0] != "https://old.example" {
		t.Fatalf("unexpected initial pool: %#v", first)
	}

	updated := `{"name":"shared","proxyUrl":"https://new.example","type":"cloudflare","strictProxy":true}`
	if _, err := database.Exec(`UPDATE proxyPools SET data = ? WHERE id = ?`, updated, "pool-refresh"); err != nil {
		t.Fatalf("update shared DB: %v", err)
	}

	second, err := repo.GetProxyPool("pool-refresh")
	if err != nil {
		t.Fatalf("second GetProxyPool: %v", err)
	}
	if !second.StrictProxy || second.Type != "cloudflare" || len(second.URLs) != 1 || second.URLs[0] != "https://new.example" {
		t.Fatalf("expected refreshed pool config, got %#v", second)
	}
}

func TestProxyPoolNextURL_RoundRobinStartsWithFirstURL(t *testing.T) {
	pool := &ProxyPool{URLs: []string{"https://one.example", "https://two.example", "https://three.example"}}
	want := []string{"https://one.example", "https://two.example", "https://three.example", "https://one.example"}
	for i, expected := range want {
		if got := pool.NextURL(); got != expected {
			t.Fatalf("call %d: expected %s, got %s", i+1, expected, got)
		}
	}
}
