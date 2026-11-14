package media

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"9router/proxy/internal/db"
	"9router/proxy/internal/headroom"
)

func TestCLIToolsWithFakeBinaries(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	writeExec(t, dir, "devin", "#!/bin/sh\necho 'devin 9.9.9'\necho extra\n")
	writeExec(t, dir, "claude", "#!/bin/sh\nexit 0\n")
	writeExec(t, dir, "blank", "#!/bin/sh\nexit 0\n")
	writeExec(t, dir, "slow", "#!/bin/sh\nsleep 5\n")
	if err := os.MkdirAll(filepath.Join(home, "Library", "Application Support", "Claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("HOME", home)

	h := NewCLIToolsHandler()
	rec := httptest.NewRecorder()
	h.HandleAllStatuses(rec, httptest.NewRequest(http.MethodGet, "/cli-tools/all-statuses", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	var body map[string]*cliStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["copilot"] == nil || !body["copilot"].Installed || body["copilot"].Version != nil {
		t.Fatalf("copilot = %+v", body["copilot"])
	}
	if body["cowork"] == nil || !body["cowork"].Installed {
		t.Fatalf("cowork = %+v", body["cowork"])
	}
	if body["devin"] == nil || !body["devin"].Installed || body["devin"].Version == nil || *body["devin"].Version != "devin 9.9.9" {
		t.Fatalf("devin = %+v", body["devin"])
	}
	if body["claude"] == nil || !body["claude"].Installed || body["claude"].Version != nil {
		t.Fatalf("claude = %+v", body["claude"])
	}
	if body["codex"] == nil || body["codex"].Installed {
		t.Fatalf("codex = %+v", body["codex"])
	}

	if v, err := binVersion(t.Context(), "blank"); err != nil || v != "" {
		t.Fatalf("blank version = %q err=%v", v, err)
	}
	if _, err := binVersion(t.Context(), "missing-bin"); err == nil {
		t.Fatal("expected missing binary to fail")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := binVersion(ctx, "slow"); err == nil {
		t.Fatal("expected slow binary to time out")
	}

	t.Setenv("HOME", t.TempDir())
	if coworkDirOK() {
		t.Fatal("empty home should not see Claude Desktop")
	}
	if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "Claude-3p"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !coworkDirOK() {
		t.Fatal("Claude-3p dir should count as installed")
	}
}

func TestHeadroomHandlerLocalFake(t *testing.T) {
	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	t.Run("external url is rejected", func(t *testing.T) {
		putSettings(t, database, `{"headroomUrl":"https://example.com"}`)
		h := NewHeadroomHandler(repo)
		h.DataDir = t.TempDir()
		rec := httptest.NewRecorder()
		h.HandleHeadroomStart(rec, httptest.NewRequest(http.MethodPost, "/headroom/start", nil))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "EXTERNAL_PROXY") {
			t.Fatalf("start %d %s", rec.Code, rec.Body.String())
		}
		rec = httptest.NewRecorder()
		h.HandleHeadroomRestart(rec, httptest.NewRequest(http.MethodPost, "/headroom/restart", nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("restart %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("missing binary", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		putSettings(t, database, `{"headroomUrl":"http://127.0.0.1:9"}`)
		h := NewHeadroomHandler(repo)
		h.DataDir = t.TempDir()
		rec := httptest.NewRecorder()
		h.HandleHeadroomStart(rec, httptest.NewRequest(http.MethodPost, "/headroom/start", nil))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "NOT_INSTALLED") {
			t.Fatalf("start %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("early exit", func(t *testing.T) {
		installFakeHeadroom(t, fakeHeadroomExit7)
		putSettings(t, database, `{"headroomUrl":"http://127.0.0.1:9"}`)
		h := NewHeadroomHandler(repo)
		h.DataDir = t.TempDir()
		rec := httptest.NewRecorder()
		h.HandleHeadroomStart(rec, httptest.NewRequest(http.MethodPost, "/headroom/start", nil))
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "code=7") {
			t.Fatalf("early %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("start stop and restart", func(t *testing.T) {
		installFakeHeadroom(t, fakeHeadroomSleep)
		putSettings(t, database, `{"headroomUrl":"http://localhost","headroomCodeAware":true,"headroomKompress":false}`)
		h := NewHeadroomHandler(repo)
		h.DataDir = t.TempDir()
		t.Cleanup(func() { _, _ = headroom.StopHeadroomProxy(h.DataDir) })

		rec := httptest.NewRecorder()
		h.HandleHeadroomStart(rec, httptest.NewRequest(http.MethodPost, "/headroom/start", nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"alreadyRunning":false`) {
			t.Fatalf("start %d %s", rec.Code, rec.Body.String())
		}
		logRaw, err := os.ReadFile(filepath.Join(h.DataDir, "headroom", "proxy.log"))
		if err != nil {
			t.Fatal(err)
		}
		logText := string(logRaw)
		if !strings.Contains(logText, "proxy --port 8787") || !strings.Contains(logText, "--code-aware") || !strings.Contains(logText, "--disable-kompress") {
			t.Fatalf("proxy log = %q", logText)
		}
		rec = httptest.NewRecorder()
		h.HandleHeadroomStart(rec, httptest.NewRequest(http.MethodPost, "/headroom/start", nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"alreadyRunning":true`) {
			t.Fatalf("second start %d %s", rec.Code, rec.Body.String())
		}
		rec = httptest.NewRecorder()
		h.HandleHeadroomStatus(rec, httptest.NewRequest(http.MethodGet, "/headroom/status", nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"managedPid":`) || strings.Contains(rec.Body.String(), `"managedPid":null`) {
			t.Fatalf("status %d %s", rec.Code, rec.Body.String())
		}
		rec = httptest.NewRecorder()
		h.HandleHeadroomStop(rec, httptest.NewRequest(http.MethodPost, "/headroom/stop", nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"stopped":true`) {
			t.Fatalf("stop %d %s", rec.Code, rec.Body.String())
		}
		rec = httptest.NewRecorder()
		h.HandleHeadroomStop(rec, httptest.NewRequest(http.MethodPost, "/headroom/stop", nil))
		if rec.Code != http.StatusConflict {
			t.Fatalf("second stop %d %s", rec.Code, rec.Body.String())
		}
		rec = httptest.NewRecorder()
		h.HandleHeadroomRestart(rec, httptest.NewRequest(http.MethodPost, "/headroom/restart", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("restart %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("extras", func(t *testing.T) {
		dir := installFakeHeadroom(t, fakeHeadroomSleep)
		h := NewHeadroomHandler(repo)
		h.DataDir = t.TempDir()
		rec := httptest.NewRecorder()
		h.HandleHeadroomExtras(rec, httptest.NewRequest(http.MethodGet, "/headroom/extras", nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"version":"1.2.3"`) || !strings.Contains(rec.Body.String(), `"code":true`) {
			t.Fatalf("get %d %s", rec.Code, rec.Body.String())
		}
		rec = httptest.NewRecorder()
		h.HandleHeadroomExtras(rec, httptest.NewRequest(http.MethodPost, "/headroom/extras", strings.NewReader(`{"extras":["code","nope"]}`)))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "headroom-ai[proxy,code]") {
			t.Fatalf("install %d %s", rec.Code, rec.Body.String())
		}
		rec = httptest.NewRecorder()
		h.HandleHeadroomExtras(rec, httptest.NewRequest(http.MethodGet, "/headroom/extras?log=1", nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"log"`) {
			t.Fatalf("log %d %s", rec.Code, rec.Body.String())
		}
		if err := os.WriteFile(filepath.Join(dir, "fail-install"), []byte("1"), 0o644); err != nil {
			t.Fatal(err)
		}
		rec = httptest.NewRecorder()
		h.HandleHeadroomExtras(rec, httptest.NewRequest(http.MethodPost, "/headroom/extras", strings.NewReader(`{"extras":["ml"]}`)))
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "code=3") {
			t.Fatalf("install fail %d %s", rec.Code, rec.Body.String())
		}
		rec = httptest.NewRecorder()
		h.HandleHeadroomExtras(rec, httptest.NewRequest(http.MethodDelete, "/headroom/extras", strings.NewReader(`{"extras":["nope"]}`)))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "INVALID_EXTRAS") {
			t.Fatalf("invalid %d %s", rec.Code, rec.Body.String())
		}
		rec = httptest.NewRecorder()
		h.HandleHeadroomExtras(rec, httptest.NewRequest(http.MethodDelete, "/headroom/extras", strings.NewReader(`{"extras":["ml"]}`)))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "torch") {
			t.Fatalf("uninstall %d %s", rec.Code, rec.Body.String())
		}
		rec = httptest.NewRecorder()
		h.HandleHeadroomExtras(rec, httptest.NewRequest(http.MethodPut, "/headroom/extras", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("put %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("status against loopback health", func(t *testing.T) {
		installFakeHeadroom(t, fakeHeadroomExit7)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/health" {
				t.Errorf("path %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()
		putSettings(t, database, `{"headroomUrl":"`+srv.URL+`"}`)
		h := NewHeadroomHandler(repo)
		h.DataDir = t.TempDir()
		rec := httptest.NewRecorder()
		h.HandleHeadroomStatus(rec, httptest.NewRequest(http.MethodGet, "/headroom/status", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d %s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["running"] != true || body["installed"] != true || body["localUrl"] != true || body["canStart"] != true || body["managedPid"] != nil {
			t.Fatalf("status body = %#v", body)
		}
		if body["version"] != "1.2.3" {
			t.Fatalf("version = %v", body["version"])
		}
	})
}

func TestHeadroomProxyLocalFake(t *testing.T) {
	database, cleanup := setupEmbeddingsTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Connection") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("forwarded hop or creds: conn=%q cookie=%q auth=%q", r.Header.Get("Connection"), r.Header.Get("Cookie"), r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Kept") != "yes" {
			t.Errorf("dropped X-Kept")
		}
		if r.Method == http.MethodPost {
			raw, _ := ioReadAll(r.Body)
			if string(raw) != "ping" {
				t.Errorf("body %q", raw)
			}
		}
		w.Header().Set("X-Resp", "1")
		w.Header().Set("Connection", "close")
		if strings.Contains(r.URL.Path, "dashboard") {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`fetch('/stats')`))
			return
		}
		_, _ = w.Write([]byte("plain"))
	}))
	defer srv.Close()

	h := NewHeadroomHandler(repo)
	h.DataDir = t.TempDir()
	client, _ := newLoopbackClient(srv, srv.URL)
	h.Client = client

	putSettings(t, database, `{"headroomUrl":"http://example.com"}`)
	req := httptest.NewRequest(http.MethodPost, "/headroom/proxy/dashboard?x=1", strings.NewReader("ping"))
	req.Header.Set("Connection", "close")
	req.Header.Set("Cookie", "session=fake")
	req.Header.Set("Authorization", "Bearer fake")
	req.Header.Set("X-Kept", "yes")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, func() *chi.Context {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("*", "dashboard")
		return rctx
	}()))
	rec := httptest.NewRecorder()
	h.HandleHeadroomProxy(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "fetch('/api/headroom/proxy/stats')") {
		t.Fatalf("dashboard %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Resp") != "1" {
		t.Fatalf("missing response header")
	}
	if rec.Header().Get("Connection") != "" {
		t.Fatalf("hop-by-hop response header leaked: %q", rec.Header().Get("Connection"))
	}

	putSettings(t, database, `{"headroomUrl":"`+srv.URL+`"}`)
	h.Client = srv.Client()
	req = httptest.NewRequest(http.MethodGet, "/headroom/proxy/health", nil)
	req.Header.Set("X-Kept", "yes")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, func() *chi.Context {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("*", "health")
		return rctx
	}()))
	rec = httptest.NewRecorder()
	h.HandleHeadroomProxy(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "plain" {
		t.Fatalf("plain %d %s", rec.Code, rec.Body.String())
	}

	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("hop") == "" {
			http.Redirect(w, r, "/health?hop=1", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("stopped"))
	}))
	defer redir.Close()
	putSettings(t, database, `{"headroomUrl":"`+redir.URL+`"}`)
	own := NewHeadroomHandler(repo)
	own.DataDir = t.TempDir()
	req = httptest.NewRequest(http.MethodGet, "/headroom/proxy/health", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, func() *chi.Context {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("*", "health")
		return rctx
	}()))
	rec = httptest.NewRecorder()
	own.HandleHeadroomProxy(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("redirect %d %s", rec.Code, rec.Body.String())
	}

	putSettings(t, database, `{"headroomUrl":"ftp://127.0.0.1/dash"}`)
	rec = httptest.NewRecorder()
	h.HandleHeadroomProxy(rec, httptest.NewRequest(http.MethodGet, "/headroom/proxy/", nil))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "http or https") {
		t.Fatalf("scheme %d %s", rec.Code, rec.Body.String())
	}

	putSettings(t, database, `{"headroomUrl":"http://example.com"}`)
	h.Client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, ioErrClosed()
	})}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/headroom/proxy/x", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, func() *chi.Context {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("*", "x")
		return rctx
	}()))
	h.HandleHeadroomProxy(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("dial %d %s", rec.Code, rec.Body.String())
	}
}

func ioErrClosed() error { return errors.New("closed") }
