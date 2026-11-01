package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A stalled Kiro auth host must not pin the social-exchange handler forever:
// once the dashboard request is cancelled (browser closed, server shutdown) the
// outbound exchange has to stop too. Fully local; no real Kiro call.
func TestHandleOAuthKiroSocialExchange_StalledUpstreamHonoursCancel(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	defer close(release)

	prev := kiroSocialTokenURL
	kiroSocialTokenURL = upstream.URL + "/oauth/token"
	defer func() { kiroSocialTokenURL = prev }()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/kiro/social-exchange",
		strings.NewReader(`{"code":"synthetic-code","codeVerifier":"synthetic-verifier","provider":"google"}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		NewOAuthHandler(nil).HandleOAuthKiroSocialExchange(rec, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("social exchange ignored request cancellation and kept waiting on the stalled upstream")
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 after cancellation, got %d: %s", rec.Code, rec.Body.String())
	}
}
