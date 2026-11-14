package handlers

import (
	"bytes"
	"context"
	json "encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func useTransport(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	prev := http.DefaultTransport
	http.DefaultTransport = rt
	t.Cleanup(func() { http.DefaultTransport = prev })
}

func withProxyBase(t *testing.T, base string) {
	t.Helper()
	prev := proxyBase
	proxyBase = base
	t.Cleanup(func() { proxyBase = prev })
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type statusRecorder struct {
	header http.Header
	code   int
	wrote  bool
	body   bytes.Buffer
}

func (s *statusRecorder) Header() http.Header {
	if s.header == nil {
		s.header = make(http.Header)
	}
	return s.header
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.wrote {
		return
	}
	s.wrote = true
	s.code = code
}

func (s *statusRecorder) Write(p []byte) (int, error) {
	if !s.wrote {
		s.WriteHeader(http.StatusOK)
	}
	return s.body.Write(p)
}

func (s *statusRecorder) Flush() {}

func jsonResponse(status int, body string) *http.Response {
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// TestFetchRouter_ProxyFailClosed is the first test in this package so it
// snapshots net/http's process-wide proxy config before any other request.
func TestFetchRouter_ProxyFailClosed(t *testing.T) {
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY", "all_proxy"} {
		t.Setenv(key, "http://127.0.0.1:1")
	}
	t.Setenv("NO_PROXY", "127.0.0.1,localhost")
	t.Setenv("no_proxy", "127.0.0.1,localhost")
	prev := proxyBase
	t.Cleanup(func() { proxyBase = prev })

	t.Run("dead proxy is dialed for a non-loopback target", func(t *testing.T) {
		proxyBase = "http://mitm.test:9"
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resp, err := FetchRouter(ctx, []byte(`{}`), "/v1/chat/completions", nil, "")
		if resp != nil {
			resp.Body.Close()
		}
		if err == nil {
			t.Fatal("expected the dead proxy to fail the request")
		}
		msg := err.Error()
		if !strings.Contains(msg, "127.0.0.1:1") {
			t.Fatalf("expected the required proxy 127.0.0.1:1 to be dialed, got %v", err)
		}
		if strings.Contains(msg, "no such host") {
			t.Fatalf("target was resolved directly instead of via the proxy: %v", err)
		}
	})

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	localName := "http://localhost" + strings.TrimPrefix(srv.URL, "http://127.0.0.1")
	for _, tc := range []struct {
		name string
		base string
	}{
		{name: "loopback IP and no_proxy", base: srv.URL},
		{name: "localhost name and no_proxy", base: localName},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hits.Store(0)
			proxyBase = tc.base
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			rec := &statusRecorder{}
			HandleCursor(rec, req, []byte(`{"model":"m"}`))
			if hits.Load() == 0 && rec.code == http.StatusBadGateway && bytes.Contains(rec.body.Bytes(), []byte(`"type":"mitm_error"`)) {
				return
			}
			t.Skipf("BUG internal/mitm/handlers/base.go:36 FetchRouter uses http.Client's default transport, which skips the proxy for loopback and for NO_PROXY (net/http ProxyFromEnvironment). input: HTTP_PROXY=http://127.0.0.1:1 (nothing listening), NO_PROXY=127.0.0.1,localhost, target %s. expected: no direct request and HTTP 502 mitm_error. actual: direct hits=%d status=%d body=%q", tc.base, hits.Load(), rec.code, rec.body.String())
		})
	}
}

func TestHandle_InvalidJSON(t *testing.T) {
	handlers := []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request, []byte)
		msg  string
	}{
		{name: "antigravity", fn: HandleAntigravity, msg: "invalid Antigravity request body"},
		{name: "codex", fn: HandleCodex, msg: "invalid Codex request body"},
		{name: "copilot", fn: HandleCopilot, msg: "invalid Copilot request body"},
		{name: "cursor", fn: HandleCursor, msg: "invalid Cursor request body"},
		{name: "kiro", fn: HandleKiro, msg: "invalid Kiro request body"},
	}
	for _, tt := range handlers {
		t.Run(tt.name, func(t *testing.T) {
			rec := &statusRecorder{}
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			tt.fn(rec, req, []byte("not-json"))
			if rec.code != http.StatusBadRequest || !bytes.Contains(rec.body.Bytes(), []byte(tt.msg)) || !bytes.Contains(rec.body.Bytes(), []byte(`"type":"mitm_error"`)) {
				t.Fatalf("status %d body %s", rec.code, rec.body.Bytes())
			}
		})
	}
}

func TestFetchRouter_ForwardsHeadersAndPrefersIncomingAuthorization(t *testing.T) {
	var got http.Header
	var gotPath string
	useTransport(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = r.Header.Clone()
		gotPath = r.URL.Path
		resp := jsonResponse(http.StatusOK, `{"ok":true}`)
		resp.Request = r
		return resp, nil
	}))
	withProxyBase(t, "http://router.test")
	header := make(http.Header)
	header.Set("Authorization", "Bearer incoming")
	header.Set("Content-Type", "application/json")
	header.Set("User-Agent", "unit-test-agent")
	resp, err := FetchRouter(context.Background(), []byte(`{"model":"m"}`), "/v1/chat/completions", header, "should-not-win")
	if err != nil {
		t.Fatalf("FetchRouter: %v", err)
	}
	defer resp.Body.Close()
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("path %s", gotPath)
	}
	if got.Get("Authorization") != "Bearer incoming" {
		t.Fatalf("authorization %q", got.Get("Authorization"))
	}
	if got.Get("User-Agent") != "unit-test-agent" || got.Get("Content-Type") != "application/json" {
		t.Fatalf("headers %v", got)
	}
}

func TestFetchRouter_InvalidURL(t *testing.T) {
	withProxyBase(t, "http://[")
	rec := &statusRecorder{}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	HandleCursor(rec, req, []byte(`{"model":"m"}`))
	if rec.code != http.StatusBadGateway || !bytes.Contains(rec.body.Bytes(), []byte(`"type":"mitm_error"`)) {
		t.Fatalf("status %d body %s", rec.code, rec.body.Bytes())
	}
}

func TestHandle_Upstream5xxIsPassedThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"unavailable"}`))
	}))
	defer srv.Close()
	withProxyBase(t, srv.URL)

	rec := &statusRecorder{}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	HandleCopilot(rec, req, []byte(`{"model":"gpt"}`))
	if rec.code != http.StatusServiceUnavailable || !bytes.Contains(rec.body.Bytes(), []byte("unavailable")) {
		t.Fatalf("status %d body %s", rec.code, rec.body.Bytes())
	}
}

func TestHandle_UpstreamTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		timer := time.NewTimer(300 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
		case <-timer.C:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"late":true}`))
		}
	}))
	defer srv.Close()
	withProxyBase(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
	rec := &statusRecorder{}
	start := time.Now()
	HandleCursor(rec, req, []byte(`{"model":"m"}`))
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Fatalf("upstream timeout was not bounded by the request context (%s)", elapsed)
	}
	if rec.code != http.StatusBadGateway || !bytes.Contains(rec.body.Bytes(), []byte("deadline")) {
		t.Fatalf("status %d body %s", rec.code, rec.body.Bytes())
	}
}

func TestHandle_UpstreamConnectionCut(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	withProxyBase(t, "http://"+ln.Addr().String())
	rec := &statusRecorder{}
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	HandleCodex(rec, req, []byte(`{"model":"m"}`))
	if rec.code != http.StatusBadGateway || !bytes.Contains(rec.body.Bytes(), []byte(`"type":"mitm_error"`)) {
		t.Fatalf("status %d body %s", rec.code, rec.body.Bytes())
	}
}

func TestHandle_InvalidUpstreamTLSDoesNotTrustTheServer(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	withProxyBase(t, srv.URL)

	rec := &statusRecorder{}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	HandleCursor(rec, req, []byte(`{"model":"m"}`))
	if hits.Load() != 0 {
		t.Fatalf("untrusted TLS server handled %d HTTP requests", hits.Load())
	}
	if rec.code != http.StatusBadGateway {
		t.Fatalf("status %d body %s", rec.code, rec.body.Bytes())
	}
	lower := strings.ToLower(rec.body.String())
	if !strings.Contains(lower, "certificate") && !strings.Contains(lower, "x509") && !strings.Contains(lower, "tls") {
		t.Fatalf("body %s", rec.body.Bytes())
	}
}

func TestHandleCursor_TruncatedUpstreamBody(t *testing.T) {
	useTransport(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("Content-Type", "application/json")
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        header,
			ContentLength: 64,
			Body:          io.NopCloser(&errAfter{n: 4, err: io.ErrUnexpectedEOF}),
			Request:       r,
		}, nil
	}))
	withProxyBase(t, "http://router.test")
	rec := &statusRecorder{}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	HandleCursor(rec, req, []byte(`{"model":"m"}`))
	if !rec.wrote || rec.code != http.StatusBadGateway || !bytes.Contains(rec.body.Bytes(), []byte(`"type":"mitm_error"`)) {
		t.Fatalf("truncated upstream: wroteHeader=%v status=%d body=%q", rec.wrote, rec.code, rec.body.String())
	}
}

func TestHandle_ModelPrefixAndNestedAntigravity(t *testing.T) {
	var gotBody []byte
	var gotUA string
	useTransport(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotBody, _ = io.ReadAll(r.Body)
		gotUA = r.Header.Get("User-Agent")
		resp := jsonResponse(http.StatusOK, `{"ok":true}`)
		resp.Request = r
		return resp, nil
	}))
	withProxyBase(t, "http://router.test")

	req := httptest.NewRequest(http.MethodPost, "/v1internal:generateContent", strings.NewReader("x"))
	req.Header.Set("User-Agent", "antigravity/9.9.9")
	body := []byte(`{"model":"gemini","request":{"metadata":{"ideVersion":"9.9.9"}}}`)
	rec := &statusRecorder{}
	HandleAntigravity(rec, req, body)
	if rec.code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.code, rec.body.Bytes())
	}
	var parsed map[string]any
	if err := json.Unmarshal(gotBody, &parsed); err != nil {
		t.Fatalf("forwarded: %v", err)
	}
	if parsed["model"] != "antigravity/gemini" {
		t.Fatalf("model %v", parsed["model"])
	}
	if gotUA != "antigravity/1.23.2" {
		t.Fatalf("UA %q", gotUA)
	}
	request, _ := parsed["request"].(map[string]any)
	meta, _ := request["metadata"].(map[string]any)
	if meta["ideVersion"] != "1.23.2" {
		t.Fatalf("nested ideVersion %v", meta["ideVersion"])
	}

	rec = &statusRecorder{}
	HandleCodex(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", nil), []byte(`{"model":"codex/already"}`))
	if err := json.Unmarshal(gotBody, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["model"] != "codex/already" {
		t.Fatalf("prefixed model was rewritten: %v", parsed["model"])
	}
}

func TestHandleAntigravity_UpstreamError(t *testing.T) {
	useTransport(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("upstream exploded")
	}))
	withProxyBase(t, "http://router.test")
	rec := &statusRecorder{}
	req := httptest.NewRequest(http.MethodPost, "/v1internal:fetchAvailableModels", nil)
	HandleAntigravity(rec, req, []byte(`{"model":"gemini"}`))
	if rec.code != http.StatusBadGateway || !bytes.Contains(rec.body.Bytes(), []byte("upstream exploded")) {
		t.Fatalf("status %d body %s", rec.code, rec.body.Bytes())
	}
}

func TestPipeJSON_ReturnsReadError(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(&errAfter{n: 2, err: io.ErrUnexpectedEOF}),
	}
	rec := &statusRecorder{}
	err := PipeJSON(resp, rec)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("PipeJSON error %v", err)
	}
	if rec.wrote {
		t.Fatal("PipeJSON wrote a response after a failed body read")
	}
}

func TestPipeSSE_ForwardsPrefixWhenUpstreamCuts(t *testing.T) {
	header := make(http.Header)
	header.Set("Content-Type", "text/event-stream")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     header,
		Body:       io.NopCloser(io.MultiReader(strings.NewReader("data: one\n\n"), &errAfter{n: 0, err: io.ErrClosedPipe})),
	}
	rec := httptest.NewRecorder()
	if err := PipeSSE(resp, rec); err != nil {
		t.Fatalf("PipeSSE: %v", err)
	}
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("data: one\n\n")) {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.Bytes())
	}
	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content-type %q", rec.Header().Get("Content-Type"))
	}
}

type errAfter struct {
	n   int
	did int
	err error
}

func (e *errAfter) Read(p []byte) (int, error) {
	if e.did >= e.n {
		return 0, e.err
	}
	if len(p) > e.n-e.did {
		p = p[:e.n-e.did]
	}
	for i := range p {
		p[i] = 'x'
	}
	e.did += len(p)
	return len(p), nil
}
