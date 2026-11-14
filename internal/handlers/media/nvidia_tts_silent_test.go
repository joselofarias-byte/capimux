package media

import (
	"database/sql"
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"9router/proxy/internal/db"
	"9router/proxy/internal/log"
)

// Silent NVIDIA TTS paths: a cut audio body, and an upstream 4xx/429 that
// ClassifyError marks ShouldFallback. Both return done=true. These tests go
// through HandleAudioSpeech against httptest servers on loopback.

func TestNvidiaTTSReadErrorReturnsJSON(t *testing.T) {
	const (
		apiKey   = "sk-nvidia-silent-key-should-not-leak"
		user     = "nvuser"
		pass     = "nv-silent-pass"
		queryKey = "sk-nvidia-silent-query-should-not-leak"
	)
	secrets := nvidiaSilentSecrets(apiKey, user, pass, queryKey)

	var secondHits atomic.Int32
	cut := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 200 headers, then a short body against Content-Length, so the
		// client ReadAll fails instead of returning audio.
		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("X-Api-Key", apiKey)
		w.Header().Set("Authorization", "Bearer "+apiKey)
		w.Header().Set("Content-Length", "64")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("RIFF"))
	}))
	defer cut.Close()
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondHits.Add(1)
		w.Header().Set("Content-Type", "audio/wav")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("RIFF-should-not-be-used"))
	}))
	defer okSrv.Close()

	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	insertMediaConn(t, database, "nv-cut", "nvidia", apiKey, credentialedNvidiaURL(t, cut.URL, user, pass, queryKey), 1)
	insertMediaConn(t, database, "nv-cut-next", "nvidia", apiKey, okSrv.URL, 2)
	h := newTestMediaHandler(db.NewRepo(database))

	log.ClearConsoleLogs()
	rec := postNvidiaSpeech(h)

	if secondHits.Load() != 0 {
		t.Fatalf("read failure tried the next connection %d times", secondHits.Load())
	}
	payload := decodeAPIError(t, rec, secrets)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d", rec.Code)
	}
	if payload.Error.Code != "bad_gateway" || payload.Error.Type != "server_error" {
		t.Fatalf("error type/code = %s/%s", payload.Error.Type, payload.Error.Code)
	}
	if payload.Error.Message != "failed to read nvidia audio response" {
		t.Fatal("read error message changed")
	}
	assertNoLeak(t, rec.Body.String(), secrets)
	assertNoHeaderMaterial(t, rec.Body.String())
	logs := strings.Join(log.ConsoleLogs(), "\n")
	if !strings.Contains(logs, "provider=nvidia") || !strings.Contains(logs, "model=fastpitch/alloy") {
		t.Fatal("log missing real provider/model")
	}
	assertNoLeak(t, logs, secrets)
	assertNoHeaderMaterial(t, logs)
}

func TestNvidiaTTSShouldFallbackWritesJSONAndStops(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		wantCode string
		wantType string
	}{
		{name: "403", status: http.StatusForbidden, wantCode: "insufficient_quota", wantType: "permission_error"},
		{name: "429", status: http.StatusTooManyRequests, wantCode: "rate_limit_exceeded", wantType: "rate_limit_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const (
				apiKey   = "sk-nvidia-silent-key-should-not-leak"
				user     = "nvuser"
				pass     = "nv-silent-pass"
				queryKey = "sk-nvidia-silent-query-should-not-leak"
			)
			secrets := nvidiaSilentSecrets(apiKey, user, pass, queryKey)

			var firstHits, secondHits atomic.Int32
			bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				firstHits.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Api-Key", apiKey)
				w.Header().Set("Authorization", "Bearer "+apiKey)
				w.WriteHeader(tt.status)
				leaked := "http://" + user + ":" + pass + "@" + r.Host + r.RequestURI
				_, _ = w.Write([]byte("voice rejected " + apiKey + " " + leaked +
					"\nAuthorization: Bearer " + apiKey +
					"\nX-Api-Key: " + apiKey + "\n"))
			}))
			defer bad.Close()
			okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				secondHits.Add(1)
				w.Header().Set("Content-Type", "audio/wav")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("RIFF-should-not-be-used"))
			}))
			defer okSrv.Close()

			database, cleanup := setupEmbeddingsTestDB(t)
			defer cleanup()
			base := credentialedNvidiaURL(t, bad.URL, user, pass, queryKey)
			insertMediaConn(t, database, "nv-fb", "nvidia", apiKey, base, 1)
			insertMediaConn(t, database, "nv-fb-next", "nvidia", apiKey, okSrv.URL, 2)
			h := newTestMediaHandler(db.NewRepo(database))

			log.ClearConsoleLogs()
			rec := postNvidiaSpeech(h)

			if firstHits.Load() != 1 {
				t.Fatalf("first connection hits %d", firstHits.Load())
			}
			// ShouldFallback currently stops the loop after locking the model.
			// A healthy second connection must not be called.
			if secondHits.Load() != 0 {
				t.Fatalf("fallback status tried the next connection %d times", secondHits.Load())
			}
			payload := decodeAPIError(t, rec, secrets)
			if rec.Code != tt.status {
				t.Fatalf("status %d, want %d", rec.Code, tt.status)
			}
			if payload.Error.Code != tt.wantCode || payload.Error.Type != tt.wantType {
				t.Fatalf("error type/code = %s/%s", payload.Error.Type, payload.Error.Code)
			}
			if !strings.Contains(payload.Error.Message, "voice rejected") {
				t.Fatal("upstream detail was dropped")
			}
			assertNoLeak(t, rec.Body.String(), secrets)
			assertNoHeaderMaterial(t, rec.Body.String())
			logs := strings.Join(log.ConsoleLogs(), "\n")
			if !strings.Contains(logs, "provider=nvidia") || !strings.Contains(logs, "model=fastpitch/alloy") {
				t.Fatal("log missing real provider/model")
			}
			assertNoLeak(t, logs, secrets)
			assertNoHeaderMaterial(t, logs)
			assertModelLocked(t, database, "nv-fb")
		})
	}
}

func TestNvidiaTTSSilentPathSuccessStillReturnsAudio(t *testing.T) {
	const apiKey = "sk-nvidia-success-silent-key"
	var gotBody string
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		buf, _ := io.ReadAll(r.Body)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "audio/wav")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("RIFF-nvidia-ok"))
	}))
	defer srv.Close()

	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	insertMediaConn(t, database, "nv-ok", "nvidia", apiKey, srv.URL, 1)
	h := newTestMediaHandler(db.NewRepo(database))

	rec := postNvidiaSpeech(h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "audio/wav" {
		t.Fatalf("content-type %q", ct)
	}
	if rec.Body.String() != "RIFF-nvidia-ok" {
		t.Fatal("audio body mismatch")
	}
	if gotAuth != "Bearer "+apiKey {
		t.Fatal("upstream authorization did not match the connection key")
	}
	var payload struct {
		Input struct {
			Text string `json:"text"`
		} `json:"input"`
		Voice string `json:"voice"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("decode upstream body: %v", err)
	}
	if payload.Input.Text != "hello" || payload.Voice != "alloy" || payload.Model != "fastpitch" {
		t.Fatalf("upstream payload text=%q voice=%q model=%q", payload.Input.Text, payload.Voice, payload.Model)
	}
}

func TestNvidiaTTSRequestScopedStatusStillRotates(t *testing.T) {
	var firstHits, secondHits atomic.Int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstHits.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request body"))
	}))
	defer bad.Close()
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondHits.Add(1)
		w.Header().Set("Content-Type", "audio/wav")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("RIFF-rotated-ok"))
	}))
	defer okSrv.Close()

	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	insertMediaConn(t, database, "nv-400", "nvidia", "sk-nvidia-rotate-a", bad.URL, 1)
	insertMediaConn(t, database, "nv-400-next", "nvidia", "sk-nvidia-rotate-b", okSrv.URL, 2)
	h := newTestMediaHandler(db.NewRepo(database))

	rec := postNvidiaSpeech(h)
	if firstHits.Load() != 1 || secondHits.Load() != 1 {
		t.Fatalf("hits first=%d second=%d", firstHits.Load(), secondHits.Load())
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "audio/wav" {
		t.Fatalf("content-type %q", ct)
	}
	if rec.Body.String() != "RIFF-rotated-ok" {
		t.Fatal("rotated audio body mismatch")
	}
}

func postNvidiaSpeech(h *MediaHandler) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"nvidia/fastpitch/alloy","input":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	h.HandleAudioSpeech(rec, req)
	return rec
}

func insertMediaConn(t *testing.T, database *sql.DB, id, provider, apiKey, baseURL string, priority int) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"apiKey":  apiKey,
		"baseUrl": baseURL,
	})
	if err != nil {
		t.Fatalf("marshal conn data: %v", err)
	}
	_, err = database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		(?, ?, 'apikey', ?, ?, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
		id, provider, provider+" "+id, priority, string(raw))
	if err != nil {
		t.Fatalf("seed connection %s: %v", id, err)
	}
}

func credentialedNvidiaURL(t *testing.T, rawBase, user, pass, queryKey string) string {
	t.Helper()
	u, err := url.Parse(rawBase)
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}
	return "http://" + user + ":" + pass + "@" + u.Host + "/v1/audio/speech?api_key=" + queryKey
}

type apiErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

func decodeAPIError(t *testing.T, rec *httptest.ResponseRecorder, secrets []string) apiErrorBody {
	t.Helper()
	if rec.Code == http.StatusOK {
		t.Fatalf("status 200 body %s", redactListed(rec.Body.String(), secrets))
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type %q body %s", ct, redactListed(rec.Body.String(), secrets))
	}
	var payload apiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if payload.Error.Code == "" || payload.Error.Message == "" || payload.Error.Type == "" {
		t.Fatal("error envelope is missing code, message, or type")
	}
	return payload
}

func nvidiaSilentSecrets(apiKey, user, pass, queryKey string) []string {
	return []string{apiKey, pass, queryKey, user + ":" + pass, user + ":", "api_key"}
}

func assertNoLeak(t *testing.T, text string, secrets []string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatalf("leaked credential material in %s", redactListed(text, secrets))
		}
	}
}

func assertNoHeaderMaterial(t *testing.T, text string) {
	t.Helper()
	lower := strings.ToLower(text)
	for _, marker := range []string{"authorization:", "x-api-key:", "proxy-authorization:", "cookie:", "set-cookie:"} {
		if strings.Contains(lower, marker) {
			t.Fatalf("message contains header %s", marker)
		}
	}
}

func assertModelLocked(t *testing.T, database *sql.DB, id string) {
	t.Helper()
	var raw string
	if err := database.QueryRow(`SELECT data FROM providerConnections WHERE id = ?`, id).Scan(&raw); err != nil {
		t.Fatalf("read connection: %v", err)
	}
	if !strings.Contains(raw, "modelLock_") {
		t.Fatal("expected the failing connection to stay model-locked")
	}
}

func redactListed(text string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	return text
}
