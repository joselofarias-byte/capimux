package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/db"
	"9router/proxy/internal/providers"
)

func TestRefreshOAuthTokenLocal(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	script := newHostScript()
	script.allow("oauth2.googleapis.com", func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/token" {
			t.Errorf("token path %s", r.URL.Path)
		}
		return jsonResp(http.StatusOK, `{"access_token":"refreshed-token","expires_in":3600,"refresh_token":"rotated"}`), nil
	})
	h := NewChatHandler(db.NewRepo(database))
	h.Client = scriptedClient(script)

	token, pid, err := h.RefreshOAuthTokenIfExpired("", "current")
	if err != nil || token != "current" || pid != "" {
		t.Fatalf("empty id token=%q pid=%q err=%v", token, pid, err)
	}
	token, _, err = h.RefreshOAuthTokenIfExpired("missing", "current")
	if err != nil || token != "current" {
		t.Fatalf("missing row token=%q err=%v", token, err)
	}

	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		('bad-json', 'gemini-cli', 'oauth', 'bad', 1, 1, '{', '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.RefreshOAuthTokenIfExpired("bad-json", "current"); err != nil {
		t.Fatalf("bad json: %v", err)
	}

	future := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		('fresh', 'gemini-cli', 'oauth', 'fresh', 1, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
		`{"accessToken":"live","refreshToken":"r","expiresAt":"`+future+`","projectId":"p1"}`); err != nil {
		t.Fatal(err)
	}
	token, pid, err = h.RefreshOAuthTokenIfExpired("fresh", "live")
	if err != nil || token != "live" || pid != "p1" {
		t.Fatalf("fresh token=%q pid=%q err=%v", token, pid, err)
	}

	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		('stale', 'gemini-cli', 'oauth', 'stale', 1, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
		`{"accessToken":"old","refreshToken":"refresh-me","expiresAt":"`+past+`","projectId":"proj"}`); err != nil {
		t.Fatal(err)
	}
	token, pid, err = h.RefreshOAuthTokenIfExpired("stale", "old")
	if err != nil || token != "refreshed-token" || pid != "proj" {
		t.Fatalf("refresh token=%q pid=%q err=%v", token, pid, err)
	}

	forced, pid, err := h.forceRefreshOAuthToken("stale")
	if err != nil || forced != "refreshed-token" || pid != "proj" {
		t.Fatalf("force token=%q pid=%q err=%v", forced, pid, err)
	}
	if _, _, err := h.forceRefreshOAuthToken(""); err != nil {
		t.Fatalf("empty force: %v", err)
	}
	if _, _, err := h.forceRefreshOAuthToken("missing"); err == nil {
		t.Fatal("missing connection should fail force refresh")
	}

	script.allow("oauth2.googleapis.com", func(r *http.Request) (*http.Response, error) {
		return jsonResp(http.StatusUnauthorized, `{"error":"invalid_grant"}`), nil
	})
	if _, err := database.Exec(`UPDATE providerConnections SET data = ? WHERE id = 'stale'`,
		`{"accessToken":"old","refreshToken":"refresh-me","expiresAt":"`+past+`","projectId":"proj"}`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.RefreshOAuthTokenIfExpired("stale", "old"); err == nil {
		t.Fatal("401 refresh should fail")
	}
}

func TestStoreAndFetchAntigravityProjectLocal(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	prevDelay := antigravityProbeDelay
	antigravityProbeDelay = time.Millisecond
	t.Cleanup(func() { antigravityProbeDelay = prevDelay })
	t.Setenv("ONBOARD_MAX_ATTEMPTS", "1")

	h := NewChatHandler(db.NewRepo(database))
	h.StoreAntigravityProjectID("", "pid")
	h.StoreAntigravityProjectID("conn-1", "")

	h.StoreAntigravityProjectID("conn-1", "proj-stored")
	deadline := time.Now().Add(2 * time.Second)
	var data string
	for time.Now().Before(deadline) {
		if err := database.QueryRow(`SELECT data FROM providerConnections WHERE id = 'conn-1'`).Scan(&data); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(data, "proj-stored") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(data, "proj-stored") {
		t.Fatalf("project id not stored: %s", data)
	}

	script := newHostScript()
	h.Client = scriptedClient(script)
	script.allow("cloudcode-pa.googleapis.com", func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(r.URL.Path, "loadCodeAssist"):
			return jsonResp(http.StatusOK, `{"cloudaicompanionProject":{"id":" from-load "}}`), nil
		default:
			return jsonResp(http.StatusOK, `{"done":true,"response":{"cloudaicompanionProject":"from-onboard"}}`), nil
		}
	})
	pid, authFailed, noProject := FetchAntigravityProjectID(context.Background(), h.Client, "token")
	if pid != "from-load" || authFailed || noProject {
		t.Fatalf("load pid=%q auth=%v none=%v", pid, authFailed, noProject)
	}

	script.allow("cloudcode-pa.googleapis.com", func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "loadCodeAssist") {
			return jsonResp(http.StatusOK, `{"allowedTiers":[{"id":"free-tier","isDefault":true}]}`), nil
		}
		return jsonResp(http.StatusOK, `{"done":true,"response":{"cloudaicompanionProject":"from-onboard"}}`), nil
	})
	pid, authFailed, noProject = fetchAntigravityProjectID(context.Background(), h.Client, "token")
	if pid != "from-onboard" || authFailed || noProject {
		t.Fatalf("onboard pid=%q auth=%v none=%v", pid, authFailed, noProject)
	}

	script.allow("cloudcode-pa.googleapis.com", func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "loadCodeAssist") {
			return jsonResp(http.StatusUnauthorized, `{"error":"nope"}`), nil
		}
		return jsonResp(http.StatusForbidden, `{"error":"nope"}`), nil
	})
	_, authFailed, noProject = fetchCloudCodeProjectID(context.Background(), h.Client, "token", "gemini-cli")
	if !authFailed || noProject {
		t.Fatalf("auth failed=%v noProject=%v", authFailed, noProject)
	}

	script.allow("cloudcode-pa.googleapis.com", func(r *http.Request) (*http.Response, error) {
		return jsonResp(http.StatusTooManyRequests, `{"error":"slow"}`), nil
	})
	pid, authFailed, noProject = fetchCloudCodeProjectID(context.Background(), h.Client, "token", "antigravity")
	if pid != "" || authFailed || noProject {
		t.Fatalf("429 pid=%q auth=%v none=%v", pid, authFailed, noProject)
	}

	script.allow("cloudcode-pa.googleapis.com", func(*http.Request) (*http.Response, error) {
		return nil, errString("down")
	})
	pid, _, _ = FetchAntigravityProjectID(context.Background(), h.Client, "token")
	if pid != "" {
		t.Fatalf("transport pid %q", pid)
	}
}

func TestForwardGeminiNativeLocal(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	future := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		('ag-1', 'antigravity', 'oauth', 'ag', 1, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
		`{"accessToken":"ag-token","refreshToken":"r","expiresAt":"`+future+`","projectId":"proj-ag"}`); err != nil {
		t.Fatal(err)
	}
	script := newHostScript()
	geminiBody := `{"candidates":[{"content":{"role":"model","parts":[{"text":"gemini-ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1}}`
	script.allow("daily-cloudcode-pa.googleapis.com", func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.RawQuery, "alt=sse") {
			return textResp(http.StatusOK, "text/event-stream", "data: "+geminiBody+"\n\n"), nil
		}
		return jsonResp(http.StatusOK, geminiBody), nil
	})
	h := NewChatHandler(db.NewRepo(database))
	h.Client = scriptedClient(script)
	cfg := providers.KnownProviders["antigravity"]

	rec := httptest.NewRecorder()
	err := h.forwardGeminiNativeRequest(context.Background(), rec, "antigravity", &cfg, "ag-token", "ag-1",
		[]byte(`{"model":"gemini-3-flash","messages":[{"role":"user","content":"hi"}]}`), false, false, &streamMetrics{})
	if err != nil || rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "gemini-ok") {
		t.Fatalf("non-stream err=%v code=%d body=%s", err, rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	err = h.forwardGeminiNativeRequest(context.Background(), rec, "antigravity", &cfg, "ag-token", "ag-1",
		[]byte(`{"model":"gemini-3-flash","messages":[{"role":"user","content":"hi"}]}`), true, false, &streamMetrics{})
	if err != nil {
		t.Fatalf("stream err=%v body=%s", err, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	err = h.forwardGeminiNativeRequest(context.Background(), rec, "antigravity", &cfg, "ag-token", "no-such-conn",
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`), false, false, nil)
	if err == nil || !strings.Contains(err.Error(), "no project ID") {
		t.Fatalf("missing project err=%v", err)
	}
}
