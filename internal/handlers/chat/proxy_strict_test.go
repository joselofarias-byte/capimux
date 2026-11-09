package chat

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"9router/proxy/internal/db"
)

// Ported from #130 (#99/#101 audit): strict proxy must fail closed.
func TestGetClientForConnection_StrictInvalidProxyFailsClosed(t *testing.T) {
	var directHits atomic.Int32
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer direct.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	h := &ChatHandler{Client: &http.Client{}, Repo: db.NewRepo(database)}

	connData := &ConnectionData{
		ConnectionProxyEnabled: true,
		ConnectionProxyURL:     "://invalid",
		StrictProxy:            true,
	}

	client := h.GetClientForConnection(connData)
	if client == h.Client {
		t.Fatal("strict invalid proxy must not fall back to the direct client")
	}
	_, err := client.Get(direct.URL)
	if err == nil {
		t.Fatal("expected strict invalid proxy client to fail closed")
	}
	if got := directHits.Load(); got != 0 {
		t.Fatalf("strict proxy failure leaked %d direct request(s)", got)
	}
}

func TestGetClientForConnection_StrictUnknownProxyTypeFailsClosed(t *testing.T) {
	var directHits atomic.Int32
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer direct.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`CREATE TABLE IF NOT EXISTS proxyPools (
		id TEXT PRIMARY KEY,
		isActive INTEGER DEFAULT 1,
		testStatus TEXT,
		data TEXT NOT NULL,
		createdAt TEXT NOT NULL,
		updatedAt TEXT NOT NULL
	);`); err != nil {
		t.Fatalf("failed to create proxyPools table: %v", err)
	}

	repo := db.NewRepo(database)
	pool, err := repo.InsertProxyPool(db.ProxyPoolData{
		Name:        "bad-type",
		ProxyURL:    "https://proxy.example.invalid",
		Type:        "mystery-proxy",
		StrictProxy: true,
	})
	if err != nil {
		t.Fatalf("failed to insert proxy pool: %v", err)
	}

	h := &ChatHandler{Client: &http.Client{}, Repo: repo}
	client := h.GetClientForConnection(&ConnectionData{ProxyPoolID: pool["id"].(string)})
	if client == h.Client {
		t.Fatal("strict unknown proxy type must not fall back to the direct client")
	}
	_, err = client.Get(direct.URL)
	if err == nil {
		t.Fatal("expected strict unknown proxy type to fail closed")
	}
	if got := directHits.Load(); got != 0 {
		t.Fatalf("strict unknown proxy type leaked %d direct request(s)", got)
	}
}

func TestGetClientForConnection_StrictMissingPoolFailsClosed(t *testing.T) {
	var directHits atomic.Int32
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer direct.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	h := &ChatHandler{Client: &http.Client{}, Repo: db.NewRepo(database)}

	client := h.GetClientForConnection(&ConnectionData{
		ProxyPoolID: "missing-pool",
		StrictProxy: true,
	})
	_, err := client.Get(direct.URL)
	if err == nil {
		t.Fatal("expected missing strict proxy pool to fail closed")
	}
	if got := directHits.Load(); got != 0 {
		t.Fatalf("missing strict proxy pool leaked %d direct request(s)", got)
	}
}

func TestGetClientForConnection_StrictPoolWithoutURLFailsClosed(t *testing.T) {
	var directHits atomic.Int32
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer direct.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`CREATE TABLE IF NOT EXISTS proxyPools (
		id TEXT PRIMARY KEY,
		isActive INTEGER DEFAULT 1,
		testStatus TEXT,
		data TEXT NOT NULL,
		createdAt TEXT NOT NULL,
		updatedAt TEXT NOT NULL
	);`); err != nil {
		t.Fatalf("create proxyPools: %v", err)
	}

	repo := db.NewRepo(database)
	pool, err := repo.InsertProxyPool(db.ProxyPoolData{
		Name:        "empty-strict",
		Type:        "http",
		StrictProxy: true,
	})
	if err != nil {
		t.Fatalf("insert empty strict pool: %v", err)
	}

	h := &ChatHandler{Client: &http.Client{}, Repo: repo}
	client := h.GetClientForConnection(&ConnectionData{ProxyPoolID: pool["id"].(string)})
	_, err = client.Get(direct.URL)
	if err == nil {
		t.Fatal("expected strict pool without URL to fail closed")
	}
	if got := directHits.Load(); got != 0 {
		t.Fatalf("strict pool without URL leaked %d direct request(s)", got)
	}
}

func TestGetClientForConnection_StrictInactivePoolFailsClosed(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`CREATE TABLE IF NOT EXISTS proxyPools (
		id TEXT PRIMARY KEY,
		isActive INTEGER DEFAULT 1,
		testStatus TEXT,
		data TEXT NOT NULL,
		createdAt TEXT NOT NULL,
		updatedAt TEXT NOT NULL
	);`); err != nil {
		t.Fatalf("create proxyPools: %v", err)
	}

	repo := db.NewRepo(database)
	pool, err := repo.InsertProxyPool(db.ProxyPoolData{
		Name:        "inactive-strict",
		ProxyURL:    "http://proxy.example.invalid:8080",
		Type:        "http",
		StrictProxy: true,
	})
	if err != nil {
		t.Fatalf("insert strict pool: %v", err)
	}
	poolID := pool["id"].(string)
	if _, err := database.Exec(`UPDATE proxyPools SET isActive = 0 WHERE id = ?`, poolID); err != nil {
		t.Fatalf("deactivate pool: %v", err)
	}

	h := &ChatHandler{Client: &http.Client{}, Repo: repo}
	client := h.GetClientForConnection(&ConnectionData{ProxyPoolID: poolID})
	if client == h.Client {
		t.Fatal("inactive strict pool must not return direct client")
	}
	_, err = client.Get("http://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected inactive strict pool to fail closed")
	}
}

// A non-strict connection keeps main's behaviour: no usable proxy -> direct client.
func TestGetClientForConnection_NonStrictMissingPoolUsesDirectClient(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	h := &ChatHandler{Client: &http.Client{}, Repo: db.NewRepo(database)}
	if got := h.GetClientForConnection(&ConnectionData{ProxyPoolID: "missing-pool"}); got != h.Client {
		t.Fatal("non-strict missing pool must keep using the direct client")
	}
}

// strictProxy in providerSpecificData is honoured even when the legacy proxy is enabled.
func TestGetClientForConnection_StrictFromProviderSpecificDataFailsClosed(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	h := &ChatHandler{Client: &http.Client{}, Repo: db.NewRepo(database)}
	client := h.GetClientForConnection(&ConnectionData{
		ConnectionProxyEnabled: true,
		ConnectionProxyURL:     "://invalid",
		ProviderSpecificData:   map[string]any{"strictProxy": true},
	})
	if client == h.Client {
		t.Fatal("strictProxy from providerSpecificData must not fall back to direct")
	}
}
