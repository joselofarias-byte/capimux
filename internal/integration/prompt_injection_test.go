//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"
	"time"

	json "encoding/json/v2"

	"9router/proxy/internal/auth"
)

// enablePI turns the prompt-injection switches on as the dashboard owner.
func enablePI(t *testing.T, env *Env, toggles map[string]any) {
	t.Helper()
	if res := env.Put(t, "/api/prompt-injections/toggles", toggles, operator(t)...); res.Status != http.StatusOK {
		t.Fatalf("toggles = %d %s", res.Status, truncate(res.Body))
	}
}

func putLayer(t *testing.T, env *Env, layer map[string]any) {
	t.Helper()
	if res := env.Put(t, "/api/prompt-injections", layer, operator(t)...); res.Status != http.StatusOK {
		t.Fatalf("PUT layer = %d %s", res.Status, truncate(res.Body))
	}
}

func upstreamSystem(t *testing.T, body []byte) string {
	t.Helper()
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode upstream body: %v", err)
	}
	var parts []string
	for _, m := range req.Messages {
		if m.Role != "system" && m.Role != "developer" {
			continue
		}
		switch c := m.Content.(type) {
		case string:
			parts = append(parts, c)
		case []any:
			for _, p := range c {
				if mm, ok := p.(map[string]any); ok {
					if s, ok := mm["text"].(string); ok {
						parts = append(parts, s)
					}
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

// Off by default: a stored layer changes nothing until the owner enables it.
func TestPromptInjectionIsOffByDefault(t *testing.T) {
	env, upstream := newProviderEnv(t)
	putLayer(t, env, map[string]any{"scope": "global", "content": "Respondé en español, conciso.", "enabled": true})
	if res := env.Post(t, "/v1/chat/completions", ChatBody("deepseek/deepseek-chat", false)); res.Status != http.StatusOK {
		t.Fatalf("chat = %d %s", res.Status, truncate(res.Body))
	}
	if strings.Contains(string(upstream.Last(t).Body), "Respondé") {
		t.Fatal("la capa se aplicó con el interruptor apagado")
	}
}

// Global + API key layers reach the provider, for OpenAI chat and for
// Anthropic Messages translated to an OpenAI-compatible upstream.
func TestPromptInjectionReachesUpstreamForChatAndMessages(t *testing.T) {
	env, upstream := newProviderEnv(t)
	enablePI(t, env, map[string]any{"promptInjectionEnabled": true})
	putLayer(t, env, map[string]any{"scope": "global", "content": "Respondé en español, conciso.", "enabled": true})
	putLayer(t, env, map[string]any{"scope": "key", "target": "integration-key", "mode": "append", "content": "Usá viñetas.", "enabled": true})
	putLayer(t, env, map[string]any{"scope": "key", "target": "otra-key", "content": "Capa de otra key.", "enabled": true})

	if res := env.Post(t, "/v1/chat/completions", ChatBody("deepseek/deepseek-chat", false)); res.Status != http.StatusOK {
		t.Fatalf("chat = %d %s", res.Status, truncate(res.Body))
	}
	sys := upstreamSystem(t, upstream.Last(t).Body)
	if !strings.Contains(sys, "Respondé en español, conciso.") || !strings.Contains(sys, "Usá viñetas.") {
		t.Fatalf("sistema enviado = %q", sys)
	}
	if strings.Contains(sys, "otra key") {
		t.Fatal("se aplicó la capa de otra API key")
	}

	if res := env.Post(t, "/v1/messages", claudeMessagesBody("deepseek/deepseek-chat", false)); res.Status != http.StatusOK {
		t.Fatalf("messages = %d %s", res.Status, truncate(res.Body))
	}
	sys = upstreamSystem(t, upstream.Last(t).Body)
	if !strings.Contains(sys, "Respondé en español, conciso.") || !strings.Contains(sys, "be terse") {
		t.Fatalf("system de /v1/messages traducido = %q", sys)
	}
}

// Persisted request details never contain the injected text.
func TestPromptInjectionIsNotPersistedInRequestDetails(t *testing.T) {
	env, _ := newProviderEnv(t)
	enablePI(t, env, map[string]any{"promptInjectionEnabled": true})
	putLayer(t, env, map[string]any{"scope": "global", "content": "Texto de configuración interna 7731.", "enabled": true})
	if res := env.Post(t, "/v1/chat/completions", ChatBody("deepseek/deepseek-chat", false)); res.Status != http.StatusOK {
		t.Fatalf("chat = %d %s", res.Status, truncate(res.Body))
	}
	var rows []string
	for i := 0; i < 50; i++ {
		rows, _, _ = env.Repo.GetRequestDetailsPaged(10, 0)
		if len(rows) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(rows) == 0 {
		t.Fatal("no se registró el detalle de la solicitud")
	}
	for _, r := range rows {
		if strings.Contains(r, "7731") {
			t.Fatalf("el detalle persistido contiene la instrucción inyectada: %s", truncate([]byte(r)))
		}
	}
}

// Public identity: the client reads the alias, while usage, request details
// and the upstream request keep the real provider and model.
func TestComboPublicIdentityKeepsRealModelInAudit(t *testing.T) {
	env, upstream := newProviderEnv(t)
	env.AddCombo(t, "combo-1", "equipo", []string{"deepseek/deepseek-chat"})
	enablePI(t, env, map[string]any{"comboPublicIdentityEnabled": true})
	if res := env.Put(t, "/api/combo-identities", map[string]any{"combo": "equipo", "publicModel": "asistente-equipo", "enabled": true}, operator(t)...); res.Status != http.StatusOK {
		t.Fatalf("PUT identidad = %d %s", res.Status, truncate(res.Body))
	}

	res := env.Post(t, "/v1/chat/completions", ChatBody("equipo", false))
	if res.Status != http.StatusOK {
		t.Fatalf("chat = %d %s", res.Status, truncate(res.Body))
	}
	var out struct {
		Model   string `json:"model"`
		Choices []any  `json:"choices"`
	}
	res.Decode(t, &out)
	if out.Model != "asistente-equipo" || len(out.Choices) == 0 {
		t.Fatalf("respuesta = %s", truncate(res.Body))
	}
	if got := upstream.Last(t).Model(t); got != "deepseek-chat" {
		t.Fatalf("modelo enviado al proveedor = %q, se esperaba el real", got)
	}
	rows, err := env.Repo.GetRecentUsageHistory(10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("usageHistory: %v %d", err, len(rows))
	}
	if rows[0].Provider != "deepseek" || rows[0].Model != "deepseek-chat" {
		t.Fatalf("auditoría falsificada: provider=%q model=%q", rows[0].Provider, rows[0].Model)
	}
	details, _, _ := env.Repo.GetRequestDetailsPaged(10, 0)
	for _, d := range details {
		if strings.Contains(d, "asistente-equipo") {
			t.Fatalf("el detalle registró el alias como modelo: %s", truncate([]byte(d)))
		}
	}
}

// Streaming through a combo keeps the SSE framing and rewrites every chunk.
func TestComboPublicIdentityStreaming(t *testing.T) {
	env := newEnv(t)
	up := env.NewUpstream(t, streamResponder())
	env.AddConnection(t, "conn-deepseek", "deepseek", "DeepSeek", up, "sk-upstream")
	env.AddCombo(t, "combo-1", "equipo", []string{"deepseek/deepseek-chat"})
	enablePI(t, env, map[string]any{"comboPublicIdentityEnabled": true, "promptInjectionEnabled": true})
	putLayer(t, env, map[string]any{"scope": "combo", "target": "equipo", "content": "Respondé en español, conciso.", "enabled": true})
	if res := env.Put(t, "/api/combo-identities", map[string]any{"combo": "equipo", "publicModel": "asistente-equipo", "enabled": true}, operator(t)...); res.Status != http.StatusOK {
		t.Fatalf("PUT identidad = %d", res.Status)
	}
	res := env.Post(t, "/v1/chat/completions", ChatBody("equipo", true))
	if res.Status != http.StatusOK {
		t.Fatalf("stream = %d %s", res.Status, truncate(res.Body))
	}
	body := string(res.Body)
	if !strings.Contains(body, "data: [DONE]") || !strings.Contains(body, "streamed ") {
		t.Fatalf("stream incompleto: %s", truncate(res.Body))
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data: {") && strings.Contains(line, `"model"`) && !strings.Contains(line, `"asistente-equipo"`) {
			t.Fatalf("chunk sin alias: %s", line)
		}
	}
	if !strings.Contains(upstreamSystem(t, up.Last(t).Body), "Respondé en español") {
		t.Fatal("la capa de combo no llegó al proveedor")
	}
}

// Negative: a client API key can neither read nor change any injection
// policy (global, another key's or its own), identities, captures or the
// switches — with the dashboard login off and on.
func TestPromptInjectionAdminOnlyForClientKeys(t *testing.T) {
	for _, requireLogin := range []bool{false, true} {
		t.Run(map[bool]string{false: "login-apagado", true: "login-encendido"}[requireLogin], func(t *testing.T) {
			env, _ := newProviderEnv(t)
			env.AddCombo(t, "combo-1", "equipo", []string{"deepseek/deepseek-chat"})
			if err := env.Repo.UpdateSettingsRaw(map[string]any{"requireLogin": requireLogin}); err != nil {
				t.Fatal(err)
			}
			cases := []struct {
				method, path string
				body         any
			}{
				{http.MethodGet, "/api/prompt-injections", nil},
				{http.MethodPut, "/api/prompt-injections", map[string]any{"scope": "global", "content": "x", "enabled": true}},
				{http.MethodPut, "/api/prompt-injections", map[string]any{"scope": "key", "target": "integration-key", "content": "x", "enabled": true}},
				{http.MethodPut, "/api/prompt-injections", map[string]any{"scope": "key", "target": "otra-key", "content": "x", "enabled": true}},
				{http.MethodDelete, "/api/prompt-injections?scope=global", nil},
				{http.MethodPut, "/api/prompt-injections/toggles", map[string]any{"promptInjectionEnabled": true}},
				{http.MethodPost, "/api/prompt-injections/preview", map[string]any{"model": "deepseek/deepseek-chat"}},
				{http.MethodGet, "/api/prompt-injections/captures", nil},
				{http.MethodDelete, "/api/prompt-injections/captures", nil},
				{http.MethodGet, "/api/combo-identities", nil},
				{http.MethodPut, "/api/combo-identities", map[string]any{"combo": "equipo", "publicModel": "x", "enabled": true}},
				{http.MethodDelete, "/api/combo-identities?combo=equipo", nil},
				{http.MethodPatch, "/api/settings", map[string]any{"promptInjectionEnabled": true}},
				{http.MethodPatch, "/api/settings", map[string]any{"promptInjectionCaptureEnabled": true}},
				{http.MethodPatch, "/api/settings", map[string]any{"comboPublicIdentityEnabled": true}},
			}
			for _, c := range cases {
				res := env.Do(t, c.method, c.path, c.body)
				if res.Status != http.StatusForbidden {
					t.Errorf("%s %s con API key = %d, se esperaba 403 (%s)", c.method, c.path, res.Status, truncate(res.Body))
				}
			}
			raw, _ := env.Repo.GetSettingsRaw()
			for _, k := range []string{"promptInjectionEnabled", "promptInjectionCaptureEnabled", "comboPublicIdentityEnabled"} {
				if v, _ := raw[k].(bool); v {
					t.Errorf("la API key activó %s", k)
				}
			}
			if kv, _ := env.Repo.GetKVScope("promptInjection"); len(kv) != 0 {
				t.Errorf("la API key escribió capas: %v", kv)
			}
			if !requireLogin {
				// Compatibility: a full-form save that re-sends the unchanged
				// (default off) switch keeps working with the key.
				if res := env.Do(t, http.MethodPatch, "/api/settings", map[string]any{"language": "es", "promptInjectionEnabled": false}); res.Status != http.StatusOK {
					t.Errorf("guardado sin cambios con API key = %d %s", res.Status, truncate(res.Body))
				}
			}
			// The owner (session) and the local CLI token are allowed.
			if res := env.Get(t, "/api/prompt-injections", operator(t)...); res.Status != http.StatusOK {
				t.Errorf("sesión admin = %d", res.Status)
			}
			if res := env.Get(t, "/api/prompt-injections", WithoutAPIKey(), WithHeader(auth.CLITokenHeader, auth.CLIToken())); res.Status != http.StatusOK {
				t.Errorf("token CLI = %d", res.Status)
			}
		})
	}
}

// Upload: .txt only, UTF-8, size limit; stores nothing by itself.
func TestPromptInjectionUploadValidation(t *testing.T) {
	env := newEnv(t)
	upload := func(name string, data []byte, opts ...RequestOpt) *Result {
		t.Helper()
		var b strings.Builder
		b.WriteString("--LIMITE\r\nContent-Disposition: form-data; name=\"file\"; filename=\"" + name + "\"\r\nContent-Type: text/plain\r\n\r\n")
		b.Write(data)
		b.WriteString("\r\n--LIMITE--\r\n")
		opts = append(opts, WithHeader("Content-Type", "multipart/form-data; boundary=LIMITE"))
		return env.Do(t, http.MethodPost, "/api/prompt-injections/upload", b.String(), opts...)
	}
	ok := upload("instrucciones.txt", []byte("\ufeffRespondé en español,\r\nconciso."), operator(t)...)
	if ok.Status != http.StatusOK || !strings.Contains(string(ok.Body), `Respondé en español,\nconciso.`) {
		t.Fatalf("upload válido = %d %s", ok.Status, truncate(ok.Body))
	}
	for name, data := range map[string][]byte{
		"x.md":       []byte("hola"),
		"latin1.txt": {0x52, 0x65, 0x73, 0xe9},
		"vacio.txt":  []byte("   "),
		"grande.txt": []byte(strings.Repeat("a", 64*1024+10)),
	} {
		if res := upload(name, data, operator(t)...); res.Status != http.StatusBadRequest {
			t.Errorf("%s = %d, se esperaba 400", name, res.Status)
		}
	}
	if res := upload("instrucciones.txt", []byte("hola")); res.Status != http.StatusForbidden {
		t.Errorf("upload con API key = %d, se esperaba 403", res.Status)
	}
	if kv, _ := env.Repo.GetKVScope("promptInjection"); len(kv) != 0 {
		t.Errorf("upload guardó capas: %v", kv)
	}
}

// The inspector shows the final, redacted request without calling upstream.
func TestPromptInjectionPreviewDoesNotCallUpstream(t *testing.T) {
	env, upstream := newProviderEnv(t)
	putLayer(t, env, map[string]any{"scope": "model", "target": "deepseek/deepseek-chat", "mode": "replace", "content": "Sé breve.", "enabled": true})
	res := env.Post(t, "/api/prompt-injections/preview", map[string]any{
		"path": "/v1/chat/completions", "model": "deepseek/deepseek-chat",
		"body": map[string]any{"messages": []any{
			map[string]any{"role": "system", "content": "original"},
			map[string]any{"role": "user", "content": "mi clave es sk-abcdefghijklmnopqrstu"},
		}},
	}, operator(t)...)
	if res.Status != http.StatusOK {
		t.Fatalf("preview = %d %s", res.Status, truncate(res.Body))
	}
	body := string(res.Body)
	if !strings.Contains(body, "Sé breve.") || strings.Contains(body, "sk-abcdefghijklmnopqrstu") || strings.Contains(body, `"content":"original"`) {
		t.Fatalf("preview = %s", body)
	}
	if !strings.Contains(body, `"wouldApply":false`) {
		t.Fatalf("con el interruptor apagado wouldApply debe ser false: %s", body)
	}
	if upstream.Count() != 0 {
		t.Fatalf("la vista previa llamó al proveedor %d veces", upstream.Count())
	}
}
