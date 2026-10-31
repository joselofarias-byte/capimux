package promptinject

import (
	"bytes"
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type fakeBackend struct {
	mu       sync.Mutex
	kv       map[string]map[string]string
	settings map[string]any
}

func newFake() *fakeBackend {
	return &fakeBackend{kv: map[string]map[string]string{}, settings: map[string]any{}}
}

func (f *fakeBackend) GetKVScope(scope string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	for k, v := range f.kv[scope] {
		out[k] = v
	}
	return out, nil
}
func (f *fakeBackend) SetKV(scope, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.kv[scope] == nil {
		f.kv[scope] = map[string]string{}
	}
	f.kv[scope][key] = value
	return nil
}
func (f *fakeBackend) DeleteKV(scope, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.kv[scope], key)
	return nil
}
func (f *fakeBackend) GetSettingsRaw() (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]any{}
	for k, v := range f.settings {
		out[k] = v
	}
	return out, nil
}
func (f *fakeBackend) set(k string, v any) { f.mu.Lock(); f.settings[k] = v; f.mu.Unlock() }

func mustPut(t *testing.T, s *Store, e Entry) {
	t.Helper()
	if _, err := s.Put(e); err != nil {
		t.Fatalf("Put: %v", err)
	}
}

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("json: %v (%s)", err, b)
	}
	return m
}

func TestValidateContentTechnicalLimitsOnly(t *testing.T) {
	if err := ValidateContent("Respondé en español, conciso."); err != nil {
		t.Fatalf("contenido neutro rechazado: %v", err)
	}
	if err := ValidateContent(strings.Repeat("a", MaxContentBytes+1)); err == nil {
		t.Fatal("se esperaba error por tamaño")
	}
	if err := ValidateContent(string([]byte{0xff, 0xfe})); err == nil {
		t.Fatal("se esperaba error por UTF-8 inválido")
	}
	if err := ValidateContent("a\x00b"); err == nil {
		t.Fatal("se esperaba error por NUL")
	}
	if err := ValidateContent("   "); err == nil {
		t.Fatal("se esperaba error por vacío")
	}
}

func TestNormalizeRules(t *testing.T) {
	if _, err := (Entry{Scope: ScopeModel, Content: "x"}).Normalize(); err == nil {
		t.Fatal("modelo sin destino debe fallar")
	}
	if _, err := (Entry{Scope: "otro", Content: "x"}).Normalize(); err == nil {
		t.Fatal("alcance desconocido debe fallar")
	}
	if _, err := (Entry{Scope: ScopeGlobal, Mode: "rewrite", Content: "x"}).Normalize(); err == nil {
		t.Fatal("modo desconocido debe fallar")
	}
	e, err := (Entry{Scope: ScopeGlobal, Target: "ignorado", Content: "x"}).Normalize()
	if err != nil || e.Mode != ModePrepend || e.Target != "" {
		t.Fatalf("normalización inesperada: %+v %v", e, err)
	}
}

func TestResolveScopesAndPrecedence(t *testing.T) {
	entries := map[string]Entry{
		"global":         {Scope: ScopeGlobal, Mode: ModePrepend, Content: "G", Enabled: true},
		"combo:mi-combo": {Scope: ScopeCombo, Target: "mi-combo", Mode: ModePrepend, Content: "C", Enabled: true},
		"model:gpt-x":    {Scope: ScopeModel, Target: "gpt-x", Mode: ModeAppend, Content: "M", Enabled: true},
		"key:k1":         {Scope: ScopeKey, Target: "k1", Mode: ModePrepend, Content: "K", Enabled: true},
		"key:k2":         {Scope: ScopeKey, Target: "k2", Mode: ModePrepend, Content: "K2", Enabled: false},
	}
	p := Compose(Resolve(entries, Request{Model: "gpt-x", APIKeyID: "k1"}))
	if p.Prefix != "G\n\nK" || p.Suffix != "M" {
		t.Fatalf("modelo: %+v", p)
	}
	p = Compose(Resolve(entries, Request{Model: "mi-combo", IsCombo: true, APIKeyID: "k1"}))
	if p.Prefix != "G\n\nC\n\nK" || p.Suffix != "" {
		t.Fatalf("combo: %+v", p)
	}
	if got := Compose(Resolve(entries, Request{Model: "otro", APIKeyID: "k2"})); got.Prefix != "G" {
		t.Fatalf("capa deshabilitada aplicada: %+v", got)
	}
}

func TestComposeReplaceDiscardsLessSpecific(t *testing.T) {
	p := Compose([]Entry{
		{Scope: ScopeGlobal, Mode: ModePrepend, Content: "G"},
		{Scope: ScopeModel, Mode: ModeReplace, Content: "R"},
		{Scope: ScopeKey, Mode: ModeAppend, Content: "K"},
	})
	if !p.Replace || p.Prefix != "" || p.Suffix != "K" || p.wrap("cliente") != "R\n\nK" {
		t.Fatalf("plan: %+v wrap=%q", p, p.wrap("cliente"))
	}
	if len(p.Layers) != 2 {
		t.Fatalf("capas: %+v", p.Layers)
	}
}

func TestApplyOpenAIChatPreservesToolsStreamAndRoles(t *testing.T) {
	body := `{"model":"m","stream":true,"tools":[{"type":"function","function":{"name":"f","parameters":{}}}],"messages":[{"role":"system","content":"S1"},{"role":"developer","content":[{"type":"text","text":"D"}]},{"role":"user","content":"hola"},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c1","content":"ok"}],"x_ext":{"a":1}}`
	out, err := Apply(ProtocolOpenAIChat, []byte(body), Plan{Prefix: "P", Suffix: "A"})
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, out)
	if m["stream"] != true || m["x_ext"] == nil || m["tools"] == nil {
		t.Fatalf("campos perdidos: %s", out)
	}
	msgs := m["messages"].([]any)
	if len(msgs) != 5 {
		t.Fatalf("mensajes: %d", len(msgs))
	}
	if c := msgs[0].(map[string]any)["content"]; c != "P\n\nS1" {
		t.Fatalf("primer sistema: %v", c)
	}
	parts := msgs[1].(map[string]any)["content"].([]any)
	if parts[len(parts)-1].(map[string]any)["text"] != "A" {
		t.Fatalf("append en developer: %v", parts)
	}
	tc := msgs[3].(map[string]any)["tool_calls"].([]any)
	if tc[0].(map[string]any)["id"] != "c1" || msgs[4].(map[string]any)["tool_call_id"] != "c1" {
		t.Fatalf("tool calls alterados: %s", out)
	}
}

func TestApplyOpenAIChatInsertsAndReplaces(t *testing.T) {
	body := `{"model":"m","messages":[{"role":"user","content":"hola"}]}`
	out, _ := Apply(ProtocolOpenAIChat, []byte(body), Plan{Prefix: "P"})
	msgs := decode(t, out)["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" || msgs[0].(map[string]any)["content"] != "P" {
		t.Fatalf("inserción: %s", out)
	}
	body = `{"model":"m","messages":[{"role":"system","content":"viejo"},{"role":"system","content":"viejo2"},{"role":"user","content":"hola"},{"role":"system","content":"intermedio"}]}`
	out, _ = Apply(ProtocolOpenAIChat, []byte(body), Plan{Replace: true, Base: "nuevo"})
	msgs = decode(t, out)["messages"].([]any)
	if len(msgs) != 3 || msgs[0].(map[string]any)["content"] != "nuevo" || msgs[2].(map[string]any)["content"] != "intermedio" {
		t.Fatalf("reemplazo controlado: %s", out)
	}
}

func TestApplyAnthropicStringAndBlocks(t *testing.T) {
	out, _ := Apply(ProtocolAnthropic, []byte(`{"model":"c","system":"S","messages":[{"role":"user","content":"hola"}]}`), Plan{Prefix: "P", Suffix: "A"})
	if decode(t, out)["system"] != "P\n\nS\n\nA" {
		t.Fatalf("string: %s", out)
	}
	out, _ = Apply(ProtocolAnthropic, []byte(`{"model":"c","system":[{"type":"text","text":"S","cache_control":{"type":"ephemeral"}}],"messages":[]}`), Plan{Prefix: "P"})
	blocks := decode(t, out)["system"].([]any)
	if len(blocks) != 2 || blocks[0].(map[string]any)["text"] != "P" || blocks[1].(map[string]any)["cache_control"] == nil {
		t.Fatalf("bloques: %s", out)
	}
	out, _ = Apply(ProtocolAnthropic, []byte(`{"model":"c","messages":[]}`), Plan{Replace: true, Base: "R"})
	if decode(t, out)["system"] != "R" {
		t.Fatalf("reemplazo: %s", out)
	}
}

func TestApplyResponsesInstructionsOnly(t *testing.T) {
	body := `{"model":"m","instructions":"I","input":[{"role":"developer","content":"interno"},{"role":"user","content":"hola"}]}`
	out, _ := Apply(ProtocolResponses, []byte(body), Plan{Replace: true, Base: "R"})
	m := decode(t, out)
	if m["instructions"] != "R" {
		t.Fatalf("instructions: %s", out)
	}
	if in := m["input"].([]any); len(in) != 2 {
		t.Fatalf("input alterado: %s", out)
	}
}

func TestApplyRejectsInvalidJSON(t *testing.T) {
	if _, err := Apply(ProtocolOpenAIChat, []byte(`{no`), Plan{Prefix: "P"}); err == nil {
		t.Fatal("se esperaba error")
	}
	if out, err := Apply(ProtocolOpenAIChat, []byte(`{no`), Plan{}); err != nil || string(out) != `{no` {
		t.Fatal("plan vacío debe devolver el cuerpo intacto")
	}
}

func TestRedact(t *testing.T) {
	in := `{"api_key":"abc123","x":"Bearer abcdefghijklmnop","k":"sk-abcdefghijklmnop","g":"AIzaSyA1234567890123456789012"}`
	out := Redact(in)
	for _, leak := range []string{"abc123", "abcdefghijklmnop", "AIzaSyA123"} {
		if strings.Contains(out, leak) {
			t.Fatalf("filtró %q: %s", leak, out)
		}
	}
}

func TestRewriteModelField(t *testing.T) {
	cases := map[string]string{
		`{"model":"real","id":"x"}`:                               "model",
		`{"type":"message_start","message":{"model":"real"}}`:     "message",
		`{"type":"response.created","response":{"model":"real"}}`: "response",
	}
	for in := range cases {
		out := RewriteModelField([]byte(in), "publico")
		if strings.Contains(string(out), "real") || !strings.Contains(string(out), "publico") {
			t.Fatalf("%s → %s", in, out)
		}
	}
	if out := RewriteModelField([]byte(`[DONE]`), "p"); string(out) != `[DONE]` {
		t.Fatal("no-JSON alterado")
	}
}

func newTestMiddleware(b *fakeBackend, combos ...string) func(http.Handler) http.Handler {
	set := map[string]bool{}
	for _, c := range combos {
		set[c] = true
	}
	return Middleware(b, func(n string) bool { return set[n] }, func(r *http.Request) string { return r.Header.Get("X-Test-Key-ID") })
}

func TestMiddlewareOffByDefaultIsPassthrough(t *testing.T) {
	b := newFake()
	mustPut(t, For(b), Entry{Scope: ScopeGlobal, Content: "P", Enabled: true})
	var got []byte
	h := newTestMiddleware(b)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"real"}`))
	}))
	body := `{"model":"m","messages":[{"role":"user","content":"hola"}]}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/chat/completions", strings.NewReader(body)))
	if string(got) != body {
		t.Fatalf("con el toggle apagado el cuerpo cambió: %s", got)
	}
}

func TestMiddlewareInjectsPerKeyAndRewritesIdentityStreaming(t *testing.T) {
	b := newFake()
	b.set(SettingInjectionEnabled, true)
	b.set(SettingIdentityEnabled, true)
	s := For(b)
	mustPut(t, s, Entry{Scope: ScopeGlobal, Content: "Respondé en español, conciso.", Enabled: true})
	mustPut(t, s, Entry{Scope: ScopeKey, Target: "k1", Mode: ModeAppend, Content: "Usá viñetas.", Enabled: true})
	if _, err := s.PutIdentity(Identity{Combo: "mi-combo", PublicModel: "asistente-equipo", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	var got []byte
	h := newTestMiddleware(b, "mi-combo")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("data: {\"model\":\"gpt-real\",\"choices\":[]}\n\n: ping\n\ndata: {\"mod"))
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("el\":\"gpt-real\"}\n\ndata: [DONE]\n\n"))
	}))
	req := httptest.NewRequest(http.MethodPost, "/chat/completions", strings.NewReader(`{"model":"mi-combo","stream":true,"messages":[{"role":"user","content":"hola"}]}`))
	req.Header.Set("X-Test-Key-ID", "k1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	sys := decode(t, got)["messages"].([]any)[0].(map[string]any)["content"]
	if sys != "Respondé en español, conciso.\n\nUsá viñetas." {
		t.Fatalf("sistema: %v", sys)
	}
	out := rec.Body.String()
	if strings.Contains(out, "gpt-real") || strings.Count(out, "asistente-equipo") != 2 || !strings.Contains(out, ": ping") || !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("stream: %q", out)
	}
	// Otra key no recibe la capa de k1.
	req = httptest.NewRequest(http.MethodPost, "/chat/completions", strings.NewReader(`{"model":"gpt-x","messages":[{"role":"user","content":"hola"}]}`))
	req.Header.Set("X-Test-Key-ID", "k2")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if bytes.Contains(got, []byte("viñetas")) {
		t.Fatalf("capa de otra key aplicada: %s", got)
	}
}

func TestMiddlewareIdentityNonStreamAndNonCombo(t *testing.T) {
	b := newFake()
	b.set(SettingIdentityEnabled, true)
	_, _ = For(b).PutIdentity(Identity{Combo: "mi-combo", PublicModel: "publico", Enabled: true})
	h := newTestMiddleware(b, "mi-combo")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "40")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"type":"message","model":"claude-real"}`))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/messages", strings.NewReader(`{"model":"mi-combo","messages":[]}`)))
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"model":"publico"`) {
		t.Fatalf("no-stream: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/messages", strings.NewReader(`{"model":"claude-x","messages":[]}`)))
	if !strings.Contains(rec.Body.String(), "claude-real") {
		t.Fatalf("modelo no-combo reescrito: %s", rec.Body)
	}
}

func TestCaptureRedactsAndIsOptIn(t *testing.T) {
	ClearCaptures()
	b := newFake()
	b.set(SettingInjectionEnabled, true)
	mustPut(t, For(b), Entry{Scope: ScopeGlobal, Content: "Respondé en español.", Enabled: true})
	h := newTestMiddleware(b)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	body := `{"model":"m","messages":[{"role":"user","content":"mi clave es sk-abcdefghijklmnop"}]}`
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/chat/completions", strings.NewReader(body)))
	if len(Captures()) != 0 {
		t.Fatal("capturó sin autorización del admin")
	}
	b.set(SettingCaptureEnabled, true)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/chat/completions", strings.NewReader(body)))
	c := Captures()
	if len(c) != 1 || strings.Contains(c[0].Body, "sk-abcdefghijklmnop") || !strings.Contains(c[0].Body, "Respondé") {
		t.Fatalf("captura: %+v", c)
	}
	ClearCaptures()
}

func TestStoreConcurrentReadWrite(t *testing.T) {
	b := newFake()
	b.set(SettingInjectionEnabled, true)
	s := For(b)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = s.Put(Entry{Scope: ScopeGlobal, Content: "x", Enabled: true})
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = s.Match(Request{Model: "m"})
			}
		}()
	}
	wg.Wait()
}

func TestPreviewReportsLayersWithoutSending(t *testing.T) {
	b := newFake()
	s := For(b)
	mustPut(t, s, Entry{Scope: ScopeModel, Target: "gpt-x", Mode: ModeReplace, Content: "Sé breve.", Enabled: true})
	res, err := s.Preview(ReadToggles(b), ProtocolOpenAIChat, Request{Model: "gpt-x"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.WouldApply || res.Effective != "Sé breve." || len(res.Layers) != 1 || !res.Replace {
		t.Fatalf("preview: %+v", res)
	}
}
