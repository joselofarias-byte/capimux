package discovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// servePhases answers each request with the next body (the last one repeats).
func servePhases(t *testing.T, bodies ...string) *httptest.Server {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		i := int(n.Add(1)) - 1
		if i >= len(bodies) {
			i = len(bodies) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bodies[i]))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Cycle 9, decision 2 applied to the scoped free catalogs: an HTTP 200
// without the "free" list (missing, null) or whose entries have no ids is a
// broken response, not "Cline has no free models". It must not deactivate the
// models classified by the previous good sync. An explicit empty list stays
// authoritative (otherwise a stale "free" could route to a now-paid model).
func TestClineFreeAdapter_MissingFreeListKeepsClassification(t *testing.T) {
	good := `{"free":[{"id":"deepseek/deepseek-v4-flash","name":"x"}]}`
	for _, tc := range []struct {
		name, body string
		wantActive bool
	}{
		{"missing key", `{"recommended":[{"id":"a"}]}`, true},
		{"null list", `{"free":null}`, true},
		{"null body", `null`, true},
		{"empty object", `{}`, true},
		{"entries without ids", `{"free":[{"name":"x"},{"id":""}]}`, true},
		{"explicit empty list is authoritative", `{"free":[]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openDiscoveryDB(t)
			srv := servePhases(t, good, tc.body)
			old := ClineRecommendedModelsURL
			ClineRecommendedModelsURL = srv.URL
			t.Cleanup(func() { ClineRecommendedModelsURL = old })

			orch := NewOrchestrator(db, []Adapter{NewClineFreeAdapter(srv.Client())})
			orch.RunSync(context.Background())
			if mode, active := modelPricing(t, "cline", "deepseek/deepseek-v4-flash"); mode != "free" || !active {
				t.Fatalf("seed: %s active=%v", mode, active)
			}
			orch.RunSync(context.Background())
			if _, active := modelPricing(t, "cline", "deepseek/deepseek-v4-flash"); active != tc.wantActive {
				t.Fatalf("after %s: active=%v, want %v", tc.name, active, tc.wantActive)
			}
		})
	}
}

func TestUnoRouterAdapter_MissingDataListKeepsClassification(t *testing.T) {
	good := `{"data":[{"id":"gpt-4:free"}]}`
	for _, tc := range []struct {
		name, body string
		wantActive bool
	}{
		{"missing key", `{"object":"list"}`, true},
		{"null list", `{"data":null}`, true},
		{"null body", `null`, true},
		{"entries without ids", `{"data":[{"id":""},{}]}`, true},
		{"explicit empty list is authoritative", `{"data":[]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openDiscoveryDB(t)
			now := time.Now().UTC().Format(time.RFC3339)
			if _, err := db.Exec(`INSERT INTO providerConnections (id, provider, authType, isActive, data, createdAt, updatedAt) VALUES ('conn-uno', 'unorouter', 'apikey', 1, '{}', ?, ?)`, now, now); err != nil {
				t.Fatal(err)
			}
			srv := servePhases(t, good, tc.body)
			old := UnoRouterCatalogURL
			UnoRouterCatalogURL = srv.URL
			t.Cleanup(func() { UnoRouterCatalogURL = old })

			orch := NewOrchestrator(db, []Adapter{NewUnoRouterAdapter(srv.Client(), "test-key")})
			orch.RunSync(context.Background())
			if mode, active := modelPricing(t, "unorouter", "gpt-4:free"); mode != "free" || !active {
				t.Fatalf("seed: %s active=%v", mode, active)
			}
			orch.RunSync(context.Background())
			if _, active := modelPricing(t, "unorouter", "gpt-4:free"); active != tc.wantActive {
				t.Fatalf("after %s: active=%v, want %v", tc.name, active, tc.wantActive)
			}
		})
	}
}

// The models.dev discovery adapter reports an empty feed as an error (logged
// as "adapter sync failed") instead of an empty, successful discovery.
func TestModelsDevAdapter_EmptyResponseIsAnError(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"openai":{"models":{}}}`} {
		srv := servePhases(t, body)
		old := ModelsDevCatalogURL
		ModelsDevCatalogURL = srv.URL
		got, err := NewModelsDevAdapter(srv.Client()).Discover(context.Background())
		ModelsDevCatalogURL = old
		if err == nil {
			t.Errorf("%s: expected an error, got %d candidates", body, len(got))
		}
	}
}
