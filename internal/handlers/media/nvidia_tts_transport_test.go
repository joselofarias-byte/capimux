package media

import (
	"database/sql"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"9router/proxy/internal/db"
	"9router/proxy/internal/log"
)

// Brought from cursor/media-test-coverage-8611 (PR #193) coverage_helpers_test.go.
// Only the helper this transport test needs.
func seedConn194(t *testing.T, database *sql.DB, id, provider, apiKey, baseURL string, priority int) {
	t.Helper()
	data := map[string]any{}
	if apiKey != "" {
		data["apiKey"] = apiKey
	}
	if baseURL != "" {
		data["baseUrl"] = baseURL
	}
	raw, err := json.Marshal(data)
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

// TestNvidiaTTSTransportFailureReturns502 is TestNvidiaTTSTransportFailureIsSilent
// from PR #193, without the skip. A closed loopback port must surface as 502
// JSON, and the body and logs must not carry the connection URL's userinfo,
// query key, or the stored API key.
func TestNvidiaTTSTransportFailureReturns502(t *testing.T) {
	const (
		apiKey   = "sk-nvidia-conn-key-should-not-leak"
		userinfo = "nv-secret-pass"
		queryKey = "sk-nvidia-query-key-should-not-leak"
	)
	secrets := []string{apiKey, userinfo, queryKey, "nvuser:" + userinfo}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	baseURL := "http://nvuser:" + userinfo + "@" + addr + "/v1/audio/speech?api_key=" + queryKey
	seedConn194(t, database, "nv-down", "nvidia", apiKey, baseURL, 1)
	h := newTestMediaHandler(db.NewRepo(database))

	log.ClearConsoleLogs()
	rec := httptest.NewRecorder()
	h.HandleAudioSpeech(rec, httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"nvidia/fastpitch/alloy","input":"hello"}`)))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type %q", ct)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("empty error body")
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if payload.Error.Message == "" {
		t.Fatal("error.message is empty")
	}
	if payload.Error.Type != "server_error" || payload.Error.Code != "bad_gateway" {
		t.Fatalf("error type/code = %s/%s", payload.Error.Type, payload.Error.Code)
	}
	assertNoSecrets(t, rec.Body.String(), secrets)

	logs := strings.Join(log.ConsoleLogs(), "\n")
	if !strings.Contains(logs, "provider=nvidia") || !strings.Contains(logs, "model=fastpitch/alloy") {
		t.Fatalf("log missing real provider/model: %s", redactSecrets(logs, secrets))
	}
	assertNoSecrets(t, logs, secrets)
}

func TestNvidiaTTSSuccessStillReturnsAudio(t *testing.T) {
	const apiKey = "sk-nvidia-success-key-not-logged"
	var gotBody string
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "audio/wav")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("RIFF-nvidia-ok"))
	}))
	defer srv.Close()

	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	seedConn194(t, database, "nv-ok", "nvidia", apiKey, srv.URL, 1)
	h := newTestMediaHandler(db.NewRepo(database))

	rec := httptest.NewRecorder()
	h.HandleAudioSpeech(rec, httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"nvidia/fastpitch/alloy","input":"hello"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "audio/wav" {
		t.Fatalf("content-type %q", ct)
	}
	if rec.Body.String() != "RIFF-nvidia-ok" {
		t.Fatalf("audio body %q", rec.Body.String())
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

func TestNvidiaTTSTransportMessageDropsCredentials(t *testing.T) {
	const secret = "nv-secret-pass"
	const queryKey = "sk-nvidia-query-key-should-not-leak"
	rawURL := "http://nvuser:" + secret + "@127.0.0.1:9/v1/audio/speech?api_key=" + queryKey
	cause := errors.New("dial tcp 127.0.0.1:9: connect: connection refused")
	got := nvidiaTTSTransportMessage(&url.Error{Op: "Post", URL: rawURL, Err: cause})
	if strings.Contains(got, secret) || strings.Contains(got, queryKey) || strings.Contains(got, "api_key") {
		t.Fatalf("message leaked url material: %s", redactSecrets(got, []string{secret, queryKey}))
	}
	if !strings.Contains(got, "connection refused") || !strings.Contains(got, "nvidia tts request failed") {
		t.Fatalf("message %q", got)
	}

	plain := nvidiaTTSTransportMessage(fmt.Errorf("Get %q: bearer %s", rawURL, queryKey))
	if strings.Contains(plain, secret) || strings.Contains(plain, queryKey) || strings.Contains(plain, "api_key=") {
		t.Fatalf("plain message leaked url material: %s", redactSecrets(plain, []string{secret, queryKey}))
	}
}

func assertNoSecrets(t *testing.T, text string, secrets []string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatalf("response leaked credential material: %s", redactSecrets(text, secrets))
		}
	}
}

func redactSecrets(text string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[redacted]")
		}
	}
	return text
}
