package handlers

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
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

func truncatedRoundTrip(r *http.Request) (*http.Response, error) {
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        header,
		ContentLength: 64,
		Body:          io.NopCloser(&errAfter{n: 4, err: io.ErrUnexpectedEOF}),
		Request:       r,
	}, nil
}

const wantTruncatedJSON = `{"error":{"message":"upstream response truncated","type":"mitm_error","code":502}}`

// TestHandleCursor_TruncatedUpstreamBody was ported from
// cursor/mitm-intercept-coverage-65e0. The skip that documented the bug is
// now a hard failure: a short upstream read must be HTTP 502 mitm_error.
func TestHandleCursor_TruncatedUpstreamBody(t *testing.T) {
	useTransport(t, roundTripFunc(truncatedRoundTrip))
	withProxyBase(t, "http://router.test")
	rec := &statusRecorder{}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	HandleCursor(rec, req, []byte(`{"model":"m"}`))
	if !rec.wrote || rec.code != http.StatusBadGateway || !bytes.Contains(rec.body.Bytes(), []byte(`"type":"mitm_error"`)) {
		t.Fatalf("wroteHeader=%v status=%d body=%q", rec.wrote, rec.code, rec.body.String())
	}
	if bytes.Contains(rec.body.Bytes(), []byte("xxxx")) {
		t.Fatalf("partial upstream body was forwarded: %q", rec.body.String())
	}
}

func TestHandle_TruncatedUpstreamBody(t *testing.T) {
	handlers := []struct {
		name   string
		fn     func(http.ResponseWriter, *http.Request, []byte)
		target string
	}{
		{name: "cursor", fn: HandleCursor, target: "/v1/chat/completions"},
		{name: "codex", fn: HandleCodex, target: "/v1/responses"},
		{name: "copilot", fn: HandleCopilot, target: "/v1/chat/completions"},
		{name: "kiro", fn: HandleKiro, target: "/v1/chat/completions"},
		{name: "antigravity", fn: HandleAntigravity, target: "/v1internal:fetchAvailableModels"},
	}
	for _, tt := range handlers {
		t.Run(tt.name, func(t *testing.T) {
			useTransport(t, roundTripFunc(truncatedRoundTrip))
			withProxyBase(t, "http://router.test")
			rec := &statusRecorder{}
			req := httptest.NewRequest(http.MethodPost, tt.target, nil)
			req.Header.Set("Authorization", "Bearer super-secret-token-value")
			tt.fn(rec, req, []byte(`{"model":"m","password":"p@ss-not-a-key"}`))
			if rec.code != http.StatusBadGateway || rec.body.String() != wantTruncatedJSON {
				t.Fatalf("status %d body %q", rec.code, rec.body.String())
			}
			if bytes.Contains(rec.body.Bytes(), []byte("p@ss-not-a-key")) || bytes.Contains(rec.body.Bytes(), []byte("super-secret-token-value")) || bytes.Contains(rec.body.Bytes(), []byte("xxxx")) {
				t.Fatal("502 body leaked the request or the partial upstream body")
			}
		})
	}
}

func TestHandle_CompleteJSONBodyUnchanged(t *testing.T) {
	const payload = `{"id":"resp-1","ok":true}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(srv.Close)
	withProxyBase(t, srv.URL)

	handlers := []struct {
		name   string
		fn     func(http.ResponseWriter, *http.Request, []byte)
		target string
	}{
		{name: "cursor", fn: HandleCursor, target: "/v1/chat/completions"},
		{name: "codex", fn: HandleCodex, target: "/v1/responses"},
		{name: "copilot", fn: HandleCopilot, target: "/v1/chat/completions"},
		{name: "kiro", fn: HandleKiro, target: "/v1/chat/completions"},
		{name: "antigravity", fn: HandleAntigravity, target: "/v1internal:fetchAvailableModels"},
	}
	for _, tt := range handlers {
		t.Run(tt.name, func(t *testing.T) {
			rec := &statusRecorder{}
			req := httptest.NewRequest(http.MethodPost, tt.target, nil)
			tt.fn(rec, req, []byte(`{"model":"m"}`))
			if rec.code != http.StatusOK || rec.body.String() != payload {
				t.Fatalf("status %d body %q", rec.code, rec.body.String())
			}
		})
	}

	t.Run("cursor passes through a complete 503", func(t *testing.T) {
		const denied = `{"error":"unavailable"}`
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(denied))
		}))
		t.Cleanup(upstream.Close)
		withProxyBase(t, upstream.URL)
		rec := &statusRecorder{}
		HandleCursor(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil), []byte(`{"model":"m"}`))
		if rec.code != http.StatusServiceUnavailable || rec.body.String() != denied {
			t.Fatalf("status %d body %q", rec.code, rec.body.String())
		}
	})
}
