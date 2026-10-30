package dashboard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/auth"
)

// asDashboardAdmin attaches a valid dashboard session cookie, the credential a
// logged-in owner's browser carries. Handler-level tests that exercise the
// password or Tailscale logic itself use it; the API-key-only path is covered
// separately below and in internal/handlers through the real router.
func asDashboardAdmin(t *testing.T, req *http.Request) {
	t.Helper()
	t.Setenv("JWT_SECRET", "dashboard-admin-test-secret")
	token, err := auth.Sign(auth.Secret(), time.Now())
	if err != nil {
		t.Fatalf("sign admin session: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
}

func TestChangedPostureKey(t *testing.T) {
	for _, tc := range []struct {
		name             string
		current, updates map[string]any
		wantKey          string
		wantChanged      bool
	}{
		{"unrelated keys", map[string]any{"requireLogin": true}, map[string]any{"language": "es"}, "", false},
		{"unchanged requireLogin resent", map[string]any{"requireLogin": true}, map[string]any{"requireLogin": true}, "", false},
		{"missing requireLogin means on", map[string]any{}, map[string]any{"requireLogin": true}, "", false},
		{"disable login", map[string]any{}, map[string]any{"requireLogin": false}, "requireLogin", true},
		{"tunnel access", map[string]any{"tunnelDashboardAccess": false}, map[string]any{"tunnelDashboardAccess": true}, "tunnelDashboardAccess", true},
		{"unchanged sso fields", map[string]any{"authMode": "password", "oidcIssuerUrl": ""}, map[string]any{"authMode": "password", "oidcIssuerUrl": ""}, "", false},
		{"sso switch", map[string]any{"authMode": "password"}, map[string]any{"authMode": "sso"}, "authMode", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key, changed := changedPostureKey(tc.current, tc.updates)
			if key != tc.wantKey || changed != tc.wantChanged {
				t.Fatalf("changedPostureKey = (%q, %v), want (%q, %v)", key, changed, tc.wantKey, tc.wantChanged)
			}
		})
	}
}

// Without an admin credential and with login enforced, Tailscale must be
// refused before any tailscale command runs on the host.
func TestHandleTailscaleEnable_NonAdminNeverExecs(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	h := NewDashboardHandler(repo)
	stubTailscaleHost(t, "/usr/bin/tailscale", func(_ context.Context, _ string, args ...string) ([]byte, error) {
		t.Fatalf("tailscale must not run for a non-admin caller (args %v)", args)
		return nil, errors.New("unreachable")
	})

	req := httptest.NewRequest(http.MethodPost, "/api/tunnel/tailscale-enable", nil)
	req.Header.Set("Authorization", "Bearer some-client-key")
	rec := httptest.NewRecorder()
	h.HandleTailscaleEnable(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Tailscale") {
		t.Fatalf("expected a Spanish explanation naming Tailscale, got %s", rec.Body.String())
	}
}
