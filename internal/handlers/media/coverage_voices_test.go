package media

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"9router/proxy/internal/db"
)

func TestVoiceNameHelpers(t *testing.T) {
	if countryName("", "US") != "United States" || countryName("ZZ", "US") != "ZZ" || countryName("", "") != "" {
		t.Fatalf("countryName: %q %q %q", countryName("", "US"), countryName("ZZ", "US"), countryName("", ""))
	}
	if langName("en") != "English" || langName("zz") != "zz" {
		t.Fatalf("langName: %q %q", langName("en"), langName("zz"))
	}
	if firstNonEmpty("", "a", "b") != "a" || firstNonEmpty() != "" {
		t.Fatal("firstNonEmpty")
	}
	tests := []struct {
		in   any
		want float64
		ok   bool
	}{
		{float64(1.5), 1.5, true},
		{float32(2), 2, true},
		{3, 3, true},
		{int64(4), 4, true},
		{" 5.5 ", 5.5, true},
		{"nope", 0, false},
		{true, 0, false},
	}
	for _, tc := range tests {
		got, ok := toFloat(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("toFloat(%v) = %v,%v want %v,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestConnectionAPIKey(t *testing.T) {
	if (&MediaHandler{}).connectionAPIKey("deepgram") != "" {
		t.Fatal("nil repo should yield an empty key")
	}
	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	seedConn(t, database, "dg-token", "deepgram", "", "", 1)
	if _, err := database.Exec(`UPDATE providerConnections SET data = ? WHERE id = ?`, `{"accessToken":"dg-access"}`, "dg-token"); err != nil {
		t.Fatal(err)
	}
	h := newTestMediaHandler(db.NewRepo(database))
	if got := h.connectionAPIKey("deepgram"); got != "dg-access" {
		t.Fatalf("access token = %q", got)
	}
	if got := h.connectionAPIKey("missing"); got != "" {
		t.Fatalf("missing provider = %q", got)
	}
}

func TestHandleAudioVoices_GeminiAndLocal(t *testing.T) {
	resetVoiceCaches(t)
	h := NewMediaHandler(nil, nil, nil)

	rec := httptest.NewRecorder()
	h.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/voices?provider=gemini", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("gemini: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Voices    []map[string]any `json:"voices"`
		Languages []map[string]any `json:"languages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Voices) != len(geminiPrebuilt) || body.Voices[0]["id"] != "Zephyr" || body.Voices[0]["gender"] != "Female" {
		t.Fatalf("gemini voices: len=%d first=%v", len(body.Voices), body.Voices[0])
	}
	if len(body.Languages) != 1 || body.Languages[0]["name"] != "English" {
		t.Fatalf("languages = %v", body.Languages)
	}

	rec = httptest.NewRecorder()
	h.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/voices?provider=gemini&lang=zz", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Voices) != 0 {
		t.Fatalf("lang filter kept %d voices", len(body.Voices))
	}

	dir := t.TempDir()
	countFile := filepath.Join(dir, "say-count")
	writeExec(t, dir, "say", "#!/bin/sh\necho x >> \""+countFile+"\"\nprintf '%s\\n' 'Samantha            en_US    # Hello' 'not a voice' 'Alex                en_GB'\n")
	t.Setenv("PATH", dir)
	rec = httptest.NewRecorder()
	h.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/voices?provider=local-device", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("local: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Voices) != 2 || body.Voices[0]["countryName"] != "United States" {
		t.Fatalf("local voices = %v", body.Voices)
	}
	rec = httptest.NewRecorder()
	h.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/voices?provider=local-device", nil))
	raw, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "x") != 1 {
		t.Fatalf("say ran %d times, cache missed", strings.Count(string(raw), "x"))
	}

	t.Setenv("PATH", t.TempDir())
	clearVoiceCaches()
	rec = httptest.NewRecorder()
	h.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/voices?provider=local-device", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Voices) != 0 {
		t.Fatalf("missing say returned %v", body.Voices)
	}
}

func TestFetchEdgeTTSVoices_ErrorsAndCache(t *testing.T) {
	resetVoiceCaches(t)
	orig := edgeVoicesURL
	t.Cleanup(func() { edgeVoicesURL = orig })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("mode") {
		case "bad":
			w.WriteHeader(http.StatusBadGateway)
		case "json":
			_, _ = w.Write([]byte(`not-json`))
		default:
			_, _ = w.Write([]byte(`[{"ShortName":"en-US-AriaNeural","FriendlyName":"","Locale":"en","Gender":"Female"}]`))
		}
	}))
	defer srv.Close()

	edgeVoicesURL = srv.URL + "?mode=bad"
	if _, err := fetchEdgeTTSVoices(srv.Client(), t.Context()); err == nil {
		t.Fatal("expected non-200")
	}
	edgeVoicesURL = srv.URL + "?mode=json"
	if _, err := fetchEdgeTTSVoices(srv.Client(), t.Context()); err == nil {
		t.Fatal("expected bad json")
	}
	edgeVoicesURL = srv.URL + "?mode=ok"
	voices, err := fetchEdgeTTSVoices(srv.Client(), t.Context())
	if err != nil || len(voices) != 1 || voices[0]["name"] != "en-US-AriaNeural" || voices[0]["country"] != "" {
		t.Fatalf("voices=%v err=%v", voices, err)
	}
	edgeVoicesURL = srv.URL + "?mode=bad"
	voices, err = fetchEdgeTTSVoices(srv.Client(), t.Context())
	if err != nil || len(voices) != 1 {
		t.Fatalf("cache miss: %v %v", voices, err)
	}
}

func TestHandleAudioVoices_ElevenLabs(t *testing.T) {
	resetVoiceCaches(t)
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("xi-api-key") != "el-key" {
			t.Errorf("xi-api-key = %q", r.Header.Get("xi-api-key"))
		}
		switch r.URL.Query().Get("mode") {
		case "bad":
			w.WriteHeader(http.StatusUnauthorized)
			return
		case "json":
			_, _ = w.Write([]byte(`{`))
			return
		}
		_, _ = w.Write([]byte(`{"voices":[
			{"voice_id":"v1","name":"Rachel","labels":{"language":"en-US","gender":"female"},"category":"premade"},
			{"voice_id":"v2","name":"Plain","labels":{}}
		]}`))
	}))
	defer srv.Close()

	h := NewMediaHandler(nil, nil, nil)
	client, rt := newLoopbackClient(srv, srv.URL)
	h.Client = client
	// The production URL is a const. Point the direct fallback at the same
	// loopback server so a transport retry cannot leave the machine. The first
	// hop already uses h.Client, which rewrites api.elevenlabs.io.
	swapDirectClient(t, client)

	if _, err := fetchElevenLabsVoices(client, t.Context(), ""); err == nil {
		t.Fatal("expected missing key")
	}

	rec := httptest.NewRecorder()
	h.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/voices?provider=elevenlabs&apiKey=el-key", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Voices []map[string]any `json:"voices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Voices) != 2 || body.Voices[1]["lang"] != "en" || body.Voices[0]["category"] != "premade" {
		t.Fatalf("voices = %v", body.Voices)
	}
	callsBefore := calls
	rec = httptest.NewRecorder()
	h.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/voices?provider=elevenlabs&apiKey=el-key", nil))
	if rec.Code != http.StatusOK || calls != callsBefore {
		t.Fatalf("cache: calls %d->%d status %d", callsBefore, calls, rec.Code)
	}
	for _, host := range rt.hosts {
		if host != "api.elevenlabs.io" {
			t.Fatalf("dialed %q", host)
		}
	}

	clearVoiceCaches()
	// Force the rewritten URL to be whatever the const is; the server switches
	// on nothing for the error once we close and use a status-only server.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer bad.Close()
	badClient, _ := newLoopbackClient(bad, bad.URL)
	if _, err := fetchElevenLabsVoices(badClient, t.Context(), "el-key"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("status error: %v", err)
	}
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{`))
	}))
	defer broken.Close()
	brokenClient, _ := newLoopbackClient(broken, broken.URL)
	if _, err := fetchElevenLabsVoices(brokenClient, t.Context(), "other-key"); err == nil {
		t.Fatal("expected json error")
	}
}

func TestHandleAudioVoices_DeepgramInworldMinimax(t *testing.T) {
	resetVoiceCaches(t)
	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	seedConn(t, database, "dg", "deepgram", "dg-key", "", 1)
	seedConn(t, database, "iw", "inworld", "iw-key", "", 1)
	seedConn(t, database, "mm", "minimax", "mm-key", "", 1)
	seedConn(t, database, "mmcn", "minimax-cn", "mmcn-key", "", 1)
	h := newTestMediaHandler(db.NewRepo(database))

	var deepgramCalls int
	deepgram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deepgramCalls++
		if r.Header.Get("Authorization") != "Token dg-key" {
			t.Errorf("deepgram auth %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"tts":[
			{"name":"Aura","canonical_name":"aura-asteria-en","languages":["en","es"],"metadata":{"tags":["feminine","other"]}},
			{"name":"Bare","canonical_name":"","languages":[],"metadata":{"tags":["masculine"]}},
			{"name":"Dup","canonical_name":"aura-asteria-en","languages":["en"],"metadata":{"tags":[]}}
		]}`))
	}))
	defer deepgram.Close()
	client, rt := newLoopbackClient(deepgram, deepgram.URL)
	h.Client = client
	swapDirectClient(t, client)
	rec := httptest.NewRecorder()
	h.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/voices?provider=deepgram", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("deepgram %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Voices []map[string]any `json:"voices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	// en+aura, es+aura, en+Bare. The duplicate en+aura is dropped.
	if len(body.Voices) != 3 {
		t.Fatalf("deepgram voices = %v", body.Voices)
	}
	rec = httptest.NewRecorder()
	h.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/voices?provider=deepgram", nil))
	if deepgramCalls != 1 {
		t.Fatalf("deepgram cache calls = %d", deepgramCalls)
	}
	for _, host := range rt.hosts {
		if host != "api.deepgram.com" {
			t.Fatalf("deepgram dialed %q", host)
		}
	}

	inworld := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Basic iw-key" {
			t.Errorf("inworld auth %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"voices":[
			{"voiceId":"","voice_id":"iv1","name":"","languages":[]},
			{"voiceId":"iv2","name":"Sam","languages":["ja","ko"]},
			{"name":"skip-me"}
		]}`))
	}))
	defer inworld.Close()
	clearVoiceCaches()
	h.Client, _ = newLoopbackClient(inworld, inworld.URL)
	rec = httptest.NewRecorder()
	h.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/voices?provider=inworld", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("inworld %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Voices) != 3 || body.Voices[0]["name"] != "iv1" || body.Voices[0]["lang"] != "en" {
		t.Fatalf("inworld voices = %v", body.Voices)
	}

	var minimaxHosts []string
	minimax := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			VoiceType string `json:"voice_type"`
		}
		_ = json.UnmarshalRead(r.Body, &payload)
		if payload.VoiceType == "clone" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"nope"}`))
			return
		}
		if payload.VoiceType == "badstatus" {
			_, _ = w.Write([]byte(`{"baseResp":{"statusCode":"7","statusMsg":"quota"}}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"base_resp":{"status_code":0,"status_msg":"success"},
			"system_voice":[{"voice_id":"English_Calm","voice_name":"Calm"},{"voiceId":"_Warm","voiceName":"Warm"}],
			"voice_cloning":[{"voice_id":"clone1","voice_name":""},{"voice_id":"clone1"},{"voice_id":""}],
			"voice_generation":[],
			"music_generation":[{"voice_id":"music1","voice_name":"Song"}]
		}`))
	}))
	defer minimax.Close()
	clearVoiceCaches()
	client, rt = newLoopbackClient(minimax, minimax.URL)
	h.Client = client
	rec = httptest.NewRecorder()
	h.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/voices?provider=minimax&voice_type=all", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("minimax %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Voices) != 4 {
		t.Fatalf("minimax voices = %v", body.Voices)
	}
	rec = httptest.NewRecorder()
	h.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/voices?provider=minimax&voice_type=all", nil))
	minimaxHosts = append(minimaxHosts, rt.hosts...)
	if len(rt.hosts) != 1 || rt.hosts[0] != "api.minimax.io" {
		t.Fatalf("minimax hosts = %v", rt.hosts)
	}

	clearVoiceCaches()
	rec = httptest.NewRecorder()
	h.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/voices?provider=minimax-cn", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("minimax-cn %d %s", rec.Code, rec.Body.String())
	}
	if rt.hosts[len(rt.hosts)-1] != "api.minimaxi.com" {
		t.Fatalf("minimax-cn host = %v", rt.hosts)
	}

	clearVoiceCaches()
	rec = httptest.NewRecorder()
	h.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/voices?provider=minimax&voice_type=clone", nil))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "nope") {
		t.Fatalf("minimax http: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/voices?provider=minimax&voice_type=badstatus", nil))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "quota") {
		t.Fatalf("minimax status: %d %s", rec.Code, rec.Body.String())
	}

	empty := NewMediaHandler(nil, nil, nil)
	empty.Client = client
	rec = httptest.NewRecorder()
	empty.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/voices?provider=deepgram", nil))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "No Deepgram connection") {
		t.Fatalf("no deepgram: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	empty.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/voices?provider=inworld", nil))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "No Inworld connection") {
		t.Fatalf("no inworld: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	empty.HandleAudioVoices(rec, httptest.NewRequest(http.MethodGet, "/voices?provider=minimax", nil))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "No minimax connection") {
		t.Fatalf("no minimax: %d %s", rec.Code, rec.Body.String())
	}
}

func TestFetchVoiceHTTPErrors(t *testing.T) {
	resetVoiceCaches(t)
	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	seedConn(t, database, "dg", "deepgram", "dg-key", "", 1)
	seedConn(t, database, "iw", "inworld", "iw-key", "", 1)
	h := newTestMediaHandler(db.NewRepo(database))

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "json") {
			_, _ = w.Write([]byte(`{`))
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer bad.Close()
	h.Client, _ = newLoopbackClient(bad, bad.URL)
	if _, err := h.fetchDeepgramVoices(t.Context()); err == nil || !strings.Contains(err.Error(), "Failed") {
		t.Fatalf("deepgram empty body: %v", err)
	}
	clearVoiceCaches()
	if _, err := h.fetchInworldVoices(t.Context()); err == nil {
		t.Fatal("expected inworld status error")
	}
}
