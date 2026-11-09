//go:build integration

package integration

import (
	"net/http"
	"testing"
)

func dropAfterSend(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }

// refusedUpstream returns an Upstream whose address refuses connections, so
// the request provably never leaves the box.
func refusedUpstream(t *testing.T, env *Env) *Upstream {
	t.Helper()
	up := env.NewUpstream(t, chatCompletionResponder())
	up.Server.Close()
	return up
}

func mediaComboEnv(t *testing.T, first http.HandlerFunc, closeFirst bool) (*Env, *Upstream, *Upstream) {
	t.Helper()
	env := newEnv(t)
	a := env.NewUpstream(t, first)
	if closeFirst {
		a = refusedUpstream(t, env)
	}
	b := env.NewUpstream(t, JSONResponder(http.StatusOK, `{"created":1,"data":[{"url":"https://example.invalid/x.png"}]}`))
	env.AddConnection(t, "conn-a", "deepseek", "A", a, "sk-a")
	env.AddConnection(t, "conn-b", "groq", "B", b, "sk-b")
	if err := env.Repo.CreateCombo("combo-m", "medios", "llm", `["deepseek/modelo-a","groq/modelo-b"]`, "fallback"); err != nil {
		t.Fatal(err)
	}
	return env, a, b
}

// Billable creation (images, videos) through a combo: an ambiguous failure on
// the first member must not be repeated on the second.
func TestNonIdempotentMediaComboNeverDuplicates(t *testing.T) {
	for _, ep := range []string{"/v1/images/generations", "/v1/videos/generations"} {
		for _, c := range []struct {
			name  string
			first http.HandlerFunc
		}{
			{"corte tras enviar", dropAfterSend},
			{"500", JSONResponder(http.StatusInternalServerError, `{"error":{"message":"x"}}`)},
			{"503", JSONResponder(http.StatusServiceUnavailable, `{"error":{"message":"x"}}`)},
		} {
			t.Run(ep+"/"+c.name, func(t *testing.T) {
				env, a, b := mediaComboEnv(t, c.first, false)
				res := env.Post(t, ep, map[string]any{"model": "medios", "prompt": "un faro al atardecer"})
				if a.Count() != 1 || b.Count() != 0 {
					t.Fatalf("intentos a=%d b=%d (se esperaba 1/0) status=%d %s", a.Count(), b.Count(), res.Status, truncate(res.Body))
				}
				if res.Status < 500 {
					t.Fatalf("status = %d, se esperaba error 5xx", res.Status)
				}
			})
		}
	}
}

// Failures that prove nothing was created still rotate: 4xx and connection
// refused (the request never left).
func TestNonIdempotentMediaComboRotatesOnSafeFailures(t *testing.T) {
	for _, c := range []struct {
		name   string
		first  http.HandlerFunc
		closed bool
	}{
		{"429", JSONResponder(http.StatusTooManyRequests, `{"error":{"message":"x"}}`), false},
		{"401", JSONResponder(http.StatusUnauthorized, `{"error":{"message":"x"}}`), false},
		{"conexión rechazada", nil, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			env, _, b := mediaComboEnv(t, c.first, c.closed)
			res := env.Post(t, "/v1/images/generations", map[string]any{"model": "medios", "prompt": "un faro"})
			if res.Status != http.StatusOK || b.Count() != 1 {
				t.Fatalf("status=%d b=%d %s", res.Status, b.Count(), truncate(res.Body))
			}
		})
	}
}

// Regression: chat generation has no side effects and keeps failing over
// after a cut, as before.
func TestChatComboStillFailsOverAfterCut(t *testing.T) {
	env := newEnv(t)
	a := env.NewUpstream(t, dropAfterSend)
	b := env.NewUpstream(t, chatCompletionResponder())
	env.AddConnection(t, "conn-a", "deepseek", "A", a, "sk-a")
	env.AddConnection(t, "conn-b", "groq", "B", b, "sk-b")
	if err := env.Repo.CreateCombo("combo-c", "chat-combo", "llm", `["deepseek/deepseek-chat","groq/llama-3.3-70b-versatile"]`, "fallback"); err != nil {
		t.Fatal(err)
	}
	res := env.Post(t, "/v1/chat/completions", ChatBody("chat-combo", false))
	if res.Status != http.StatusOK || a.Count() < 1 || b.Count() != 1 {
		t.Fatalf("status=%d a=%d b=%d %s", res.Status, a.Count(), b.Count(), truncate(res.Body))
	}
}
