package dashboard

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	json "encoding/json/v2"

	"github.com/go-chi/chi/v5"

	"9router/proxy/internal/db"
	"9router/proxy/internal/fetchgate"
	"9router/proxy/internal/models"
)

// errBody fails the first read so handlers can exercise their body-read errors
// without touching the network.
type errBody struct{}

func (errBody) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (errBody) Close() error             { return nil }

func withProxyPools(t *testing.T, repo *db.Repo) {
	t.Helper()
	if _, err := repo.RawDB().Exec(`CREATE TABLE IF NOT EXISTS proxyPools (
		id TEXT PRIMARY KEY,
		isActive INTEGER DEFAULT 1,
		testStatus TEXT,
		data TEXT NOT NULL,
		createdAt TEXT NOT NULL,
		updatedAt TEXT NOT NULL
	);`); err != nil {
		t.Fatalf("create proxyPools: %v", err)
	}
}

func seedConn(t *testing.T, repo *db.Repo, id, provider, data string) {
	t.Helper()
	if err := repo.CreateProviderConnectionFull(id, provider, "apikey", id, nil, data); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func callWithID(h http.HandlerFunc, method, target, id string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, body)
	req.SetPathValue("id", id)
	req.SetPathValue("connectionId", id)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func decodeMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %d %s: %v", rec.Code, rec.Body.String(), err)
	}
	return out
}

func TestMaskClientKeyAndSameIntPtr(t *testing.T) {
	if maskClientKey("short") != "***" || maskClientKey("123456789012") != "***" {
		t.Fatal("short keys collapse")
	}
	if got := maskClientKey("1234567890123"); got != "123456…0123" {
		t.Fatalf("mask = %q", got)
	}
	a, b, c := 1, 1, 2
	if !sameIntPtr(nil, nil) || sameIntPtr(&a, nil) || !sameIntPtr(&a, &b) || sameIntPtr(&a, &c) {
		t.Fatal("sameIntPtr")
	}
}

func TestReorderConnectionLocal(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	openDashboard(t, repo)
	router := setupTestRouter(repo)
	for _, id := range []string{"re-a", "re-b"} {
		body, _ := json.Marshal(map[string]any{
			"id": id, "provider": "openai", "authType": "apikey", "name": id, "apiKey": "sk-" + id,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/connections", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("create %s: %d %s", id, rec.Code, rec.Body.String())
		}
	}
	h := NewDashboardHandler(repo)

	missing := callWithID(h.HandleReorderConnection, http.MethodPost, "/api/connections//reorder", "", strings.NewReader(`{"direction":"up"}`))
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing id %d", missing.Code)
	}
	badRead := callWithID(h.HandleReorderConnection, http.MethodPost, "/api/connections/re-a/reorder", "re-a", errBody{})
	if badRead.Code != http.StatusBadRequest {
		t.Fatalf("bad body %d", badRead.Code)
	}
	badJSON := callWithID(h.HandleReorderConnection, http.MethodPost, "/api/connections/re-a/reorder", "re-a", strings.NewReader(`{`))
	if badJSON.Code != http.StatusBadRequest {
		t.Fatalf("bad json %d", badJSON.Code)
	}
	badDir := callWithID(h.HandleReorderConnection, http.MethodPost, "/api/connections/re-a/reorder", "re-a", strings.NewReader(`{"direction":"sideways"}`))
	if badDir.Code != http.StatusBadRequest || !strings.Contains(badDir.Body.String(), "direction") {
		t.Fatalf("bad direction %d %s", badDir.Code, badDir.Body.String())
	}
	missingConn := callWithID(h.HandleReorderConnection, http.MethodPost, "/api/connections/nope/reorder", "nope", strings.NewReader(`{"direction":"up"}`))
	if missingConn.Code != http.StatusNotFound {
		t.Fatalf("missing conn %d %s", missingConn.Code, missingConn.Body.String())
	}

	moved := callWithID(h.HandleReorderConnection, http.MethodPost, "/api/connections/re-a/reorder", "re-a", strings.NewReader(`{"direction":"down"}`))
	if moved.Code != http.StatusOK {
		t.Fatalf("reorder %d %s", moved.Code, moved.Body.String())
	}
	body := decodeMap(t, moved)
	conns, _ := body["connections"].([]any)
	if len(conns) != 2 {
		t.Fatalf("connections %#v", body["connections"])
	}
	edge := callWithID(h.HandleReorderConnection, http.MethodPost, "/api/connections/re-a/reorder", "re-a", strings.NewReader(`{"direction":" UP "}`))
	if edge.Code != http.StatusOK {
		t.Fatalf("edge reorder %d %s", edge.Code, edge.Body.String())
	}
}

func TestModelAliasGetAndDelete(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	h := NewDashboardHandler(repo)

	empty := httptest.NewRecorder()
	h.HandleGetModelAliases(empty, httptest.NewRequest(http.MethodGet, "/api/models/alias", nil))
	if empty.Code != http.StatusOK {
		t.Fatalf("empty get %d", empty.Code)
	}
	if aliases, _ := decodeMap(t, empty)["aliases"].(map[string]any); len(aliases) != 0 {
		t.Fatalf("aliases %#v", aliases)
	}

	stored, _ := json.Marshal("gpt-4o")
	if err := repo.SetKV("modelAliases", "fast", string(stored)); err != nil {
		t.Fatal(err)
	}
	got := httptest.NewRecorder()
	h.HandleGetModelAliases(got, httptest.NewRequest(http.MethodGet, "/api/models/alias", nil))
	aliases, _ := decodeMap(t, got)["aliases"].(map[string]any)
	if aliases["fast"] != "gpt-4o" {
		t.Fatalf("aliases %#v", aliases)
	}

	noAlias := httptest.NewRecorder()
	h.HandleDeleteModelAlias(noAlias, httptest.NewRequest(http.MethodDelete, "/api/models/alias", nil))
	if noAlias.Code != http.StatusBadRequest {
		t.Fatalf("missing alias %d", noAlias.Code)
	}
	del := httptest.NewRecorder()
	h.HandleDeleteModelAlias(del, httptest.NewRequest(http.MethodDelete, "/api/models/alias?alias=fast", nil))
	if del.Code != http.StatusOK {
		t.Fatalf("delete %d %s", del.Code, del.Body.String())
	}
	after := httptest.NewRecorder()
	h.HandleGetModelAliases(after, httptest.NewRequest(http.MethodGet, "/api/models/alias", nil))
	left, _ := decodeMap(t, after)["aliases"].(map[string]any)
	if len(left) != 0 {
		t.Fatalf("alias survived %#v", left)
	}
}

func TestProviderNodesGetAndCreate(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	h := NewDashboardHandler(repo)
	r := chi.NewRouter()
	r.Get("/api/provider-nodes", h.HandleGetProviderNodes)
	r.Post("/api/provider-nodes", h.HandleCreateProviderNode)

	do := func(method, path string, body io.Reader) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, body)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if do(http.MethodGet, "/api/provider-nodes", nil).Code != http.StatusOK {
		t.Fatal("empty list")
	}
	if do(http.MethodPost, "/api/provider-nodes", errBody{}).Code != http.StatusBadRequest {
		t.Fatal("read")
	}
	if do(http.MethodPost, "/api/provider-nodes", strings.NewReader("{")).Code != http.StatusBadRequest {
		t.Fatal("json")
	}
	if do(http.MethodPost, "/api/provider-nodes", strings.NewReader(`{"prefix":"p"}`)).Code != http.StatusBadRequest {
		t.Fatal("name")
	}
	if do(http.MethodPost, "/api/provider-nodes", strings.NewReader(`{"name":"n"}`)).Code != http.StatusBadRequest {
		t.Fatal("prefix")
	}

	created := do(http.MethodPost, "/api/provider-nodes", strings.NewReader(`{"name":" OpenAI ","prefix":" oa ","apiType":"embeddings","baseUrl":"https://example.test/v1"}`))
	if created.Code != http.StatusCreated {
		t.Fatalf("create %d %s", created.Code, created.Body.String())
	}
	node, _ := decodeMap(t, created)["node"].(map[string]any)
	if node["apiType"] != "embeddings" || node["baseUrl"] != "https://example.test/v1" || !strings.HasPrefix(node["id"].(string), "openai-compatible-embeddings-") {
		t.Fatalf("node %#v", node)
	}

	anth := do(http.MethodPost, "/api/provider-nodes", strings.NewReader(`{"name":"Anth","prefix":"an","type":"anthropic-compatible"}`))
	if anth.Code != http.StatusCreated {
		t.Fatalf("anthropic %d %s", anth.Code, anth.Body.String())
	}
	anthNode, _ := decodeMap(t, anth)["node"].(map[string]any)
	if anthNode["baseUrl"] != "https://api.anthropic.com/v1" || anthNode["apiType"] != "chat" {
		t.Fatalf("defaults %#v", anthNode)
	}

	if _, err := repo.RawDB().Exec(
		`INSERT INTO providerNodes (id, type, name, data, createdAt, updatedAt) VALUES ('bad-node','openai-compatible','Bad','{','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatal(err)
	}
	listed := do(http.MethodGet, "/api/provider-nodes", nil)
	raw := listed.Body.String()
	if !strings.Contains(raw, "bad-node") || !strings.Contains(raw, "oa") {
		t.Fatalf("list %s", raw)
	}
}

func TestProxyPoolReadUpdateAndLocalTest(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	openDashboard(t, repo)
	withProxyPools(t, repo)
	h := NewDashboardHandler(repo)

	created, err := repo.InsertProxyPool(db.ProxyPoolData{Name: "local", ProxyURL: "http://127.0.0.1:9", Type: "http", StrictProxy: true})
	if err != nil {
		t.Fatal(err)
	}
	id, _ := created["id"].(string)
	seedConn(t, repo, "bound", "openai", `{"proxyPoolId":"`+id+`"}`)
	seedConn(t, repo, "bound-psd", "openai", `{"providerSpecificData":{"proxyPoolId":"`+id+`"}}`)

	req := httptest.NewRequest(http.MethodGet, "/api/proxy-pools?includeUsage=true", nil)
	rec := httptest.NewRecorder()
	h.HandleGetProxyPools(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list %d %s", rec.Code, rec.Body.String())
	}
	var listed struct {
		ProxyPools []map[string]any `json:"proxyPools"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.ProxyPools) != 1 || listed.ProxyPools[0]["boundConnectionCount"] != float64(2) {
		t.Fatalf("usage %#v", listed.ProxyPools)
	}

	// Login stays on (openDashboard turned it off). A client without an admin
	// credential must not be able to turn strictProxy off.
	if err := repo.UpdateSettingsRaw(map[string]any{"requireLogin": true}); err != nil {
		t.Fatal(err)
	}
	put := func(payload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/proxy-pools/"+id, strings.NewReader(payload))
		req.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		h.HandleUpdateProxyPool(rec, req)
		return rec
	}
	renamed := put(`{"name":"renamed"}`)
	if renamed.Code != http.StatusOK {
		t.Fatalf("rename %d %s", renamed.Code, renamed.Body.String())
	}
	strict := put(`{"strictProxy":false}`)
	if strict.Code != http.StatusForbidden {
		t.Fatalf("bajar strictProxy sin admin: status %d body %s", strict.Code, strict.Body.String())
	}
	if !strings.Contains(strict.Body.String(), "strictProxy") {
		t.Fatalf("403 should name strictProxy: %s", strict.Body.String())
	}

	if callWithID(h.HandleUpdateProxyPool, http.MethodPut, "/api/proxy-pools/", "", strings.NewReader(`{}`)).Code != http.StatusBadRequest {
		t.Fatal("missing id")
	}
	if callWithID(h.HandleUpdateProxyPool, http.MethodPut, "/api/proxy-pools/"+id, id, errBody{}).Code != http.StatusBadRequest {
		t.Fatal("read")
	}
	if callWithID(h.HandleUpdateProxyPool, http.MethodPut, "/api/proxy-pools/"+id, id, strings.NewReader("{")).Code != http.StatusBadRequest {
		t.Fatal("json")
	}
	gone := callWithID(h.HandleUpdateProxyPool, http.MethodPut, "/api/proxy-pools/missing", "missing", strings.NewReader(`{"strictProxy":false}`))
	if gone.Code != http.StatusNotFound {
		t.Fatalf("missing pool %d %s", gone.Code, gone.Body.String())
	}

	if callWithID(h.HandleTestProxyPool, http.MethodPost, "/api/proxy-pools//test", "", nil).Code != http.StatusBadRequest {
		t.Fatal("test missing id")
	}
	if callWithID(h.HandleTestProxyPool, http.MethodPost, "/api/proxy-pools/missing/test", "missing", nil).Code != http.StatusNotFound {
		t.Fatal("test missing pool")
	}

	empty, err := repo.InsertProxyPool(db.ProxyPoolData{Name: "empty", Type: "http", StrictProxy: true})
	if err != nil {
		t.Fatal(err)
	}
	emptyID, _ := empty["id"].(string)
	emptyRec := callWithID(h.HandleTestProxyPool, http.MethodPost, "/api/proxy-pools/"+emptyID+"/test", emptyID, nil)
	if emptyRec.Code != http.StatusOK || decodeMap(t, emptyRec)["success"] != false {
		t.Fatalf("empty urls %#v", decodeMap(t, emptyRec))
	}

	bad, err := repo.InsertProxyPool(db.ProxyPoolData{Name: "bad", ProxyURL: "http://[", Type: "http"})
	if err != nil {
		t.Fatal(err)
	}
	badID, _ := bad["id"].(string)
	badRec := callWithID(h.HandleTestProxyPool, http.MethodPost, "/api/proxy-pools/"+badID+"/test", badID, nil)
	if decodeMap(t, badRec)["error"] != "invalid proxy URL format" {
		t.Fatalf("invalid url %#v", decodeMap(t, badRec))
	}

	// The handler dials the proxy, then asks THAT proxy for google. A closed
	// loopback port refuses the TCP connection before any byte leaves the machine.
	dead, err := repo.InsertProxyPool(db.ProxyPoolData{Name: "dead", ProxyURL: "http://127.0.0.1:1", Type: "http", StrictProxy: true})
	if err != nil {
		t.Fatal(err)
	}
	deadID, _ := dead["id"].(string)
	deadRec := callWithID(h.HandleTestProxyPool, http.MethodPost, "/api/proxy-pools/"+deadID+"/test", deadID, nil)
	deadBody := decodeMap(t, deadRec)
	if deadBody["success"] != false || deadBody["status"] != "failed" {
		t.Fatalf("dead proxy must fail closed: %#v", deadBody)
	}

	relayBad, err := repo.InsertProxyPool(db.ProxyPoolData{Name: "relay-bad", ProxyURL: "http://[", Type: "vercel"})
	if err != nil {
		t.Fatal(err)
	}
	relayID, _ := relayBad["id"].(string)
	relayRec := callWithID(h.HandleTestProxyPool, http.MethodPost, "/api/proxy-pools/"+relayID+"/test", relayID, nil)
	if decodeMap(t, relayRec)["error"] != "invalid relay URL format" {
		t.Fatalf("relay %#v", decodeMap(t, relayRec))
	}
	relayDead, err := repo.InsertProxyPool(db.ProxyPoolData{Name: "relay-dead", ProxyURL: "http://127.0.0.1:1", Type: "cloudflare"})
	if err != nil {
		t.Fatal(err)
	}
	relayDeadID, _ := relayDead["id"].(string)
	relayDeadRec := callWithID(h.HandleTestProxyPool, http.MethodPost, "/api/proxy-pools/"+relayDeadID+"/test", relayDeadID, nil)
	if decodeMap(t, relayDeadRec)["success"] != false {
		t.Fatalf("dead relay %#v", decodeMap(t, relayDeadRec))
	}
}

func TestHandleProxyTestStaysOnLoopback(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	h := NewDashboardHandler(repo)
	post := func(body io.Reader) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/settings/proxy-test", body)
		rec := httptest.NewRecorder()
		h.HandleProxyTest(rec, req)
		return rec
	}
	if post(errBody{}).Code != http.StatusBadRequest {
		t.Fatal("read")
	}
	if post(strings.NewReader("{")).Code != http.StatusBadRequest {
		t.Fatal("json")
	}
	if post(strings.NewReader(`{}`)).Code != http.StatusBadRequest {
		t.Fatal("missing proxy")
	}
	if post(strings.NewReader(`{"proxyUrl":"not a url"}`)).Code != http.StatusBadRequest {
		t.Fatal("invalid proxy")
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			http.Error(w, "no", http.StatusTeapot)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	proxyURL := startLoopbackProxy(t)

	okRec := post(strings.NewReader(`{"proxyUrl":"` + proxyURL + `","testUrl":"` + upstream.URL + `/ok","timeoutMs":999999}`))
	okBody := decodeMap(t, okRec)
	if okRec.Code != http.StatusOK || okBody["ok"] != true || okBody["status"] != float64(204) {
		t.Fatalf("proxy ok %#v %s", okBody, okRec.Body.String())
	}
	failRec := post(strings.NewReader(`{"proxyUrl":"` + proxyURL + `","testUrl":"` + upstream.URL + `/fail","timeoutMs":500}`))
	if failRec.Code != http.StatusTeapot {
		t.Fatalf("upstream status %d %s", failRec.Code, failRec.Body.String())
	}

	// Closed proxy, explicit loopback target: the dial fails locally. timeoutMs
	// of 1 still goes through the positive-timeout branch.
	dead := post(strings.NewReader(`{"proxyUrl":"http://127.0.0.1:1","testUrl":"http://127.0.0.1:1/nope","timeoutMs":1}`))
	if dead.Code != http.StatusInternalServerError || decodeMap(t, dead)["ok"] != false {
		t.Fatalf("dead proxy %#v", decodeMap(t, dead))
	}
}

func startLoopbackProxy(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.URL.Hostname()
		if host != "127.0.0.1" && host != "localhost" {
			http.Error(w, "external target blocked", http.StatusForbidden)
			return
		}
		out, err := http.NewRequest(r.Method, r.URL.String(), nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		resp, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "http://" + ln.Addr().String()
}

func TestUsageHandlersLocal(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	h := NewDashboardHandler(repo)
	prev := quotaFetchGate
	quotaFetchGate = fetchgate.New(0, 0)
	t.Cleanup(func() { quotaFetchGate = prev })

	seedConn(t, repo, "u-openai", "openai", `{"rateLimitedUntil":"2026-08-01T00:00:00Z","modelLock_gpt":"2026-08-02T00:00:00Z","note":"x"}`)
	seedConn(t, repo, "u-openai-2", "openai", `{}`)
	seedConn(t, repo, "u-ag", "antigravity", `{"projectId":"p"}`)

	list := httptest.NewRecorder()
	h.HandleGetUsageProviders(list, httptest.NewRequest(http.MethodGet, "/api/usage/providers", nil))
	if list.Code != http.StatusOK || strings.Count(list.Body.String(), `"id":"openai"`) != 1 || !strings.Contains(list.Body.String(), `"id":"antigravity"`) {
		t.Fatalf("providers %s", list.Body.String())
	}

	missing := callWithID(h.HandleGetConnectionUsage, http.MethodGet, "/api/usage/", "", nil)
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing id %d", missing.Code)
	}
	unknown := callWithID(h.HandleGetConnectionUsage, http.MethodGet, "/api/usage/nope", "nope", nil)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown %d", unknown.Code)
	}
	fallback := callWithID(h.HandleGetConnectionUsage, http.MethodGet, "/api/usage/u-openai", "u-openai", nil)
	if fallback.Code != http.StatusOK {
		t.Fatalf("fallback %d %s", fallback.Code, fallback.Body.String())
	}
	quotas, _ := decodeMap(t, fallback)["quotas"].(map[string]any)
	if quotas["default"] == nil || quotas["gpt"] == nil {
		t.Fatalf("locks %#v", quotas)
	}
	ag := callWithID(h.HandleGetConnectionUsage, http.MethodGet, "/api/usage/u-ag", "u-ag", nil)
	if ag.Code != http.StatusOK || decodeMap(t, ag)["plan"] != "antigravity" {
		t.Fatalf("antigravity without token should fall through: %s", ag.Body.String())
	}

	useUsageScript(t, map[string][]pathReply{
		"/v1internal:loadCodeAssist":           {okJSON(`{"currentTier":{"name":"Pro"},"paidTier":{"id":"free-tier"}}`)},
		"/v1internal:fetchAvailableModels":     {okJSON(`{"models":{}}`)},
		"/v1internal:retrieveUserQuotaSummary": {okJSON(`{}`)},
	})
	seedConn(t, repo, "u-ag-live", "antigravity", `{"accessToken":"tok","projectId":"proj"}`)
	live := callWithID(h.HandleGetConnectionUsage, http.MethodGet, "/api/usage/u-ag-live", "u-ag-live", nil)
	if live.Code != http.StatusOK || decodeMap(t, live)["plan"] != "Pro" {
		t.Fatalf("live antigravity %d %s", live.Code, live.Body.String())
	}

	quotaFetchGate = fetchgate.New(time.Second, 0)
	if err := quotaFetchGate.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/usage/u-openai", nil).WithContext(ctx)
	req.SetPathValue("connectionId", "u-openai")
	rec := httptest.NewRecorder()
	h.HandleGetConnectionUsage(rec, req)
	if rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "" {
		t.Fatalf("cancelled client still got a body: %d %s", rec.Code, rec.Body.String())
	}
}

func stubValidate(t *testing.T, fn func(method, rawURL string, body []byte) (int, []byte, error)) {
	t.Helper()
	prev := validateProbeDo
	validateProbeDo = func(_ context.Context, method, rawURL string, _ map[string]string, body []byte) (int, []byte, error) {
		return fn(method, rawURL, body)
	}
	t.Cleanup(func() { validateProbeDo = prev })
}

func TestConnectionModelsLocal(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	h := NewDashboardHandler(repo)

	if callWithID(h.HandleGetConnectionModels, http.MethodGet, "/api/providers//models", "", nil).Code != http.StatusBadRequest {
		t.Fatal("missing id")
	}
	if callWithID(h.HandleGetConnectionModels, http.MethodGet, "/api/providers/nope/models", "nope", nil).Code != http.StatusNotFound {
		t.Fatal("missing conn")
	}
	seedConn(t, repo, "plain", "openai", `{}`)
	if rec := callWithID(h.HandleGetConnectionModels, http.MethodGet, "/api/providers/plain/models", "plain", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("unsupported %d %s", rec.Code, rec.Body.String())
	}

	var seen []string
	stubValidate(t, func(method, rawURL string, body []byte) (int, []byte, error) {
		seen = append(seen, method+" "+rawURL+" "+string(body))
		switch {
		case strings.Contains(rawURL, "daily-cloudcode-pa.googleapis.com"):
			if strings.Contains(string(body), `"project":"proj-ag"`) {
				return http.StatusOK, []byte(`{"models":{"chat_hidden":{"displayName":"H","isInternal":false},"tab_x":{},"real":{"supportsImages":true,"supportsThinking":true},"secret":{"isInternal":true},"blank":{}}}`), nil
			}
			return http.StatusForbidden, []byte(`{}`), nil
		case strings.Contains(rawURL, "cloudcode-pa.googleapis.com"):
			if strings.Contains(string(body), `"project":"gem-proj"`) {
				return http.StatusOK, []byte(`{"models":{"gemini-real":{"displayName":"G","supportsImages":true}}}`), nil
			}
			return 0, nil, errors.New("gemini down")
		case strings.Contains(rawURL, "api.cline.bot/api/v1/models"):
			return http.StatusOK, []byte(`{"data":[{"id":"paid","name":"Paid"}]}`), nil
		case strings.Contains(rawURL, "recommended-models"):
			return http.StatusOK, []byte(`{"free":[{"id":"paid","name":"dup"},{"id":"cline-free/x"}]}`), nil
		case strings.Contains(rawURL, "api.meta.ai"):
			return http.StatusOK, []byte(`{"models":[{"id":"muse-1"}]}`), nil
		case strings.Contains(rawURL, "api3.qoder.sh") || strings.Contains(rawURL, "api2.qoder.sh") || strings.Contains(rawURL, "gateway.qoder.com.cn"):
			return http.StatusOK, []byte(`{"chat":[{"key":"m1","display_name":"M","max_input_tokens":"1000","enable":true},{"key":"off","enable":false},{"key":""}]}`), nil
		case strings.Contains(rawURL, "127.0.0.1") || strings.Contains(rawURL, "example.test"):
			return http.StatusOK, []byte(`{"data":[{"id":"compat"}]}`), nil
		default:
			return 0, nil, errors.New("unscripted " + rawURL)
		}
	})

	seedConn(t, repo, "ag-notoken", "antigravity", `{}`)
	if callWithID(h.HandleGetConnectionModels, http.MethodGet, "/m", "ag-notoken", nil).Code != http.StatusUnauthorized {
		t.Fatal("ag token")
	}
	seedConn(t, repo, "ag", "antigravity", `{"accessToken":"tok","providerSpecificData":{"projectId":"proj-ag"}}`)
	ag := callWithID(h.HandleGetConnectionModels, http.MethodGet, "/m", "ag", nil)
	if ag.Code != http.StatusOK || !strings.Contains(ag.Body.String(), `"real"`) || strings.Contains(ag.Body.String(), "chat_hidden") || strings.Contains(ag.Body.String(), "secret") {
		t.Fatalf("ag models %s", ag.Body.String())
	}
	seedConn(t, repo, "ag-bad", "antigravity", `{"apiKey":"k"}`)
	if callWithID(h.HandleGetConnectionModels, http.MethodGet, "/m", "ag-bad", nil).Code != http.StatusForbidden {
		t.Fatal("ag status")
	}

	seedConn(t, repo, "gem-notoken", "gemini-cli", `{}`)
	if callWithID(h.HandleGetConnectionModels, http.MethodGet, "/m", "gem-notoken", nil).Code != http.StatusUnauthorized {
		t.Fatal("gem token")
	}
	seedConn(t, repo, "gem-down", "gemini-cli", `{"accessToken":"tok"}`)
	if callWithID(h.HandleGetConnectionModels, http.MethodGet, "/m", "gem-down", nil).Code != http.StatusBadGateway {
		t.Fatal("gem down")
	}
	seedConn(t, repo, "gem", "gemini-cli", `{"accessToken":"tok","providerSpecificData":{"projectId":"gem-proj"}}`)
	if rec := callWithID(h.HandleGetConnectionModels, http.MethodGet, "/m", "gem", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "gemini-real") {
		t.Fatalf("gem %d %s", rec.Code, rec.Body.String())
	}

	seedConn(t, repo, "cline-notoken", "cline", `{}`)
	if callWithID(h.HandleGetConnectionModels, http.MethodGet, "/m", "cline-notoken", nil).Code != http.StatusUnauthorized {
		t.Fatal("cline token")
	}
	seedConn(t, repo, "cline", "cline", `{"accessToken":"eyJabc.def"}`)
	cline := callWithID(h.HandleGetConnectionModels, http.MethodGet, "/m", "cline", nil)
	if cline.Code != http.StatusOK || strings.Count(cline.Body.String(), `"paid"`) != 1 || !strings.Contains(cline.Body.String(), "cline-free/x") {
		t.Fatalf("cline %s", cline.Body.String())
	}
	var clineAuth string
	for _, line := range seen {
		if strings.Contains(line, "api.cline.bot/api/v1/models") {
			clineAuth = line
		}
	}
	_ = clineAuth

	seedConn(t, repo, "muse-notoken", "muse", `{}`)
	if callWithID(h.HandleGetConnectionModels, http.MethodGet, "/m", "muse-notoken", nil).Code != http.StatusUnauthorized {
		t.Fatal("muse token")
	}
	seedConn(t, repo, "muse", "muse", `{"apiKey":"mk"}`)
	if rec := callWithID(h.HandleGetConnectionModels, http.MethodGet, "/m", "muse", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "muse-1") {
		t.Fatalf("muse %s", rec.Body.String())
	}

	seedConn(t, repo, "qoder-empty", "qoder", `{}`)
	if callWithID(h.HandleGetConnectionModels, http.MethodGet, "/m", "qoder-empty", nil).Code != http.StatusBadGateway {
		t.Fatal("qoder empty")
	}
	seedConn(t, repo, "qoder", "qoder", `{"apiKey":"dt-token","providerSpecificData":{"userId":"user-1"}}`)
	if rec := callWithID(h.HandleGetConnectionModels, http.MethodGet, "/m", "qoder", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"m1"`) {
		t.Fatalf("qoder %d %s", rec.Code, rec.Body.String())
	}

	seedConn(t, repo, "compat", "openai-compatible-chat-x", `{"apiKey":"sk","providerSpecificData":{"baseUrl":"http://127.0.0.1:9/v1"}}`)
	if rec := callWithID(h.HandleGetConnectionModels, http.MethodGet, "/m", "compat", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "compat") {
		t.Fatalf("compat %d %s", rec.Code, rec.Body.String())
	}
	seedConn(t, repo, "compat-none", "openai-compatible-chat-y", `{"apiKey":"sk"}`)
	if callWithID(h.HandleGetConnectionModels, http.MethodGet, "/m", "compat-none", nil).Code != http.StatusBadRequest {
		t.Fatal("no base url")
	}
}

func TestQoderCatalogLocal(t *testing.T) {
	if _, err := fetchQoderCatalogModels(context.Background(), "qoder", " ", nil); err == nil {
		t.Fatal("empty token")
	}
	if _, err := fetchQoderCatalogModels(context.Background(), "qoder", "dt-x", nil); err == nil || !strings.Contains(err.Error(), "user id") {
		t.Fatalf("missing user: %v", err)
	}

	var urls []string
	stubValidate(t, func(method, rawURL string, body []byte) (int, []byte, error) {
		urls = append(urls, method+" "+rawURL)
		switch {
		case strings.Contains(rawURL, "jobToken/exchange"):
			if !strings.Contains(string(body), "pt-secret") {
				return http.StatusBadRequest, []byte(`{}`), nil
			}
			return http.StatusOK, []byte(`{"token":"jt-job"}`), nil
		case strings.Contains(rawURL, "userinfo"):
			return http.StatusOK, []byte(`{"user_id":"from-info"}`), nil
		case strings.Contains(rawURL, "model/list"):
			if strings.Contains(rawURL, "want-fail") {
				return http.StatusBadGateway, []byte(`nope`), nil
			}
			return http.StatusOK, []byte(`{"chat":[
				{"key":"shown","display_name":"","max_input_tokens":128,"max_output_tokens":"16","is_vl":true,"is_reasoning":true,"description":"d"},
				{"key":"hidden","enable":false},
				{"key":""},
				{"key":"fallback","max_input_tokens":0}
			]}`), nil
		default:
			return 0, nil, errors.New("unscripted")
		}
	})

	models, err := fetchQoderCatalogModels(context.Background(), "qoder", "jt-direct", map[string]any{"userId": "user-9"})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].Name != "shown" || models[0].ContextLength != 128 || !models[0].IsVL || models[0].MaxOutputTokens != 16 {
		t.Fatalf("models %+v", models)
	}
	if models[1].Name != "fallback" || models[1].ContextLength != qoderDefaultContextLength {
		t.Fatalf("fallback %+v", models[1])
	}
	if !strings.Contains(strings.Join(urls, "\n"), "api2.qoder.sh") {
		t.Fatalf("job token should use the alt host: %v", urls)
	}

	cn, err := fetchQoderCatalogModels(context.Background(), "qoder-cn", "jt-direct", map[string]any{"user_id": "cn-user"})
	if err != nil || len(cn) != 2 {
		t.Fatalf("cn %+v err %v", cn, err)
	}
	if !strings.Contains(strings.Join(urls, "\n"), "gateway.qoder.com.cn") {
		t.Fatalf("cn host %v", urls)
	}

	viaPAT, err := fetchQoderCatalogModels(context.Background(), "qoder", "pt-secret", nil)
	if err != nil || len(viaPAT) != 2 {
		t.Fatalf("pat %+v %v", viaPAT, err)
	}

	if _, err := fetchQoderCatalogModels(context.Background(), "qoder", "dt-x", map[string]any{"userId": "u"}); err == nil {
		// The stub returns 200 for every model list. Force a failure with a
		// provider that still resolves, by closing over a flag below.
	}
	failing := false
	validateProbeDo = func(_ context.Context, method, rawURL string, _ map[string]string, body []byte) (int, []byte, error) {
		if failing {
			if strings.Contains(rawURL, "model/list") {
				return http.StatusBadGateway, []byte(`nope`), nil
			}
			return 0, nil, errors.New("catalog down")
		}
		return http.StatusOK, []byte(`not-json`), nil
	}
	failing = false
	if _, err := fetchQoderCatalogModels(context.Background(), "qoder", "dt-x", map[string]any{"id": "user"}); err == nil || !strings.Contains(err.Error(), "not JSON") {
		t.Fatalf("bad json %v", err)
	}
	failing = true
	if _, err := fetchQoderCatalogModels(context.Background(), "qoder", "dt-x", map[string]any{"id": "user"}); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("status %v", err)
	}
	if _, err := fetchQoderCatalogModels(context.Background(), "qoder", "pt-secret", nil); err == nil || !strings.Contains(err.Error(), "catalog down") {
		t.Fatalf("exchange %v", err)
	}
	if got := qoderIntOr("nope", 7); got != 7 {
		t.Fatalf("qoderIntOr %d", got)
	}
	if got := qoderIntOr(float64(-1), 3); got != 3 {
		t.Fatalf("negative %d", got)
	}
}

func TestValidateV1MAndNodeStatus(t *testing.T) {
	stubValidate(t, func(method, rawURL string, body []byte) (int, []byte, error) {
		if strings.Contains(string(body), "probe") && method == http.MethodPost {
			return http.StatusOK, []byte(`{}`), nil
		}
		return http.StatusUnauthorized, nil, nil
	})
	ok := validateV1M(context.Background(), "k")
	if !ok.valid || !ok.supported {
		t.Fatalf("valid %+v", ok)
	}
	validateProbeDo = func(context.Context, string, string, map[string]string, []byte) (int, []byte, error) {
		return http.StatusForbidden, nil, nil
	}
	bad := validateV1M(context.Background(), "k")
	if bad.valid {
		t.Fatal("401/403 must be invalid")
	}
	validateProbeDo = func(context.Context, string, string, map[string]string, []byte) (int, []byte, error) {
		return 0, nil, errors.New("down")
	}
	if !strings.Contains(validateV1M(context.Background(), "k").message, "down") {
		t.Fatal("transport")
	}

	if validateNodeModelsStatusMessage(http.StatusUnauthorized) != "API key unauthorized" {
		t.Fatal("models 401")
	}
	if validateNodeModelsStatusMessage(http.StatusNotFound) == "" || validateNodeModelsStatusMessage(http.StatusBadGateway) == "" || validateNodeModelsStatusMessage(http.StatusTeapot) == "" {
		t.Fatal("models status")
	}
	if validateNodeChatStatusMessage(http.StatusBadRequest) == "" || validateNodeChatStatusMessage(http.StatusNotFound) == "" || validateNodeChatStatusMessage(http.StatusBadGateway) == "" || validateNodeChatStatusMessage(418) == "" {
		t.Fatal("chat status")
	}

	rec := httptest.NewRecorder()
	validateProbeDo = func(_ context.Context, method, rawURL string, _ map[string]string, _ []byte) (int, []byte, error) {
		if method == http.MethodGet && strings.HasSuffix(rawURL, "/models") {
			return http.StatusNotFound, nil, nil
		}
		return http.StatusOK, []byte(`{}`), nil
	}
	validateAnthropicCompatibleNode(rec, context.Background(), "http://127.0.0.1:9/v1/messages", "sk", "m")
	if !strings.Contains(rec.Body.String(), `"valid":true`) || !strings.Contains(rec.Body.String(), "chat") {
		t.Fatalf("anthropic fallback %s", rec.Body.String())
	}
}

func TestProbeClineFreebuffAndCloudCode(t *testing.T) {
	if isClineWorkOSJWT("workos:eyJxx.y") || isClineWorkOSJWT("plain") || isClineWorkOSJWT("eyJ") || isClineWorkOSJWT("eyJ$.x") {
		t.Fatal("jwt shape")
	}
	if !isClineWorkOSJWT("eyJabc.def") {
		t.Fatal("jwt")
	}

	h := NewDashboardHandler(nil)
	prev := connectionProbeDo
	t.Cleanup(func() { connectionProbeDo = prev })

	var auth []string
	connectionProbeDo = func(_ context.Context, _ *http.Client, _, rawURL string, headers map[string]string, _ []byte) (int, []byte, error) {
		auth = append(auth, headers["Authorization"]+" "+rawURL)
		if strings.Contains(headers["Authorization"], "new-token") {
			return http.StatusOK, []byte(`{}`), nil
		}
		if strings.Contains(headers["User-Agent"], "antigravity") {
			return http.StatusBadRequest, []byte(`{"error":{"message":"no quota"}}`), nil
		}
		if strings.Contains(headers["User-Agent"], "gemini-cli") {
			return 0, nil, errors.New("cloud down")
		}
		return http.StatusUnauthorized, []byte(`{}`), nil
	}
	refreshClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Host, "cline.bot") && !strings.Contains(r.URL.Host, "127.0.0.1") {
			return nil, errors.New("refresh host blocked")
		}
		// Answer every refresh locally. A "forbidden"/"proxy" error would make
		// RefreshCline fall through to the direct client and dial api.cline.bot.
		body := `{"success":true,"data":{"accessToken":"new-token","refreshToken":"new-refresh"}}`
		return &http.Response{StatusCode: 200, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}
	out := h.probeCline(context.Background(), connectionProbeData{RefreshToken: "rt"}, refreshClient, "eyJabc.def", false, nil)
	if !out.valid || !out.refreshed || out.tokens == nil || out.tokens.AccessToken != "new-token" {
		t.Fatalf("refresh %+v", out)
	}
	if len(auth) < 2 || !strings.Contains(auth[0], "workos:eyJabc.def") {
		t.Fatalf("auth headers %v", auth)
	}

	plain := h.probeCline(context.Background(), connectionProbeData{}, refreshClient, "workos:already", false, nil)
	if plain.valid || plain.message != "Token invalid or revoked" {
		t.Fatalf("no refresh %+v", plain)
	}
	connectionProbeDo = func(context.Context, *http.Client, string, string, map[string]string, []byte) (int, []byte, error) {
		return http.StatusForbidden, nil, nil
	}
	if h.probeCline(context.Background(), connectionProbeData{}, nil, "sk-cline", false, nil).message != "Access denied" {
		t.Fatal("403")
	}
	connectionProbeDo = func(context.Context, *http.Client, string, string, map[string]string, []byte) (int, []byte, error) {
		return http.StatusTeapot, nil, nil
	}
	if !strings.Contains(h.probeCline(context.Background(), connectionProbeData{}, nil, "sk-cline", false, nil).message, "418") {
		t.Fatal("status")
	}
	connectionProbeDo = func(context.Context, *http.Client, string, string, map[string]string, []byte) (int, []byte, error) {
		return 0, nil, errors.New("probe down")
	}
	if !strings.Contains(h.probeCline(context.Background(), connectionProbeData{}, nil, "sk", false, nil).message, "probe down") {
		t.Fatal("probe error")
	}

	freeClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "www.codebuff.com" {
			return nil, errors.New("unexpected host " + r.URL.Host)
		}
		switch r.Header.Get("X-Case") {
		default:
			return nil, errors.New("blocked")
		}
	})}
	// The production request does not send X-Case. Drive the response off a counter.
	step := 0
	freeClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "www.codebuff.com" {
			return nil, errors.New("unexpected host " + r.URL.Host)
		}
		step++
		switch step {
		case 1:
			return nil, errors.New("blocked")
		case 2:
			return jsonResp(http.StatusOK, `{}`, r), nil
		case 3:
			return jsonResp(http.StatusNotFound, `{}`, r), nil
		case 4:
			return jsonResp(http.StatusUnauthorized, `{}`, r), nil
		case 5:
			return jsonResp(http.StatusForbidden, `{"status":"Banned"}`, r), nil
		case 6:
			return jsonResp(http.StatusForbidden, `{"status":"other"}`, r), nil
		default:
			return jsonResp(http.StatusTeapot, `{}`, r), nil
		}
	})}
	conn := &models.ProviderConnection{Provider: "freebuff"}
	if msg := h.probeFreebuff(context.Background(), conn, connectionProbeData{}, freeClient).message; msg != "No access token" {
		t.Fatalf("no token %q", msg)
	}
	if !strings.Contains(h.probeFreebuff(context.Background(), conn, connectionProbeData{APIKey: "k"}, freeClient).message, "blocked") {
		t.Fatal("transport")
	}
	if !h.probeFreebuff(context.Background(), conn, connectionProbeData{AccessToken: "tok"}, freeClient).valid {
		t.Fatal("200")
	}
	if !h.probeFreebuff(context.Background(), conn, connectionProbeData{ProviderSpecificData: map[string]any{"authToken": "psd"}}, freeClient).valid {
		t.Fatal("404 is valid")
	}
	if h.probeFreebuff(context.Background(), conn, connectionProbeData{AccessToken: "tok"}, freeClient).message != "Invalid token or expired" {
		t.Fatal("401")
	}
	if h.probeFreebuff(context.Background(), conn, connectionProbeData{AccessToken: "tok"}, freeClient).message != "Account banned" {
		t.Fatal("banned")
	}
	region := h.probeFreebuff(context.Background(), conn, connectionProbeData{AccessToken: "tok"}, freeClient)
	if !region.valid || !strings.Contains(region.message, "Region flagged") {
		t.Fatalf("region %+v", region)
	}
	if !h.probeFreebuff(context.Background(), conn, connectionProbeData{AccessToken: "tok"}, freeClient).valid {
		t.Fatal("other status stays valid")
	}

	connectionProbeDo = func(_ context.Context, _ *http.Client, _, _ string, headers map[string]string, _ []byte) (int, []byte, error) {
		if strings.Contains(headers["User-Agent"], "antigravity") {
			return http.StatusBadRequest, []byte(`{"error":{"message":"no quota"}}`), nil
		}
		return 0, nil, errors.New("cloud down")
	}
	ag := probeCloudCodeAssist(context.Background(), nil, "antigravity", "tok")
	if ag.valid || ag.message != "no quota" {
		t.Fatalf("ag %+v", ag)
	}
	gem := probeCloudCodeAssist(context.Background(), nil, "gemini-cli", "tok")
	if gem.message != "cloud down" {
		t.Fatalf("gem %+v", gem)
	}
	connectionProbeDo = func(context.Context, *http.Client, string, string, map[string]string, []byte) (int, []byte, error) {
		return http.StatusOK, []byte(`{}`), nil
	}
	if !probeCloudCodeAssist(context.Background(), nil, "gemini-cli", "tok").valid {
		t.Fatal("cloud 200")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResp(status int, body string, r *http.Request) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}
}

func TestTailscaleSeamsStayLocal(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	h := NewDashboardHandler(repo)

	// findTailscaleBin only looks up a path. It must not be executed.
	if bin := findTailscaleBin(); bin != "" {
		if !strings.Contains(bin, "tailscale") {
			t.Fatalf("lookup returned %q", bin)
		}
	}
	if hasBrew() {
		t.Fatal("hasBrew is darwin-only")
	}
	if _, err := probeTailscaleStatus(context.Background(), ""); err == nil {
		t.Fatal("empty bin")
	}

	stubTailscaleHost(t, "/fake/tailscale", func(_ context.Context, bin string, args ...string) ([]byte, error) {
		if bin != "/fake/tailscale" {
			return nil, errors.New("unexpected bin")
		}
		joined := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(joined, "status"):
			return []byte(`{"BackendState":"Running","Self":{"DNSName":"host.tailnet.ts.net.","Online":true}}`), nil
		case strings.Contains(joined, "funnel") && strings.Contains(joined, "reset"):
			return []byte("reset"), nil
		case strings.Contains(joined, "funnel"):
			return []byte("available at https://host.tailnet.ts.net"), nil
		default:
			return nil, errors.New("unexpected args " + joined)
		}
	})

	check := httptest.NewRecorder()
	h.HandleTailscaleCheck(check, httptest.NewRequest(http.MethodGet, "/api/tunnel/tailscale-check", nil))
	body := decodeMap(t, check)
	if body["installed"] != true || body["loggedIn"] != true || body["tunnelUrl"] != "https://host.tailnet.ts.net" {
		t.Fatalf("check %#v", body)
	}

	status := httptest.NewRecorder()
	h.HandleTunnelStatus(status, httptest.NewRequest(http.MethodGet, "/api/tunnel/status", nil))
	raw := status.Body.String()
	if !strings.Contains(raw, "host.tailnet.ts.net") {
		t.Fatalf("status %s", raw)
	}

	enable := httptest.NewRequest(http.MethodPost, "/api/tunnel/tailscale-enable", nil)
	asDashboardAdmin(t, enable)
	enableRec := httptest.NewRecorder()
	h.HandleTailscaleEnable(enableRec, enable)
	if enableRec.Code != http.StatusOK || !strings.Contains(enableRec.Body.String(), "host.tailnet.ts.net") {
		t.Fatalf("enable %d %s", enableRec.Code, enableRec.Body.String())
	}

	disable := httptest.NewRequest(http.MethodPost, "/api/tunnel/tailscale-disable", nil)
	asDashboardAdmin(t, disable)
	disableRec := httptest.NewRecorder()
	h.HandleTailscaleDisable(disableRec, disable)
	if disableRec.Code != http.StatusOK {
		t.Fatalf("disable %d %s", disableRec.Code, disableRec.Body.String())
	}
}

func TestTailscaleEnableBranches(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	h := NewDashboardHandler(repo)

	forbidden := httptest.NewRequest(http.MethodPost, "/api/tunnel/tailscale-enable", nil)
	stubTailscaleHost(t, "/fake/tailscale", func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("non-admin must not reach tailscale")
		return nil, errors.New("unreachable")
	})
	rec := httptest.NewRecorder()
	h.HandleTailscaleEnable(rec, forbidden)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin %d %s", rec.Code, rec.Body.String())
	}

	cases := []struct {
		name string
		exec func(args []string) ([]byte, error)
		want int
		body string
	}{
		{
			name: "auth url from status",
			exec: func(args []string) ([]byte, error) {
				if strings.HasPrefix(strings.Join(args, " "), "status") {
					return []byte(`{"BackendState":"NeedsLogin","AuthURL":"https://login.tailscale.com/a/from-status"}`), nil
				}
				return nil, errors.New("up must not run when status already has an auth url")
			},
			want: http.StatusOK,
			body: "from-status",
		},
		{
			name: "auth url from up output",
			exec: func(args []string) ([]byte, error) {
				joined := strings.Join(args, " ")
				if strings.HasPrefix(joined, "status") {
					return []byte(`{"BackendState":"NeedsLogin"}`), nil
				}
				if strings.HasPrefix(joined, "up") {
					return []byte("log in at https://login.tailscale.com/a/from-up\n"), nil
				}
				return nil, errors.New(joined)
			},
			want: http.StatusOK,
			body: "from-up",
		},
		{
			name: "daemon down",
			exec: func(args []string) ([]byte, error) {
				return nil, errors.New("status down")
			},
			want: http.StatusBadRequest,
			body: "not running",
		},
		{
			name: "funnel disabled",
			exec: func(args []string) ([]byte, error) {
				joined := strings.Join(args, " ")
				if strings.HasPrefix(joined, "status") {
					return []byte(`{"BackendState":"Running","Self":{"DNSName":"host.tailnet.ts.net."}}`), nil
				}
				if strings.Contains(joined, "reset") {
					return []byte("reset"), nil
				}
				return []byte("Funnel is not enabled on this tailnet\nhttps://login.tailscale.com/admin/settings/features\n"), nil
			},
			want: http.StatusOK,
			body: "funnelNotEnabled",
		},
		{
			name: "funnel failed",
			exec: func(args []string) ([]byte, error) {
				joined := strings.Join(args, " ")
				if strings.HasPrefix(joined, "status") {
					return []byte(`{"BackendState":"Running","Self":{"DNSName":"host.tailnet.ts.net."}}`), nil
				}
				return []byte("boom"), errors.New("funnel failed")
			},
			want: http.StatusInternalServerError,
			body: "boom",
		},
		{
			name: "no dns",
			exec: func(args []string) ([]byte, error) {
				joined := strings.Join(args, " ")
				if strings.HasPrefix(joined, "status") {
					return []byte(`{"BackendState":"Running","Self":{"DNSName":""}}`), nil
				}
				return []byte("available at https://unused.example"), nil
			},
			want: http.StatusInternalServerError,
			body: "DNS",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubTailscaleHost(t, "/fake/tailscale", func(_ context.Context, _ string, args ...string) ([]byte, error) {
				return tc.exec(args)
			})
			req := httptest.NewRequest(http.MethodPost, "/api/tunnel/tailscale-enable", nil)
			asDashboardAdmin(t, req)
			rec := httptest.NewRecorder()
			h.HandleTailscaleEnable(rec, req)
			if rec.Code != tc.want || !strings.Contains(rec.Body.String(), tc.body) {
				t.Fatalf("got %d %s", rec.Code, rec.Body.String())
			}
		})
	}

	stubTailscaleHost(t, "/fake/tailscale", func(_ context.Context, _ string, args ...string) ([]byte, error) {
		return []byte("not-json"), nil
	})
	check := httptest.NewRecorder()
	h.HandleTailscaleCheck(check, httptest.NewRequest(http.MethodGet, "/api/tunnel/tailscale-check", nil))
	checked := decodeMap(t, check)
	if checked["installed"] != true || checked["daemonRunning"] != false {
		t.Fatalf("bad status json %#v", checked)
	}
}

func TestGoogleDriveDefaultRedirect(t *testing.T) {
	uri := googleDriveDefaultRedirectURI()
	if !strings.HasPrefix(uri, "http://localhost:") || !strings.HasSuffix(uri, "/callback") {
		t.Fatalf("redirect %q", uri)
	}
}
