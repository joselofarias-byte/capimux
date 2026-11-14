package media

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"9router/proxy/internal/db"
	"9router/proxy/internal/log"
)

const (
	relayUserinfo = "capimux-userinfo-9f3a"
	relayQuery    = "capimux-query-9f3a"
	relayHeader   = "capimux-header-9f3a"
)

func relayPoisonErr() error {
	return errors.New("read http://user:" + relayUserinfo + "@127.0.0.1/v1?api_key=" + relayQuery + " Authorization: Bearer " + relayHeader)
}

type errReadCloser struct{ err error }

func (e errReadCloser) Read([]byte) (int, error) { return 0, e.err }
func (e errReadCloser) Close() error             { return nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func assertNoRelayFixture(t *testing.T, body string) {
	t.Helper()
	for _, token := range []string{relayUserinfo, relayQuery, relayHeader} {
		if strings.Contains(body, token) {
			t.Errorf("response contains fixture material %q", token)
		}
	}
}

func assertLogsHideFixture(t *testing.T) {
	t.Helper()
	for _, line := range log.ConsoleLogs() {
		for _, token := range []string{relayUserinfo, relayQuery, relayHeader} {
			if strings.Contains(line, token) {
				t.Errorf("log contains fixture material %q", token)
				return
			}
		}
	}
}

func assertBadGateway(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, bytes %d", rec.Code, rec.Body.Len())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"message":"failed to read upstream response"`,
		`"code":"bad_gateway"`,
		`"type":"server_error"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s", want)
		}
	}
	if rec.Header().Get("X-Api-Key") != "" {
		t.Fatal("upstream header was forwarded on the error")
	}
	assertNoRelayFixture(t, body)
	assertLogsHideFixture(t)
}

func seedOpenAI(t *testing.T, repo *db.Repo, baseURL string) {
	t.Helper()
	p := 1
	data := `{"apiKey":"sk-test","baseUrl":` + jsonString(baseURL) + `}`
	if err := repo.CreateProviderConnectionFull("conn-relay", "openai", "apikey", "OpenAI", &p, data); err != nil {
		t.Fatalf("seed connection: %v", err)
	}
}

func jsonString(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

func hijackShort(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("server does not support hijack")
			return
		}
		conn, bufrw, err := hj.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer conn.Close()
		_, _ = bufrw.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 64\r\nX-Api-Key: " + relayHeader + "\r\n\r\n")
		_ = bufrw.Flush()
	}))
}

func TestEmbeddingsReadErrorReturnsJSON(t *testing.T) {
	upstream := hijackShort(t)
	defer upstream.Close()

	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	base := "http://user:" + relayUserinfo + "@" + upstream.Listener.Addr().String() + "/v1/chat/completions?api_key=" + relayQuery
	seedOpenAI(t, repo, base)
	handler := newTestMediaHandler(repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"openai/text-embedding-ada-002","input":"hi"}`))
	handler.HandleEmbeddings(rec, req)
	assertBadGateway(t, rec)
}

func TestEmbeddingsEmpty2xxStays200(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	seedOpenAI(t, repo, upstream.URL+"/v1/chat/completions")
	handler := newTestMediaHandler(repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"openai/text-embedding-ada-002","input":"hi"}`))
	handler.HandleEmbeddings(rec, req)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("status %d, bytes %d", rec.Code, rec.Body.Len())
	}
}

func TestEmbeddingsSuccessKeepsBody(t *testing.T) {
	const payload = `{"object":"list","data":[{"embedding":[0.1]}]}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(payload))
	}))
	defer upstream.Close()

	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	seedOpenAI(t, repo, upstream.URL+"/v1/chat/completions")
	handler := newTestMediaHandler(repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"openai/text-embedding-ada-002","input":"hi"}`))
	handler.HandleEmbeddings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != payload {
		t.Fatalf("body %q", rec.Body.String())
	}
}

func TestEmbeddingsNon2xxStaysUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"slow"}`))
	}))
	defer upstream.Close()

	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	seedOpenAI(t, repo, upstream.URL+"/v1/chat/completions")
	handler := newTestMediaHandler(repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"openai/text-embedding-ada-002","input":"hi"}`))
	handler.HandleEmbeddings(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, bytes %d", rec.Code, rec.Body.Len())
	}
	if !strings.Contains(rec.Body.String(), "slow") {
		t.Fatalf("body %q", rec.Body.String())
	}
}

func TestEmbeddingsCreateErrorIsNotSilent(t *testing.T) {
	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	seedOpenAI(t, repo, "http://%zz")
	handler := newTestMediaHandler(repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"openai/text-embedding-ada-002","input":"hi"}`))
	handler.HandleEmbeddings(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("status 200, bytes %d", rec.Body.Len())
	}
	if rec.Body.Len() == 0 {
		t.Fatal("empty body")
	}
}

func TestSystemoneReadErrorReturnsJSON(t *testing.T) {
	upstream := hijackShort(t)
	defer upstream.Close()

	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	base := "http://user:" + relayUserinfo + "@" + upstream.Listener.Addr().String() + "/v1/chat/completions?api_key=" + relayQuery
	p := 1
	data := `{"baseUrl":` + jsonString(base) + `}`
	if err := repo.CreateProviderConnectionFull("conn-oc", "opencode", "none", "OpenCode", &p, data); err != nil {
		t.Fatalf("seed: %v", err)
	}
	handler := newTestMediaHandler(repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(`{"model":"oc/jev-1.13-free","state":"ok","questions":{}}`))
	handler.HandleSystemone(rec, req)
	assertBadGateway(t, rec)
}

func TestSystemoneSuccessKeepsBody(t *testing.T) {
	const payload = `{"answers":{"ok":true}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "req-ok")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(payload))
	}))
	defer upstream.Close()

	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	p := 1
	data := `{"baseUrl":` + jsonString(upstream.URL+"/v1/chat/completions") + `}`
	if err := repo.CreateProviderConnectionFull("conn-oc", "opencode", "none", "OpenCode", &p, data); err != nil {
		t.Fatalf("seed: %v", err)
	}
	handler := newTestMediaHandler(repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(`{"model":"oc/jev-1.13-free","state":"ok","questions":{}}`))
	handler.HandleSystemone(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != payload {
		t.Fatalf("body %q", rec.Body.String())
	}
	if rec.Header().Get("X-Request-Id") != "req-ok" {
		t.Fatalf("missing forwarded header, got %q", rec.Header().Get("X-Request-Id"))
	}
}

func TestSystemoneCreateErrorIsFixedMessage(t *testing.T) {
	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	p := 1
	bad := "http://user:" + relayUserinfo + "@%zz?api_key=" + relayQuery
	data := `{"baseUrl":` + jsonString(bad) + `}`
	if err := repo.CreateProviderConnectionFull("conn-oc", "opencode", "none", "OpenCode", &p, data); err != nil {
		t.Fatalf("seed: %v", err)
	}
	handler := newTestMediaHandler(repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(`{"model":"oc/jev-1.13-free","state":"ok","questions":{}}`))
	handler.HandleSystemone(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, bytes %d", rec.Code, rec.Body.Len())
	}
	if !strings.Contains(rec.Body.String(), `"message":"failed to create request"`) {
		t.Fatalf("body %s", rec.Body.String())
	}
	assertNoRelayFixture(t, rec.Body.String())
}

func newHeadroom(repo *db.Repo, rt http.RoundTripper) *HeadroomHandler {
	return &HeadroomHandler{
		Repo:   repo,
		Client: &http.Client{Transport: rt},
	}
}

func TestHeadroomReadErrorReturnsJSON(t *testing.T) {
	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	handler := newHeadroom(db.NewRepo(database), roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"X-Api-Key": []string{relayHeader}, "Content-Type": []string{"text/plain"}},
			Body:       errReadCloser{err: relayPoisonErr()},
			Request:    r,
		}, nil
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/headroom/proxy/status", nil)
	handler.HandleHeadroomProxy(rec, req)
	assertBadGateway(t, rec)
}

func TestHeadroomSuccessKeepsBody(t *testing.T) {
	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	handler := newHeadroom(db.NewRepo(database), roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/plain"}, "X-Request-Id": []string{"hr-1"}},
			Body:       io.NopCloser(strings.NewReader("hello-headroom")),
			Request:    r,
		}, nil
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/headroom/proxy/status", nil)
	handler.HandleHeadroomProxy(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "hello-headroom" {
		t.Fatalf("body %q", rec.Body.String())
	}
	if rec.Header().Get("X-Request-Id") != "hr-1" {
		t.Fatalf("header %q", rec.Header().Get("X-Request-Id"))
	}
}

func TestHeadroomDashboardReadErrorIsNotSilent(t *testing.T) {
	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	handler := newHeadroom(db.NewRepo(database), roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html"}},
			Body:       errReadCloser{err: errors.New("dashboard read failed")},
			Request:    r,
		}, nil
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/headroom/proxy/dashboard", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("*", "dashboard")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	handler.HandleHeadroomProxy(rec, req)
	if rec.Code == http.StatusOK && rec.Body.Len() == 0 {
		t.Fatal("status 200, bytes 0")
	}
	if rec.Code == http.StatusOK {
		t.Fatalf("status 200, bytes %d", rec.Body.Len())
	}
}

func resetMinimaxVoiceCache(t *testing.T) {
	t.Helper()
	voiceCacheMu.Lock()
	minimaxVoiceCache = map[string]voiceCacheEntry{}
	voiceCacheMu.Unlock()
	t.Cleanup(func() {
		voiceCacheMu.Lock()
		minimaxVoiceCache = map[string]voiceCacheEntry{}
		voiceCacheMu.Unlock()
	})
}

func TestMinimaxVoiceReadErrorReturnsJSON(t *testing.T) {
	resetMinimaxVoiceCache(t)
	var hits int
	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	p := 1
	if err := repo.CreateProviderConnectionFull("conn-mm", "minimax", "apikey", "MiniMax", &p, `{"apiKey":"sk-mm"}`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	handler := newTestMediaHandler(repo)
	handler.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hits++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Api-Key": []string{relayHeader}},
			Body:       errReadCloser{err: relayPoisonErr()},
			Request:    r,
		}, nil
	})}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/media-providers/tts/voices?provider=minimax", nil)
	handler.HandleAudioVoices(rec, req)
	assertBadGateway(t, rec)
	if hits != 1 {
		t.Fatalf("upstream calls %d", hits)
	}
}

func TestMinimaxVoiceSuccessKeepsVoice(t *testing.T) {
	resetMinimaxVoiceCache(t)
	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	p := 1
	if err := repo.CreateProviderConnectionFull("conn-mm", "minimax", "apikey", "MiniMax", &p, `{"apiKey":"sk-mm-ok"}`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	handler := newTestMediaHandler(repo)
	handler.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"base_resp":{"status_code":0},"system_voice":[{"voice_id":"English_Calm","voice_name":"Calm"}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/media-providers/tts/voices?provider=minimax", nil)
	handler.HandleAudioVoices(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "English_Calm") {
		t.Fatalf("body %s", rec.Body.String())
	}
}
