package chat

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"9router/proxy/internal/db"
)

func newDirectCounter(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func assertFailsClosed(t *testing.T, h *ChatHandler, client *http.Client, target string, hits *atomic.Int32) {
	t.Helper()
	if client == h.Client {
		t.Fatal("strict proxy must not fall back to the direct client")
	}
	if _, err := client.Get(target); err == nil {
		t.Fatal("expected strict proxy client to fail closed")
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("strict proxy leaked %d direct request(s)", got)
	}
}

// Strictness set only on the pool (not on the connection) with an invalid pool URL.
func TestGetClientForConnection_PoolStrictInvalidURLFailsClosed(t *testing.T) {
	direct, hits := newDirectCounter(t)
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`CREATE TABLE IF NOT EXISTS proxyPools (
		id TEXT PRIMARY KEY, isActive INTEGER DEFAULT 1, testStatus TEXT,
		data TEXT NOT NULL, createdAt TEXT NOT NULL, updatedAt TEXT NOT NULL);`); err != nil {
		t.Fatalf("create proxyPools: %v", err)
	}
	repo := db.NewRepo(database)
	pool, err := repo.InsertProxyPool(db.ProxyPoolData{
		Name: "strict-invalid", ProxyURL: "://invalid", Type: "http", StrictProxy: true,
	})
	if err != nil {
		t.Fatalf("insert pool: %v", err)
	}
	h := &ChatHandler{Client: &http.Client{}, Repo: repo}
	client := h.GetClientForConnection(&ConnectionData{ProxyPoolID: pool["id"].(string)})
	assertFailsClosed(t, h, client, direct.URL, hits)
}

// Strict connection with no pool and the legacy proxy disabled: no route at all.
func TestGetClientForConnection_StrictNoProxyConfiguredFailsClosed(t *testing.T) {
	direct, hits := newDirectCounter(t)
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	h := &ChatHandler{Client: &http.Client{}, Repo: db.NewRepo(database)}
	client := h.GetClientForConnection(&ConnectionData{StrictProxy: true})
	assertFailsClosed(t, h, client, direct.URL, hits)
}

// Strict connection with the legacy proxy enabled but an empty URL.
func TestGetClientForConnection_StrictLegacyEnabledEmptyURLFailsClosed(t *testing.T) {
	direct, hits := newDirectCounter(t)
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	h := &ChatHandler{Client: &http.Client{}, Repo: db.NewRepo(database)}
	client := h.GetClientForConnection(&ConnectionData{
		StrictProxy: true, ConnectionProxyEnabled: true, ConnectionProxyURL: "",
	})
	assertFailsClosed(t, h, client, direct.URL, hits)
}
