package discovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"9router/proxy/internal/controlplane/registry"
)

// models.dev lists a gateway's free lane next to its paid model
// ("poolside/laguna-s-2.1:free" and "poolside/laguna-s-2.1" on openrouter) and
// several vendors' copies of one model ("cerebras/gpt-oss-120b",
// "groq/gpt-oss-120b"). The adapter collapses them to one base id, so it must
// pick one candidate deterministically. Before, both were emitted and the
// orchestrator kept whichever Go's map iteration produced last: the free lane
// left and re-entered the free pool on every sync.
const modelsDevVariantsFixture = `{
	"openrouter": {
		"models": {
			"poolside/laguna-s-2.1": {"limit": {"context": 262144, "output": 32768}},
			"poolside/laguna-s-2.1:free": {"limit": {"context": 131072, "output": 16384}},
			"thinkingmachines/inkling:free": {},
			"thinkingmachines/inkling": {},
			"cerebras/gpt-oss-120b": {},
			"groq/gpt-oss-120b": {},
			"acme/solo-model": {}
		}
	}
}`

func serveModelsDev(t *testing.T, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	orig := ModelsDevCatalogURL
	ModelsDevCatalogURL = srv.URL
	t.Cleanup(func() { ModelsDevCatalogURL = orig })
}

func TestModelsDevAdapter_CollapsedVariantsAreDeterministic(t *testing.T) {
	serveModelsDev(t, modelsDevVariantsFixture)
	adapter := NewModelsDevAdapter(nil)

	want := map[string]struct{ upstream, pricing string }{
		"laguna-s-2.1": {"poolside/laguna-s-2.1:free", "free"},
		"inkling":      {"thinkingmachines/inkling:free", "free"},
		"gpt-oss-120b": {"cerebras/gpt-oss-120b", "unknown"},
		"solo-model":   {"acme/solo-model", "unknown"},
	}
	// Map iteration order changes between runs; repeat so a lucky order on
	// main cannot hide the bug.
	for round := 0; round < 25; round++ {
		candidates, err := adapter.Discover(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]Candidate{}
		for _, c := range candidates {
			if c.ProviderID != "openrouter" {
				continue
			}
			if prev, dup := got[c.ModelID]; dup {
				t.Fatalf("round %d: openrouter/%s emitted twice (%s and %s)", round, c.ModelID, prev.UpstreamModel, c.UpstreamModel)
			}
			got[c.ModelID] = c
		}
		if len(got) != len(want) {
			t.Fatalf("round %d: candidates = %d, want %d", round, len(got), len(want))
		}
		for id, w := range want {
			c := got[id]
			if c.UpstreamModel != w.upstream || c.PricingMode != w.pricing {
				t.Fatalf("round %d: openrouter/%s = %s (%s), want %s (%s)", round, id, c.UpstreamModel, c.PricingMode, w.upstream, w.pricing)
			}
		}
	}
}

// End to end through the orchestrator: the free lane stays free (and keeps
// the ":free" upstream, so free routing never falls through to the paid id)
// across repeated syncs.
func TestOrchestrator_ModelsDevFreeLaneStableAcrossSyncs(t *testing.T) {
	serveModelsDev(t, modelsDevVariantsFixture)
	db := openDiscoveryDB(t)
	orch := NewOrchestrator(db, []Adapter{NewModelsDevAdapter(nil)})

	for round := 0; round < 15; round++ {
		orch.RunSync(context.Background())
		pm := registry.GetActiveState().ProviderModels["openrouter"]["laguna-s-2.1"]
		if pm == nil {
			t.Fatalf("round %d: openrouter/laguna-s-2.1 missing", round)
		}
		if pm.PricingMode != "free" || pm.UpstreamModel != "poolside/laguna-s-2.1:free" || !pm.IsActive {
			t.Fatalf("round %d: free lane flipped: pricing=%s upstream=%s active=%v", round, pm.PricingMode, pm.UpstreamModel, pm.IsActive)
		}
	}
}
