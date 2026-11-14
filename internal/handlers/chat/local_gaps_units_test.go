package chat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9router/proxy/internal/db"
	"9router/proxy/internal/models"
	"9router/proxy/internal/providers"
)

func TestEstimateAnthropicTokensShapes(t *testing.T) {
	body := []byte(`{
		"system": [{"type":"text","text":"sys"}, {"n":1}, true, null],
		"messages": [
			{"role":"user","content":"hi"},
			{"role":"user","content":[
				{"type":"text","text":"abc"},
				{"type":"tool_use","name":"lookup","input":{"q":"x"}},
				{"type":"tool_result","content":"done"},
				{"type":"thinking","thinking":"hmm"},
				{"type":"other","text":"z"},
				"plain"
			]},
			{"role":"user","content":{"nested":"v"}}
		],
		"tools": [{"name":"lookup","input_schema":{"type":"object"}}]
	}`)
	if got := EstimateAnthropicTokens(body); got <= 0 {
		t.Fatalf("EstimateAnthropicTokens = %d", got)
	}
	if got := estimateAnthropicTokens([]byte("{")); got != 0 {
		t.Fatalf("bad json tokens = %d, want 0", got)
	}
	sysOnly := []byte(`{"system":"hello system"}`)
	if got := estimateAnthropicTokens(sysOnly); got <= 0 {
		t.Fatalf("string system tokens = %d", got)
	}
	empty := []byte(`{}`)
	if got := estimateAnthropicTokens(empty); got != (len(empty)+3)/4 {
		t.Fatalf("empty object tokens = %d", got)
	}

	if countValueChars(nil) != 0 || countValueChars([]byte("ab")) != 2 {
		t.Fatal("nil/bytes char count")
	}
	if countValueChars(true) != 4 || countValueChars(false) != 5 {
		t.Fatal("bool char count")
	}
	if countValueChars(1.5) == 0 || countValueChars(struct{}{}) != 0 {
		t.Fatal("float/unknown char count")
	}
	if countValueChars([]any{nil, "a"}) != 1 {
		t.Fatal("slice char count")
	}
	if messageContentChars(nil) != 0 || MessageContentChars("xy") != 2 {
		t.Fatal("message content chars")
	}
	if ContentBlockChars(nil) != 0 || ContentBlockChars("z") != 1 {
		t.Fatal("content block chars")
	}
	if contentBlockChars(map[string]any{"type": "text"}) != 0 {
		t.Fatal("text block without text")
	}
}

func TestHandleCountTokens(t *testing.T) {
	h := NewChatHandler(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{"system":"abcd","messages":[{"role":"user","content":"efgh"}]}`))
	h.HandleCountTokens(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "input_tokens") {
		t.Fatalf("count tokens %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", errReadCloser{err: ioErr})
	h.HandleCountTokens(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body status %d", rec.Code)
	}
}

func TestSSEToClaudeJSONBranches(t *testing.T) {
	if _, ok := sseToClaudeJSON([]byte("event: ping\n")); ok {
		t.Fatal("non-data stream should not fold")
	}
	errBody, ok := sseToClaudeJSON([]byte("data: {\"error\":{\"message\":\"nope\",\"type\":\"x\"}}\n\n"))
	if !ok || !strings.Contains(string(errBody), "nope") {
		t.Fatalf("error fold = %s ok=%v", errBody, ok)
	}
	raw := strings.Join([]string{
		"data: not-json",
		"data:",
		"data: [DONE]",
		`data: {"id":"c1","created":1,"model":"m","choices":[{"delta":{"content":"he","reasoning_content":"think","tool_calls":[{"index":0,"id":"call_1","function":{"name":"do","arguments":"{"}}]},"finish_reason":null}]}`,
		`data: {"usage":{"prompt_tokens":1,"completion_tokens":2},"choices":[{"delta":{"content":"llo","tool_calls":[{"index":0,"function":{"name":"do","arguments":"\"a\":1}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"name":"other","arguments":""}}]}}]}`,
		`data: {"choices":[]}`,
	}, "\n")
	folded, ok := sseToClaudeJSON([]byte(raw))
	if !ok {
		t.Fatal("expected a folded completion")
	}
	text := string(folded)
	for _, want := range []string{"hello", "think", "tool_calls", "do", "other", "c1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("folded body missing %q: %s", want, text)
		}
	}

	emptyTools, ok := sseToClaudeJSON([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n"))
	if !ok || !strings.Contains(string(emptyTools), `"content":""`) {
		t.Fatalf("empty delta = %s ok=%v", emptyTools, ok)
	}
}

func TestHandleBypassRequestPatterns(t *testing.T) {
	if handleBypassRequest(httptest.NewRecorder(), []byte("{"), "m", false) {
		t.Fatal("invalid json must not bypass")
	}
	if handleBypassRequest(httptest.NewRecorder(), []byte(`{"messages":[]}`), "m", false) {
		t.Fatal("empty messages must not bypass")
	}
	if handleBypassRequest(httptest.NewRecorder(), []byte(`{"messages":[{"role":"user","content":"hello"}]}`), "m", false) {
		t.Fatal("ordinary chat must not bypass")
	}

	cases := []struct {
		name   string
		body   string
		stream bool
		want   string
	}{
		{"assistant brace", `{"messages":[{"role":"assistant","content":" { "}]}`, false, defaultBypassText},
		{"warmup array", `{"messages":[{"role":"user","content":[{"type":"text","text":"Warmup"},{"type":"image"}]}]}`, false, defaultBypassText},
		{"count", `{"messages":[{"role":"user","content":"count"}]}`, false, defaultBypassText},
		{"system string", `{"system":"topic isNewTopic please","messages":[{"role":"user","content":"hi"}]}`, false, "isNewTopic"},
		{"system blocks", `{"system":[{"type":"text","text":"isNewTopic"}],"messages":[{"role":"user","content":null},{"role":"system","content":"extra"}]}`, true, "isNewTopic"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
			if !handleBypassRequest(rec, []byte(tc.body), "alias-model", tc.stream) {
				t.Fatal("expected bypass")
			}
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
			if tc.stream && rec.flushes == 0 {
				t.Fatal("stream bypass did not flush")
			}
		})
	}
}

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func (f *flushRecorder) Flush() { f.flushes++ }

func TestSmallUncoveredHelpers(t *testing.T) {
	if _, ok := parseFloatStrict("nope"); ok {
		t.Fatal("parseFloatStrict accepted text")
	}
	if _, ok := parseFloatStrict("NaN"); ok {
		t.Fatal("parseFloatStrict accepted NaN")
	}
	if _, ok := parseFloatStrict("+Inf"); ok {
		t.Fatal("parseFloatStrict accepted Inf")
	}
	if v, ok := parseFloatStrict(" 1.5 "); !ok || v != 1.5 {
		t.Fatalf("parseFloatStrict = %v %v", v, ok)
	}

	if ConnectionHasCredential(nil) || ConnectionHasCredential(&models.ProviderConnection{}) {
		t.Fatal("empty connection reported a credential")
	}
	if ConnectionHasCredential(&models.ProviderConnection{Data: "{"}) {
		t.Fatal("bad json reported a credential")
	}
	for _, data := range []string{
		`{"apiKey":"k"}`,
		`{"accessToken":"a"}`,
		`{"authToken":"t"}`,
		`{"refreshToken":"r"}`,
	} {
		if !connectionHasCredential(&models.ProviderConnection{Data: data}) {
			t.Fatalf("missing credential in %s", data)
		}
	}
	if connectionHasCredential(&models.ProviderConnection{Data: `{"apiKey":""}`}) {
		t.Fatal("blank api key counted")
	}

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	h := NewChatHandler(db.NewRepo(database))
	if h.comboHealthKey("deepseek/deepseek-chat") == "" {
		t.Fatal("expected a health key for a known model")
	}
	if h.comboHealthKey("not a model entry") != "" {
		t.Fatal("unresolved entry should not have a health key")
	}
	if ue := clientClosedUpstream(); ue == nil || ue.StatusCode != StatusClientClosedRequest {
		t.Fatalf("clientClosedUpstream = %#v", ue)
	}

	h.releaseRetriedLocks(nil)
	(*ChatHandler)(nil).releaseRetriedLocks([]retriedLock{{connID: "x", provider: "deepseek", model: "deepseek-chat"}})
	h.releaseRetriedLocks([]retriedLock{{connID: "conn-1", provider: "deepseek", model: "deepseek-chat"}})
	h.clearOAuthAccountPark("")
	(&ChatHandler{}).clearOAuthAccountPark("conn-1")

	for _, cap := range []string{"vision", "pdf", "audioInput", "videoInput", "imageOutput", "audioOutput", "search", "tools", "reasoning", "unknown"} {
		_ = modelHasCapability("deepseek/deepseek-chat", cap)
		_ = modelHasCapability("deepseek", cap)
	}

	body := []byte(`{"messages":[{"role":"user","content":[
		{"type":"image_url"},{"type":"image"},{"type":"input_image"},
		{"type":"file"},{"type":"document"},{"type":"input_file"},
		{"type":"audio_url"},{"type":"audio"},{"type":"input_audio"},
		{"type":"video_url"},{"type":"video"},{"type":"input_video"},
		{"mimeType":"image/png"},{"mimeType":"audio/wav"},{"mimeType":"video/mp4"},{"mimeType":"application/pdf"},
		{"inlineData":{"mimeType":"image/jpeg"}},{"fileData":{"mimeType":"audio/mpeg"}},
		{"inlineData":{"mimeType":"video/webm"}},{"fileData":{"mimeType":"application/pdf"}},
		"not-a-block"
	]}],"input":[{"role":"user","content":[{"type":"image"}]}],"tools":[{"type":"function","function":{"name":"a"}}]}`)
	required := DetectRequiredCapabilities(body)
	for _, cap := range []string{"vision", "pdf", "audioInput", "videoInput", "tools"} {
		if !required[cap] {
			t.Fatalf("missing capability %s in %#v", cap, required)
		}
	}
	if len(DetectRequiredCapabilities([]byte("{"))) != 0 {
		t.Fatal("bad json should not require capabilities")
	}

	if info, err := (*ChatHandler)(nil).SelectVirtualFreeEntry(nil); info != nil || err != nil {
		t.Fatalf("nil handler/info = %v %v", info, err)
	}
	plain := &ModelInfo{Provider: "deepseek", Model: "deepseek-chat"}
	got, err := h.SelectVirtualFreeEntry(plain)
	if err != nil || got != plain {
		t.Fatalf("non-virtual select = %v %v", got, err)
	}
	virtual := &ModelInfo{VirtualFree: true, Strategy: "fallback", ComboModels: []string{"deepseek/deepseek-chat", "not-a-model"}}
	if _, err := h.SelectVirtualFreeEntry(virtual); err == nil {
		t.Fatal("stale virtual pool should be unavailable without a free registry")
	}
	// The free registry is process-global. An earlier test in this package may
	// already have published a local snapshot, so either outcome is valid:
	// an empty pool errors, a snapshot returns a virtual combo.
	if info, err := h.resolveDynamicFreeBest(); err != nil {
		if info != nil {
			t.Fatalf("resolveDynamicFreeBest info=%v err=%v", info, err)
		}
	} else if info == nil || !info.VirtualFree || len(info.ComboModels) == 0 {
		t.Fatalf("resolveDynamicFreeBest = %#v", info)
	}

	rec := httptest.NewRecorder()
	writeModelPrepError(rec, ErrFreeRouteUnavailable)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("free-route status %d", rec.Code)
	}
	for _, msg := range []string{"no active connections for x", "model not found", "no API key found", "bad request"} {
		if statusForModelErr(errString(msg)) == 0 {
			t.Fatalf("status for %q", msg)
		}
	}
	rec = httptest.NewRecorder()
	writeModelPrepError(rec, errString("no API key found"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("api key status %d", rec.Code)
	}

	cfg := &providers.ProviderConfig{
		BaseURL:  "https://example.test/v1/chat/completions",
		ImageURL: "https://example.test/images",
		TTSURL:   "https://example.test/tts",
		STTURL:   "https://example.test/stt",
		VideoURL: "https://example.test/videos",
	}
	if multimodalPath(cfg, "/images/generations") != cfg.ImageURL {
		t.Fatal("image url")
	}
	if multimodalPath(cfg, "/audio/speech") != cfg.TTSURL {
		t.Fatal("tts url")
	}
	if multimodalPath(cfg, "/audio/transcriptions") != cfg.STTURL {
		t.Fatal("stt url")
	}
	if multimodalPath(cfg, "/videos/generations") != cfg.VideoURL {
		t.Fatal("video url")
	}
	plainCfg := &providers.ProviderConfig{BaseURL: "https://example.test/v1"}
	if got := multimodalPath(plainCfg, "/images/generations"); got != "https://example.test/v1/images/generations" {
		t.Fatalf("joined path %s", got)
	}
	chatCfg := &providers.ProviderConfig{BaseURL: "https://example.test/v1/chat/completions"}
	if got := multimodalPath(chatCfg, "/audio/speech"); !strings.HasSuffix(got, "/audio/speech") || strings.Contains(got, "chat/completions") {
		t.Fatalf("rewritten path %s", got)
	}
}

type stringError string

func (e stringError) Error() string { return string(e) }

func errString(msg string) error { return stringError(msg) }

var ioErr = stringError("read failed")
