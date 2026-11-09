package dashboard

import (
	"math/rand/v2"
	"net/http"
	"slices"
	"testing"
)

// Owner decision 4 (cycle 9): semantic version order, newest first.

func TestModelVersion(t *testing.T) {
	tests := []struct {
		key  string
		want []int // nil = unversioned
	}{
		{"gemini/gemini-4.10-flash", []int{4, 10}},
		{"gemini/gemini-4.9-flash", []int{4, 9}},
		{"anthropic/claude-sonnet-4-5", []int{4, 5}},
		{"cc/claude-opus-4.7", []int{4, 7}},
		{"or/moonshotai/kimi-k2.6", []int{2, 6}},
		{"ds/deepseek-r1", []int{1}},
		{"ds/deepseek-v4.1-flash:free", []int{4, 1}},
		{"oc/muse-spark-1.3-contributor-free", []int{1, 3}},
		{"openai/gpt-4-0613", []int{4}},         // snapshot id is not a component
		{"openai/gpt-4-1106-preview", []int{4}}, // idem
		{"meta/llama-3.1-8b-instruct", []int{3, 1}},
		{"mistral/mistral-large-2411", nil}, // date-like YYMM
		{"x/model-2024-08-06", nil},         // date
		{"ds/deepseek-r1-0528", []int{1}},   // date suffix ignored
		{"x/model-0528", nil},               // leading zero
		{"openai/gpt-4o", nil},              // glued to a letter
		{"openai/o3-mini", nil},             // first token is the name
		{"qwen/qwen3-coder", nil},
		{"x/model-latest", nil},
		{"x/model-1.2.3:free", []int{1, 2, 3}},
	}
	for _, tt := range tests {
		got, ok := modelVersion(tt.key)
		if (tt.want == nil) != !ok || !slices.Equal(got, tt.want) {
			t.Errorf("modelVersion(%q) = %v, %v; want %v", tt.key, got, ok, tt.want)
		}
	}
}

func TestSortModelsByVersionNumericNewestFirst(t *testing.T) {
	in := []string{
		"g/gemini-4.9-flash",
		"g/gemini-4.10-flash",
		"g/gemini-4-flash",
		"g/gemini-4.2-flash",
		"g/gemini-10-flash",
		"g/gemini-flash-latest",
		"g/gemini-2024-flash",
	}
	want := []string{
		"g/gemini-10-flash",
		"g/gemini-4.10-flash", // 4.10 > 4.9: numeric, not lexicographic
		"g/gemini-4.9-flash",
		"g/gemini-4.2-flash",
		"g/gemini-4-flash",
		"g/gemini-2024-flash", // a date is never a version: unversioned, last
		"g/gemini-flash-latest",
	}
	got := slices.Clone(in)
	sortModelsByVersion(got)
	if !slices.Equal(got, want) {
		t.Fatalf("order = %v\nwant    %v", got, want)
	}
}

func TestSortModelsByVersionDeterministicTies(t *testing.T) {
	// Same version on several providers, and 4 vs 4.0 (equal): the key breaks
	// the tie, whatever the input order.
	want := []string{
		"a/glm-5.3",
		"b/glm-5.3",
		"z/glm-5.3",
		"a/glm-5",
		"b/glm-5.0",
		"a/glm-air",
		"b/glm-air",
	}
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 50; i++ {
		got := slices.Clone(want)
		r.Shuffle(len(got), func(i, j int) { got[i], got[j] = got[j], got[i] })
		sortModelsByVersion(got)
		if !slices.Equal(got, want) {
			t.Fatalf("run %d: order = %v, want %v", i, got, want)
		}
	}
}

func TestAutoFamilyOrdersMembersByVersion(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)
	seedConnection(t, repo.RawDB(), "gemini")
	setCustomModel(t, repo, "gemini", "gemini-40.9-flash")
	setCustomModel(t, repo, "gemini", "gemini-40.10-flash")

	if rec := postJSON(t, router, "/api/combos/auto-family", nil); rec.Code != http.StatusOK {
		t.Fatalf("auto-family: %d %s", rec.Code, rec.Body.String())
	}
	members, err := comboModels(mustCombo(t, repo, autoFamilyComboID("gemini-flash")).Models)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) < 2 || members[0] != "gemini/gemini-40.10-flash" || members[1] != "gemini/gemini-40.9-flash" {
		t.Fatalf("members must start with 40.10 then 40.9 (numeric, newest first): %v", members)
	}
	for i := 1; i < len(members); i++ {
		if newerModelFirst(members[i], members[i-1]) {
			t.Errorf("members out of order at %d: %v", i, members)
		}
	}
}

func TestAutoFreeOrdersNewestFirst(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)
	seedConnection(t, repo.RawDB(), "opencode")
	if rec := postJSON(t, router, "/api/combos/auto-free", nil); rec.Code != http.StatusOK {
		t.Fatalf("auto-free: %d %s", rec.Code, rec.Body.String())
	}
	members, err := comboModels(mustCombo(t, repo, AutoFreeComboID).Models)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(members, ocFreeModels) {
		t.Fatalf("members = %v, want %v (1.3 before 1.2)", members, ocFreeModels)
	}
}
