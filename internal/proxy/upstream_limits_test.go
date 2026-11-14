package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/providers"
)

func TestDoRequestUpstreamFailures(t *testing.T) {
	blockDirect(t)
	cases := []struct {
		name   string
		status int
		body   string
		cut    bool
	}{
		{name: "500", status: http.StatusInternalServerError, body: `{"error":"boom"}`},
		{name: "502", status: http.StatusBadGateway, body: `{"error":"gateway"}`},
		{name: "503", status: http.StatusServiceUnavailable, body: `{"error":"down"}`},
		{name: "invalid error body", status: http.StatusBadGateway, cut: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				if tc.cut {
					hj, ok := w.(http.Hijacker)
					if !ok {
						t.Fatal("no hijacker")
					}
					conn, _, err := hj.Hijack()
					if err != nil {
						t.Fatal(err)
					}
					_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\nContent-Length: 5\r\n\r\nnope"))
					conn.Close()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			_, err := DoRequest(context.Background(), srv.Client(), http.MethodPost, srv.URL, map[string]string{"X-Test": "1"}, []byte(`{"n":1}`))
			if err == nil {
				t.Fatal("expected upstream failure")
			}
			if !tc.cut {
				var ue *UpstreamError
				if !errors.As(err, &ue) || ue.StatusCode != tc.status || !strings.Contains(string(ue.Body), "error") {
					t.Fatalf("err=%v", err)
				}
			}
			if hits != 1 {
				t.Fatalf("hits=%d, want 1", hits)
			}
			if directHits() != 0 {
				t.Fatalf("direct egress hits=%d", directHits())
			}
		})
	}
}

func TestDoRequestTimeoutAndResetDoNotDialDirect(t *testing.T) {
	blockDirect(t)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancel()
	_, err := DoRequest(ctx, srv.Client(), http.MethodPost, srv.URL, nil, []byte(`{}`))
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout err=%v", err)
	}

	reset := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("read tcp 127.0.0.1:9->127.0.0.1:9: connection reset by peer")
	})}
	_, err = DoRequest(context.Background(), reset, http.MethodPost, srv.URL, nil, []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("reset err=%v", err)
	}
	if hits != 0 || directHits() != 0 {
		t.Fatalf("hits=%d direct=%d", hits, directHits())
	}
}

func TestReadBodyAndUpstreamBodyLimits(t *testing.T) {
	const limit = 10 * 1024 * 1024
	t.Run("exact", func(t *testing.T) {
		var closes int
		body := bytes.Repeat([]byte("a"), limit)
		resp := &http.Response{StatusCode: http.StatusOK, Body: &closeTracker{Reader: bytes.NewReader(body), closes: &closes}}
		got, err := ReadBody(resp)
		if err != nil || len(got) != limit {
			t.Fatalf("len=%d err=%v", len(got), err)
		}
		if closes != 1 {
			t.Fatalf("closes=%d", closes)
		}
	})
	t.Run("limit plus one truncates and closes", func(t *testing.T) {
		var closes int
		src := &countReader{n: limit + 1}
		resp := &http.Response{StatusCode: http.StatusOK, Body: &closeTracker{Reader: src, closes: &closes}}
		got, err := ReadBody(resp)
		if err != nil || len(got) != limit || src.read != limit {
			t.Fatalf("len=%d read=%d err=%v", len(got), src.read, err)
		}
		if closes != 1 {
			t.Fatalf("closes=%d", closes)
		}
	})
	t.Run("huge stops at cap", func(t *testing.T) {
		var closes int
		src := &countReader{n: limit * 4}
		resp := &http.Response{StatusCode: http.StatusOK, Body: &closeTracker{Reader: src, closes: &closes}}
		got, err := ReadBody(resp)
		if err != nil || len(got) != limit || src.read != limit {
			t.Fatalf("len=%d read=%d err=%v", len(got), src.read, err)
		}
		if closes != 1 {
			t.Fatalf("closes=%d", closes)
		}
	})
	t.Run("read error closes", func(t *testing.T) {
		var closes int
		src := &countReader{n: 8, err: io.ErrUnexpectedEOF}
		resp := &http.Response{StatusCode: http.StatusOK, Body: &closeTracker{Reader: src, closes: &closes}}
		_, err := ReadBody(resp)
		if err == nil || closes != 1 {
			t.Fatalf("err=%v closes=%d", err, closes)
		}
	})
	t.Run("non-200", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader("nope"))}
		_, err := UpstreamBody(resp)
		var ue *UpstreamError
		if !errors.As(err, &ue) || ue.StatusCode != http.StatusBadGateway || string(ue.Body) != "nope" {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestUpstreamErrorHTMLShapes(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "cloudflare", body: "<!DOCTYPE html><html><title>Just a moment</title>cloudflare</html>", want: "Cloudflare WAF"},
		{name: "title", body: "<html><title>Bad Gateway</title></html>", want: "HTML page (Bad Gateway)"},
		{name: "bare", body: "<html>no title</html>", want: "HTML error page"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := (&UpstreamError{StatusCode: 502, Body: []byte(tc.body)}).Error()
			if !strings.Contains(msg, tc.want) {
				t.Fatalf("msg=%q", msg)
			}
		})
	}
}

func TestBuildURL(t *testing.T) {
	if BuildURL("", "/v1") != "/v1" || BuildURL("https://example.test", "") != "https://example.test" {
		t.Fatal("empty side")
	}
	if BuildURL("https://example.test", "v1/chat") != "https://example.test/v1/chat" {
		t.Fatal("join")
	}
}

func TestForwardHelpersUpstreamFailures(t *testing.T) {
	blockDirect(t)
	body := []byte(`{"model":"demo","messages":[{"role":"user","content":"hi"}]}`)
	for _, tc := range []struct {
		name string
		call func(ctx context.Context, client *http.Client, cfg *providers.ProviderConfig) error
	}{
		{name: "grok", call: func(ctx context.Context, client *http.Client, cfg *providers.ProviderConfig) error {
			_, err := ForwardGrokCLI(ctx, client, cfg, "sk-test", body, false)
			return err
		}},
		{name: "iflow", call: func(ctx context.Context, client *http.Client, cfg *providers.ProviderConfig) error {
			_, err := ForwardIflow(ctx, client, cfg, "sk-test", body, true, map[string]string{"session-id": "s"})
			return err
		}},
		{name: "kimchi", call: func(ctx context.Context, client *http.Client, cfg *providers.ProviderConfig) error {
			_, err := ForwardKimchi(ctx, client, cfg, "sk-test", body, false)
			return err
		}},
		{name: "azure", call: func(ctx context.Context, client *http.Client, cfg *providers.ProviderConfig) error {
			_, err := ForwardAzure(ctx, client, cfg, "sk-test", body, false, cfg.BaseURL)
			return err
		}},
		{name: "commandcode", call: func(ctx context.Context, client *http.Client, cfg *providers.ProviderConfig) error {
			_, err := ForwardCommandcode(ctx, client, cfg, "sk-test", body)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				switch hits {
				case 1:
					w.WriteHeader(http.StatusBadGateway)
					_, _ = w.Write([]byte(`{"error":"down"}`))
				default:
					t.Errorf("unexpected extra hit %d", hits)
				}
			}))
			defer srv.Close()
			cfg := &providers.ProviderConfig{BaseURL: srv.URL, AuthHeader: "Authorization", AuthScheme: "bearer"}
			err := tc.call(context.Background(), srv.Client(), cfg)
			var ue *UpstreamError
			if !errors.As(err, &ue) || ue.StatusCode != http.StatusBadGateway {
				t.Fatalf("err=%v", err)
			}
			ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
			defer cancel()
			if err := tc.call(ctx, srv.Client(), cfg); err == nil || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("timeout err=%v", err)
			}
			cut := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("connection reset by peer")
			})}
			if err := tc.call(context.Background(), cut, cfg); err == nil || !strings.Contains(err.Error(), "connection reset") {
				t.Fatalf("cut err=%v", err)
			}
			if directHits() != 0 {
				t.Fatalf("direct hits=%d", directHits())
			}
		})
	}
}

func TestForwardGeminiUpstreamFailuresStayOnLoopback(t *testing.T) {
	blockDirect(t)
	t.Cleanup(func() {
		_ = osRemoveGeminiDumps()
	})
	body := `{"model":"gemini-2.0-flash","messages":[{"role":"user","content":"hi"}]}`
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("auth=%q", r.Header.Get("Authorization"))
		}
		switch hits {
		case 1:
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"down"}`))
		case 2:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"bad"}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`))
		}
	}))
	defer srv.Close()
	cfg := &providers.ProviderConfig{BaseURL: srv.URL}
	client := srv.Client()
	_, err := ForwardGemini(context.Background(), client, cfg, "sk-test", body, false, "", "gemini-2.0-flash")
	var ue *UpstreamError
	if !errors.As(err, &ue) || ue.StatusCode != http.StatusBadGateway {
		t.Fatalf("502 err=%v", err)
	}
	_, err = ForwardGemini(context.Background(), client, cfg, "sk-test", body, false, "", "gemini-2.0-flash")
	if !errors.As(err, &ue) || ue.StatusCode != http.StatusBadRequest {
		t.Fatalf("400 err=%v", err)
	}
	resp, err := ForwardGemini(context.Background(), client, cfg, "sk-test", body, false, "", "gemini-2.0-flash")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || !bytes.Contains(got, []byte("ok")) {
		t.Fatalf("body=%s err=%v", got, err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancel()
	if _, err := ForwardGemini(ctx, client, cfg, "sk-test", body, false, "", "gemini-2.0-flash"); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout err=%v", err)
	}
	cut := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection reset by peer")
	})}
	if _, err := ForwardGemini(context.Background(), cut, cfg, "sk-test", body, false, "", "gemini-2.0-flash"); err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("cut err=%v", err)
	}
	if _, err := ForwardGemini(context.Background(), client, cfg, "sk-test", "{", false, "", "gemini-2.0-flash"); err == nil {
		t.Fatal("invalid body was forwarded")
	}
	if directHits() != 0 {
		t.Fatalf("direct hits=%d", directHits())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type closeTracker struct {
	io.Reader
	closes *int
}

func (c *closeTracker) Close() error {
	if c.closes != nil {
		*c.closes++
	}
	return nil
}

type countReader struct {
	n    int
	read int
	err  error
}

func (c *countReader) Read(p []byte) (int, error) {
	if c.read >= c.n {
		if c.err != nil {
			return 0, c.err
		}
		return 0, io.EOF
	}
	want := len(p)
	if c.read+want > c.n {
		want = c.n - c.read
	}
	for i := 0; i < want; i++ {
		p[i] = 'a'
	}
	c.read += want
	if c.err != nil && c.read >= c.n {
		return want, c.err
	}
	return want, nil
}

var directCounter int

func blockDirect(t *testing.T) {
	t.Helper()
	prev := directProxyClient
	directCounter = 0
	directProxyClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		directCounter++
		return nil, errors.New("direct egress blocked in test")
	})}
	t.Cleanup(func() { directProxyClient = prev })
}

func directHits() int { return directCounter }

func osRemoveGeminiDumps() error {
	// Local debug dumps written on a Gemini 400. They contain the fixture
	// request, never a live credential, and must not linger between tests.
	for _, name := range []string{"/tmp/9router-gemini-400.json", "/tmp/9router-ag-debug.json"} {
		_ = os.Remove(name)
	}
	return nil
}
