//go:build integration

package integration

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	json "encoding/json/v2"

	"9router/proxy/internal/auth"
)

func enableQuotas(t *testing.T, env *Env) {
	t.Helper()
	if res := env.Put(t, "/api/api-key-limits/toggle", map[string]any{"enabled": true}, operator(t)...); res.Status != http.StatusOK {
		t.Fatalf("toggle = %d %s", res.Status, truncate(res.Body))
	}
}

func putLimits(t *testing.T, env *Env, id string, lim map[string]any) {
	t.Helper()
	if res := env.Put(t, "/api/api-key-limits/"+id, lim, operator(t)...); res.Status != http.StatusOK {
		t.Fatalf("PUT limits = %d %s", res.Status, truncate(res.Body))
	}
}

type quotaList struct {
	Enabled bool `json:"enabled"`
	Keys    []struct {
		ID     string         `json:"id"`
		Limits map[string]any `json:"limits"`
		Usage  struct {
			Inflight    int   `json:"inflight"`
			Tokens24h   int64 `json:"tokens24h"`
			Requests24h int64 `json:"requests24h"`
		} `json:"usage"`
	} `json:"keys"`
}

func readQuotas(t *testing.T, env *Env) quotaList {
	t.Helper()
	res := env.Get(t, "/api/api-key-limits", operator(t)...)
	if res.Status != http.StatusOK {
		t.Fatalf("GET limits = %d %s", res.Status, truncate(res.Body))
	}
	var out quotaList
	if err := json.Unmarshal(res.Body, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(res.Body), testAPIKey) {
		t.Fatal("la lista de cuotas expone el secreto de la API key")
	}
	return out
}

// Off by default: stored limits change nothing until the owner enables them.
func TestKeyQuotasOffByDefault(t *testing.T) {
	env, upstream := newProviderEnv(t)
	putLimits(t, env, "integration-key", map[string]any{"rpm": 1})
	for i := 0; i < 3; i++ {
		if res := env.Post(t, "/v1/chat/completions", ChatBody("deepseek/deepseek-chat", false)); res.Status != http.StatusOK {
			t.Fatalf("chat %d = %d %s", i, res.Status, truncate(res.Body))
		}
	}
	if upstream.Count() != 3 {
		t.Fatalf("upstream = %d", upstream.Count())
	}
}

// RPM: the third request in the minute is refused with 429 + Retry-After and
// never reaches the provider.
func TestKeyQuotaRPMReturns429(t *testing.T) {
	env, upstream := newProviderEnv(t)
	enableQuotas(t, env)
	putLimits(t, env, "integration-key", map[string]any{"rpm": 2})
	for i := 0; i < 2; i++ {
		if res := env.Post(t, "/v1/chat/completions", ChatBody("deepseek/deepseek-chat", false)); res.Status != http.StatusOK {
			t.Fatalf("chat %d = %d %s", i, res.Status, truncate(res.Body))
		}
	}
	res := env.Post(t, "/v1/chat/completions", ChatBody("deepseek/deepseek-chat", false))
	if res.Status != http.StatusTooManyRequests || res.Header.Get("Retry-After") == "" {
		t.Fatalf("tercera = %d retry=%q %s", res.Status, res.Header.Get("Retry-After"), truncate(res.Body))
	}
	if !strings.Contains(string(res.Body), "api_key_rpm_exceeded") {
		t.Fatalf("cuerpo: %s", truncate(res.Body))
	}
	// Anthropic Messages gets the Anthropic error shape.
	res = env.Post(t, "/v1/messages", claudeMessagesBody("deepseek/deepseek-chat", false))
	if res.Status != http.StatusTooManyRequests || !strings.Contains(string(res.Body), `"type":"error"`) {
		t.Fatalf("messages = %d %s", res.Status, truncate(res.Body))
	}
	if upstream.Count() != 2 {
		t.Fatalf("el proveedor recibió %d solicitudes, se esperaban 2", upstream.Count())
	}
}

// 24h token quota: the upstream reports 18 tokens; with a quota of 18 the
// second request is refused, and the usage is persisted per key.
func TestKeyQuotaTokens24hAndPersistence(t *testing.T) {
	env, upstream := newProviderEnv(t)
	enableQuotas(t, env)
	putLimits(t, env, "integration-key", map[string]any{"tokens24h": 18})
	if res := env.Post(t, "/v1/chat/completions", ChatBody("deepseek/deepseek-chat", false)); res.Status != http.StatusOK {
		t.Fatalf("primera = %d %s", res.Status, truncate(res.Body))
	}
	res := env.Post(t, "/v1/chat/completions", ChatBody("deepseek/deepseek-chat", false))
	if res.Status != http.StatusTooManyRequests || !strings.Contains(string(res.Body), "insufficient_quota") {
		t.Fatalf("segunda = %d %s", res.Status, truncate(res.Body))
	}
	if upstream.Count() != 1 {
		t.Fatalf("upstream = %d", upstream.Count())
	}
	q := readQuotas(t, env)
	found := false
	for _, k := range q.Keys {
		if k.ID == "integration-key" {
			found = true
			if k.Usage.Tokens24h != 18 || k.Usage.Requests24h != 1 || k.Usage.Inflight != 0 {
				t.Fatalf("uso = %+v", k.Usage)
			}
		}
	}
	if !found {
		t.Fatal("la key no aparece en la lista")
	}
	buckets, err := env.Repo.GetApiKeyUsageBuckets(0)
	if err != nil || len(buckets) != 1 || buckets[0].Tokens != 18 || buckets[0].KeyID != "integration-key" {
		t.Fatalf("persistido = %+v err=%v", buckets, err)
	}
}

// Streaming usage counts too.
func TestKeyQuotaCountsStreamingUsage(t *testing.T) {
	env := newEnv(t)
	up := env.NewUpstream(t, streamResponder())
	env.AddConnection(t, "conn-deepseek", "deepseek", "DeepSeek Integration", up, "sk-upstream")
	enableQuotas(t, env)
	putLimits(t, env, "integration-key", map[string]any{"tokens24h": 1000})
	if res := env.Post(t, "/v1/chat/completions", ChatBody("deepseek/deepseek-chat", true)); res.Status != http.StatusOK {
		t.Fatalf("stream = %d %s", res.Status, truncate(res.Body))
	}
	q := readQuotas(t, env)
	for _, k := range q.Keys {
		if k.ID == "integration-key" && k.Usage.Tokens24h != 18 {
			t.Fatalf("tokens de streaming = %d, se esperaban 18", k.Usage.Tokens24h)
		}
	}
}

// Concurrency: while one request is held by the provider, a second one from
// the same key gets 429.
func TestKeyQuotaConcurrency(t *testing.T) {
	env := newEnv(t)
	release := make(chan struct{})
	entered := make(chan struct{}, 4)
	up := env.NewUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		chatCompletionResponder()(w, r)
	})
	env.AddConnection(t, "conn-deepseek", "deepseek", "DeepSeek Integration", up, "sk-upstream")
	enableQuotas(t, env)
	putLimits(t, env, "integration-key", map[string]any{"concurrency": 1})
	var wg sync.WaitGroup
	wg.Add(1)
	var first *Result
	go func() {
		defer wg.Done()
		first = env.Post(t, "/v1/chat/completions", ChatBody("deepseek/deepseek-chat", false))
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("la primera solicitud no llegó al proveedor")
	}
	second := env.Post(t, "/v1/chat/completions", ChatBody("deepseek/deepseek-chat", false))
	close(release)
	wg.Wait()
	if second.Status != http.StatusTooManyRequests || !strings.Contains(string(second.Body), "api_key_concurrency_exceeded") {
		t.Fatalf("segunda simultánea = %d %s", second.Status, truncate(second.Body))
	}
	if first.Status != http.StatusOK {
		t.Fatalf("primera = %d", first.Status)
	}
	if res := env.Post(t, "/v1/chat/completions", ChatBody("deepseek/deepseek-chat", false)); res.Status != http.StatusOK {
		t.Fatalf("tras liberar = %d %s", res.Status, truncate(res.Body))
	}
}

// Negative: a client API key cannot read or change any quota (its own, another
// key's, or the switch), with the dashboard login on or off.
func TestKeyQuotasAdminOnlyForClientKeys(t *testing.T) {
	for _, requireLogin := range []bool{false, true} {
		t.Run(map[bool]string{false: "login-apagado", true: "login-encendido"}[requireLogin], func(t *testing.T) {
			env, _ := newProviderEnv(t)
			if err := env.Repo.CreateApiKey("otra-key", "sk-otra-key-integracion", "Otra", "m"); err != nil {
				t.Fatal(err)
			}
			putLimits(t, env, "integration-key", map[string]any{"rpm": 5})
			if err := env.Repo.UpdateSettingsRaw(map[string]any{"requireLogin": requireLogin}); err != nil {
				t.Fatal(err)
			}
			cases := []struct {
				method, path string
				body         any
			}{
				{http.MethodGet, "/api/api-key-limits", nil},
				{http.MethodPut, "/api/api-key-limits/integration-key", map[string]any{"rpm": 100000}},
				{http.MethodDelete, "/api/api-key-limits/integration-key", nil},
				{http.MethodPut, "/api/api-key-limits/otra-key", map[string]any{"rpm": 1}},
				{http.MethodDelete, "/api/api-key-limits/otra-key", nil},
				{http.MethodPut, "/api/api-key-limits/toggle", map[string]any{"enabled": false}},
			}
			for _, c := range cases {
				if res := env.Do(t, c.method, c.path, c.body); res.Status != http.StatusForbidden {
					t.Errorf("%s %s con API key = %d, se esperaba 403 (%s)", c.method, c.path, res.Status, truncate(res.Body))
				}
			}
			kv, _ := env.Repo.GetKVScope("apiKeyLimits")
			if len(kv) != 1 || !strings.Contains(kv["integration-key"], `"rpm":5`) {
				t.Errorf("la API key modificó límites: %v", kv)
			}
			if res := env.Get(t, "/api/api-key-limits", WithoutAPIKey(), WithHeader(auth.CLITokenHeader, auth.CLIToken())); res.Status != http.StatusOK {
				t.Errorf("token CLI = %d", res.Status)
			}
		})
	}
}

func TestKeyQuotaValidation(t *testing.T) {
	env, _ := newProviderEnv(t)
	if res := env.Put(t, "/api/api-key-limits/integration-key", map[string]any{"rpm": -1}, operator(t)...); res.Status != http.StatusBadRequest {
		t.Fatalf("rpm negativo = %d", res.Status)
	}
	if res := env.Put(t, "/api/api-key-limits/no-existe", map[string]any{"rpm": 1}, operator(t)...); res.Status != http.StatusNotFound {
		t.Fatalf("key inexistente = %d", res.Status)
	}
	if res := env.Put(t, "/api/api-key-limits/_enabled", map[string]any{"rpm": 1}, operator(t)...); res.Status != http.StatusNotFound {
		t.Fatalf("id reservado = %d", res.Status)
	}
}
