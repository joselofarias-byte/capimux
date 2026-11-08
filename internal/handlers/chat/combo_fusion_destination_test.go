package chat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"9router/proxy/internal/controlplane/registry"
	"9router/proxy/internal/db"
	"9router/proxy/internal/providers"
)

func TestFusionPanel_ConfiguredDestinationAndRetryableStatuses(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var configuredHits, defaultHits atomic.Int32
			configured := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				configuredHits.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":{"message":"configured route denied"}}`))
			}))
			defer configured.Close()
			defaultUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defaultHits.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"wrong destination"}}]}`))
			}))
			defer defaultUpstream.Close()

			database, cleanup := setupChatTestDB(t)
			defer cleanup()
			if _, err := database.Exec(`DELETE FROM providerConnections WHERE provider = 'opencode'`); err != nil {
				t.Fatal(err)
			}
			model := fmt.Sprintf("audit-fusion-destination-%d", status)
			connID := fmt.Sprintf("conn-fusion-destination-%d", status)
			seedConnDB(t, database, "opencode", connID, "", configured.URL)

			orig, ok := providers.KnownProviders["opencode"]
			if !ok {
				t.Fatal("opencode provider config missing")
			}
			cfg := orig
			cfg.BaseURL = defaultUpstream.URL
			providers.KnownProviders["opencode"] = cfg
			defer func() { providers.KnownProviders["opencode"] = orig }()

			if err := registry.InitRegistry(nil); err != nil {
				t.Fatal(err)
			}

			repo := db.NewRepo(database)
			h := NewChatHandler(repo)
			result := h.makePanelCall(
				[]byte(`{"model":"ignored","messages":[{"role":"user","content":"hi"}],"stream":false}`),
				"opencode/"+model,
			)(context.Background())
			if result == nil || result.err == nil {
				t.Fatalf("expected configured upstream %d error, got %#v", status, result)
			}
			var ue *upstreamError
			if !errors.As(result.err, &ue) || ue.StatusCode != status {
				t.Fatalf("expected upstream status %d, got %v", status, result.err)
			}
			if got := configuredHits.Load(); got != 1 {
				t.Fatalf("configured destination hits = %d, want 1", got)
			}
			if got := defaultHits.Load(); got != 0 {
				t.Fatalf("global default destination was used: hits = %d", got)
			}

			locked, err := repo.IsConnectionModelLocked(connID, model)
			if err != nil {
				t.Fatal(err)
			}
			if !locked {
				t.Fatalf("real account/model not locked after %d", status)
			}
			for _, synthetic := range []string{"default", "noauth"} {
				syntheticLocked, lockErr := repo.IsConnectionModelLocked(synthetic, model)
				if lockErr != nil {
					t.Fatalf("IsConnectionModelLocked(%s): %v", synthetic, lockErr)
				}
				if syntheticLocked {
					t.Fatalf("synthetic %s id was locked after %d", synthetic, status)
				}
			}
		})
	}
}
