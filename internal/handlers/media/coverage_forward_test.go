package media

import (
	"bytes"
	json "encoding/json/v2"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9router/proxy/internal/db"
	"9router/proxy/internal/handlers/chat"
	"9router/proxy/internal/models"
	"9router/proxy/internal/providers"
	"9router/proxy/internal/proxy"
)

func TestBuildEmbeddingsURLExported(t *testing.T) {
	if BuildEmbeddingsURL("https://api.openai.com/v1/chat/completions") != "https://api.openai.com/v1/embeddings" {
		t.Fatal(BuildEmbeddingsURL("https://api.openai.com/v1/chat/completions"))
	}
}

func TestNewMediaHandlerTransportBranches(t *testing.T) {
	orig := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = orig })

	http.DefaultTransport = &proxy.FallbackTransport{Base: orig}
	if NewMediaHandler(nil, nil, nil).Client == nil {
		t.Fatal("fallback transport produced a nil client")
	}
	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errReader{}
	})
	if NewMediaHandler(nil, nil, nil).Client == nil {
		t.Fatal("plain transport produced a nil client")
	}
}

func TestParseMiMoModelVoice(t *testing.T) {
	tests := []struct {
		in, model, voice string
	}{
		{"", mimoDefaultModel, mimoDefaultVoice},
		{mimoDefaultModel, mimoDefaultModel, mimoDefaultVoice},
		{mimoDefaultModel + "/ice", mimoDefaultModel, "ice"},
		{"other/voice", "other", "voice"},
		{"bare", mimoDefaultModel, mimoDefaultVoice},
	}
	for _, tc := range tests {
		model, voice := parseMiMoModelVoice(tc.in)
		if model != tc.model || voice != tc.voice {
			t.Errorf("parseMiMoModelVoice(%q) = %q %q", tc.in, model, voice)
		}
	}
}

func TestSearchAndAudioHelpers(t *testing.T) {
	if sanitizeSearchQuery("  cats\t\nand  dogs  ") != "cats and dogs" {
		t.Fatalf("sanitize = %q", sanitizeSearchQuery("  cats\t\nand  dogs  "))
	}
	if sanitizeSearchQuery("bad\x00query") != "" || sanitizeSearchQuery("") != "" {
		t.Fatal("control query should be rejected")
	}
	if got := sanitizeSearchQuery("１ ２"); got != "1 2" {
		t.Fatalf("nfkc = %q", got)
	}

	msg, status := searchErrorParts(searchError(0, "upstream"))
	if msg != "upstream" || status != http.StatusBadGateway {
		t.Fatalf("zero status: %s %d", msg, status)
	}
	msg, status = searchErrorParts(searchError(http.StatusConflict, "taken"))
	if msg != "taken" || status != http.StatusConflict {
		t.Fatalf("conflict: %s %d", msg, status)
	}
	msg, status = searchErrorParts(errReader{})
	if status != http.StatusBadGateway || msg == "" {
		t.Fatalf("plain: %s %d", msg, status)
	}
	rec := httptest.NewRecorder()
	writeSearchError(rec, searchError(http.StatusUnprocessableEntity, "bad query"))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "bad query") {
		t.Fatalf("write %d %s", rec.Code, rec.Body.String())
	}

	text := "one two three four"
	if expandSegment("", 0, 1) != "" || expandSegment(text, -1, 1) != "" || expandSegment(text, 3, 1) != "" {
		t.Fatal("invalid expand")
	}
	if expandSegment(text, 4, 7) != text {
		t.Fatalf("short expand = %q", expandSegment(text, 4, 7))
	}
	wide := strings.Repeat("x", 20) + " " + strings.Repeat("y", 600)
	got := expandSegment(wide, 200, 210)
	if !strings.HasPrefix(got, "...") || !strings.HasSuffix(got, "...") {
		t.Fatalf("wide = %q", got)
	}
	nospace := strings.Repeat("z", 500)
	got = expandSegment(nospace, 200, 220)
	if !strings.HasPrefix(got, "...") || !strings.HasSuffix(got, "...") {
		t.Fatalf("nospace = %q", got)
	}

	for _, tc := range []struct{ name, want string }{
		{"a.MP3", "audio/mpeg"},
		{"a.wav", "audio/wav"},
		{"a.m4a", "audio/mp4"},
		{"a.ogg", "audio/ogg"},
		{"a.opus", "audio/ogg"},
		{"a.flac", "audio/flac"},
		{"a.webm", "audio/webm"},
		{"a.bin", "audio/mpeg"},
		{"noext", "audio/mpeg"},
	} {
		if got := detectAudioMime(tc.name); got != tc.want {
			t.Errorf("detectAudioMime(%q) = %q", tc.name, got)
		}
	}
}

func TestMediaBodyReadErrors(t *testing.T) {
	h := NewMediaHandler(nil, nil, nil)
	handlers := []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
	}{
		{"embeddings", h.HandleEmbeddings},
		{"images", h.HandleImages},
		{"speech", h.HandleAudioSpeech},
		{"systemone", h.HandleSystemone},
		{"transcriptions", h.HandleAudioTranscriptions},
		{"video", h.HandleVideoGenerations},
		{"video edits", h.HandleVideoEdits},
		{"video extensions", h.HandleVideoExtensions},
		{"search", h.HandleSearch},
		{"scrape", h.HandleScrape},
		{"fetch", h.HandleWebFetch},
	}
	for _, tc := range handlers {
		rec := httptest.NewRecorder()
		tc.fn(rec, httptest.NewRequest(http.MethodPost, "/", errReader{}))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", tc.name, rec.Code, rec.Body.String())
		}
	}
}

func TestForwardMediaSpecialURLsAndUsage(t *testing.T) {
	type hit struct {
		path, method, body, referer, auth string
	}
	var calls []hit
	var authFails int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := ioReadAll(r.Body)
		calls = append(calls, hit{
			path: r.URL.Path, method: r.Method, body: string(raw),
			referer: r.Header.Get("HTTP-Referer"), auth: r.Header.Get("Authorization"),
		})
		w.Header().Set("X-Upstream", "1")
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(raw), "fail-500") {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"busy"}`))
			return
		}
		if strings.Contains(string(raw), "fail-401") {
			authFails++
			if authFails == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"nope"}`))
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	ensureUsageTables(t, database)
	ensureCombos(t, database)
	seedConn(t, database, "or", "openrouter", "sk-or", "", 1)
	seedConn(t, database, "el", "elevenlabs", "sk-el", "", 1)
	seedConn(t, database, "dg", "deepgram", "sk-dg", "", 1)
	seedConn(t, database, "ol", "ollama", "sk-ol", "", 1)
	seedConn(t, database, "oa", "openai", "sk-oa", upstream.URL, 1)
	repo := db.NewRepo(database)
	h := newTestMediaHandler(repo)
	blockDirectDial(t)

	withProvider(t, "openrouter", func(cfg *providers.ProviderConfig) {
		cfg.ImageURL = upstream.URL + "/images/generations"
		cfg.VideoURL = upstream.URL + "/videos"
		cfg.SystemoneURL = upstream.URL + "/systemone"
	})
	withProvider(t, "elevenlabs", func(cfg *providers.ProviderConfig) {
		cfg.TTSURL = upstream.URL + "/custom-tts"
	})
	withProvider(t, "deepgram", func(cfg *providers.ProviderConfig) {
		cfg.STTURL = upstream.URL + "/listen"
	})
	withProvider(t, "ollama", func(cfg *providers.ProviderConfig) {
		cfg.FetchURL = upstream.URL + "/web_fetch"
		cfg.FetchMethod = "POST"
	})

	post := func(target, body string, header map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range header {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		switch {
		case strings.Contains(target, "images"):
			h.HandleImages(rec, req)
		case strings.Contains(target, "videos/generations"):
			h.HandleVideoGenerations(rec, req)
		case strings.Contains(target, "systemone"):
			h.HandleSystemone(rec, req)
		case strings.Contains(target, "speech"):
			h.HandleAudioSpeech(rec, req)
		case strings.Contains(target, "transcriptions"):
			h.HandleAudioTranscriptions(rec, req)
		case strings.Contains(target, "web/fetch"):
			h.HandleWebFetch(rec, req)
		case strings.Contains(target, "search"):
			h.HandleSearch(rec, req)
		case strings.Contains(target, "scrape"):
			h.HandleScrape(rec, req)
		default:
			t.Fatalf("unknown target %s", target)
		}
		return rec
	}

	rec := post("/v1/images/generations", `{"model":"openrouter/image","prompt":"cat"}`, map[string]string{"x-connection-id": "or"})
	if rec.Code != http.StatusOK || rec.Header().Get("X-Upstream") != "1" {
		t.Fatalf("image %d %s", rec.Code, rec.Body.String())
	}
	if calls[0].path != "/images/generations" || calls[0].referer == "" || !strings.Contains(calls[0].auth, "sk-or") {
		t.Fatalf("image hit = %+v", calls[0])
	}
	var provider, model, endpoint string
	if err := database.QueryRow(`SELECT provider, model, endpoint FROM usageHistory ORDER BY timestamp DESC LIMIT 1`).Scan(&provider, &model, &endpoint); err != nil {
		t.Fatalf("usage: %v", err)
	}
	if provider != "openrouter" || model != "image" || endpoint != "/v1/images/generations" {
		t.Fatalf("usage provider=%s model=%s endpoint=%s", provider, model, endpoint)
	}

	calls = nil
	withProvider(t, "openrouter", func(cfg *providers.ProviderConfig) {
		cfg.ImageURL = upstream.URL + "/images/generations"
		cfg.VideoURL = upstream.URL + "/videos/generations"
		cfg.SystemoneURL = upstream.URL + "/systemone"
	})
	rec = post("/v1/videos/generations", `{"model":"openrouter/sora","prompt":"cat"}`, nil)
	if rec.Code != http.StatusOK || calls[0].path != "/videos/generations" {
		t.Fatalf("video exact %d path=%v body=%s", rec.Code, calls, rec.Body.String())
	}

	calls = nil
	withProvider(t, "openrouter", func(cfg *providers.ProviderConfig) {
		cfg.VideoURL = upstream.URL + "/videos"
		cfg.SystemoneURL = upstream.URL + "/systemone"
		cfg.ImageURL = upstream.URL + "/images/generations"
	})
	rec = post("/v1/videos/generations", `{"model":"openrouter/sora","prompt":"dog"}`, nil)
	if rec.Code != http.StatusOK || calls[0].path != "/videos/generations" {
		t.Fatalf("video suffix %d path=%v", rec.Code, calls)
	}

	calls = nil
	rec = post("/v1/systemone", `{"model":"openrouter/judge","state":"s","questions":{}}`, nil)
	if rec.Code != http.StatusOK || calls[0].path != "/systemone" {
		t.Fatalf("systemone %d path=%v body=%s", rec.Code, calls, rec.Body.String())
	}

	calls = nil
	rec = post("/v1/audio/speech", `{"model":"elevenlabs/rachel","input":"hello","voice":"rachel"}`, nil)
	if rec.Code != http.StatusOK || calls[0].path != "/custom-tts" {
		t.Fatalf("tts url %d path=%v body=%s", rec.Code, calls, rec.Body.String())
	}

	calls = nil
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", "deepgram/nova")
	fw, err := mw.CreateFormFile("file", "a.wav")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("audio"))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec = httptest.NewRecorder()
	h.HandleAudioTranscriptions(rec, req)
	if rec.Code != http.StatusOK || len(calls) == 0 || calls[0].path != "/listen" {
		t.Fatalf("stt %d calls=%v body=%s", rec.Code, calls, rec.Body.String())
	}

	calls = nil
	rec = post("/v1/web/fetch", `{"model":"ollama/llama","url":"https://example.com/a"}`, nil)
	if rec.Code != http.StatusOK || calls[0].path != "/web_fetch" || calls[0].method != http.MethodPost {
		t.Fatalf("fetch %d calls=%v", rec.Code, calls)
	}
	if strings.Contains(calls[0].body, "llama") || !strings.Contains(calls[0].body, "https://example.com/a") {
		t.Fatalf("ollama body = %s", calls[0].body)
	}

	calls = nil
	rec = post("/v1/search", `{"model":"openai/gpt-4o","query":"cats and dogs"}`, nil)
	if rec.Code != http.StatusOK || !strings.HasSuffix(calls[0].path, "/v1/search") {
		t.Fatalf("search %d calls=%v body=%s", rec.Code, calls, rec.Body.String())
	}

	calls = nil
	rec = post("/v1/scrape", `{"model":"openai/gpt-4o","url":"https://example.com"}`, nil)
	if rec.Code != http.StatusOK || !strings.HasSuffix(calls[0].path, "/v1/scrape") {
		t.Fatalf("scrape %d calls=%v", rec.Code, calls)
	}

	// No API key: the connection exists but the stored blob has neither key.
	if _, err := database.Exec(`UPDATE providerConnections SET data = '{}' WHERE id = 'oa'`); err != nil {
		t.Fatal(err)
	}
	rec = post("/v1/images/generations", `{"model":"openai/dall-e-3","prompt":"x"}`, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no key %d %s", rec.Code, rec.Body.String())
	}
	if _, err := database.Exec(`UPDATE providerConnections SET data = ? WHERE id = 'oa'`, `{"apiKey":"sk-oa","baseUrl":"`+upstream.URL+`"}`); err != nil {
		t.Fatal(err)
	}

	models, _ := json.Marshal([]string{"openai/gpt-image-bad", "openai/dall-e-3"})
	if _, err := database.Exec(`INSERT INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES (?,?,?,?,?,?)`,
		"combo-1", "media-fallback", "fallback", string(models), "2026-07-18T00:00:00Z", "2026-07-18T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	calls = nil
	rec = post("/v1/images/generations", `{"model":"media-fallback","prompt":"fail-401 then ok"}`, nil)
	if rec.Code != http.StatusOK || len(calls) != 2 {
		t.Fatalf("combo rotate status=%d calls=%d body=%s", rec.Code, len(calls), rec.Body.String())
	}

	calls = nil
	rec = post("/v1/images/generations", `{"model":"media-fallback","prompt":"fail-500"}`, nil)
	if rec.Code != http.StatusInternalServerError || len(calls) != 1 {
		t.Fatalf("combo stop status=%d calls=%d body=%s", rec.Code, len(calls), rec.Body.String())
	}
}

func TestMiMoSpeechBranches(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := ioReadAll(r.Body)
		switch {
		case strings.Contains(string(raw), "no-audio"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"choices":[{"message":{"audio":{}}}],"error":{"message":"silent"}}`))
		case strings.Contains(string(raw), "bad-status"):
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"rejected"}}`))
		case strings.Contains(string(raw), "plain-error"):
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`upstream plain`))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"audio":{"data":"@@@@","format":"wav"}}}]}`))
		}
	}))
	defer upstream.Close()
	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	seedMiMoConn(t, database, upstream.URL+"/v1/chat/completions")
	h := newTestMediaHandler(db.NewRepo(database))

	rec := httptest.NewRecorder()
	h.HandleAudioSpeech(rec, httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"mimo/mimo-v2.5-tts","input":"  "}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.HandleAudioSpeech(rec, httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"mimo/mimo-v2.5-tts","input":"no-audio"}`)))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "silent") {
		t.Fatalf("no audio %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.HandleAudioSpeech(rec, httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"mimo/mimo-v2.5-tts","input":"bad-status"}`)))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "rejected") {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.HandleAudioSpeech(rec, httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"mimo/mimo-v2.5-tts","input":"plain-error"}`)))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "upstream plain") {
		t.Fatalf("plain %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.HandleAudioSpeech(rec, httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"mimo/mimo-v2.5-tts","input":"audio","response_format":"json"}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "@@@@") {
		t.Fatalf("json %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.HandleAudioSpeech(rec, httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"mimo/mimo-v2.5-tts","input":"audio"}`)))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "invalid audio") {
		t.Fatalf("b64 %d %s", rec.Code, rec.Body.String())
	}
}

func TestAntigravityMediaEarlyErrors(t *testing.T) {
	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	seedConn(t, database, "ag-empty", "antigravity", "", "", 1)
	h := newTestMediaHandler(db.NewRepo(database))

	rec := httptest.NewRecorder()
	h.HandleImages(rec, httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"antigravity/gemini-3.1-flash-image","prompt":"  "}`)))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "prompt") {
		t.Fatalf("prompt %d %s", rec.Code, rec.Body.String())
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", "antigravity/gemini-2.5-flash")
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec = httptest.NewRecorder()
	h.HandleAudioTranscriptions(rec, req)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "file") {
		t.Fatalf("stt file %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", strings.NewReader(`{"model":"antigravity/gemini-2.5-flash"}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.HandleAudioTranscriptions(rec, req)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "multipart") {
		t.Fatalf("stt type %d %s", rec.Code, rec.Body.String())
	}

	info := &chat.ModelInfo{Provider: "antigravity", Model: "gemini-2.5-flash"}
	conn := &models.ProviderConnection{ID: "ag-empty"}
	if err := h.tryAntigravitySTTConn(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil), nil, []byte("audio"), "a.wav", "gemini-2.5-flash", "", "", "", conn, &chat.ConnectionData{}); err == nil || !strings.Contains(err.Error(), "no API key") {
		t.Fatalf("stt key: %v", err)
	}
	if err := h.tryAntigravityImageConn(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil), []byte(`{"prompt":"x"}`), "x", "gemini", "1:1", conn, &chat.ConnectionData{}); err == nil || !strings.Contains(err.Error(), "no API key") {
		t.Fatalf("image key: %v", err)
	}
	_ = info
}

func TestCatalogKeyWhenNoConnection(t *testing.T) {
	var auth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	withProvider(t, "openai", func(cfg *providers.ProviderConfig) {
		cfg.BaseURL = upstream.URL + "/v1/chat/completions"
		cfg.DefaultAPIKey = "catalog-key"
	})
	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	ensureUsageTables(t, database)
	h := newTestMediaHandler(db.NewRepo(database))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"openai/dall-e-3","prompt":"cat"}`))
	req.Header.Set("Content-Type", "application/json")
	h.HandleImages(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	if auth != "Bearer catalog-key" {
		t.Fatalf("auth = %q", auth)
	}
}
