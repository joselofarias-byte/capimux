package dashboard

import (
	"context"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"9router/proxy/internal/db"
)

// probeTarget is a local stand-in for the provider: it counts every request,
// so a test can assert the connection test never reached it directly.
func probeTarget(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func insertProbePool(t *testing.T, repo *db.Repo, d db.ProxyPoolData, active bool) string {
	t.Helper()
	pool, err := repo.InsertProxyPool(d)
	if err != nil {
		t.Fatalf("insert pool: %v", err)
	}
	id := pool["id"].(string)
	if !active {
		if _, err := repo.RawDB().Exec(`UPDATE proxyPools SET isActive = 0 WHERE id = ?`, id); err != nil {
			t.Fatalf("deactivate pool: %v", err)
		}
	}
	return id
}

func createProbeConn(t *testing.T, repo *db.Repo, id string, blob map[string]any) {
	t.Helper()
	b, err := json.Marshal(blob)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateProviderConnectionFull(id, id, "apikey", "Node", nil, string(b)); err != nil {
		t.Fatalf("create connection: %v", err)
	}
}

// --- strictProxy: the test fails and the provider gets zero direct requests ---

func TestProbeStrict_FailsWithoutDirectTraffic(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, repo *db.Repo, base string) map[string]any
	}{
		{"missing pool", func(t *testing.T, repo *db.Repo, base string) map[string]any {
			return map[string]any{"apiKey": "sk", "strictProxy": true,
				"providerSpecificData": map[string]any{"baseUrl": base, "proxyPoolId": "missing-pool"}}
		}},
		{"inactive strict pool", func(t *testing.T, repo *db.Repo, base string) map[string]any {
			id := insertProbePool(t, repo, db.ProxyPoolData{Name: "p", ProxyURL: "http://127.0.0.1:1", Type: "http", StrictProxy: true}, false)
			return map[string]any{"apiKey": "sk", "providerSpecificData": map[string]any{"baseUrl": base, "proxyPoolId": id}}
		}},
		{"strict pool without URL", func(t *testing.T, repo *db.Repo, base string) map[string]any {
			id := insertProbePool(t, repo, db.ProxyPoolData{Name: "p", Type: "http", StrictProxy: true}, true)
			return map[string]any{"apiKey": "sk", "providerSpecificData": map[string]any{"baseUrl": base, "proxyPoolId": id}}
		}},
		{"invalid legacy proxy URL", func(t *testing.T, repo *db.Repo, base string) map[string]any {
			return map[string]any{"apiKey": "sk", "connectionProxyEnabled": true, "connectionProxyUrl": "://invalid",
				"providerSpecificData": map[string]any{"baseUrl": base, "strictProxy": true}}
		}},
		{"invalid strict pool URL", func(t *testing.T, repo *db.Repo, base string) map[string]any {
			id := insertProbePool(t, repo, db.ProxyPoolData{Name: "p", ProxyURL: "not a url", Type: "http", StrictProxy: true}, true)
			return map[string]any{"apiKey": "sk", "providerSpecificData": map[string]any{"baseUrl": base, "proxyPoolId": id}}
		}},
		{"unknown proxy type", func(t *testing.T, repo *db.Repo, base string) map[string]any {
			id := insertProbePool(t, repo, db.ProxyPoolData{Name: "p", ProxyURL: "http://127.0.0.1:1", Type: "mystery", StrictProxy: true}, true)
			return map[string]any{"apiKey": "sk", "providerSpecificData": map[string]any{"baseUrl": base, "proxyPoolId": id}}
		}},
		{"strict edge relay pool", func(t *testing.T, repo *db.Repo, base string) map[string]any {
			id := insertProbePool(t, repo, db.ProxyPoolData{Name: "p", ProxyURL: "https://relay.example.invalid", Type: "vercel", StrictProxy: true}, true)
			return map[string]any{"apiKey": "sk", "providerSpecificData": map[string]any{"baseUrl": base, "proxyPoolId": id}}
		}},
		{"strict without any proxy", func(t *testing.T, repo *db.Repo, base string) map[string]any {
			return map[string]any{"apiKey": "sk", "providerSpecificData": map[string]any{"baseUrl": base, "strictProxy": true}}
		}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, cleanup := setupProbeTestDB(t)
			defer cleanup()
			target, hits := probeTarget(t)
			id := "openai-compatible-chat-strict" + string(rune('a'+i))
			createProbeConn(t, repo, id, tc.setup(t, repo, target.URL+"/v1"))

			_, body := postTestConnection(t, repo, id)
			if body["valid"] != false {
				t.Fatalf("expected the strict test to fail, got %v", body)
			}
			if msg, _ := body["error"].(string); !strings.Contains(msg, "strict proxy") {
				t.Errorf("error = %v, want a strict proxy message", body["error"])
			}
			if got := hits.Load(); got != 0 {
				t.Fatalf("provider received %d direct request(s)", got)
			}
		})
	}
}

// failingProxy is a local HTTP proxy that rejects everything like an
// allowlisting proxy does (403 + X-Proxy-Error), which the probe helpers
// classify as a proxy failure.
func failingProxy(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Proxy-Error", "blocked-by-allowlist")
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func probeClientFor(t *testing.T, proxyURL string, strict bool) *http.Client {
	t.Helper()
	h := &DashboardHandler{}
	psd := map[string]any{"strictProxy": strict}
	client, _, err := h.probeHTTPClient(connectionProbeData{
		ConnectionProxyEnabled: true, ConnectionProxyURL: proxyURL, ProviderSpecificData: psd,
	}, map[string]any{})
	if err != nil || client == nil {
		t.Fatalf("probeHTTPClient: client=%v err=%v", client, err)
	}
	return client
}

// A failing mandatory proxy must not trigger the direct retry.
func TestProbeStrict_ProxyFailureNoDirectRetry(t *testing.T) {
	proxy := failingProxy(t)
	target, hits := probeTarget(t)
	client := probeClientFor(t, proxy.URL, true)

	if status, _, err := connectionProbeDo(context.Background(), client, http.MethodGet, target.URL+"/v1/models", nil, nil); err == nil && status == http.StatusOK {
		t.Fatalf("connectionProbeDo succeeded through a failing strict proxy (status %d)", status)
	}
	ctx := withProbeClient(context.Background(), client)
	if status, _, err := validateProbeDo(ctx, http.MethodGet, target.URL+"/v1/models", nil, nil); err == nil && status == http.StatusOK {
		t.Fatalf("validateProbeDo succeeded through a failing strict proxy (status %d)", status)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("provider received %d direct request(s)", got)
	}
}

// --- without strictProxy: behavior unchanged ---

func TestProbeNonStrict_ProxyFailureKeepsDirectRetry(t *testing.T) {
	proxy := failingProxy(t)
	target, hits := probeTarget(t)
	client := probeClientFor(t, proxy.URL, false)

	status, _, err := connectionProbeDo(context.Background(), client, http.MethodGet, target.URL+"/v1/models", nil, nil)
	if err != nil || status != http.StatusOK {
		t.Fatalf("non-strict direct retry: status=%d err=%v", status, err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("provider hits = %d, want 1 (direct retry)", got)
	}
}

func TestProbeNonStrict_DirectEgressUnchanged(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, repo *db.Repo, base string) map[string]any
	}{
		{"missing pool", func(t *testing.T, repo *db.Repo, base string) map[string]any {
			return map[string]any{"apiKey": "sk", "providerSpecificData": map[string]any{"baseUrl": base, "proxyPoolId": "missing-pool"}}
		}},
		{"inactive pool", func(t *testing.T, repo *db.Repo, base string) map[string]any {
			id := insertProbePool(t, repo, db.ProxyPoolData{Name: "p", ProxyURL: "http://127.0.0.1:1", Type: "http"}, false)
			return map[string]any{"apiKey": "sk", "providerSpecificData": map[string]any{"baseUrl": base, "proxyPoolId": id}}
		}},
		{"edge relay pool with noProxy", func(t *testing.T, repo *db.Repo, base string) map[string]any {
			id := insertProbePool(t, repo, db.ProxyPoolData{Name: "p", ProxyURL: "https://relay.example.invalid", NoProxy: "*", Type: "vercel"}, true)
			return map[string]any{"apiKey": "sk", "providerSpecificData": map[string]any{"baseUrl": base, "proxyPoolId": id}}
		}},
		{"no proxy at all", func(t *testing.T, repo *db.Repo, base string) map[string]any {
			return map[string]any{"apiKey": "sk", "providerSpecificData": map[string]any{"baseUrl": base}}
		}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, cleanup := setupProbeTestDB(t)
			defer cleanup()
			target, hits := probeTarget(t)
			id := "openai-compatible-chat-plain" + string(rune('a'+i))
			createProbeConn(t, repo, id, tc.setup(t, repo, target.URL+"/v1"))

			_, body := postTestConnection(t, repo, id)
			if body["valid"] != true {
				t.Fatalf("non-strict test must keep probing directly, got %v", body)
			}
			if got := hits.Load(); got != 1 {
				t.Fatalf("provider hits = %d, want 1", got)
			}
		})
	}
}
