//go:build integration

package integration

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"9router/proxy/internal/deepseekprofile"
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
