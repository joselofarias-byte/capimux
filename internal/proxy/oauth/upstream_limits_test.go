package oauth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/providers"
)

// scriptTransport never dials. Every refresh under test must stay on it so a
// failure mode cannot escape to a real token endpoint.
type scriptTransport struct {
	status  int
	body    io.Reader
	err     error
	hosts   []string
	reads   *int
	closes  *int
	onClose func()
}

func (s *scriptTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	if r.URL != nil {
		s.hosts = append(s.hosts, r.URL.Host)
	}
	if s.err != nil {
		return nil, s.err
	}
	status := s.status
	if status == 0 {
		status = http.StatusOK
	}
	body := s.body
	if body == nil {
		body = strings.NewReader(`{"access_token":"new-access","expires_in":60}`)
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       &closeTracker{Reader: body, closes: s.closes, extra: s.onClose},
		Request:    r,
	}, nil
}

type closeTracker struct {
	io.Reader
	closes *int
	extra  func()
}

func (c *closeTracker) Close() error {
	if c.closes != nil {
		*c.closes++
	}
	if c.extra != nil {
		c.extra()
	}
	return nil
}

// countReader yields n bytes and records how many the caller actually pulled.
type countReader struct {
	n    int
	read int
	err  error
}

func (c *countReader) Read(p []byte) (int, error) {
	if c.err != nil && c.read > 0 {
		return 0, c.err
	}
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
	if c.read >= c.n && c.err != nil {
		return want, c.err
	}
	return want, nil
}

func tokenJSON(n int, accessKey, access string) []byte {
	prefix := `{"` + accessKey + `":"` + access + `","expires_in":3600,"pad":"`
	suffix := `"}`
	pad := n - len(prefix) - len(suffix)
	if pad < 0 {
		panic("tokenJSON size too small")
	}
	buf := make([]byte, 0, n)
	buf = append(buf, prefix...)
	buf = append(buf, bytes.Repeat([]byte("a"), pad)...)
	buf = append(buf, suffix...)
	if len(buf) != n {
		panic("tokenJSON size mismatch")
	}
	return buf
}

func kiroTokenJSON(n int) []byte {
	prefix := `{"accessToken":"new-access","expiresIn":3600,"pad":"`
	suffix := `"}`
	pad := n - len(prefix) - len(suffix)
	buf := make([]byte, 0, n)
	buf = append(buf, prefix...)
	buf = append(buf, bytes.Repeat([]byte("a"), pad)...)
	buf = append(buf, suffix...)
	return buf
}

type refreshFn func(ctx context.Context, client *http.Client) (*TokenResult, error)

func standardRefreshers() map[string]refreshFn {
	return map[string]refreshFn{
		"claude": func(ctx context.Context, c *http.Client) (*TokenResult, error) {
			return refreshClaude(ctx, &Params{Client: c, Provider: "claude", RefreshToken: "refresh-fixture"})
		},
		"xai": func(ctx context.Context, c *http.Client) (*TokenResult, error) {
			return refreshXAI(ctx, &Params{Client: c, Provider: "xai", RefreshToken: "refresh-fixture"})
		},
		"grok-cli": func(ctx context.Context, c *http.Client) (*TokenResult, error) {
			return refreshGrokCLI(ctx, &Params{Client: c, Provider: "grok-cli", RefreshToken: "refresh-fixture"})
		},
		"standard": func(ctx context.Context, c *http.Client) (*TokenResult, error) {
			return doFormRefresh(ctx, c, "https://token.example.test/oauth/token", url.Values{
				"grant_type":    {"refresh_token"},
				"refresh_token": {"refresh-fixture"},
			})
		},
	}
}

func TestOAuthRefreshUpstreamFailuresStayOffline(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		tripErr    error
		wantSub    string
		wantStatus int
	}{
		{name: "500", status: http.StatusInternalServerError, body: `{"error":"upstream exploded"}`, wantSub: "status 500", wantStatus: 500},
		{name: "502", status: http.StatusBadGateway, body: `{"error":"bad gateway"}`, wantSub: "status 502", wantStatus: 502},
		{name: "503", status: http.StatusServiceUnavailable, body: `{"error":"unavailable"}`, wantSub: "status 503", wantStatus: 503},
		{name: "invalid json", status: http.StatusOK, body: `{"access_token":`, wantSub: "parse"},
		{name: "empty token", status: http.StatusOK, body: `{"expires_in":1}`, wantSub: "empty access token"},
		{name: "timeout", tripErr: context.DeadlineExceeded, wantSub: "deadline"},
		{name: "connection cut", tripErr: errors.New("read tcp 127.0.0.1:1->127.0.0.1:2: connection reset by peer"), wantSub: "connection reset"},
	}
	for name, refresh := range standardRefreshers() {
		for _, tc := range cases {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				var closes int
				rt := &scriptTransport{status: tc.status, err: tc.tripErr, closes: &closes}
				if tc.body != "" {
					rt.body = strings.NewReader(tc.body)
				}
				ctx := context.Background()
				if errors.Is(tc.tripErr, context.DeadlineExceeded) {
					var cancel context.CancelFunc
					ctx, cancel = context.WithDeadline(ctx, time.Unix(0, 0))
					defer cancel()
					rt.err = nil
				}
				res, err := refresh(ctx, &http.Client{Transport: rt})
				if err == nil || res != nil {
					t.Fatalf("expected failure, got res=%v err=%v", res, err)
				}
				if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantSub)) && !strings.Contains(err.Error(), tc.wantSub) {
					t.Fatalf("error %q, want substring %q", err, tc.wantSub)
				}
				if tc.wantStatus != 0 {
					var oe *providers.OAuthRefreshError
					if !errors.As(err, &oe) || oe.Status != tc.wantStatus {
						t.Fatalf("status error = %#v, want %d", oe, tc.wantStatus)
					}
				}
				if tc.tripErr == nil && closes != 1 {
					t.Fatalf("response body closed %d times, want 1", closes)
				}
				if len(rt.hosts) == 0 && !errors.Is(tc.tripErr, context.DeadlineExceeded) {
					t.Fatal("refresh did not record an in-memory host")
				}
				for _, host := range rt.hosts {
					if host == "" || strings.Contains(host, " ") {
						t.Fatalf("unexpected host %q", host)
					}
				}
			})
		}
	}
}

func TestOAuthRefreshBodyLimitExactOverAndHuge(t *testing.T) {
	const limit = 1 << 20
	for name, refresh := range standardRefreshers() {
		t.Run(name, func(t *testing.T) {
			t.Run("exact", func(t *testing.T) {
				var closes int
				body := tokenJSON(limit, "access_token", "new-access")
				rt := &scriptTransport{body: bytes.NewReader(body), closes: &closes}
				res, err := refresh(context.Background(), &http.Client{Transport: rt})
				if err != nil {
					t.Fatalf("exact limit rejected: %v", err)
				}
				if res.AccessToken != "new-access" {
					t.Fatalf("access token = %q", res.AccessToken)
				}
				if closes != 1 {
					t.Fatalf("closes=%d", closes)
				}
			})
			t.Run("limit plus one", func(t *testing.T) {
				var closes int
				body := tokenJSON(limit+1, "access_token", "new-access")
				rt := &scriptTransport{body: bytes.NewReader(body), closes: &closes}
				res, err := refresh(context.Background(), &http.Client{Transport: rt})
				if err == nil || res != nil || !strings.Contains(err.Error(), "exceeds 1 MiB") {
					t.Fatalf("res=%v err=%v", res, err)
				}
				if closes != 1 {
					t.Fatalf("closes=%d", closes)
				}
			})
			t.Run("huge stops at cap", func(t *testing.T) {
				var closes int
				src := &countReader{n: 8 << 20}
				rt := &scriptTransport{body: src, closes: &closes}
				res, err := refresh(context.Background(), &http.Client{Transport: rt})
				if err == nil || res != nil || !strings.Contains(err.Error(), "exceeds 1 MiB") {
					t.Fatalf("res=%v err=%v", res, err)
				}
				if src.read != limit+1 {
					t.Fatalf("read %d bytes, want cap %d", src.read, limit+1)
				}
				if closes != 1 {
					t.Fatalf("closes=%d", closes)
				}
			})
			t.Run("read error", func(t *testing.T) {
				var closes int
				src := &countReader{n: 32, err: io.ErrUnexpectedEOF}
				rt := &scriptTransport{body: src, closes: &closes}
				_, err := refresh(context.Background(), &http.Client{Transport: rt})
				if err == nil || !strings.Contains(err.Error(), "read") {
					t.Fatalf("err=%v", err)
				}
				if closes != 1 {
					t.Fatalf("closes=%d", closes)
				}
			})
		})
	}
}

func TestKiroRefreshUpstreamFailuresAndBodyCap(t *testing.T) {
	const limit = 1 << 20
	social := func(ctx context.Context, c *http.Client) (*TokenResult, error) {
		return refreshKiro(ctx, &Params{Client: c, Provider: "kiro", RefreshToken: "refresh-fixture"})
	}
	aws := func(ctx context.Context, c *http.Client) (*TokenResult, error) {
		return refreshKiro(ctx, &Params{
			Client: c, Provider: "kiro", RefreshToken: "refresh-fixture",
			ProviderSpecificData: map[string]string{"clientId": "cid", "clientSecret": "cs", "region": "us-east-1"},
		})
	}
	for _, tc := range []struct {
		name    string
		refresh refreshFn
		host    string
	}{
		{name: "social", refresh: social, host: "prod.us-east-1.auth.desktop.kiro.dev"},
		{name: "aws", refresh: aws, host: "oidc.us-east-1.amazonaws.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("500", func(t *testing.T) {
				var closes int
				rt := &scriptTransport{status: http.StatusInternalServerError, body: strings.NewReader(`{"message":"boom"}`), closes: &closes}
				_, err := tc.refresh(context.Background(), &http.Client{Transport: rt})
				var oe *providers.OAuthRefreshError
				if !errors.As(err, &oe) || oe.Status != http.StatusInternalServerError {
					t.Fatalf("err=%v", err)
				}
				if len(rt.hosts) != 1 || rt.hosts[0] != tc.host {
					t.Fatalf("hosts=%v", rt.hosts)
				}
				if closes != 1 {
					t.Fatalf("closes=%d", closes)
				}
			})
			t.Run("invalid body", func(t *testing.T) {
				rt := &scriptTransport{body: strings.NewReader(`{"accessToken":`)}
				_, err := tc.refresh(context.Background(), &http.Client{Transport: rt})
				if err == nil || !strings.Contains(err.Error(), "parse") {
					t.Fatalf("err=%v", err)
				}
			})
			t.Run("empty token", func(t *testing.T) {
				rt := &scriptTransport{body: strings.NewReader(`{"expiresIn":1}`)}
				_, err := tc.refresh(context.Background(), &http.Client{Transport: rt})
				if err == nil || !strings.Contains(err.Error(), "empty access token") {
					t.Fatalf("err=%v", err)
				}
			})
			t.Run("connection cut", func(t *testing.T) {
				rt := &scriptTransport{err: errors.New("connection reset by peer")}
				_, err := tc.refresh(context.Background(), &http.Client{Transport: rt})
				if err == nil || !strings.Contains(err.Error(), "connection reset") {
					t.Fatalf("err=%v", err)
				}
				if len(rt.hosts) != 1 {
					t.Fatalf("hosts=%v", rt.hosts)
				}
			})
			t.Run("timeout", func(t *testing.T) {
				ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
				defer cancel()
				rt := &scriptTransport{}
				_, err := tc.refresh(ctx, &http.Client{Transport: rt})
				if err == nil || !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("err=%v", err)
				}
			})
			t.Run("exact limit", func(t *testing.T) {
				var closes int
				rt := &scriptTransport{body: bytes.NewReader(kiroTokenJSON(limit)), closes: &closes}
				res, err := tc.refresh(context.Background(), &http.Client{Transport: rt})
				if err != nil {
					t.Fatalf("exact limit: %v", err)
				}
				if res.AccessToken != "new-access" {
					t.Fatalf("token=%q", res.AccessToken)
				}
				if closes != 1 {
					t.Fatalf("closes=%d", closes)
				}
			})
			t.Run("huge truncated", func(t *testing.T) {
				var closes int
				src := &countReader{n: 8 << 20}
				rt := &scriptTransport{body: src, closes: &closes}
				_, err := tc.refresh(context.Background(), &http.Client{Transport: rt})
				if err == nil {
					t.Fatal("expected truncated body to fail parse")
				}
				if src.read != limit {
					t.Fatalf("read %d, want cap %d", src.read, limit)
				}
				if closes != 1 {
					t.Fatalf("closes=%d", closes)
				}
			})
		})
	}
}

func TestKiroInvalidRegionDoesNotDial(t *testing.T) {
	rt := &scriptTransport{}
	_, err := refreshKiro(context.Background(), &Params{
		Client:               &http.Client{Transport: rt},
		RefreshToken:         "refresh-fixture",
		ProviderSpecificData: map[string]string{"clientId": "cid", "clientSecret": "cs", "region": "not a region"},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid region") {
		t.Fatalf("err=%v", err)
	}
	if len(rt.hosts) != 0 {
		t.Fatalf("invalid region dialed %v", rt.hosts)
	}
}

func TestClineRefreshDoesNotReplayDirectOnNonProxyFailure(t *testing.T) {
	var hits int
	srv := newClineServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"blip"}`))
	})
	_, err := RefreshCline(context.Background(), &Params{
		Client: srv.Client(), Provider: "cline", RefreshToken: "refresh-fixture",
	})
	var oe *providers.OAuthRefreshError
	if !errors.As(err, &oe) || oe.Status != http.StatusInternalServerError {
		t.Fatalf("err=%v", err)
	}
	if hits != 1 {
		t.Fatalf("hits=%d, want 1 (no direct replay)", hits)
	}
}

func TestClineRefreshReplaysForbiddenOnLoopbackOnly(t *testing.T) {
	var hits int
	srv := newClineServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`blocked-by-allowlist`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"accessToken":"second","expiresIn":30}}`))
	})
	res, err := RefreshCline(context.Background(), &Params{
		Client: srv.Client(), Provider: "cline", RefreshToken: "refresh-fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.AccessToken != "second" || hits != 2 {
		t.Fatalf("res=%+v hits=%d", res, hits)
	}
}

func TestClineRefreshConnectionCutAndInvalidBody(t *testing.T) {
	t.Run("cut", func(t *testing.T) {
		srv := newClineServer(t, func(w http.ResponseWriter, r *http.Request) {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("no hijacker")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Fatal(err)
			}
			conn.Close()
		})
		_, err := RefreshCline(context.Background(), &Params{
			Client: srv.Client(), Provider: "cline", RefreshToken: "refresh-fixture",
		})
		if err == nil {
			t.Fatal("expected connection cut to fail")
		}
	})
	t.Run("invalid", func(t *testing.T) {
		srv := newClineServer(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"success":`))
		})
		_, err := RefreshCline(context.Background(), &Params{
			Client: srv.Client(), Provider: "cline", RefreshToken: "refresh-fixture",
		})
		if err == nil || !strings.Contains(err.Error(), "parse") {
			t.Fatalf("err=%v", err)
		}
	})
}

func newClineServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	prev, ok := providers.KnownOAuthConfigs["cline"]
	providers.KnownOAuthConfigs["cline"] = providers.OAuthClientConfig{TokenURL: srv.URL, ClientID: "cid"}
	t.Cleanup(func() {
		if ok {
			providers.KnownOAuthConfigs["cline"] = prev
		} else {
			delete(providers.KnownOAuthConfigs, "cline")
		}
	})
	return srv
}
