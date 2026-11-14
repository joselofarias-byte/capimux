package chat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9router/proxy/internal/db"
)

func TestMultimodalForwardsToScriptedUpstream(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`UPDATE providerConnections SET data = ? WHERE id = 'conn-1'`,
		`{"apiKey":"sk-test","baseUrl":"https://upstream.test/v1/chat/completions"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		('conn-xai', 'xai', 'apikey', 'xai', 1, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
		`{"apiKey":"xai-test","baseUrl":"https://api.x.ai/v1/chat/completions"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		('conn-or', 'openrouter', 'apikey', 'or', 1, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
		`{"apiKey":"or-test"}`); err != nil {
		t.Fatal(err)
	}

	script := newHostScript()
	script.allow("upstream.test", func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") == "" {
			t.Errorf("missing auth on %s", r.URL.Path)
		}
		return textResp(http.StatusOK, "image/png", "png-bytes"), nil
	})
	script.allow("openrouter.ai", func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v1/images/generations" {
			t.Errorf("openrouter path %s", r.URL.Path)
		}
		return jsonResp(http.StatusOK, `{"created":1}`), nil
	})
	script.allow("api.x.ai", func(r *http.Request) (*http.Response, error) {
		return jsonResp(http.StatusOK, `{"id":"vid","status":"done"}`), nil
	})
	h := NewChatHandler(db.NewRepo(database))
	h.Client = scriptedClient(script)

	post := func(handler func(http.ResponseWriter, *http.Request), path, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		handler(rec, req)
		return rec
	}

	body := `{"model":"deepseek/deepseek-chat","prompt":"a cat"}`
	if rec := post(h.HandleImages, "/v1/images/generations", body); rec.Code != http.StatusOK || rec.Body.String() != "png-bytes" {
		t.Fatalf("images %d %s", rec.Code, rec.Body.String())
	}
	if rec := post(h.HandleAudioSpeech, "/v1/audio/speech", `{"model":"fast-model","input":"hi"}`); rec.Code != http.StatusOK {
		t.Fatalf("speech %d %s", rec.Code, rec.Body.String())
	}
	if rec := post(h.HandleVideoGenerations, "/v1/videos/generations", body); rec.Code != http.StatusOK {
		t.Fatalf("video gen %d %s", rec.Code, rec.Body.String())
	}
	if rec := post(h.HandleVideoEdits, "/v1/videos/edits", body); rec.Code != http.StatusOK {
		t.Fatalf("video edit %d %s", rec.Code, rec.Body.String())
	}
	if rec := post(h.HandleVideoExtensions, "/v1/videos/extensions", body); rec.Code != http.StatusOK {
		t.Fatalf("video ext %d %s", rec.Code, rec.Body.String())
	}
	if rec := post(h.HandleImages, "/v1/images/generations", `{"model":"openrouter/openai/gpt-image"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "created") {
		t.Fatalf("openrouter image %d %s", rec.Code, rec.Body.String())
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", strings.NewReader("audio-bytes"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	req.Form = map[string][]string{"model": {"deepseek/deepseek-chat"}}
	h.HandleAudioTranscriptions(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "png-bytes" {
		t.Fatalf("stt %d %s ct %s", rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"))
	}

	rec = httptest.NewRecorder()
	get := httptest.NewRequest(http.MethodGet, "/v1/videos/job-1?provider=xai", nil)
	get.SetPathValue("id", "job-1")
	h.HandleVideoGet(rec, get)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "vid") {
		t.Fatalf("video get %d %s", rec.Code, rec.Body.String())
	}
}

func TestMultimodalPrepAndUpstreamErrors(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		('conn-empty', 'groq', 'apikey', 'empty', 0, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
		`{"apiKey":""}`); err != nil {
		t.Fatal(err)
	}
	h := NewChatHandler(db.NewRepo(database))
	h.Client = scriptedClient(newHostScript())

	bad := func(handler func(http.ResponseWriter, *http.Request), body string, want int) {
		t.Helper()
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
		if rec.Code != want {
			t.Fatalf("body %s status %d %s", body, rec.Code, rec.Body.String())
		}
	}
	bad(h.HandleImages, "", http.StatusBadRequest)
	bad(h.HandleImages, "{", http.StatusBadRequest)
	bad(h.HandleImages, `{"prompt":"x"}`, http.StatusBadRequest)
	bad(h.HandleImages, `{"model":"missing/model"}`, http.StatusNotFound)
	bad(h.HandleAudioSpeech, `{"model":"openai/gpt-4o"}`, http.StatusNotFound)
	bad(h.HandleVideoGenerations, `{"model":"deepseek/deepseek-chat"}`, http.StatusBadGateway)

	rec := httptest.NewRecorder()
	h.HandleImages(rec, httptest.NewRequest(http.MethodPost, "/", errReadCloser{err: errString("nope")}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("read %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.HandleAudioSpeech(rec, httptest.NewRequest(http.MethodPost, "/", errReadCloser{err: errString("nope")}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("speech read %d", rec.Code)
	}
	for _, handler := range []func(http.ResponseWriter, *http.Request){h.HandleVideoGenerations, h.HandleVideoEdits, h.HandleVideoExtensions} {
		rec = httptest.NewRecorder()
		handler(rec, httptest.NewRequest(http.MethodPost, "/", errReadCloser{err: errString("nope")}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("video read %d", rec.Code)
		}
	}

	rec = httptest.NewRecorder()
	h.HandleAudioTranscriptions(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("x")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("stt missing model %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", errReadCloser{err: errString("nope")})
	req.Form = map[string][]string{"model": {"deepseek/deepseek-chat"}}
	h.HandleAudioTranscriptions(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("stt read %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("x"))
	req.Form = map[string][]string{"model": {"openai/gpt-4o"}}
	h.HandleAudioTranscriptions(rec, req)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
		t.Fatalf("stt resolve %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	get := httptest.NewRequest(http.MethodGet, "/v1/videos/", nil)
	h.HandleVideoGet(rec, get)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing id %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	get = httptest.NewRequest(http.MethodGet, "/v1/videos/job?provider=deepseek", nil)
	get.SetPathValue("id", "job")
	h.HandleVideoGet(rec, get)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("no video endpoint %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	get = httptest.NewRequest(http.MethodGet, "/v1/videos/job?provider=xai", nil)
	get.SetPathValue("id", "job")
	h.HandleVideoGet(rec, get)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusBadGateway {
		t.Fatalf("xai without connection %d %s", rec.Code, rec.Body.String())
	}

	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		('conn-xai-empty', 'xai', 'apikey', 'xai', 1, 1, '{}', '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	get = httptest.NewRequest(http.MethodGet, "/v1/videos/job?provider=xai", nil)
	get.SetPathValue("id", "job")
	h.HandleVideoGet(rec, get)
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusBadGateway {
		t.Fatalf("xai empty key %d %s", rec.Code, rec.Body.String())
	}
}
