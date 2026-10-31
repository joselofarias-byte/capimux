//go:build integration

package integration

import (
	"net/http"
	"testing"

	"9router/proxy/internal/auth"
	"9router/proxy/internal/combohealth"
)

func healthComboEnv(t *testing.T, strategy string, brokenHandler http.HandlerFunc) (*Env, *Upstream, *Upstream) {
	t.Helper()
	combohealth.Default.Reset()
	t.Cleanup(combohealth.Default.Reset)
	env := newEnv(t)
	broken := env.NewUpstream(t, brokenHandler)
	healthy := env.NewUpstream(t, chatCompletionResponder())
	env.AddConnection(t, "conn-a", "deepseek", "A", broken, "sk-a")
	env.AddConnection(t, "conn-b", "groq", "B", healthy, "sk-b")
	if err := env.Repo.CreateCombo("combo-h", "salud", "llm", `["deepseek/deepseek-chat","groq/llama-3.3-70b-versatile"]`, strategy); err != nil {
		t.Fatal(err)
	}
	return env, broken, healthy
}

// With "health", a member that keeps failing (503) stops being tried first:
// after the circuit opens, requests go straight to the healthy member.
// dropConnection simulates a transport failure: the upstream closes the
// connection without answering.
func dropConnection(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }

func failing(status int) http.HandlerFunc {
	return JSONResponder(status, `{"error":{"message":"falla simulada","type":"server_error"}}`)
}

func TestHealthStrategySkipsFailingMemberFirst(t *testing.T) {
	env, broken, healthy := healthComboEnv(t, "health", dropConnection)
	for i := 0; i < 6; i++ {
		if res := env.Post(t, "/v1/chat/completions", ChatBody("salud", false)); res.Status != http.StatusOK {
			t.Fatalf("solicitud %d = %d %s", i, res.Status, truncate(res.Body))
		}
	}
	if healthy.Count() != 6 {
		t.Fatalf("el miembro sano atendió %d de 6", healthy.Count())
	}
	t.Logf("intentos al miembro caído: %d de 6", broken.Count())
	if broken.Count() > 2 {
		t.Fatalf("el miembro caído se siguió probando primero: %d intentos", broken.Count())
	}
	res := env.Get(t, "/api/combo-health", operator(t)...)
	if res.Status != http.StatusOK {
		t.Fatalf("combo-health = %d", res.Status)
	}
}

// Regression: with "fallback" the configured order is kept (the failing first
// member is tried on every request).
func TestFallbackStrategyUnchanged(t *testing.T) {
	env, broken, healthy := healthComboEnv(t, "fallback", dropConnection)
	for i := 0; i < 4; i++ {
		if res := env.Post(t, "/v1/chat/completions", ChatBody("salud", false)); res.Status != http.StatusOK {
			t.Fatalf("solicitud %d = %d %s", i, res.Status, truncate(res.Body))
		}
	}
	t.Logf("fallback: caído=%d sano=%d", broken.Count(), healthy.Count())
	if healthy.Count() != 4 || broken.Count() < 4 {
		t.Fatalf("fallback cambió: caído=%d sano=%d", broken.Count(), healthy.Count())
	}
}

// Client errors (400) do not demote a member: they say nothing about the
// provider's health.
func TestHealthStrategyIgnoresClientErrors(t *testing.T) {
	env, _, _ := healthComboEnv(t, "health", failing(http.StatusBadRequest))
	for i := 0; i < 4; i++ {
		env.Post(t, "/v1/chat/completions", ChatBody("salud", false))
	}
	for _, m := range combohealth.Default.Snapshot() {
		if m.Key == "deepseek/deepseek-chat" && m.ConsecFails != 0 {
			t.Fatalf("un 400 contó como falla de salud: %+v", m)
		}
	}
}

// Negative: a client API key cannot read or reset the health tracker.
func TestComboHealthAdminOnly(t *testing.T) {
	for _, requireLogin := range []bool{false, true} {
		env, _ := newProviderEnv(t)
		if err := env.Repo.UpdateSettingsRaw(map[string]any{"requireLogin": requireLogin}); err != nil {
			t.Fatal(err)
		}
		for _, m := range []string{http.MethodGet, http.MethodDelete} {
			if res := env.Do(t, m, "/api/combo-health", nil); res.Status != http.StatusForbidden {
				t.Errorf("login=%v %s con API key = %d", requireLogin, m, res.Status)
			}
		}
		if res := env.Get(t, "/api/combo-health", WithoutAPIKey(), WithHeader(auth.CLITokenHeader, auth.CLIToken())); res.Status != http.StatusOK {
			t.Errorf("token CLI = %d", res.Status)
		}
	}
}

// Same behaviour on the Anthropic Messages combo loop.
func TestHealthStrategyMessagesEndpoint(t *testing.T) {
	env, broken, healthy := healthComboEnv(t, "health", dropConnection)
	for i := 0; i < 5; i++ {
		if res := env.Post(t, "/v1/messages", claudeMessagesBody("salud", false)); res.Status != http.StatusOK {
			t.Fatalf("solicitud %d = %d %s", i, res.Status, truncate(res.Body))
		}
	}
	if healthy.Count() != 5 || broken.Count() > 2 {
		t.Fatalf("messages: caído=%d sano=%d", broken.Count(), healthy.Count())
	}
}

// Streaming keeps working through the health ordering.
func TestHealthStrategyStreaming(t *testing.T) {
	env, _, healthy := healthComboEnv(t, "health", dropConnection)
	res := env.Post(t, "/v1/chat/completions", ChatBody("salud", true))
	if res.Status != http.StatusOK || healthy.Count() != 1 {
		t.Fatalf("stream = %d sano=%d %s", res.Status, healthy.Count(), truncate(res.Body))
	}
}
