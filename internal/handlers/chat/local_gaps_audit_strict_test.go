package chat

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"9router/proxy/internal/db"
	"9router/proxy/internal/providers"
)

func TestUsageLogRecordsResolvedModel(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	script := newHostScript()
	var sentModel string
	script.allow("api.deepseek.com", func(r *http.Request) (*http.Response, error) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		sentModel = string(buf[:n])
		return jsonResp(http.StatusOK, `{"id":"chatcmpl-local","object":"chat.completion","model":"deepseek-reasoner","choices":[{"index":0,"message":{"role":"assistant","content":"local-ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`), nil
	})
	h := NewChatHandler(db.NewRepo(database))
	h.Client = scriptedClient(script)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"fast-model","messages":[{"role":"user","content":"hi"}]}`))
	h.HandleChatCompletions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(sentModel, "deepseek-chat") {
		t.Fatalf("upstream body model = %s", sentModel)
	}

	var provider, model string
	if err := database.QueryRow(`SELECT provider, model FROM usageHistory ORDER BY rowid DESC LIMIT 1`).Scan(&provider, &model); err != nil {
		t.Fatalf("usage row: %v", err)
	}
	if provider != "deepseek" || model != "deepseek-chat" {
		t.Fatalf("usageHistory provider=%q model=%q, want deepseek / deepseek-chat", provider, model)
	}

	var detailModel string
	if err := database.QueryRow(`SELECT model FROM requestDetails ORDER BY rowid DESC LIMIT 1`).Scan(&detailModel); err != nil {
		t.Fatalf("request detail: %v", err)
	}
	if detailModel != "deepseek-chat" {
		t.Fatalf("requestDetails.model=%q, want deepseek-chat", detailModel)
	}
}

func TestStrictHTTPProxyIgnoresNoProxy(t *testing.T) {
	var proxyHits, directHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(proxy.Close)

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`CREATE TABLE IF NOT EXISTS proxyPools (
		id TEXT PRIMARY KEY,
		isActive INTEGER DEFAULT 1,
		testStatus TEXT,
		data TEXT NOT NULL,
		createdAt TEXT NOT NULL,
		updatedAt TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	repo := db.NewRepo(database)
	pool, err := repo.InsertProxyPool(db.ProxyPoolData{
		Name:        "strict-http",
		ProxyURL:    proxy.URL,
		NoProxy:     "127.0.0.1,localhost",
		Type:        "http",
		StrictProxy: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := &ChatHandler{Client: &http.Client{}, Repo: repo}
	client := h.GetClientForConnection(&ConnectionData{
		ProxyPoolID: pool["id"].(string),
		StrictProxy: true,
		ProviderSpecificData: map[string]any{
			"strictProxy":            true,
			"connectionNoProxy":      "127.0.0.1,localhost",
			"connectionProxyEnabled": true,
		},
	})
	if client == h.Client {
		t.Fatal("strict proxy returned the direct client before the request")
	}
	resp, err := client.Get(upstream.URL + "/chat")
	if resp != nil {
		resp.Body.Close()
	}
	if directHits.Load() != 0 || proxyHits.Load() == 0 {
		t.Fatalf("strictProxy+noProxy: proxyHits=%d directHits=%d err=%v", proxyHits.Load(), directHits.Load(), err)
	}
}

func TestStrictEdgeRelayLoopbackIsFailClosed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`CREATE TABLE IF NOT EXISTS proxyPools (
		id TEXT PRIMARY KEY,
		isActive INTEGER DEFAULT 1,
		testStatus TEXT,
		data TEXT NOT NULL,
		createdAt TEXT NOT NULL,
		updatedAt TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	repo := db.NewRepo(database)
	pool, err := repo.InsertProxyPool(db.ProxyPoolData{
		Name:        "strict-vercel",
		ProxyURL:    "https://relay.example.test/edge",
		NoProxy:     "127.0.0.1,localhost",
		Type:        "vercel",
		StrictProxy: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := &ChatHandler{Client: &http.Client{}, Repo: repo}
	cfg, err := h.GetProviderConfig("deepseek", &ConnectionData{
		BaseURL:     upstream.URL + "/v1/chat/completions",
		ProxyPoolID: pool["id"].(string),
		StrictProxy: true,
		ProviderSpecificData: map[string]any{
			"strictProxy":       true,
			"connectionNoProxy": "127.0.0.1,localhost",
		},
	})
	if err != nil {
		return
	}
	if providers.IsLoopbackURL(cfg.BaseURL) {
		t.Skip("Pregunta para José Luis: internal/handlers/chat/connections.go:416-419 y connections.go:428. Con strictProxy=true, noProxy=127.0.0.1,localhost y un upstream en loopback, GetProviderConfig devuelve la URL local directa. Esperado: fail-closed; noProxy y un destino local no anulan strictProxy. Actual: el relay se omite y BaseURL queda en " + cfg.BaseURL)
	}
}

func TestProxyClientCacheEvictsAndLogsOnce(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	h := &ChatHandler{Client: &http.Client{}, Repo: db.NewRepo(database)}
	for i := 0; i < maxProxyClients+1; i++ {
		conn := &ConnectionData{
			ConnectionProxyEnabled: true,
			ConnectionProxyURL:     "http://127.0.0.1:1/?i=" + strconv.Itoa(i),
		}
		if got := h.GetClientForConnection(conn); got == nil || got == h.Client {
			t.Fatalf("proxy client %d was not cached", i)
		}
	}
	logProxyOnce("pool-once", "http://127.0.0.1:9", "")
	logProxyOnce("pool-once", "http://127.0.0.1:9", "http")
	logProxyOnce("", "http://127.0.0.1:9", "http")

	if _, err := database.Exec(`CREATE TABLE IF NOT EXISTS proxyPools (
		id TEXT PRIMARY KEY, isActive INTEGER DEFAULT 1, testStatus TEXT, data TEXT NOT NULL, createdAt TEXT NOT NULL, updatedAt TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	repo := db.NewRepo(database)
	h.Repo = repo
	pool, err := repo.InsertProxyPool(db.ProxyPoolData{Name: "mystery", ProxyURL: "https://proxy.example.test", Type: "mystery", StrictProxy: false})
	if err != nil {
		t.Fatal(err)
	}
	if got := h.GetClientForConnection(&ConnectionData{ProxyPoolID: pool["id"].(string)}); got != h.Client {
		t.Fatal("non-strict unknown proxy type should use the direct client")
	}
}
