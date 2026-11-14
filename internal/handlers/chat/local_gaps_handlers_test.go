package chat

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"9router/proxy/internal/db"
	"9router/proxy/internal/providers"
)

func TestHandleHealthAndModelsInfo(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	h := NewChatHandler(db.NewRepo(database))

	rec := httptest.NewRecorder()
	h.HandleHealth(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("health %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.HandleModelsInfo(rec, httptest.NewRequest(http.MethodGet, "/v1/models/info", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing id %d", rec.Code)
	}

	emptyDB, emptyCleanup := setupChatTestDB(t)
	defer emptyCleanup()
	if _, err := emptyDB.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatal(err)
	}
	empty := NewChatHandler(db.NewRepo(emptyDB))
	rec = httptest.NewRecorder()
	empty.HandleModelsInfo(rec, httptest.NewRequest(http.MethodGet, "/v1/models/info?id=does-not-exist", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown model %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.HandleModelsInfo(rec, httptest.NewRequest(http.MethodGet, "/v1/models/info?id=fast-model", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "deepseek") {
		t.Fatalf("alias info %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.HandleModelsInfo(rec, httptest.NewRequest(http.MethodGet, "/v1/models/info?id=my-combo", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `"combo":true`) || !strings.Contains(body, "deepseek-chat") {
		t.Fatalf("combo info %d %s", rec.Code, body)
	}
}

func TestHandleModelsByKindAndLookup(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	h := NewChatHandler(db.NewRepo(database))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models/", nil)
	h.HandleModelsByKind(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing kind %d", rec.Code)
	}

	for _, kind := range []string{"image", "tts", "stt", "systemone", "embedding", "web", "image-to-text", "other"} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, "/v1/models/"+kind, nil)
		req.SetPathValue("kind", kind)
		h.HandleModelsByKind(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("kind %s status %d", kind, rec.Code)
		}
	}

	lookup := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		h.HandleModelLookup(rec, req)
		return rec
	}
	if lookup("/nope").Code != http.StatusBadRequest {
		t.Fatal("path without /models")
	}
	if lookup("/v1/models").Code != http.StatusOK {
		t.Fatal("empty suffix should list models")
	}
	if rec := lookup("/v1/models/image"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "image") {
		t.Fatalf("kind lookup %d %s", rec.Code, rec.Body.String())
	}
	list := lookup("/v1/models")
	var published struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &published); err != nil {
		t.Fatal(err)
	}
	var publishedID string
	for _, item := range published.Data {
		if strings.Contains(item.ID, "/") {
			publishedID = item.ID
			break
		}
	}
	if publishedID == "" {
		t.Fatal("models list published no provider/model id")
	}
	if rec := lookup("/v1/models/" + publishedID); rec.Code != http.StatusOK {
		t.Fatalf("provider lookup %s %d %s", publishedID, rec.Code, rec.Body.String())
	}
	if rec := lookup("/v1/models/" + urlEscape(publishedID)); rec.Code != http.StatusOK {
		t.Fatalf("encoded lookup %d %s", rec.Code, rec.Body.String())
	}
	if rec := lookup("/v1/models/missing/model"); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "model_not_found") {
		t.Fatalf("missing lookup %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/v1/models/*", nil)
	req.SetPathValue("*", "tts")
	rec = httptest.NewRecorder()
	h.HandleModelLookup(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("wildcard kind %d %s", rec.Code, rec.Body.String())
	}
}

func urlEscape(s string) string {
	return strings.ReplaceAll(s, "/", "%2F")
}

func TestHandleAudioVoicesLocal(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	script := newHostScript()
	script.allow("api.elevenlabs.io", func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/voices") {
			t.Errorf("voices path %s", r.URL.Path)
		}
		return jsonResp(http.StatusOK, `{"voices":[{"voice_id":"local"}]}`), nil
	})
	script.allow("api.cartesia.ai", func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/voices") {
			t.Errorf("derived voices path %s", r.URL.Path)
		}
		return jsonResp(http.StatusOK, `{"voices":[]}`), nil
	})
	h := NewChatHandler(db.NewRepo(database))
	h.Client = scriptedClient(script)

	rec := httptest.NewRecorder()
	h.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/v1/audio/voices", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing provider %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/v1/audio/voices?provider=no-such", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown provider %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/v1/audio/voices?provider=deepseek", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("no voices endpoint %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/v1/audio/voices?provider=elevenlabs", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "local") {
		t.Fatalf("elevenlabs %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/v1/audio/voices?provider=cartesia", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("cartesia %d %s", rec.Code, rec.Body.String())
	}

	h.Client = scriptedClient(newHostScript())
	rec = httptest.NewRecorder()
	h.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/v1/audio/voices?provider=elevenlabs", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("unscripted voices %d %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCatalogSyncLocal(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	h := NewChatHandler(db.NewRepo(database))

	rec := httptest.NewRecorder()
	h.HandleCatalogSyncStatus(rec, httptest.NewRequest(http.MethodGet, "/api/models/catalog-sync", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), providers.ModelsDevCatalogURL) {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}

	script := newHostScript()
	var hits int
	script.allow("models.dev", func(r *http.Request) (*http.Response, error) {
		hits++
		switch hits {
		case 1:
			return nil, errString("catalog down")
		case 2:
			return jsonResp(http.StatusBadGateway, "nope"), nil
		case 3:
			return jsonResp(http.StatusOK, `{}`), nil
		default:
			resp := jsonResp(http.StatusOK, `{"deepseek":{"models":{"deepseek-chat":{"limit":{"context":128000,"output":8192},"modalities":{"input":["text"]}}}}}`)
			resp.Header.Set("ETag", `"local"`)
			return resp, nil
		}
	})
	h.Client = scriptedClient(script)

	for i := 0; i < 3; i++ {
		rec = httptest.NewRecorder()
		h.HandleCatalogSyncTrigger(rec, httptest.NewRequest(http.MethodPost, "/api/models/catalog-sync", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("trigger %d status %d body %s", i, rec.Code, rec.Body.String())
		}
	}
	rec = httptest.NewRecorder()
	h.HandleCatalogSyncTrigger(rec, httptest.NewRequest(http.MethodPost, "/api/models/catalog-sync", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "success") {
		t.Fatalf("sync ok %d %s", rec.Code, rec.Body.String())
	}
}

func TestHandleChangelogAndUpdatesLocal(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	h := NewChatHandler(db.NewRepo(database))

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.HandleChangelog(rec, httptest.NewRequest(http.MethodGet, "/changelog", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "markdown") {
		t.Fatalf("local changelog %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}

	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	h.HandleChangelog(rec, httptest.NewRequest(http.MethodGet, "/changelog", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing changelog %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.HandleToggleAutoUpdate(rec, httptest.NewRequest(http.MethodPost, "/api/version/auto", strings.NewReader("{")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad toggle %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.HandleToggleAutoUpdate(rec, httptest.NewRequest(http.MethodPost, "/api/version/auto", strings.NewReader(`{"enabled":true}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "true") {
		t.Fatalf("toggle %d %s", rec.Code, rec.Body.String())
	}

	manifest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "" {
			t.Errorf("empty host")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"0.0.1","downloadUrl":"http://127.0.0.1/unused","sha256":"abc"}`))
	}))
	t.Cleanup(manifest.Close)
	t.Setenv("UPDATE_URL", manifest.URL+"/version.json")

	rec = httptest.NewRecorder()
	h.HandleCheckUpdate(rec, httptest.NewRequest(http.MethodPost, "/api/version/check", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "hasUpdate") {
		t.Fatalf("check %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.HandleTriggerUpdate(rec, httptest.NewRequest(http.MethodPost, "/api/version/update", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "up_to_date") {
		t.Fatalf("trigger %d %s", rec.Code, rec.Body.String())
	}

	t.Setenv("UPDATE_URL", "http://127.0.0.1:1/version.json")
	rec = httptest.NewRecorder()
	h.HandleCheckUpdate(rec, httptest.NewRequest(http.MethodPost, "/api/version/check", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("check failure %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.HandleTriggerUpdate(rec, httptest.NewRequest(http.MethodPost, "/api/version/update", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("trigger failure %d %s", rec.Code, rec.Body.String())
	}

	if _, err := os.Stat(filepath.Join(dir, "CHANGELOG.md")); err == nil {
		t.Fatal("temp dir grew a changelog")
	}
}

func TestHandleOllamaAndTestModelLocal(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	script := newHostScript()
	var mode string
	script.allow("api.deepseek.com", func(r *http.Request) (*http.Response, error) {
		switch mode {
		case "401":
			return jsonResp(http.StatusUnauthorized, `{"error":{"message":"denied"}}`), nil
		case "429":
			return jsonResp(http.StatusTooManyRequests, `{"error":{"message":"slow"}}`), nil
		case "404":
			return jsonResp(http.StatusNotFound, `{"message":"gone"}`), nil
		case "503":
			return jsonResp(http.StatusServiceUnavailable, `not-json-`+strings.Repeat("x", 600)), nil
		case "html":
			return textResp(http.StatusBadGateway, "text/html", "<html>"+strings.Repeat("e", 600)+"</html>"), nil
		default:
			return jsonResp(http.StatusOK, localChatCompletion), nil
		}
	})
	h := NewChatHandler(db.NewRepo(database))
	h.Client = scriptedClient(script)

	rec := httptest.NewRecorder()
	h.HandleOllamaChat(rec, httptest.NewRequest(http.MethodPost, "/v1/api/chat", errReadCloser{err: errString("nope")}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ollama read %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.HandleOllamaChat(rec, httptest.NewRequest(http.MethodPost, "/v1/api/chat", strings.NewReader(`{"model":"deepseek/deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "local-ok") {
		t.Fatalf("ollama %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.HandleTestModel(rec, httptest.NewRequest(http.MethodPost, "/api/models/test", errReadCloser{err: errString("nope")}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("test read %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.HandleTestModel(rec, httptest.NewRequest(http.MethodPost, "/api/models/test", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Model required") {
		t.Fatalf("test missing %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.HandleTestModel(rec, httptest.NewRequest(http.MethodPost, "/api/models/test", strings.NewReader(`{"model":"fast-model"}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("test ok %d %s", rec.Code, rec.Body.String())
	}

	for _, tc := range []struct {
		mode string
		want string
	}{
		{"429", "429:"},
		{"404", "404:"},
		{"503", "503:"},
		{"html", "truncated"},
		{"401", "401:"},
	} {
		mode = tc.mode
		if _, err := database.Exec(`UPDATE providerConnections SET data = ? WHERE id = 'conn-1'`, `{"apiKey":"sk-test-deepseek-key"}`); err != nil {
			t.Fatal(err)
		}
		rec = httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/models/test", strings.NewReader(`{"model":"deepseek/deepseek-chat"}`))
		h.HandleTestModel(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.want) {
			t.Fatalf("mode %s body %s", tc.mode, rec.Body.String())
		}
	}
}

func TestHandleResponsesCompactLocal(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	script := newHostScript()
	allowChatCompletion(script, "api.deepseek.com")
	h := NewChatHandler(db.NewRepo(database))
	h.Client = scriptedClient(script)

	rec := httptest.NewRecorder()
	h.HandleResponsesCompact(rec, httptest.NewRequest(http.MethodPost, "/v1/responses/compact", errReadCloser{err: errString("nope")}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("read %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.HandleResponsesCompact(rec, httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader("{")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("json %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.HandleResponsesCompact(rec, httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(`{"model":"deepseek/deepseek-chat","input":"hi"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("compact %d %s", rec.Code, rec.Body.String())
	}
}
