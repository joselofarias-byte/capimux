package oauth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9router/proxy/internal/db"
	"9router/proxy/internal/providers"
)

func TestHandleDeviceStart_qoderLocal(t *testing.T) {
	// Qoder start is fully local (no network): PKCE + nonce + URLs.
	handler := NewOAuthHandler(nil)
	req := httptest.NewRequest("POST", "/api/oauth/device/start", strings.NewReader(`{"provider":"qoder"}`))
	rec := httptest.NewRecorder()
	handler.HandleDeviceStart(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{
		`"device_code":`, `"user_code":`, `qoder.com/device/selectAccounts`,
		`"interval":2`, `"session":`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("missing %q: %s", want, rec.Body.String())
		}
	}
}

func TestHandleDeviceStart_bad(t *testing.T) {
	handler := NewOAuthHandler(nil)
	for _, payload := range []string{`{"provider":"google"}`, `{"provider":"kiro","region":"evil;id"}`} {
		req := httptest.NewRequest("POST", "/api/oauth/device/start", strings.NewReader(payload))
		rec := httptest.NewRecorder()
		handler.HandleDeviceStart(rec, req)
		if rec.Code == http.StatusOK {
			t.Errorf("expected error for %s, got %s", payload, rec.Body.String())
		}
	}
}

func TestHandleDevicePoll_validation(t *testing.T) {
	handler := NewOAuthHandler(nil)
	for _, payload := range []string{`{}`, `{"provider":"qoder"}`, `{"provider":"nope","device_code":"x"}`} {
		req := httptest.NewRequest("POST", "/api/oauth/device/poll", strings.NewReader(payload))
		rec := httptest.NewRecorder()
		handler.HandleDevicePoll(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for %s, got %d", payload, rec.Code)
		}
	}
}

func TestKimiHeaders_deviceId(t *testing.T) {
	h := kimiHeaders("dev-1")
	if h["X-Msh-Device-Id"] != "dev-1" || h["X-Msh-Platform"] != "9router" {
		t.Errorf("bad kimi headers: %v", h)
	}
}

func TestHandleDevicePoll_PendingDoesNotInsert(t *testing.T) {
	original := deviceClient
	deviceClient = &http.Client{Transport: devicePollFixture(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"authorization_pending"}`)), Request: r}, nil
	})}
	t.Cleanup(func() { deviceClient = original })
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	handler := NewOAuthHandler(repo)

	// Qoder poll with nonexistent/unauthorized device code returns pending
	req := httptest.NewRequest("POST", "/api/oauth/device/poll", strings.NewReader(`{"provider":"qoder","device_code":"pending-code","session":{"verifier":"v"}}`))
	rec := httptest.NewRecorder()
	handler.HandleDevicePoll(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if !strings.Contains(rec.Body.String(), `"status":"pending"`) && !strings.Contains(rec.Body.String(), `"status":"error"`) {
		t.Errorf("expected pending or error status, got: %s", rec.Body.String())
	}

	var count int
	err := repo.RawDB().QueryRow("SELECT COUNT(*) FROM providerConnections").Scan(&count)
	if err != nil {
		t.Fatalf("failed to query providerConnections: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 connections inserted while pending, got %d", count)
	}
}

type devicePollFixture func(*http.Request) (*http.Response, error)

func (f devicePollFixture) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGrokCLIProxyHeadersCurrentAndOverride(t *testing.T) {
	t.Setenv("GROK_CLI_CLIENT_VERSION", "1.2.3-test")
	h := providers.GrokCLIProxyHeaders("token-value")
	if h["x-grok-client-version"] != "1.2.3-test" {
		t.Fatalf("client version = %q", h["x-grok-client-version"])
	}
	if h["X-XAI-Token-Auth"] != "xai-grok-cli" {
		t.Fatalf("token auth header = %q", h["X-XAI-Token-Auth"])
	}
	if h["x-authenticateresponse"] != "authenticate-response" {
		t.Fatalf("authenticate response header = %q", h["x-authenticateresponse"])
	}
	if h["x-grok-client-mode"] != "headless" {
		t.Fatalf("client mode = %q", h["x-grok-client-mode"])
	}
	if h["Authorization"] != "Bearer token-value" {
		t.Fatalf("authorization = %q", h["Authorization"])
	}
	if !strings.HasPrefix(h["User-Agent"], "grok-shell/1.2.3-test (") {
		t.Fatalf("user agent = %q", h["User-Agent"])
	}
}

func TestGrokCLIDeviceFlowUsesSharedIdentity(t *testing.T) {
	t.Setenv("GROK_CLI_CLIENT_VERSION", "1.0.45")
	var sawStart, sawToken, sawUser bool
	original := deviceClient
	deviceClient = &http.Client{Transport: devicePollFixture(func(r *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "grok-shell/1.0.45 (") {
			t.Errorf("%s user agent = %q", r.URL.Path, r.Header.Get("User-Agent"))
		}
		switch r.URL.Path {
		case "/oauth2/device/code":
			sawStart = true
			return deviceJSON(http.StatusOK, `{"device_code":"dc","user_code":"uc","verification_uri":"https://example.test","expires_in":300,"interval":5}`), nil
		case "/oauth2/token":
			sawToken = true
			return deviceJSON(http.StatusOK, `{"access_token":"access-secret","refresh_token":"refresh-secret"}`), nil
		case "/v1/user":
			sawUser = true
			if r.Header.Get("x-grok-client-version") != "1.0.45" ||
				r.Header.Get("x-grok-client-identifier") != "grok-shell" ||
				r.Header.Get("X-XAI-Token-Auth") != "xai-grok-cli" ||
				r.Header.Get("x-authenticateresponse") != "authenticate-response" ||
				r.Header.Get("x-grok-client-mode") != "headless" {
				t.Errorf("cli-chat-proxy identity headers = %v", redactedDeviceHeaders(r.Header))
			}
			if r.Header.Get("Authorization") != "Bearer access-secret" {
				t.Error("cli-chat-proxy authorization header did not use the device access token")
			}
			return deviceJSON(http.StatusOK, `{"email":"user@example.com","name":"User"}`), nil
		default:
			t.Errorf("unexpected device request %s", r.URL.Path)
			return deviceJSON(http.StatusNotFound, `{}`), nil
		}
	})}
	t.Cleanup(func() { deviceClient = original })

	start, err := grokcliStart(context.Background())
	if err != nil {
		t.Fatalf("grokcliStart: %v", err)
	}
	if start["device_code"] != "dc" {
		t.Fatalf("device_code = %v", start["device_code"])
	}
	tokens, err := grokcliPoll(context.Background(), "dc")
	if err != nil {
		t.Fatalf("grokcliPoll: %v", err)
	}
	if tokens.email != "user@example.com" || tokens.name != "User" {
		t.Fatalf("profile name/email = %q / %q", tokens.name, tokens.email)
	}
	if tokens.access != "access-secret" || tokens.refresh != "refresh-secret" {
		t.Fatal("device tokens were not kept from the token response")
	}
	if !sawStart || !sawToken || !sawUser {
		t.Fatalf("saw start=%v token=%v user=%v", sawStart, sawToken, sawUser)
	}
}

func deviceJSON(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func redactedDeviceHeaders(h http.Header) map[string]string {
	out := map[string]string{}
	for _, key := range []string{
		"User-Agent", "X-Grok-Client-Version", "X-Grok-Client-Identifier",
		"X-Xai-Token-Auth", "X-Authenticateresponse", "X-Grok-Client-Mode",
	} {
		out[key] = h.Get(key)
	}
	if h.Get("Authorization") != "" {
		out["Authorization"] = "present"
	}
	return out
}
