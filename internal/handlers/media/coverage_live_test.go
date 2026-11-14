package media

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"

	"9router/proxy/internal/db"
	"9router/proxy/internal/handlers/chat"
)

func TestLiveSTTHelpersAndHandlerErrors(t *testing.T) {
	if _, err := toLiveWSURL("http://[", "m", "t"); err == nil {
		t.Fatal("expected an unparseable base URL to fail")
	}
	var live liveSTTError
	live.message = "boom"
	if live.Error() != "boom" {
		t.Fatal(live.Error())
	}

	s := &liveSTTSession{goAwayLeft: 0}
	if s.tryRotate("1s") {
		t.Fatal("spent budget should not rotate")
	}
	s.goAwayLeft = 1
	if !s.tryRotate("1s") || s.tryRotate("1s") {
		t.Fatal("one advisory is the whole budget")
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "a.wav")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("abcd"))
	_ = mw.WriteField("model", "gemini-live")
	_ = mw.WriteField("language", "en")
	_ = mw.WriteField("prompt", "verbatim")
	contentType := mw.FormDataContentType()
	_ = mw.Close()
	audio, audioType, model, form, err := parseLiveSTTForm(buf.Bytes(), contentType)
	if err != nil || string(audio) != "abcd" || model != "gemini-live" || form["language"] != "en" || form["prompt"] != "verbatim" {
		t.Fatalf("form audio=%q type=%q model=%q form=%v err=%v", audio, audioType, model, form, err)
	}
	if _, _, _, _, err := parseLiveSTTForm([]byte("x"), "application/json"); err == nil {
		t.Fatal("expected invalid multipart")
	}
	if _, _, _, _, err := parseLiveSTTForm([]byte("--b\r\nContent-Disposition: form-data; name=\"file\"\r\n\r\n"), "multipart/form-data; boundary=b"); err == nil {
		t.Fatal("expected a truncated part to fail")
	}

	rec := httptest.NewRecorder()
	writeLiveSTTResult(rec, &liveSTTResult{text: "hi", chunks: []string{"h", "i"}}, "verbose_json")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"segments"`) {
		t.Fatalf("verbose %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	writeLiveSTTError(rec, liveErrf(http.StatusGatewayTimeout, "too slow"))
	if rec.Code != http.StatusGatewayTimeout || !strings.Contains(rec.Body.String(), "too slow") {
		t.Fatalf("live error %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	writeLiveSTTError(rec, errReader{})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("plain live error %d", rec.Code)
	}
	logLiveSTTTraffic("gemini", "gemini-2.5-flash-native-audio-preview-09-17", errReader{})

	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	h := newTestMediaHandler(db.NewRepo(database))
	body := buf.Bytes()
	info := &chat.ModelInfo{Provider: "gemini", Model: "gemini-2.5-flash-native-audio-preview-09-17"}

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	if err := h.handleGeminiLiveSTT(httptest.NewRecorder(), req, body, info); err == nil {
		t.Fatal("expected bad multipart from the handler")
	}
	empty := multipartBody(t, map[string]string{"model": info.Model}, nil)
	req = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(empty.body))
	req.Header.Set("Content-Type", empty.contentType)
	err = h.handleGeminiLiveSTT(httptest.NewRecorder(), req, empty.body, info)
	var liveErr *liveSTTError
	if !errors.As(err, &liveErr) || liveErr.status != http.StatusBadRequest {
		t.Fatalf("missing file: %v", err)
	}

	file := multipartBody(t, map[string]string{"model": "gemini/" + info.Model}, []byte("abcd"))
	req = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(file.body))
	req.Header.Set("Content-Type", file.contentType)
	rec = httptest.NewRecorder()
	h.HandleAudioTranscriptions(rec, req)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "no active connection") {
		t.Fatalf("no conn %d %s", rec.Code, rec.Body.String())
	}

	seedConn(t, database, "gem-empty", "gemini", "", "", 1)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(file.body))
	req.Header.Set("Content-Type", file.contentType)
	h.HandleAudioTranscriptions(rec, req)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "no API key") {
		t.Fatalf("no key %d %s", rec.Code, rec.Body.String())
	}
}

func TestTranscribeGeminiLive_GoAwayErrorAndJunk(t *testing.T) {
	audio := []byte{1, 2, 3, 4}

	t.Run("goAway reconnects once", func(t *testing.T) {
		var n atomic.Int32
		srv := newLiveSTTServer(t, func(conn *websocket.Conn) {
			if n.Add(1) == 1 {
				sendFrame(t, conn, map[string]any{"goAway": map[string]any{"time": "5s"}})
				return
			}
			sendFrame(t, conn, map[string]any{"serverContent": map[string]any{"setupComplete": true}})
			for {
				_, data, err := conn.ReadMessage()
				if err != nil {
					return
				}
				if bytes.Contains(data, []byte(`"turnComplete":true`)) {
					break
				}
			}
			sendFrame(t, conn, map[string]any{"not": "json-wait"})
			_ = conn.WriteMessage(websocket.TextMessage, []byte("not-json"))
			sendFrame(t, conn, map[string]any{"serverContent": map[string]any{
				"inputTranscription": map[string]any{"text": "rotated"},
				"turnComplete":       true,
			}})
		})
		got, err := transcribeGeminiLive(t.Context(), srv.URL, "m", "tok", audio, "audio/wav", map[string]string{
			"prompt": "say it",
		})
		if err != nil || got == nil || got.text != "rotated" {
			t.Fatalf("goAway result=%v err=%v", got, err)
		}
		if n.Load() < 2 {
			t.Fatalf("reconnects = %d", n.Load())
		}
	})

	t.Run("server error frame", func(t *testing.T) {
		srv := newLiveSTTServer(t, func(conn *websocket.Conn) {
			sendFrame(t, conn, map[string]any{"error": map[string]any{"message": "nope", "status": "INVALID"}})
		})
		_, err := transcribeGeminiLive(t.Context(), srv.URL, "m", "tok", audio, "audio/wav", nil)
		if err == nil || !strings.Contains(err.Error(), "INVALID") || !strings.Contains(err.Error(), "nope") {
			t.Fatalf("error frame: %v", err)
		}
	})
}

type multipartParts struct {
	body        []byte
	contentType string
}

func multipartBody(t *testing.T, fields map[string]string, file []byte) multipartParts {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if file != nil {
		fw, err := mw.CreateFormFile("file", "a.wav")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(file); err != nil {
			t.Fatal(err)
		}
	}
	ct := mw.FormDataContentType()
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return multipartParts{body: buf.Bytes(), contentType: ct}
}
