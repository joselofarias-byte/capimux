package chat

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/db"
	"9router/proxy/internal/log"
	"9router/proxy/internal/providers"
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

func seedChatConn(t *testing.T, repo *db.Repo, id, provider, baseURL string) {
	t.Helper()
	p := 1
	data := `{"apiKey":"sk-test","baseUrl":"` + baseURL + `"}`
	if err := repo.CreateProviderConnectionFull(id, provider, "apikey", provider, &p, data); err != nil {
		t.Fatalf("seed connection: %v", err)
	}
}

func TestForwardMultimodalReadErrorReturnsJSON(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("no hijack")
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
	defer upstream.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	base := "http://user:" + relayUserinfo + "@" + upstream.Listener.Addr().String() + "/v1/chat/completions?api_key=" + relayQuery
	seedChatConn(t, repo, "conn-img", "openai", base)
	h := &ChatHandler{Repo: repo, Client: &http.Client{}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"openai/dall-e-3","prompt":"cat"}`))
	h.HandleImages(rec, req)
	assertBadGateway(t, rec)
}

func TestForwardMultimodalSuccessKeepsBody(t *testing.T) {
	const payload = `{"created":1,"data":[{"url":"http://127.0.0.1/img.png"}]}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(payload))
	}))
	defer upstream.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	seedChatConn(t, repo, "conn-img", "openai", upstream.URL+"/v1/chat/completions")
	h := &ChatHandler{Repo: repo, Client: &http.Client{}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"openai/dall-e-3","prompt":"cat"}`))
	h.HandleImages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != payload {
		t.Fatalf("body %q", rec.Body.String())
	}
}

func TestChatTranscriptionReadErrorReturnsJSON(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	seedChatConn(t, repo, "conn-stt", "openai", "https://api.openai.com/v1/chat/completions")
	h := &ChatHandler{Repo: repo, Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Api-Key": []string{relayHeader}},
			Body:       errReadCloser{err: relayPoisonErr()},
			Request:    r,
		}, nil
	})}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions?model=openai/whisper-1", strings.NewReader("audio-bytes"))
	req.Header.Set("Content-Type", "application/octet-stream")
	h.HandleAudioTranscriptions(rec, req)
	assertBadGateway(t, rec)
}

func TestChatTranscriptionSuccessKeepsBody(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	seedChatConn(t, repo, "conn-stt", "openai", "https://api.openai.com/v1/chat/completions")
	h := &ChatHandler{Repo: repo, Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"text":"hello"}`)),
			Request:    r,
		}, nil
	})}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions?model=openai/whisper-1", strings.NewReader("audio-bytes"))
	req.Header.Set("Content-Type", "application/octet-stream")
	h.HandleAudioTranscriptions(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"text":"hello"`) {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestChatVideoGetReadErrorReturnsJSON(t *testing.T) {
	if providers.KnownProviders["xai"].VideoURL == "" {
		t.Fatal("xai video URL missing")
	}
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	seedChatConn(t, repo, "conn-xai", "xai", "https://api.x.ai/v1/chat/completions")
	h := &ChatHandler{Repo: repo, Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.String(), "api.x.ai") {
			t.Errorf("unexpected dial target host %s", r.URL.Host)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"X-Api-Key": []string{relayHeader}},
			Body:       errReadCloser{err: relayPoisonErr()},
			Request:    r,
		}, nil
	})}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/videos/job-1?provider=xai", nil)
	req.SetPathValue("id", "job-1")
	h.HandleVideoGet(rec, req)
	assertBadGateway(t, rec)
}

func TestChatVideoGetSuccessKeepsBody(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	seedChatConn(t, repo, "conn-xai", "xai", "https://api.x.ai/v1/chat/completions")
	h := &ChatHandler{Repo: repo, Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"job-1","status":"queued"}`)),
			Request:    r,
		}, nil
	})}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/videos/job-1?provider=xai", nil)
	req.SetPathValue("id", "job-1")
	h.HandleVideoGet(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"queued"`) {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestChatAudioVoicesReadErrorReturnsJSON(t *testing.T) {
	h := &ChatHandler{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Host, "elevenlabs.io") {
			t.Errorf("unexpected host %s", r.URL.Host)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"X-Api-Key": []string{relayHeader}},
			Body:       errReadCloser{err: relayPoisonErr()},
			Request:    r,
		}, nil
	})}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/audio/voices?provider=elevenlabs", nil)
	h.HandleAudioVoices(rec, req)
	assertBadGateway(t, rec)
}

func TestChatAudioVoicesSuccessKeepsBody(t *testing.T) {
	const payload = `{"voices":[{"voice_id":"aria"}]}`
	h := &ChatHandler{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(payload)),
			Request:    r,
		}, nil
	})}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/audio/voices?provider=elevenlabs", nil)
	h.HandleAudioVoices(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != payload {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
}

func withRelayMimoJWT(t *testing.T) {
	t.Helper()
	mimoJWTMu.Lock()
	prev, prevExp := mimoJWT, mimoJWTExp
	mimoJWT = "jwt-relay-test"
	mimoJWTExp = time.Now().Add(time.Hour)
	mimoJWTMu.Unlock()
	t.Cleanup(func() {
		mimoJWTMu.Lock()
		mimoJWT, mimoJWTExp = prev, prevExp
		mimoJWTMu.Unlock()
	})
}

func TestMimoFreeReadErrorReturnsJSONAndStops(t *testing.T) {
	withRelayMimoJWT(t)
	h := &ChatHandler{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Api-Key": []string{relayHeader}},
			Body:       errReadCloser{err: relayPoisonErr()},
			Request:    r,
		}, nil
	})}}

	rec := httptest.NewRecorder()
	cw := newCommittedResponseWriter(rec)
	err := h.MimoFreeChat(context.Background(), cw, []byte(`{"model":"mimo-v2.5","messages":[{"role":"user","content":"hi"}]}`), false, &streamMetrics{})
	assertBadGateway(t, rec)
	if err == nil {
		t.Fatal("read error returned nil")
	}
	if !cw.IsCommitted() {
		t.Fatal("error was not committed")
	}
	result := h.finishComboAttempt(comboAttempt{
		CW: cw, Err: err, Provider: "mimo-free", Model: "mimo-v2.5",
	})
	if !result.StopRequest {
		t.Fatal("combo would keep going")
	}
}

func TestMimoFreeSuccessKeepsBody(t *testing.T) {
	withRelayMimoJWT(t)
	const payload = `{"id":"mimo-ok","choices":[{"message":{"content":"hi"}}]}`
	h := &ChatHandler{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(payload)),
			Request:    r,
		}, nil
	})}}

	rec := httptest.NewRecorder()
	err := h.MimoFreeChat(context.Background(), rec, []byte(`{"model":"mimo-v2.5","messages":[{"role":"user","content":"hi"}]}`), false, &streamMetrics{})
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if rec.Code != http.StatusOK || rec.Body.String() != payload {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestStreamReadErrorStaysCommitted200(t *testing.T) {
	h := &ChatHandler{}
	rec := httptest.NewRecorder()
	cw := newCommittedResponseWriter(rec)
	err := h.handleStreamResponse(context.Background(), cw, errReadCloser{err: errors.New("stream body closed")}, false, time.Now(), &streamMetrics{})
	if err == nil {
		t.Fatal("expected read error")
	}
	if !cw.IsCommitted() {
		t.Fatal("stream read error left the writer uncommitted")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, bytes %d", rec.Code, rec.Body.Len())
	}
}

func TestGeminiStreamReadErrorStaysCommitted200(t *testing.T) {
	h := &ChatHandler{}
	rec := httptest.NewRecorder()
	cw := newCommittedResponseWriter(rec)
	err := h.handleGeminiStream(context.Background(), cw, errReadCloser{err: errors.New("gemini body closed")}, false, &streamMetrics{})
	if err == nil {
		t.Fatal("expected read error")
	}
	if !cw.IsCommitted() {
		t.Fatal("gemini stream read error left the writer uncommitted")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, bytes %d", rec.Code, rec.Body.Len())
	}
}

func TestStreamSuccessKeepsEvent(t *testing.T) {
	h := &ChatHandler{}
	rec := httptest.NewRecorder()
	upstream := io.NopCloser(strings.NewReader("data: {\"ok\":true}\n\n"))
	err := h.handleStreamResponse(context.Background(), rec, upstream, false, time.Now(), &streamMetrics{})
	if err != nil {
		t.Fatalf("err %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `{"ok":true}`) {
		t.Fatalf("body %q", rec.Body.String())
	}
}
