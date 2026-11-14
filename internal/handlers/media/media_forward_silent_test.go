package media

import (
	"database/sql"
	json "encoding/json/v2"
	"errors"
	"io"
	stdlog "log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"9router/proxy/internal/db"
	"9router/proxy/internal/log"
)

const (
	leakUser   = "leakuser"
	leakPass   = "leakpass"
	leakQuery  = "leakqueryvalue"
	leakKey    = "media-fwd-secret-key"
	leakCookie = "leakcookievalue"
	secondKey  = "media-fwd-second-key"
)

func leakSecrets() []string {
	return []string{leakUser, leakPass, leakQuery, leakKey, leakCookie, "api_key="}
}

func poisonousBase(invalid bool) string {
	path := "/v1"
	if invalid {
		path = "/%zz"
	}
	return "http://" + leakUser + ":" + leakPass + "@media-forward.invalid" + path + "?api_key=" + leakQuery
}

func seedMediaConn(t *testing.T, database *sql.DB, id string, priority int, apiKey, baseURL string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"apiKey": apiKey, "baseUrl": baseURL})
	if err != nil {
		t.Fatalf("marshal connection: %v", err)
	}
	_, err = database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		(?, 'openai', 'apikey', ?, ?, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
		id, id, priority, string(data))
	if err != nil {
		t.Fatalf("seed connection: %v", err)
	}
}

func beginMediaLogs(t *testing.T) {
	t.Helper()
	prevLevel := log.GetLevel()
	prevOut := stdlog.Default().Writer()
	log.SetLevel(log.LevelInfo)
	stdlog.SetOutput(io.Discard)
	log.ClearConsoleLogs()
	t.Cleanup(func() {
		log.SetLevel(prevLevel)
		stdlog.SetOutput(prevOut)
	})
}

func capturedLogs() string {
	return strings.Join(log.ConsoleLogs(), "\n")
}

func assertNoLeak196(t *testing.T, label, text string) {
	t.Helper()
	for _, secret := range leakSecrets() {
		if secret != "" && strings.Contains(text, secret) {
			t.Fatalf("%s contiene material del fixture", label)
		}
	}
}

func useTransport(t *testing.T, h *MediaHandler, rt http.RoundTripper) {
	t.Helper()
	client := &http.Client{Transport: rt}
	h.ChatH.Client = client
	prev := directHTTPClient
	directHTTPClient = client
	t.Cleanup(func() { directHTTPClient = prev })
}

func postImages(h *MediaHandler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"openai/dall-e-3","prompt":"a cat"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+leakKey)
	req.Header.Set("X-Api-Key", leakKey)
	req.Header.Set("Cookie", "session="+leakCookie)
	rec := httptest.NewRecorder()
	h.HandleImages(rec, req)
	return rec
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func leakDialErr(r *http.Request) error {
	return errors.New("dial " + r.URL.String() +
		" Authorization: " + r.Header.Get("Authorization") +
		" X-Api-Key: " + r.Header.Get("X-Api-Key") +
		" Cookie: " + r.Header.Get("Cookie"))
}

type errReadCloser struct{ msg string }

func (e errReadCloser) Read([]byte) (int, error) { return 0, errors.New(e.msg) }
func (e errReadCloser) Close() error             { return nil }

func readErrMessage(r *http.Request) string {
	return "read " + r.URL.String() + " Authorization: " + r.Header.Get("Authorization") + " Cookie: " + r.Header.Get("Cookie")
}

func assertBadGateway(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, cuerpo %d bytes", rec.Code, rec.Body.Len())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type %q", ct)
	}
	if rec.Header().Get("X-Api-Key") != "" || rec.Header().Get("Set-Cookie") != "" {
		t.Fatal("la respuesta de error reenvió una cabecera sensible")
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("json: %v", err)
	}
	if env.Error.Code != "bad_gateway" || env.Error.Type != "server_error" {
		t.Fatalf("code=%s type=%s", env.Error.Code, env.Error.Type)
	}
	if env.Error.Message == "" {
		t.Fatal("mensaje vacío")
	}
	assertNoLeak196(t, "cuerpo", rec.Body.String())
	return env.Error.Message
}

func openMediaDB(t *testing.T) *sql.DB {
	t.Helper()
	database, cleanup := setupMultimodalTestDB(t)
	t.Cleanup(cleanup)
	return database
}

func handlerWithTransport(t *testing.T, database *sql.DB, rt http.RoundTripper) *MediaHandler {
	t.Helper()
	h := newTestMediaHandler(db.NewRepo(database))
	if rt != nil {
		useTransport(t, h, rt)
	}
	return h
}

func TestMediaSafeTextDropsURLSecrets(t *testing.T) {
	raw := "dial " + poisonousBase(false) + " Authorization: Bearer " + leakKey + " X-Api-Key: " + leakKey + " Cookie: session=" + leakCookie
	assertNoLeak196(t, "texto", mediaSafeText(raw))

	_, err := http.NewRequest(http.MethodPost, poisonousBase(true)+"/v1/images/generations", nil)
	if err == nil {
		t.Fatal("la URL inválida debía fallar al armar el request")
	}
	assertNoLeak196(t, "error de parseo", mediaSafeErr(err))
	if mediaSafeURL(poisonousBase(false)) != "http://media-forward.invalid/v1" {
		t.Fatal("la URL logueada conservó userinfo o query")
	}
	if mediaSafeURL(poisonousBase(true)) != "" {
		t.Fatal("una URL inválida no debe loguearse cruda")
	}
}

func TestMediaForwardCreateErrorReturnsJSON(t *testing.T) {
	beginMediaLogs(t)
	database := openMediaDB(t)
	seedMediaConn(t, database, "conn-first", 1, leakKey, poisonousBase(true))
	h := handlerWithTransport(t, database, nil)

	rec := postImages(h)
	msg := assertBadGateway(t, rec)
	if msg != "failed to create upstream request" {
		t.Fatalf("mensaje inesperado, len=%d", len(msg))
	}
	logs := capturedLogs()
	assertNoLeak196(t, "log", logs)
	if !strings.Contains(logs, "provider=openai") || !strings.Contains(logs, "model=dall-e-3") {
		t.Fatal("el log no nombra el proveedor y el modelo resueltos")
	}
}

func TestMediaForwardTransportErrorReturnsJSON(t *testing.T) {
	beginMediaLogs(t)
	database := openMediaDB(t)
	seedMediaConn(t, database, "conn-first", 1, leakKey, poisonousBase(false))
	var calls int
	h := handlerWithTransport(t, database, rtFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, leakDialErr(r)
	}))

	rec := postImages(h)
	msg := assertBadGateway(t, rec)
	if msg != "upstream request failed" {
		t.Fatalf("mensaje inesperado, len=%d", len(msg))
	}
	if calls == 0 {
		t.Fatal("el transporte falso no recibió el request")
	}
	logs := capturedLogs()
	assertNoLeak196(t, "log", logs)
	if !strings.Contains(logs, "provider=openai") || !strings.Contains(logs, "model=dall-e-3") {
		t.Fatal("el log no nombra el proveedor y el modelo resueltos")
	}
}

func TestMediaForwardReadErrorReturnsJSON(t *testing.T) {
	beginMediaLogs(t)
	database := openMediaDB(t)
	seedMediaConn(t, database, "conn-first", 1, leakKey, poisonousBase(false))
	h := handlerWithTransport(t, database, rtFunc(func(r *http.Request) (*http.Response, error) {
		hdr := make(http.Header)
		hdr.Set("Content-Type", "text/plain")
		hdr.Set("X-Api-Key", leakKey)
		hdr.Set("Set-Cookie", "session="+leakCookie)
		return &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			Header:        hdr,
			Body:          errReadCloser{msg: readErrMessage(r)},
			ContentLength: 64,
		}, nil
	}))

	rec := postImages(h)
	msg := assertBadGateway(t, rec)
	if msg != "failed to read upstream response" {
		t.Fatalf("mensaje inesperado, len=%d", len(msg))
	}
	logs := capturedLogs()
	assertNoLeak196(t, "log", logs)
	if !strings.Contains(logs, "provider=openai") || !strings.Contains(logs, "model=dall-e-3") {
		t.Fatal("el log no nombra el proveedor y el modelo resueltos")
	}
}

func TestMediaForwardFallbackStatusStaysUpstream(t *testing.T) {
	beginMediaLogs(t)
	database := openMediaDB(t)
	base := poisonousBase(false)
	seedMediaConn(t, database, "conn-first", 1, leakKey, base)
	seedMediaConn(t, database, "conn-second", 2, secondKey, base)
	var mu sync.Mutex
	hits := map[string]int{}
	const upstreamBody = `{"error":"rate limited"}`
	h := handlerWithTransport(t, database, rtFunc(func(r *http.Request) (*http.Response, error) {
		key := r.Header.Get("Authorization")
		mu.Lock()
		hits[key]++
		mu.Unlock()
		hdr := make(http.Header)
		hdr.Set("Content-Type", "application/json")
		if key == "Bearer "+secondKey {
			return &http.Response{
				StatusCode:    http.StatusOK,
				Status:        "200 OK",
				Header:        hdr,
				Body:          io.NopCloser(strings.NewReader(`{"created":1,"data":[{"url":"https://img.example/second.png"}]}`)),
				ContentLength: -1,
			}, nil
		}
		return &http.Response{
			StatusCode:    http.StatusTooManyRequests,
			Status:        "429 Too Many Requests",
			Header:        hdr,
			Body:          io.NopCloser(strings.NewReader(upstreamBody)),
			ContentLength: int64(len(upstreamBody)),
		}, nil
	}))

	rec := postImages(h)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, cuerpo %d bytes", rec.Code, rec.Body.Len())
	}
	if rec.Body.String() != upstreamBody {
		t.Fatalf("el cuerpo no es el del upstream (len=%d)", rec.Body.Len())
	}
	mu.Lock()
	firstHits := hits["Bearer "+leakKey]
	secondHits := hits["Bearer "+secondKey]
	mu.Unlock()
	if firstHits != 1 || secondHits != 0 {
		t.Fatalf("primera=%d segunda=%d", firstHits, secondHits)
	}
	var raw string
	if err := database.QueryRow(`SELECT data FROM providerConnections WHERE id = ?`, "conn-first").Scan(&raw); err != nil {
		t.Fatalf("leer conexión: %v", err)
	}
	if strings.Contains(raw, "modelLock_") || strings.Contains(raw, "rateLimitedUntil") {
		t.Fatal("el forwarder bloqueó la conexión")
	}
	assertNoLeak196(t, "cuerpo", rec.Body.String())
	logs := capturedLogs()
	assertNoLeak196(t, "log", logs)
	if !strings.Contains(logs, "provider=openai") || !strings.Contains(logs, "model=dall-e-3") {
		t.Fatal("el log no nombra el proveedor y el modelo resueltos")
	}
}

func TestMediaForwardSuccessReturnsBody(t *testing.T) {
	const want = `{"created":1,"data":[{"url":"https://img.example/cat.png"}]}`
	var gotModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.UnmarshalRead(r.Body, &body)
		gotModel = body.Model
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(want))
	}))
	t.Cleanup(upstream.Close)

	database := openMediaDB(t)
	seedMediaConn(t, database, "conn-first", 1, leakKey, upstream.URL)
	h := handlerWithTransport(t, database, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"openai/dall-e-3","prompt":"a cat"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleImages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, cuerpo %d bytes", rec.Code, rec.Body.Len())
	}
	if rec.Body.String() != want {
		t.Fatalf("cuerpo len=%d, se esperaban %d bytes", rec.Body.Len(), len(want))
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type %q", ct)
	}
	if gotModel != "dall-e-3" {
		t.Fatalf("el upstream recibió el modelo %q", gotModel)
	}
}

func TestMediaForwardSuccessWithSecretURLKeepsBody(t *testing.T) {
	beginMediaLogs(t)
	const want = `{"created":1,"data":[{"url":"https://img.example/cat.png"}]}`
	database := openMediaDB(t)
	seedMediaConn(t, database, "conn-first", 1, leakKey, poisonousBase(false))
	var gotModel string
	h := handlerWithTransport(t, database, rtFunc(func(r *http.Request) (*http.Response, error) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.UnmarshalRead(r.Body, &body)
		gotModel = body.Model
		hdr := make(http.Header)
		hdr.Set("Content-Type", "application/json")
		return &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			Header:        hdr,
			Body:          io.NopCloser(strings.NewReader(want)),
			ContentLength: int64(len(want)),
		}, nil
	}))

	rec := postImages(h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, cuerpo %d bytes", rec.Code, rec.Body.Len())
	}
	if rec.Body.String() != want {
		t.Fatalf("cuerpo len=%d, se esperaban %d bytes", rec.Body.Len(), len(want))
	}
	if gotModel != "dall-e-3" {
		t.Fatal("el upstream no recibió el modelo resuelto")
	}
	assertNoLeak196(t, "cuerpo", rec.Body.String())
	assertNoLeak196(t, "log", capturedLogs())
}
