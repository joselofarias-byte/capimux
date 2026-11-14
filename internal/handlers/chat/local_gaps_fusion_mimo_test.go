package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/db"
	"9router/proxy/internal/models"
	"9router/proxy/internal/translator"
)

func TestHandleFusionLocal(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	script := newHostScript()
	allowChatCompletion(script, "api.deepseek.com")
	allowChatCompletion(script, "api.groq.com")
	h := NewChatHandler(db.NewRepo(database))
	h.Client = scriptedClient(script)

	body := []byte(`{"model":"fusion","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"","tool_calls":[{"function":{"name":"do","arguments":"{}"}}]},{"role":"tool","content":"ok"}],"tools":[{"type":"function"}],"stream":true}`)

	rec := httptest.NewRecorder()
	h.handleFusion(context.Background(), rec, body, nil, "fusion", false, false, "fusion", 0, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty panel %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.handleFusion(context.Background(), rec, []byte("{"), []string{"deepseek/deepseek-chat", "groq/llama"}, "fusion", false, false, "fusion", 0, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.handleFusion(context.Background(), rec, body, []string{"nope"}, "fusion", false, false, "fusion", 0, "")
	if rec.Code != http.StatusServiceUnavailable && rec.Code != http.StatusBadGateway && rec.Code != http.StatusBadRequest {
		t.Fatalf("unresolved panel %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.handleFusion(context.Background(), rec, body, []string{"deepseek/deepseek-chat", "missing/model"}, "fusion", false, false, "fusion", 0, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("degraded fusion %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.handleFusion(context.Background(), rec, body, []string{"deepseek/deepseek-chat", "groq/llama-3.3-70b-versatile"}, "fusion", false, false, "panel", 0, "deepseek/deepseek-chat")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "local-ok") {
		t.Fatalf("fusion %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.handleFusion(context.Background(), rec, body, []string{"deepseek/deepseek-chat", "groq/llama-3.3-70b-versatile"}, "fusion", true, false, "panel-stream", 0, "not-a-provider/nope")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("bad judge %d %s", rec.Code, rec.Body.String())
	}

	call := h.makePanelCall([]byte("{"), "deepseek/deepseek-chat")
	if res := call(context.Background()); res == nil || res.err == nil {
		t.Fatalf("bad panel body %#v", res)
	}
	call = h.makePanelCall(body, "unknown-provider/model")
	if res := call(context.Background()); res == nil || res.err == nil {
		t.Fatalf("unresolved panel call %#v", res)
	}
}

func TestMimoFreeChatLocal(t *testing.T) {
	mimoJWTMu.Lock()
	oldJWT, oldExp := mimoJWT, mimoJWTExp
	mimoJWT = ""
	mimoJWTExp = time.Time{}
	mimoJWTMu.Unlock()
	t.Cleanup(func() {
		mimoJWTMu.Lock()
		mimoJWT, mimoJWTExp = oldJWT, oldExp
		mimoJWTMu.Unlock()
	})

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	var chatHits int
	script := newHostScript()
	script.allow("api.xiaomimimimo.com", func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/free-ai/bootstrap":
			return jsonResp(http.StatusOK, `{"jwt":"mimo-jwt"}`), nil
		case "/api/free-ai/openai/chat":
			chatHits++
			if chatHits == 1 {
				return jsonResp(http.StatusUnauthorized, `{"error":"expired"}`), nil
			}
			if r.Header.Get("Accept") == "text/event-stream" {
				return textResp(http.StatusOK, "text/event-stream", "data: {\"choices\":[{\"delta\":{\"content\":\"mimo\"}}]}\n\ndata: [DONE]\n\n"), nil
			}
			return jsonResp(http.StatusOK, localChatCompletion), nil
		default:
			return nil, errString("unexpected mimo path " + r.URL.Path)
		}
	})
	h := NewChatHandler(db.NewRepo(database))
	h.Client = scriptedClient(script)
	body := []byte(`{"model":"mimo-v2.5-pro","messages":[{"role":"user","content":"hi"}]}`)

	rec := httptest.NewRecorder()
	if err := h.MimoFreeChat(context.Background(), rec, body, false, &streamMetrics{}); err != nil || !strings.Contains(rec.Body.String(), "local-ok") {
		t.Fatalf("mimo err=%v body=%s", err, rec.Body.String())
	}

	chatHits = 0
	rec = httptest.NewRecorder()
	if err := h.MimoFreeChat(context.Background(), rec, body, true, &streamMetrics{}); err != nil {
		t.Fatalf("mimo stream: %v", err)
	}

	ctx := translator.WithResponsesBridge(context.Background())
	rec = httptest.NewRecorder()
	if err := h.MimoFreeChat(ctx, rec, body, false, nil); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("mimo responses err=%v code=%d body=%s", err, rec.Code, rec.Body.String())
	}

	script.allow("api.xiaomimimimo.com", func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/free-ai/openai/chat" {
			return jsonResp(http.StatusBadGateway, `{"error":"down"}`), nil
		}
		return jsonResp(http.StatusOK, `{"jwt":"mimo-jwt"}`), nil
	})
	mimoJWTMu.Lock()
	mimoJWT = ""
	mimoJWTExp = time.Time{}
	mimoJWTMu.Unlock()
	if err := h.MimoFreeChat(context.Background(), httptest.NewRecorder(), body, false, nil); err == nil {
		t.Fatal("upstream 502 should fail")
	}

	h.Client = scriptedClient(newHostScript())
	mimoJWTMu.Lock()
	mimoJWT = ""
	mimoJWTExp = time.Time{}
	mimoJWTMu.Unlock()
	if err := h.MimoFreeChat(context.Background(), httptest.NewRecorder(), body, false, nil); err == nil {
		t.Fatal("unscripted bootstrap should fail")
	}
}

func TestLiveCatalogAndLooseModelsLocal(t *testing.T) {
	if got := parseOpenAIStyleModelIDs([]byte("{")); got != nil {
		t.Fatalf("bad catalog %#v", got)
	}
	got := parseOpenAIStyleModelIDs([]byte(`{"models":[{"id":"a"},{"name":"b"},{"id":"a"},{"model":""},{"slug":"skip"}]}`))
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("parsed %#v", got)
	}

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	script := newHostScript()
	prev := grokCLICatalogURL
	grokCLICatalogURL = "https://grok-catalog.test/models"
	t.Cleanup(func() { grokCLICatalogURL = prev })
	script.allow("grok-catalog.test", func(r *http.Request) (*http.Response, error) {
		return jsonResp(http.StatusOK, `{"data":[{"id":"grok-build","context_length":1000,"max_output_tokens":100}]}`), nil
	})
	script.allow("node.test", func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") == "" || r.Header.Get("x-api-key") == "" {
			t.Errorf("compatible headers auth=%q x-api-key=%q", r.Header.Get("Authorization"), r.Header.Get("x-api-key"))
		}
		return jsonResp(http.StatusOK, `{"data":[{"id":"local-node"}]}`), nil
	})
	h := NewChatHandler(db.NewRepo(database))
	h.Client = scriptedClient(script)

	live := h.fetchGrokCLILiveCatalog(context.Background(), &models.ProviderConnection{ID: "g"}, &ConnectionData{ProviderSpecificData: map[string]any{"email": "a@b.c", "userId": "u"}}, "tok")
	if len(live) != 1 || live[0].ID != "grok-build" {
		t.Fatalf("grok catalog %#v", live)
	}

	h.Client = scriptedClient(newHostScript())
	if got := h.fetchGrokCLILiveCatalog(context.Background(), &models.ProviderConnection{ID: "g"}, &ConnectionData{}, "tok"); got != nil {
		t.Fatalf("unscripted grok %#v", got)
	}

	h.Client = scriptedClient(script)
	node := h.fetchCompatibleNodeModels(context.Background(), &models.ProviderConnection{ID: "n"}, &ConnectionData{
		APIKey: "k", ProviderSpecificData: map[string]any{"baseUrl": "https://node.test/v1/messages"},
	}, "anthropic-compatible-local")
	if len(node) != 1 || node[0].ID != "local-node" {
		t.Fatalf("node catalog %#v", node)
	}
	if h.fetchCompatibleNodeModels(context.Background(), &models.ProviderConnection{}, &ConnectionData{}, "openai-compatible-x") != nil {
		t.Fatal("missing key should skip")
	}
	if h.fetchCompatibleNodeModels(context.Background(), &models.ProviderConnection{}, &ConnectionData{APIKey: "k", ProviderSpecificData: map[string]any{"baseUrl": "https://node.test"}}, "other") != nil {
		t.Fatal("unknown prefix should skip")
	}

	if _, err := database.Exec(`INSERT INTO providerNodes (id, type, name, data, createdAt, updatedAt) VALUES ('node-1', 'openai', 'n', '{"prefix":"custom"}', '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO kv (scope, key, value) VALUES ('customModels', 'custom|local-model|llm', '{"providerAlias":"custom","id":"local-model","type":"llm","name":"Local"}')`); err != nil {
		t.Fatal(err)
	}
	listed := h.appendLooseCustomModels(nil, map[string]bool{}, map[string]map[string]bool{}, false, nil)
	if len(listed) != 1 || listed[0].ID != "custom/local-model" {
		t.Fatalf("loose models %#v", listed)
	}
	skipped := h.appendLooseCustomModels(nil, map[string]bool{}, map[string]map[string]bool{"custom": {"local-model": true}}, true, map[string]bool{})
	if len(skipped) != 0 {
		t.Fatalf("filtered loose models %#v", skipped)
	}
}
