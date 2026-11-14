package media

import (
	"encoding/base64"
	json "encoding/json/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9router/proxy/internal/db"
	"9router/proxy/internal/handlers/chat"
	"9router/proxy/internal/providers"
)

func TestSynthesizeEdgeAndGoogleTTS(t *testing.T) {
	resetTTSTokens(t)
	blockDirectDial(t)

	var translatorHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "translator"):
			translatorHits++
			w.Header().Add("Set-Cookie", "MUID=abc; path=/")
			w.Header().Add("Set-Cookie", "SRCHHPGUSR=1")
			_, _ = w.Write([]byte(`params_AbusePreventionHelper = [123456,"tok-test",3600000];`))
		case strings.Contains(r.URL.Path, "tfettts"):
			body := r.FormValue("ssml")
			if !strings.Contains(body, "tok-test") && !strings.Contains(r.PostFormValue("token"), "tok-test") {
				raw := readReq(r)
				if !strings.Contains(raw, "tok-test") {
					t.Errorf("speech body missing token: %s", raw)
				}
			}
			if r.Header.Get("Cookie") == "" {
				t.Error("missing bing cookie")
			}
			if strings.Contains(r.URL.RawQuery, "short") || strings.Contains(readPeek(r), "SHORT") {
				_, _ = w.Write([]byte("tiny"))
				return
			}
			_, _ = w.Write(make([]byte, 2048))
		case r.URL.Path == "/" || strings.Contains(r.URL.Path, "translate.google.com"):
			_, _ = w.Write([]byte(`"FdrFJe":"fsid-test" "cfb2h":"bl-test"`))
		default:
			b64 := strings.Repeat("A", 120)
			inner, _ := json.Marshal([]any{b64})
			outer, _ := json.Marshal([]any{[]any{nil, nil, string(inner)}})
			_, _ = w.Write([]byte("a\nb\nc\n" + string(outer)))
		}
	}))
	defer srv.Close()
	client, rt := newLoopbackClient(srv, srv.URL)
	swapDirectClient(t, client)

	if _, err := SynthesizeEdgeTTS(t.Context(), client, "   ", "en-US-AriaNeural"); err == nil {
		t.Fatal("empty text should fail")
	}
	audio, err := SynthesizeEdgeTTS(t.Context(), client, "hello", "en-US-MaleNeural")
	if err != nil || len(audio) != 2048 {
		t.Fatalf("edge audio: len=%d err=%v", len(audio), err)
	}
	if translatorHits != 1 {
		t.Fatalf("translator hits = %d", translatorHits)
	}
	if _, err := SynthesizeEdgeTTS(t.Context(), client, "again", "alloy"); err != nil {
		t.Fatalf("cached token: %v", err)
	}
	if translatorHits != 1 {
		t.Fatalf("token cache missed, hits=%d", translatorHits)
	}
	for _, host := range rt.hosts {
		if host != "www.bing.com" && host != "translate.google.com" {
			t.Fatalf("tts dialed %q", host)
		}
	}

	clearTTSTokens()
	short := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "translator") {
			_, _ = w.Write([]byte(`params_AbusePreventionHelper = [1,"tok",1];`))
			return
		}
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("nope"))
	}))
	defer short.Close()
	shortClient, _ := newLoopbackClient(short, short.URL)
	if _, err := SynthesizeEdgeTTS(t.Context(), shortClient, "hello", "en-US-AriaNeural"); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("edge status: %v", err)
	}

	clearTTSTokens()
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`no token here`))
	}))
	defer page.Close()
	pageClient, _ := newLoopbackClient(page, page.URL)
	if _, err := SynthesizeEdgeTTS(t.Context(), pageClient, "hello", ""); err == nil {
		t.Fatal("expected token parse failure")
	}

	clearTTSTokens()
	tiny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "translator") {
			_, _ = w.Write([]byte(`params_AbusePreventionHelper = [1,"tok",1];`))
			return
		}
		_, _ = w.Write([]byte("tiny"))
	}))
	defer tiny.Close()
	tinyClient, _ := newLoopbackClient(tiny, tiny.URL)
	if _, err := SynthesizeEdgeTTS(t.Context(), tinyClient, "hello", "default"); err == nil || !strings.Contains(err.Error(), "empty audio") {
		t.Fatalf("short audio: %v", err)
	}

	if _, err := SynthesizeGoogleTTS(t.Context(), client, "  ", "en"); err == nil {
		t.Fatal("empty google text")
	}
	clearTTSTokens()
	audio, err = SynthesizeGoogleTTS(t.Context(), client, "hello @world, friend", "id")
	if err != nil {
		t.Fatalf("google: %v", err)
	}
	want, _ := base64.StdEncoding.DecodeString(strings.Repeat("A", 120))
	if string(audio) != string(want) {
		t.Fatalf("google audio len %d", len(audio))
	}
	// Cached tokens: a second call still needs the batch endpoint but not the page.
	if _, err := SynthesizeGoogleTTS(t.Context(), client, "second", ""); err != nil {
		t.Fatalf("cached google: %v", err)
	}

	clearTTSTokens()
	if _, err := SynthesizeGoogleTTS(t.Context(), pageClient, "hello", "en"); err == nil {
		t.Fatal("expected google token parse failure")
	}
	badBatch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`"FdrFJe":"fs" "cfb2h":"bl"`))
			return
		}
		_, _ = w.Write([]byte("only\ntwo"))
	}))
	defer badBatch.Close()
	badClient, _ := newLoopbackClient(badBatch, badBatch.URL)
	if _, err := SynthesizeGoogleTTS(t.Context(), badClient, "hello", "alloy"); err == nil {
		t.Fatal("expected short google response")
	}
}

func TestHandleAudioSpeech_EdgeGoogleAndNvidia(t *testing.T) {
	resetTTSTokens(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "translator"):
			_, _ = w.Write([]byte(`params_AbusePreventionHelper = [9,"tok-handler",1];`))
		case strings.Contains(r.URL.Path, "tfettts"):
			_, _ = w.Write(make([]byte, 1100))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`"FdrFJe":"fs" "cfb2h":"bl"`))
		default:
			b64 := strings.Repeat("A", 120)
			inner, _ := json.Marshal([]any{b64})
			outer, _ := json.Marshal([]any{[]any{nil, nil, string(inner)}})
			_, _ = w.Write([]byte("a\nb\nc\n" + string(outer)))
		}
	}))
	defer srv.Close()
	client, _ := newLoopbackClient(srv, srv.URL)
	swapDirectClient(t, client)

	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	h := newTestMediaHandler(db.NewRepo(database))
	h.Client = client

	rec := httptest.NewRecorder()
	h.HandleAudioSpeech(rec, httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader("{")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.HandleAudioSpeech(rec, httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"edge-tts/en-US-AriaNeural","input":"  "}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing input %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech?response_format=json", strings.NewReader(`{"model":"edge-tts/en-US-AriaNeural","input":"hello","response_format":"json"}`))
	h.HandleAudioSpeech(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"format":"mp3"`) {
		t.Fatalf("edge json %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.HandleAudioSpeech(rec, httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"google-tts/id","input":"halo","voice":"id"}`)))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "audio/mpeg" {
		t.Fatalf("google speech %d ct=%s body=%s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}

	nv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer bad-key":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`bad request`))
		case "Bearer good-key":
			_, _ = w.Write([]byte("RIFFwav"))
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer nv.Close()
	seedConn(t, database, "nv-bad", "nvidia", "bad-key", nv.URL, 1)
	seedConn(t, database, "nv-good", "nvidia", "good-key", nv.URL, 2)
	rec = httptest.NewRecorder()
	h.HandleAudioSpeech(rec, httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"nvidia/fastpitch","input":"hello","voice":"nova"}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "RIFFwav") {
		t.Fatalf("nvidia rotate %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	pinned := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"nvidia/fastpitch","input":"hello"}`))
	pinned.Header.Set("x-connection-id", "nv-bad")
	h.HandleAudioSpeech(rec, pinned)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "bad request") {
		t.Fatalf("pinned nvidia %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.HandleAudioSpeech(rec, httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"nvidia/fastpitch","input":"hello"}`)))
	// The bad account is not pinned here, so rotation still reaches the good one.
	if rec.Code != http.StatusOK {
		t.Fatalf("second rotate %d %s", rec.Code, rec.Body.String())
	}
}

func TestNvidiaTTSTransportFailureIsSilent(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	seedConn(t, database, "nv-down", "nvidia", "nv-key", "http://"+addr, 1)
	h := newTestMediaHandler(db.NewRepo(database))
	rec := httptest.NewRecorder()
	h.HandleAudioSpeech(rec, httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"nvidia/fastpitch/alloy","input":"hello"}`)))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestNvidiaTTSURLAndEmptyKey(t *testing.T) {
	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	h := newTestMediaHandler(repo)
	chatH := h.ChatH

	if got := nvidiaTTSURL(chatH, &chat.ConnectionData{BaseURL: "http://127.0.0.1:9/tts/"}); got != "http://127.0.0.1:9/tts" {
		t.Fatalf("base override = %q", got)
	}
	withProvider(t, "nvidia", func(cfg *providers.ProviderConfig) { cfg.BaseURL = "" })
	if got := nvidiaTTSURL(chatH, &chat.ConnectionData{}); got != "https://integrate.api.nvidia.com/v1/audio/speech" {
		t.Fatalf("fallback = %q", got)
	}

	seedConn(t, database, "nv-empty", "nvidia", "", "http://127.0.0.1:9", 1)
	conn, err := repo.GetProviderConnectionByID("nv-empty")
	if err != nil || conn == nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	status, msg, done := h.tryNvidiaTTSConn(rec, httptest.NewRequest(http.MethodPost, "/", nil), nil, &chat.ModelInfo{Provider: "nvidia", Model: ""}, "hi", "", "", conn, &chat.ConnectionData{})
	if done || status != http.StatusUnauthorized || !strings.Contains(msg, "no API key") {
		t.Fatalf("empty key status=%d done=%v msg=%s", status, done, msg)
	}

	rec = httptest.NewRecorder()
	h.HandleAudioSpeech(rec, httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"nvidia/fastpitch","input":"hi"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no key handler %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.HandleAudioSpeech(rec, httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"model":"no-such-provider/voice","input":"hi"}`)))
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusBadRequest {
		t.Fatalf("missing nvidia-like %d %s", rec.Code, rec.Body.String())
	}
}

func readReq(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	b, _ := ioReadAll(r.Body)
	return string(b)
}

func readPeek(r *http.Request) string { return "" }
