package providers

import (
	"context"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"testing"
)

// syncFromBody runs the real SyncModelCatalog against a local server that
// answers with body, and returns the in-memory catalog.
func syncFromBody(t *testing.T, body []byte, filePath string) error {
	t.Helper()
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(mock.Close)
	base, err := url.Parse(mock.URL)
	if err != nil {
		t.Fatal(err)
	}
	syncStateMu.Lock()
	syncState.ETag = ""
	syncStateMu.Unlock()
	return SyncModelCatalog(context.Background(), &http.Client{Transport: rewriteTransport{base: base}}, filePath)
}

func snapshotCatalog() SyncedCatalog {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	if globalCatalog == nil {
		return SyncedCatalog{}
	}
	c := *globalCatalog
	c.SyncedAt = ""
	return c
}

// TestSyncModelCatalog_DuplicateVariantsRealFixture uses real models.dev
// entries (testdata/modelsdev_variants_fixture.json, cut from the public feed
// of 2026-10-30) for the kinds of collisions found among the 137 provider/base
// keys whose variants disagree. The base key keeps one whole variant chosen
// by the owner's rule; each other variant answers with its own values when
// asked by its exact id.
func TestSyncModelCatalog_DuplicateVariantsRealFixture(t *testing.T) {
	body, err := os.ReadFile("testdata/modelsdev_variants_fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := syncFromBody(t, body, ""); err != nil {
		t.Fatalf("sync: %v", err)
	}

	limits := []struct {
		name, provider, model string
		ctx, out              int
	}{
		// Both deprecated: the larger verified context wins.
		{"both deprecated, base", "openrouter", "laguna-s-2.1", 1048576, 131072},
		{"both deprecated, free lane keeps its own", "openrouter", "poolside/laguna-s-2.1:free", 262144, 32768},
		// Free available, paid deprecated: availability first.
		{"available free beats deprecated paid", "kilo", "laguna-s-2.1", 262144, 32768},
		{"deprecated paid keeps its own", "kilo", "poolside/laguna-s-2.1", 1048576, 131072},
		// Free lane with the larger declared context.
		{"free lane with larger context", "openrouter", "nemotron-3-ultra-550b-a55b", 1000000, 65536},
		{"paid id keeps its own limits", "openrouter", "nvidia/nemotron-3-ultra-550b-a55b", 262144, 182520},
		// Context-extension suffix (":peft:N").
		{"peft extension wins on context", "thinkingmachines", "kimi-k2.6", 131072, 131072},
		{"plain id keeps its 32k", "thinkingmachines", "moonshotai/Kimi-K2.6", 32768, 32768},
		// Same context: max output decides.
		{"larger output wins", "alibaba-cn", "kimi/kimi-k2.5", 262144, 262144},
		{"bare id is its own variant", "alibaba-cn", "kimi-k2.5", 262144, 32768},
		// Same limits: cost decides between the two 262k variants.
		{"cheaper equal variant", "thinkingmachines", "thinkingmachines/Inkling:peft:262144:sampling-nvfp4", 262144, 262144},
		{"plain inkling keeps 64k", "thinkingmachines", "thinkingmachines/Inkling", 65536, 65536},
		// Available beats deprecated even with less output; here also more context.
		{"available vendor", "llmgateway-providers", "llama-4-scout-17b-instruct", 131072, 131072},
		{"deprecated vendor keeps its own", "llmgateway-providers", "aws-bedrock/llama-4-scout-17b-instruct", 8192, 2048},
	}
	for _, tc := range limits {
		ctx, out := GetCatalogLimits(tc.provider, tc.model)
		if ctx != tc.ctx || out != tc.out {
			t.Errorf("%s: GetCatalogLimits(%s, %s) = (%d, %d), want (%d, %d)", tc.name, tc.provider, tc.model, ctx, out, tc.ctx, tc.out)
		}
	}

	// Modalities are never mixed across variants: nearai serves the same
	// weights with and without image input under two vendor prefixes.
	if m := GetCatalogModalities("nearai", "deepseek-v4.1-flash-1017"); m == nil || !m.Vision {
		t.Errorf("nearai base: expected the vision variant (more input modalities), got %+v", m)
	}
	if m := GetCatalogModalities("nearai", "deepseek-ai/DeepSeek-V4.1-Flash-1017"); m == nil || m.Vision {
		t.Errorf("nearai text-only variant must not inherit vision, got %+v", m)
	}
	if m := GetCatalogModalities("nearai", "deepseek/deepseek-v4.1-flash-1017"); m == nil || !m.Vision {
		t.Errorf("nearai vision variant by exact id: got %+v", m)
	}

	cat := snapshotCatalog()
	// Identical variants are not duplicated into the variants map.
	if _, ok := cat.Variants["thinkingmachines"]["thinkingmachines/inkling:peft:262144"]; ok {
		t.Errorf("an identical variant was stored separately: %+v", cat.Variants["thinkingmachines"])
	}
	if _, ok := cat.Variants["nano-gpt"]["nvidia/nemotron-3.5-lightning:thinking"]; ok {
		t.Errorf("an identical variant was stored separately: %+v", cat.Variants["nano-gpt"])
	}
}

// TestSyncModelCatalog_DuplicateVariantsDeterministic: before the fix the
// first id in Go's randomized map order won, so the same feed produced a
// different catalog from one sync to the next.
func TestSyncModelCatalog_DuplicateVariantsDeterministic(t *testing.T) {
	body, err := os.ReadFile("testdata/modelsdev_variants_fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := syncFromBody(t, body, ""); err != nil {
		t.Fatal(err)
	}
	first := snapshotCatalog()
	for i := 0; i < 30; i++ {
		if err := syncFromBody(t, body, ""); err != nil {
			t.Fatal(err)
		}
		if got := snapshotCatalog(); !reflect.DeepEqual(got, first) {
			t.Fatalf("sync %d produced a different catalog", i)
		}
	}
}

// TestBetterCatalogCandidate pins the order of the tie-breaks on synthetic
// pairs: availability, context, output, input modalities, tool calling,
// stability, cost, id. Being ":free" is never a criterion by itself.
func TestBetterCatalogCandidate(t *testing.T) {
	type spec struct {
		id, status string
		ctx, out   int
		inputs     []string
		tools      bool
		cost       *[2]float64
	}
	mk := func(s spec) catalogCandidate {
		var m modelsDevModel
		m.Status = s.status
		m.ToolCall = s.tools
		m.Modalities.Input = s.inputs
		if s.ctx > 0 || s.out > 0 {
			m.Limit = &struct {
				Context int `json:"context"`
				Output  int `json:"output"`
			}{s.ctx, s.out}
		}
		if s.cost != nil {
			in, out := s.cost[0], s.cost[1]
			m.Cost = &struct {
				Input  *float64 `json:"input"`
				Output *float64 `json:"output"`
			}{&in, &out}
		}
		return catalogCandidate{id: s.id, m: m, mods: declaredModalities(s.inputs)}
	}
	paid := &[2]float64{1, 2}
	free := &[2]float64{0, 0}
	for _, tc := range []struct {
		name          string
		winner, loser spec
	}{
		{"paid with more context beats free", spec{id: "x", ctx: 200000, out: 8192, cost: paid}, spec{id: "x:free", ctx: 128000, out: 8192, cost: free}},
		{"available beats deprecated with more context", spec{id: "b", ctx: 1000, cost: paid}, spec{id: "a", status: "deprecated", ctx: 9000, cost: free}},
		{"declared context beats unknown", spec{id: "b", ctx: 1000}, spec{id: "a", out: 9000}},
		{"more output", spec{id: "b", ctx: 1000, out: 20}, spec{id: "a", ctx: 1000, out: 10}},
		{"more input modalities", spec{id: "b", ctx: 1000, inputs: []string{"text", "image"}}, spec{id: "a", ctx: 1000, inputs: []string{"text"}}},
		{"tool calling", spec{id: "b", ctx: 1000, tools: true}, spec{id: "a", ctx: 1000}},
		{"GA beats beta", spec{id: "b", ctx: 1000}, spec{id: "a", status: "beta", ctx: 1000}},
		{"beta beats alpha", spec{id: "b", status: "beta", ctx: 1000}, spec{id: "a", status: "alpha", ctx: 1000}},
		{"cheaper on a full tie", spec{id: "x:free", ctx: 1000, cost: free}, spec{id: "x", ctx: 1000, cost: paid}},
		{"known cost beats unknown", spec{id: "b", ctx: 1000, cost: paid}, spec{id: "a", ctx: 1000}},
		{"id breaks the last tie", spec{id: "a", ctx: 1000, cost: paid}, spec{id: "b", ctx: 1000, cost: paid}},
	} {
		w, l := mk(tc.winner), mk(tc.loser)
		if !betterCatalogCandidate(w, l) || betterCatalogCandidate(l, w) {
			t.Errorf("%s: expected %q to rank before %q", tc.name, tc.winner.id, tc.loser.id)
		}
	}
}

// TestCatalogVariantsFileRoundTrip: the variants map survives the disk cache
// and an older file without it still loads.
func TestCatalogVariantsFileRoundTrip(t *testing.T) {
	body, err := os.ReadFile("testdata/modelsdev_variants_fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/catalog.json"
	if err := syncFromBody(t, body, path); err != nil {
		t.Fatal(err)
	}
	want := snapshotCatalog()
	catalogMu.Lock()
	globalCatalog = nil
	catalogMu.Unlock()
	if err := LoadCatalogFromFile(path); err != nil {
		t.Fatal(err)
	}
	if got := snapshotCatalog(); !reflect.DeepEqual(got.Variants, want.Variants) || len(got.Variants) == 0 {
		t.Fatalf("variants lost on reload: %d providers vs %d", len(got.Variants), len(want.Variants))
	}
	if ctx, _ := GetCatalogLimits("kilo", "poolside/laguna-s-2.1"); ctx != 1048576 {
		t.Fatalf("variant lookup after reload: %d", ctx)
	}

	legacy := t.TempDir() + "/legacy.json"
	b, _ := json.Marshal(map[string]any{"models": map[string]any{}, "providers": map[string]any{"p": map[string]any{"m": map[string]int{"contextWindow": 10}}}})
	if err := os.WriteFile(legacy, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LoadCatalogFromFile(legacy); err != nil {
		t.Fatal(err)
	}
	if ctx, _ := GetCatalogLimits("p", "m"); ctx != 10 {
		t.Fatalf("legacy file lookup: %d", ctx)
	}
}
