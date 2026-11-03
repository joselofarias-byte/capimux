//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"9router/proxy/internal/auth"
	"9router/proxy/internal/deepseekprofile"
	"9router/proxy/internal/deepseekweb"
	"9router/proxy/internal/log"
)

// The DeepSeek profile through the real router (auth middleware, routes,
// usage ledger). Both upstreams are httptest servers on 127.0.0.1: the real
// DSH (Oracle Lab) and InferX are never contacted.

const integrationFakeInferXKey = "sk-test-inferx-NOT-REAL-integration" // fake, test only

type countingServer struct {
	*httptest.Server
	hits atomic.Int64
	auth atomic.Value
}

func newCountingServer(t *testing.T, h http.HandlerFunc) *countingServer {
	t.Helper()
	c := &countingServer{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.hits.Add(1)
		c.auth.Store(r.Header.Get("Authorization"))
		_, _ = io.ReadAll(r.Body)
		h(w, r)
	}))
	t.Cleanup(c.Close)
	return c
}

func enableDeepSeekProfile(t *testing.T, dsh, inf string) {
	t.Setenv(deepseekprofile.EnvEnabled, "1")
	t.Setenv(deepseekprofile.EnvDSHURL, dsh+"/v1")
	t.Setenv(deepseekprofile.EnvInferXURL, inf+"/v1")
	t.Setenv(deepseekprofile.EnvInferXAPIKey, integrationFakeInferXKey)
	t.Setenv(deepseekprofile.EnvDSHTimeout, "2s")
	t.Setenv(deepseekprofile.EnvInferXTimeout, "2s")
}

func TestDeepSeekProfile_StreamFallsBackOnlyBeforeStart(t *testing.T) {
	dsh := newCountingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	inf := newCountingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"desde inferx\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2,\"total_tokens\":6}}\n\ndata: [DONE]\n\n")
	})
	enableDeepSeekProfile(t, dsh.URL, inf.URL)
	env := newEnv(t)
	connectWebSessionAndEnableFallback(t, env)

	res := env.Post(t, "/v1/chat/completions", ChatBody(deepseekprofile.DefaultModelID, true))
	if res.Status != http.StatusOK || !strings.Contains(string(res.Body), "desde inferx") || !strings.Contains(string(res.Body), "[DONE]") {
		t.Fatalf("status %d body %s", res.Status, truncate(res.Body))
	}
	if dsh.hits.Load() != 1 || inf.hits.Load() != 1 {
		t.Fatalf("hits dsh=%d inferx=%d, want 1/1", dsh.hits.Load(), inf.hits.Load())
	}
	if got, _ := inf.auth.Load().(string); got != "Bearer "+integrationFakeInferXKey {
		t.Fatal("InferX did not get the configured Bearer")
	}
	rows, err := env.Repo.GetRecentUsageHistory(10)
	if err != nil || len(rows) != 1 || rows[0].Provider != deepseekprofile.ProviderInferX || rows[0].PromptTokens != 4 {
		t.Fatalf("usage rows %+v err=%v", rows, err)
	}
	if strings.Contains(string(res.Body), integrationFakeInferXKey) {
		t.Fatal("secret in response")
	}
}

func TestDeepSeekProfile_NoFallbackOn401AndRequiresAuth(t *testing.T) {
	dsh := newCountingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"no"}}`)
	})
	inf := newCountingServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	enableDeepSeekProfile(t, dsh.URL, inf.URL)
	env := newEnv(t)

	if res := env.Post(t, "/v1/chat/completions", ChatBody(deepseekprofile.DefaultModelID, false), WithoutAPIKey()); res.Status != http.StatusUnauthorized || dsh.hits.Load() != 0 {
		t.Fatalf("unauthenticated request reached the profile: %d dsh=%d", res.Status, dsh.hits.Load())
	}
	res := env.Post(t, "/v1/chat/completions", ChatBody(deepseekprofile.DefaultModelID, false))
	if res.Status != http.StatusUnauthorized || dsh.hits.Load() != 1 || inf.hits.Load() != 0 {
		t.Fatalf("status %d dsh=%d inferx=%d", res.Status, dsh.hits.Load(), inf.hits.Load())
	}
}

func TestDeepSeekProfile_DisabledLeavesRouterAsBefore(t *testing.T) {
	t.Setenv(deepseekprofile.EnvEnabled, "")
	env, upstream := newProviderEnv(t)
	res := env.Get(t, "/v1/models")
	if strings.Contains(string(res.Body), `"`+deepseekprofile.DefaultModelID+`"`) {
		t.Fatal("/v1/models lists the profile while it is off")
	}
	if res := env.Post(t, "/v1/chat/completions", ChatBody("deepseek/deepseek-chat", false)); res.Status != http.StatusOK {
		t.Fatalf("regular route: %d %s", res.Status, truncate(res.Body))
	}
	_ = upstream
}

// Fake DeepSeek Web session credential, test only; the checker is a mock.
const integrationFakeWebSession = "fake-dsh-web-session-NOT-REAL-integration"

type mockWebChecker struct{}

func (mockWebChecker) Check(_ context.Context, _ string, cred []byte) deepseekweb.CheckResult {
	if string(cred) == integrationFakeWebSession {
		return deepseekweb.CheckResult{Status: deepseekweb.StatusConnected}
	}
	return deepseekweb.CheckResult{Status: deepseekweb.StatusError, Detail: "rechazada"}
}

func useMockWebSessions(t *testing.T) *deepseekweb.Manager {
	t.Helper()
	m, err := deepseekweb.Open(deepseekweb.Options{Dir: t.TempDir(), Checker: mockWebChecker{}})
	if err != nil {
		t.Fatal(err)
	}
	deepseekweb.SetDefault(m)
	t.Cleanup(func() { deepseekweb.SetDefault(nil) })
	return m
}

func adminCLI() RequestOpt { return WithHeader(auth.CLITokenHeader, auth.CLIToken()) }

// connectWebSessionAndEnableFallback goes through the admin API exactly as the
// panel does: add a session, check it (mock), then enable the InferX toggle.
func connectWebSessionAndEnableFallback(t *testing.T, env *Env) {
	t.Helper()
	useMockWebSessions(t)
	res := env.Post(t, "/api/admin/deepseek-web/sessions", map[string]any{"label": "integración", "credential": integrationFakeWebSession}, WithoutAPIKey(), adminCLI())
	if res.Status != http.StatusCreated {
		t.Fatalf("create session: %d %s", res.Status, truncate(res.Body))
	}
	var s deepseekweb.SessionInfo
	_ = json.Unmarshal(res.Body, &s)
	if res = env.Post(t, "/api/admin/deepseek-web/sessions/"+s.ID+"/check", nil, WithoutAPIKey(), adminCLI()); res.Status != http.StatusOK {
		t.Fatalf("check: %d", res.Status)
	}
	if res = env.Put(t, "/api/admin/deepseek-web/fallback", map[string]any{"enabled": true}, WithoutAPIKey(), adminCLI()); res.Status != http.StatusOK || !strings.Contains(string(res.Body), `"effective":true`) {
		t.Fatalf("fallback toggle: %d %s", res.Status, truncate(res.Body))
	}
}

func TestDeepSeekWeb_AdminAPIRejectsEngineKeysEvenWithOpenDashboard(t *testing.T) {
	env := newEnv(t)
	m := useMockWebSessions(t)
	if err := env.Repo.UpdateSettingsRaw(map[string]any{"requireLogin": false}); err != nil {
		t.Fatal(err)
	}
	for _, opt := range []RequestOpt{WithAPIKey(env.APIKey), WithoutAPIKey(), WithAPIKey("sk-infer-fake")} {
		if res := env.Get(t, "/api/admin/deepseek-web/sessions", opt); res.Status != http.StatusUnauthorized {
			t.Fatalf("list: %d", res.Status)
		}
		if res := env.Post(t, "/api/admin/deepseek-web/sessions", map[string]any{"label": "x", "credential": integrationFakeWebSession}, opt); res.Status != http.StatusUnauthorized {
			t.Fatalf("create: %d", res.Status)
		}
		if res := env.Put(t, "/api/admin/deepseek-web/fallback", map[string]any{"enabled": true}, opt); res.Status != http.StatusUnauthorized {
			t.Fatalf("fallback: %d", res.Status)
		}
	}
	if len(m.List()) != 0 || m.Fallback().Requested {
		t.Fatal("engine key changed DeepSeek Web state")
	}
}

func TestDeepSeekWeb_NoFallbackWithoutToggleAndSecretsStayOut(t *testing.T) {
	dsh := newCountingServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	inf := newCountingServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	enableDeepSeekProfile(t, dsh.URL, inf.URL)
	env := newEnv(t)
	useMockWebSessions(t)
	log.ClearConsoleLogs()
	res := env.Post(t, "/api/admin/deepseek-web/sessions", map[string]any{"label": "a", "credential": integrationFakeWebSession}, WithoutAPIKey(), adminCLI())
	var s deepseekweb.SessionInfo
	_ = json.Unmarshal(res.Body, &s)
	env.Post(t, "/api/admin/deepseek-web/sessions/"+s.ID+"/check", nil, WithoutAPIKey(), adminCLI())
	// Connected session but no explicit toggle: InferX is not used.
	if res := env.Post(t, "/v1/chat/completions", ChatBody(deepseekprofile.DefaultModelID, false)); res.Status != http.StatusServiceUnavailable || inf.hits.Load() != 0 {
		t.Fatalf("status %d inferx=%d", res.Status, inf.hits.Load())
	}
	list := env.Get(t, "/api/admin/deepseek-web/sessions", WithoutAPIKey(), adminCLI())
	for _, b := range [][]byte{res.Body, list.Body} {
		if strings.Contains(string(b), integrationFakeWebSession) {
			t.Fatal("credential in a response")
		}
	}
	for _, l := range log.ConsoleLogs() {
		if strings.Contains(l, integrationFakeWebSession) {
			t.Fatal("credential in logs")
		}
	}
}
