package chat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"9router/proxy/internal/db"
)

// countingServer is a local listener that counts every request it receives.
func countingServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func relayTestHandler(t *testing.T) (*ChatHandler, *db.Repo) {
	t.Helper()
	database, cleanup := setupChatTestDB(t)
	t.Cleanup(cleanup)
	if _, err := database.Exec(`CREATE TABLE IF NOT EXISTS proxyPools (
		id TEXT PRIMARY KEY, isActive INTEGER DEFAULT 1, testStatus TEXT,
		data TEXT NOT NULL, createdAt TEXT NOT NULL, updatedAt TEXT NOT NULL);`); err != nil {
		t.Fatalf("create proxyPools: %v", err)
	}
	repo := db.NewRepo(database)
	return &ChatHandler{Client: &http.Client{}, Repo: repo}, repo
}

func insertRelayPool(t *testing.T, repo *db.Repo, relayURL, noProxy string, strict bool) string {
	t.Helper()
	pool, err := repo.InsertProxyPool(db.ProxyPoolData{
		Name: "relay", ProxyURL: relayURL, NoProxy: noProxy, Type: "vercel", StrictProxy: strict,
	})
	if err != nil {
		t.Fatalf("insert relay pool: %v", err)
	}
	return pool["id"].(string)
}

const remoteUpstream = "https://api.example.invalid/v1"

// --- strictProxy: negative tests (no direct traffic) ---

func TestRelay_StrictPoolNoProxyMatchRefusesBypass(t *testing.T) {
	h, repo := relayTestHandler(t)
	relay, relayHits := countingServer(t)
	direct, directHits := countingServer(t)
	poolID := insertRelayPool(t, repo, relay.URL, "api.example.invalid", true)
	conn := &ConnectionData{BaseURL: remoteUpstream, ProxyPoolID: poolID}

	cfg, err := h.getProviderConfig("custom-relay-test", conn)
	if err == nil || !strings.Contains(err.Error(), "noProxy") {
		t.Fatalf("strict relay + noProxy match must fail, got cfg=%+v err=%v", cfg, err)
	}
	// A caller that ignores the error and dials the upstream itself is still
	// blocked: the strict relay client only reaches the relay host.
	client := h.GetClientForConnection(conn)
	if client == h.Client {
		t.Fatal("strict relay must not return the direct client")
	}
	if _, err := client.Get(direct.URL); err == nil {
		t.Fatal("strict relay client must refuse a non-relay host")
	}
	if directHits.Load() != 0 || relayHits.Load() != 0 {
		t.Fatalf("direct=%d relay=%d, want 0/0", directHits.Load(), relayHits.Load())
	}
}

func TestRelay_StrictConnectionLoopbackUpstreamRefusesBypass(t *testing.T) {
	h, repo := relayTestHandler(t)
	relay, _ := countingServer(t)
	direct, directHits := countingServer(t) // 127.0.0.1: loopback upstream
	poolID := insertRelayPool(t, repo, relay.URL, "", false)
	conn := &ConnectionData{BaseURL: direct.URL, ProxyPoolID: poolID, StrictProxy: true}

	if _, err := h.getProviderConfig("custom-relay-test", conn); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("strict relay + loopback upstream must fail, got %v", err)
	}
	client := h.GetClientForConnection(conn)
	if _, err := client.Get(direct.URL); err == nil {
		t.Fatal("strict relay client must refuse the loopback upstream")
	}
	if got := directHits.Load(); got != 0 {
		t.Fatalf("loopback upstream received %d direct request(s)", got)
	}
}

func TestRelay_StrictProviderSpecificRelayNoProxyRefusesBypass(t *testing.T) {
	h, _ := relayTestHandler(t)
	conn := &ConnectionData{
		BaseURL: remoteUpstream,
		ProviderSpecificData: map[string]any{
			"vercelRelayUrl":    "https://relay.example.invalid",
			"connectionNoProxy": "*",
			"strictProxy":       true,
		},
	}
	if _, err := h.getProviderConfig("custom-relay-test", conn); err == nil {
		t.Fatal("strict providerSpecificData relay + noProxy=* must fail")
	}
}

func TestRelay_StrictRelayStillRoutesThroughRelay(t *testing.T) {
	h, repo := relayTestHandler(t)
	relay, relayHits := countingServer(t)
	poolID := insertRelayPool(t, repo, relay.URL, "", true)
	conn := &ConnectionData{BaseURL: remoteUpstream, ProxyPoolID: poolID}

	cfg, err := h.getProviderConfig("custom-relay-test", conn)
	if err != nil {
		t.Fatalf("strict relay with remote upstream: %v", err)
	}
	if cfg.BaseURL != relay.URL {
		t.Fatalf("BaseURL = %q, want relay %q", cfg.BaseURL, relay.URL)
	}
	resp, err := h.GetClientForConnection(conn).Get(cfg.BaseURL + "/chat/completions")
	if err != nil {
		t.Fatalf("relay request: %v", err)
	}
	resp.Body.Close()
	if relayHits.Load() != 1 {
		t.Fatalf("relay hits = %d, want 1", relayHits.Load())
	}
}

func TestRelay_StrictRelayPoolWithInvalidURLFailsClosed(t *testing.T) {
	h, repo := relayTestHandler(t)
	direct, directHits := countingServer(t)
	poolID := insertRelayPool(t, repo, "not a url", "", true)
	client := h.GetClientForConnection(&ConnectionData{ProxyPoolID: poolID})
	if _, err := client.Get(direct.URL); err == nil {
		t.Fatal("strict relay pool without a valid URL must fail closed")
	}
	if directHits.Load() != 0 {
		t.Fatalf("direct hits = %d, want 0", directHits.Load())
	}
}

// --- without strictProxy: behavior unchanged ---

func TestRelay_NonStrictNoProxyMatchKeepsDirectUpstream(t *testing.T) {
	h, repo := relayTestHandler(t)
	relay, relayHits := countingServer(t)
	direct, directHits := countingServer(t)
	poolID := insertRelayPool(t, repo, relay.URL, "*", false)
	conn := &ConnectionData{BaseURL: direct.URL, ProxyPoolID: poolID}

	cfg, err := h.getProviderConfig("custom-relay-test", conn)
	if err != nil {
		t.Fatalf("non-strict: %v", err)
	}
	if cfg.BaseURL != direct.URL {
		t.Fatalf("BaseURL = %q, want the direct upstream", cfg.BaseURL)
	}
	client := h.GetClientForConnection(conn)
	if client != h.Client {
		t.Fatal("non-strict relay must keep the standard client")
	}
	resp, err := client.Get(cfg.BaseURL)
	if err != nil {
		t.Fatalf("direct request: %v", err)
	}
	resp.Body.Close()
	if directHits.Load() != 1 || relayHits.Load() != 0 {
		t.Fatalf("direct=%d relay=%d, want 1/0", directHits.Load(), relayHits.Load())
	}
}

func TestRelay_NonStrictLoopbackUpstreamSkipsRelay(t *testing.T) {
	h, repo := relayTestHandler(t)
	relay, _ := countingServer(t)
	direct, _ := countingServer(t)
	poolID := insertRelayPool(t, repo, relay.URL, "", false)
	cfg, err := h.getProviderConfig("custom-relay-test", &ConnectionData{BaseURL: direct.URL, ProxyPoolID: poolID})
	if err != nil {
		t.Fatalf("non-strict loopback: %v", err)
	}
	if cfg.BaseURL != direct.URL {
		t.Fatalf("BaseURL = %q, want loopback upstream unchanged", cfg.BaseURL)
	}
}

func TestRelay_NonStrictRemoteUpstreamUsesRelay(t *testing.T) {
	h, repo := relayTestHandler(t)
	relay, _ := countingServer(t)
	poolID := insertRelayPool(t, repo, relay.URL, "", false)
	cfg, err := h.getProviderConfig("custom-relay-test", &ConnectionData{BaseURL: remoteUpstream, ProxyPoolID: poolID})
	if err != nil {
		t.Fatalf("non-strict remote: %v", err)
	}
	if cfg.BaseURL != relay.URL {
		t.Fatalf("BaseURL = %q, want relay", cfg.BaseURL)
	}
}

func TestRelay_NonStrictProviderSpecificNoProxyKeepsDirectUpstream(t *testing.T) {
	h, _ := relayTestHandler(t)
	cfg, err := h.getProviderConfig("custom-relay-test", &ConnectionData{
		BaseURL: remoteUpstream,
		ProviderSpecificData: map[string]any{
			"vercelRelayUrl":    "https://relay.example.invalid",
			"connectionNoProxy": "*",
		},
	})
	if err != nil {
		t.Fatalf("non-strict psd: %v", err)
	}
	if cfg.BaseURL != remoteUpstream {
		t.Fatalf("BaseURL = %q, want the upstream unchanged", cfg.BaseURL)
	}
}
