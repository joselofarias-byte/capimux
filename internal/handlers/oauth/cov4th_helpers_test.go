package oauth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9router/proxy/internal/db"
)

// installUpstreamCov4th points both the package device client and the default
// transport at fn, then restores them. Handlers that build &http.Client{Timeout}
// pick up DefaultTransport; device-code calls use deviceClient.
// happyHostsCov4th are the upstreams routeHappyCov4th answers. Everything
// else keeps the caller's transport, so a local httptest server still works.
var happyHostsCov4th = map[string]bool{
	"api.kilo.ai": true, "auth.x.ai": true, "cli-chat-proxy.grok.com": true,
	"github.com": true, "api.github.com": true, "oidc.us-east-1.amazonaws.com": true,
	"codewhisperer.us-east-1.amazonaws.com": true, "auth.kimi.com": true,
	"copilot.tencent.com": true, "www.codebuddy.ai": true, "auth.meta.com": true,
	"api.meta.ai": true, "openapi.qoder.sh": true, "openapi.qoder.com.cn": true,
	"prod.us-east-1.auth.desktop.kiro.dev": true, "oauth2.googleapis.com": true,
	"www.googleapis.com": true, "api.cline.bot": true, "api.anthropic.com": true,
	"auth.openai.com": true, "iflow.cn": true, "platform.iflow.cn": true,
}

func installHappyOrDialCov4th(t *testing.T) {
	t.Helper()
	base := http.DefaultTransport
	installUpstreamCov4th(t, func(r *http.Request) (*http.Response, error) {
		if happyHostsCov4th[r.URL.Host] {
			resp := routeHappyCov4th(r)
			resp.Request = r
			return resp, nil
		}
		return base.RoundTrip(r)
	})
}

func installUpstreamCov4th(t *testing.T, fn devicePollFixture) {
	t.Helper()
	origTransport := http.DefaultTransport
	origClient := deviceClient
	http.DefaultTransport = fn
	deviceClient = &http.Client{Transport: fn}
	t.Cleanup(func() {
		http.DefaultTransport = origTransport
		deviceClient = origClient
	})
}

func jsonBodyCov4th(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func callOAuthCov4th(handler http.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func repoCov4th(t *testing.T) *db.Repo {
	t.Helper()
	database, cleanup := setupTestDB(t)
	t.Cleanup(cleanup)
	return db.NewRepo(database)
}

func countConnsCov4th(t *testing.T, repo *db.Repo) int {
	t.Helper()
	var n int
	if err := repo.RawDB().QueryRow("SELECT COUNT(*) FROM providerConnections").Scan(&n); err != nil {
		t.Fatalf("count connections: %v", err)
	}
	return n
}

type errBodyCov4th struct{}

func (errBodyCov4th) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (errBodyCov4th) Close() error             { return nil }
