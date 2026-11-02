package chat

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"9router/proxy/internal/db"
	"9router/proxy/internal/deepseekprofile"
	"9router/proxy/internal/log"
)

// All upstreams below are httptest servers; the real DSH and InferX are never
// contacted.

const dsTestSecret = "sk-test-inferx-NOT-REAL-c0ffee42" // fake value, test only

func dsEnv(m map[string]string) deepseekprofile.Env {
	return deepseekprofile.Env{Getenv: func(k string) string { return m[k] }, ReadFile: func(string) ([]byte, error) { return nil, fmt.Errorf("no file") }}
}

type dsUpstream struct {
	srv  *httptest.Server
	hits atomic.Int64
}

func newDSUpstream(t *testing.T, h http.HandlerFunc) *dsUpstream {
	t.Helper()
	u := &dsUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.hits.Add(1)
		_, _ = io.ReadAll(r.Body)
		h(w, r)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func dsJSON(body string, code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	}
}

const (
	dsZeroUsage  = `{"id":"a","choices":[{"index":0,"message":{"role":"assistant","content":"respuesta de DSH con varias palabras"},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`
	dsKnownUsage = `{"id":"b","choices":[{"index":0,"message":{"role":"assistant","content":"respuesta de InferX"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`
	dsReq        = `{"model":"deepseek-profile","messages":[{"role":"user","content":"hola"}]}`
)

// dsRepo returns a test repo whose only usable connection is a regular
// deepseek one pointing at regular (the pre-existing, non-profile path).
func dsRepo(t *testing.T, regularURL string) *db.Repo {
	t.Helper()
	database, cleanup := setupChatTestDB(t)
	t.Cleanup(cleanup)
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"apiKey": "sk-mock", "baseUrl": regularURL})
	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		('mock-ds', 'deepseek', 'apikey', 'Mock DS', 0, 1, ?, '2026-10-31T00:00:00Z', '2026-10-31T00:00:00Z')`, string(data)); err != nil {
		t.Fatal(err)
	}
	return db.NewRepo(database)
}

func dsProfileEnv(dshURL, infURL string) map[string]string {
	return map[string]string{
		deepseekprofile.EnvEnabled:      "1",
		deepseekprofile.EnvDSHURL:       dshURL + "/v1",
		deepseekprofile.EnvInferXURL:    infURL + "/v1",
		deepseekprofile.EnvInferXAPIKey: dsTestSecret,
	}
}

func dsPost(h *ChatHandler, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.HandleChatCompletions(rec, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body)))
	return rec
}

func dsModelIDs(t *testing.T, h *ChatHandler) map[string]bool {
	t.Helper()
	rec := httptest.NewRecorder()
	h.HandleModels(rec, httptest.NewRequest("GET", "/v1/models", nil))
	var res struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("models: %v", err)
	}
	ids := map[string]bool{}
	for _, m := range res.Data {
		ids[m.ID] = true
	}
	return ids
}

func TestDeepSeekProfile_OffByDefault_NothingChanges(t *testing.T) {
	t.Setenv(deepseekprofile.EnvEnabled, "")
	regular := newDSUpstream(t, dsJSON(dsKnownUsage, 200))
	dsh := newDSUpstream(t, dsJSON(dsZeroUsage, 200))
	h := NewChatHandler(dsRepo(t, regular.srv.URL))
	if h.deepseek != nil {
		t.Fatal("profile loaded although the opt-in flag is off")
	}
	if dsModelIDs(t, h)[deepseekprofile.DefaultModelID] {
		t.Fatal("/v1/models lists the profile while it is off")
	}
	// With the flag off the id is just another model name, resolved by the
	// existing router exactly as before (here: to the deepseek provider).
	rec := dsPost(h, dsReq)
	if dsh.hits.Load() != 0 || rec.Header().Get("X-Capimux-Provider") != "" {
		t.Fatal("profile intercepted a request while off")
	}
	before := regular.hits.Load()
	// Regression: the regular route keeps working.
	if rec := dsPost(h, `{"model":"deepseek/deepseek-chat","messages":[{"role":"user","content":"hola"}]}`); rec.Code != 200 || regular.hits.Load() != before+1 {
		t.Fatalf("regular deepseek route broke: %d %s", rec.Code, rec.Body.String())
	}
}

func TestDeepSeekProfile_RollbackRestoresPreviousBehaviour(t *testing.T) {
	regular := newDSUpstream(t, dsJSON(dsKnownUsage, 200))
	dsh := newDSUpstream(t, dsJSON(dsZeroUsage, 200))
	inf := newDSUpstream(t, dsJSON(dsKnownUsage, 200))
	h := NewChatHandler(dsRepo(t, regular.srv.URL))

	off := dsPost(h, dsReq)
	h.SetDeepSeekProfileEnv(dsEnv(dsProfileEnv(dsh.srv.URL, inf.srv.URL)))
	if on := dsPost(h, dsReq); on.Code != 200 || dsh.hits.Load() != 1 {
		t.Fatalf("profile on: %d %s", on.Code, on.Body.String())
	}
	h.SetDeepSeekProfileEnv(dsEnv(map[string]string{deepseekprofile.EnvEnabled: "0"}))
	again := dsPost(h, dsReq)
	if again.Code != off.Code || again.Body.String() != off.Body.String() || dsh.hits.Load() != 1 {
		t.Fatalf("after disabling: %d %q, want %d %q (dsh hits %d)", again.Code, again.Body.String(), off.Code, off.Body.String(), dsh.hits.Load())
	}
	if dsModelIDs(t, h)[deepseekprofile.DefaultModelID] {
		t.Fatal("/v1/models still lists the profile after disabling it")
	}
}

func TestDeepSeekProfile_OnLeavesOtherModelsAlone(t *testing.T) {
	regular := newDSUpstream(t, dsJSON(dsKnownUsage, 200))
	dsh := newDSUpstream(t, dsJSON(dsZeroUsage, 200))
	inf := newDSUpstream(t, dsJSON(dsKnownUsage, 200))
	h := NewChatHandler(dsRepo(t, regular.srv.URL))
	h.SetDeepSeekProfileEnv(dsEnv(dsProfileEnv(dsh.srv.URL, inf.srv.URL)))
	if !dsModelIDs(t, h)[deepseekprofile.DefaultModelID] {
		t.Fatal("/v1/models does not list the enabled profile")
	}
	rec := dsPost(h, `{"model":"deepseek/deepseek-chat","messages":[{"role":"user","content":"hola"}]}`)
	if rec.Code != 200 || regular.hits.Load() != 1 || dsh.hits.Load() != 0 || inf.hits.Load() != 0 {
		t.Fatalf("regular model rerouted: %d regular=%d dsh=%d inferx=%d", rec.Code, regular.hits.Load(), dsh.hits.Load(), inf.hits.Load())
	}
}

func TestDeepSeekProfile_ZeroTokensAreUnknownNotFree(t *testing.T) {
	regular := newDSUpstream(t, dsJSON(dsKnownUsage, 200))
	dsh := newDSUpstream(t, dsJSON(dsZeroUsage, 200))
	inf := newDSUpstream(t, dsJSON(dsKnownUsage, 200))
	repo := dsRepo(t, regular.srv.URL)
	h := NewChatHandler(repo)
	h.SetDeepSeekProfileEnv(dsEnv(dsProfileEnv(dsh.srv.URL, inf.srv.URL)))

	if rec := dsPost(h, dsReq); rec.Code != 200 {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	rows, err := repo.GetRecentUsageHistory(10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("usage rows = %d err=%v", len(rows), err)
	}
	r := rows[0]
	if r.Provider != deepseekprofile.ProviderDSH || r.Status != "success" {
		t.Fatalf("row %+v", r)
	}
	if r.PromptTokens == 0 || r.CompletionTokens == 0 {
		t.Fatalf("zero DSH usage stored as 0 tokens: %+v", r)
	}
	var tokens map[string]any
	_ = json.Unmarshal([]byte(r.Tokens), &tokens)
	if tokens["estimated"] != true || tokens["usage_unknown"] != true || tokens["cost_unknown"] != true {
		t.Fatalf("row not flagged unknown/estimated: %s", r.Tokens)
	}
	daily, _ := repo.GetUsageDailyRecent(10)
	if len(daily) != 0 {
		t.Fatalf("unknown usage folded into daily totals: %v", daily)
	}
	details, _, _ := repo.GetRequestDetailsPaged(10, 0)
	if len(details) != 1 || !strings.Contains(details[0], `"costUnknown":true`) || strings.Contains(details[0], "hola") {
		t.Fatalf("request detail not flagged, or content stored without opt-in: %v", details)
	}
}

func TestDeepSeekProfile_KnownUsageIsRecordedNormally(t *testing.T) {
	regular := newDSUpstream(t, dsJSON(dsKnownUsage, 200))
	dsh := newDSUpstream(t, dsJSON(`{"error":{"message":"busy"}}`, 503))
	inf := newDSUpstream(t, dsJSON(dsKnownUsage, 200))
	repo := dsRepo(t, regular.srv.URL)
	h := NewChatHandler(repo)
	h.SetDeepSeekProfileEnv(dsEnv(dsProfileEnv(dsh.srv.URL, inf.srv.URL)))

	if rec := dsPost(h, dsReq); rec.Code != 200 || !strings.Contains(rec.Body.String(), "InferX") {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	rows, _ := repo.GetRecentUsageHistory(10)
	if len(rows) != 1 || rows[0].Provider != deepseekprofile.ProviderInferX || rows[0].PromptTokens != 11 || rows[0].CompletionTokens != 7 {
		t.Fatalf("rows %+v", rows)
	}
	if strings.Contains(rows[0].Tokens, "usage_unknown") {
		t.Fatalf("known usage flagged unknown: %s", rows[0].Tokens)
	}
	for _, l := range log.ConsoleLogs() {
		if strings.Contains(l, dsTestSecret) {
			t.Fatalf("secret in logs: %s", l)
		}
	}
}

func TestDeepSeekProfile_MisconfiguredFailsClosed(t *testing.T) {
	regular := newDSUpstream(t, dsJSON(dsKnownUsage, 200))
	inf := newDSUpstream(t, dsJSON(dsKnownUsage, 200))
	h := NewChatHandler(dsRepo(t, regular.srv.URL))
	// Enabled without a DSH URL: must not silently send everything to InferX.
	h.SetDeepSeekProfileEnv(dsEnv(map[string]string{
		deepseekprofile.EnvEnabled:      "1",
		deepseekprofile.EnvInferXURL:    inf.srv.URL + "/v1",
		deepseekprofile.EnvInferXAPIKey: dsTestSecret,
	}))
	rec := dsPost(h, dsReq)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "mal configurado") || inf.hits.Load() != 0 || regular.hits.Load() != 0 {
		t.Fatalf("got %d %s inferx=%d", rec.Code, rec.Body.String(), inf.hits.Load())
	}
	if strings.Contains(rec.Body.String(), dsTestSecret) {
		t.Fatal("secret in error body")
	}
	if dsModelIDs(t, h)[deepseekprofile.DefaultModelID] {
		t.Fatal("misconfigured profile listed in /v1/models")
	}
	for _, l := range log.ConsoleLogs() {
		if strings.Contains(l, dsTestSecret) {
			t.Fatalf("secret in logs: %s", l)
		}
	}
}

func TestDeepSeekProfile_FailureIsNotBilled(t *testing.T) {
	regular := newDSUpstream(t, dsJSON(dsKnownUsage, 200))
	dsh := newDSUpstream(t, dsJSON(`{"error":{"message":"bad"}}`, 400))
	inf := newDSUpstream(t, dsJSON(dsKnownUsage, 200))
	repo := dsRepo(t, regular.srv.URL)
	h := NewChatHandler(repo)
	h.SetDeepSeekProfileEnv(dsEnv(dsProfileEnv(dsh.srv.URL, inf.srv.URL)))
	if rec := dsPost(h, dsReq); rec.Code != 400 || inf.hits.Load() != 0 {
		t.Fatalf("got %d inferx=%d", rec.Code, inf.hits.Load())
	}
	if rows, _ := repo.GetRecentUsageHistory(10); len(rows) != 0 {
		t.Fatalf("failed request billed: %+v", rows)
	}
}
