package deepseekweb_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"9router/proxy/internal/auth"
	"9router/proxy/internal/deepseekweb"
	"9router/proxy/internal/log"
	"9router/proxy/internal/middleware"
)

const (
	fakeCred    = "fake-dsh-session-HTTP-NOT-REAL-91b2"
	fakeRotated = "fake-dsh-session-HTTP-ROTATED-NOT-REAL"
	fakeAPIKey  = "sk-fake-inference-key-NOT-REAL"
)

type okChecker struct{}

func (okChecker) Check(_ context.Context, _ string, cred []byte) deepseekweb.CheckResult {
	if string(cred) == fakeCred || string(cred) == fakeRotated {
		return deepseekweb.CheckResult{Status: deepseekweb.StatusConnected}
	}
	return deepseekweb.CheckResult{Status: deepseekweb.StatusError, Detail: "rechazada"}
}

func newServer(t *testing.T) (http.Handler, *deepseekweb.Manager) {
	t.Helper()
	t.Setenv("JWT_SECRET", "deepseekweb-test-secret-not-real")
	t.Setenv("DATA_DIR", t.TempDir())
	m, err := deepseekweb.Open(deepseekweb.Options{Dir: t.TempDir(), Checker: okChecker{}})
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAdminAuth())
		deepseekweb.Routes(r, func() (*deepseekweb.Manager, error) { return m, nil })
	})
	return r, m
}

type reqOpt func(*http.Request)

func cli() reqOpt {
	return func(r *http.Request) { r.Header.Set(auth.CLITokenHeader, auth.CLIToken()) }
}

func do(t *testing.T, h http.Handler, method, path string, body any, opts ...reqOpt) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	for _, o := range opts {
		o(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAdminAPIRejectsInferenceKeysAndAnonymous(t *testing.T) {
	h, m := newServer(t)
	s, _ := m.Create(deepseekweb.CreateInput{Label: "A", Credential: []byte(fakeCred)})
	base := "/api/admin/deepseek-web/"
	routes := []struct{ method, path string }{
		{"GET", base + "sessions"}, {"POST", base + "sessions"}, {"GET", base + "sessions/" + s.ID},
		{"PATCH", base + "sessions/" + s.ID}, {"DELETE", base + "sessions/" + s.ID},
		{"POST", base + "sessions/" + s.ID + "/check"}, {"GET", base + "fallback"}, {"PUT", base + "fallback"},
	}
	creds := map[string]reqOpt{
		"anonymous":     func(*http.Request) {},
		"bearer key":    func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+fakeAPIKey) },
		"x-api-key":     func(r *http.Request) { r.Header.Set("x-api-key", fakeAPIKey) },
		"bad cli token": func(r *http.Request) { r.Header.Set(auth.CLITokenHeader, "0000000000000000") },
		"forged cookie": func(r *http.Request) { r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "x.y.z"}) },
	}
	for name, opt := range creds {
		for _, rt := range routes {
			rec := do(t, h, rt.method, rt.path, map[string]any{"label": "x", "credential": fakeRotated, "enabled": true, "rev": 1}, opt)
			if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s %s: %d", name, rt.method, rt.path, rec.Code)
			}
		}
	}
	got, _ := m.Get(s.ID)
	if got.Rev != s.Rev || got.Label != "A" || m.Fallback().Requested {
		t.Fatal("an unauthenticated request changed state")
	}
}

func TestAdminAPIFlowNeverLeaksTheCredential(t *testing.T) {
	h, _ := newServer(t)
	log.ClearConsoleLogs()
	var bodies []string
	rec := do(t, h, "POST", "/api/admin/deepseek-web/sessions", map[string]any{"label": "cuenta 1", "credential": fakeCred}, cli())
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %d %s", rec.Code, rec.Body)
	}
	bodies = append(bodies, rec.Body.String())
	var s deepseekweb.SessionInfo
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	if s.Status != deepseekweb.StatusUnknown || s.Credential != deepseekweb.CredentialMask {
		t.Fatalf("new session %+v", s)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store")
	}
	rec = do(t, h, "PUT", "/api/admin/deepseek-web/fallback", map[string]any{"enabled": true}, cli())
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"effective":false`) {
		t.Fatalf("toggle without connected session: %d %s", rec.Code, rec.Body)
	}
	rec = do(t, h, "POST", "/api/admin/deepseek-web/sessions/"+s.ID+"/check", nil, cli())
	bodies = append(bodies, rec.Body.String())
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	if rec.Code != 200 || s.Status != deepseekweb.StatusConnected || s.LastCheck == nil {
		t.Fatalf("check %d %s", rec.Code, rec.Body)
	}
	rec = do(t, h, "GET", "/api/admin/deepseek-web/fallback", nil, cli())
	if !strings.Contains(rec.Body.String(), `"effective":true`) {
		t.Fatalf("fallback after connect: %s", rec.Body)
	}
	if rec = do(t, h, "PATCH", "/api/admin/deepseek-web/sessions/"+s.ID, map[string]any{"rev": s.Rev - 1, "credential": fakeRotated}, cli()); rec.Code != http.StatusConflict {
		t.Fatalf("stale rev: %d", rec.Code)
	}
	rec = do(t, h, "PATCH", "/api/admin/deepseek-web/sessions/"+s.ID, map[string]any{"rev": s.Rev, "credential": fakeRotated}, cli())
	bodies = append(bodies, rec.Body.String())
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	if rec.Code != 200 || s.Status != deepseekweb.StatusUnknown {
		t.Fatalf("rotate %d %s", rec.Code, rec.Body)
	}
	rec = do(t, h, "GET", "/api/admin/deepseek-web/fallback", nil, cli())
	if !strings.Contains(rec.Body.String(), `"effective":false`) {
		t.Fatalf("rotation must require a new check before fallback: %s", rec.Body)
	}
	for _, p := range []string{"/api/admin/deepseek-web/sessions", "/api/admin/deepseek-web/sessions/" + s.ID} {
		rec = do(t, h, "GET", p, nil, cli())
		bodies = append(bodies, rec.Body.String())
	}
	if rec = do(t, h, "PATCH", "/api/admin/deepseek-web/sessions/"+s.ID, "{", cli()); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body: %d", rec.Code)
	}
	if rec = do(t, h, "DELETE", "/api/admin/deepseek-web/sessions/"+s.ID, nil, cli()); rec.Code != http.StatusNoContent {
		t.Fatalf("delete %d", rec.Code)
	}
	if rec = do(t, h, "GET", "/api/admin/deepseek-web/sessions/"+s.ID, nil, cli()); rec.Code != http.StatusNotFound {
		t.Fatalf("after delete %d", rec.Code)
	}
	for _, b := range bodies {
		if strings.Contains(b, fakeCred) || strings.Contains(b, fakeRotated) || strings.Contains(b, "sealed") {
			t.Fatalf("response leaks credential material: %s", b)
		}
	}
	for _, l := range log.ConsoleLogs() {
		if strings.Contains(l, fakeCred) || strings.Contains(l, fakeRotated) {
			t.Fatalf("log leaks credential: %s", l)
		}
	}
}

func TestAdminAPIRejectsOversizedBody(t *testing.T) {
	h, m := newServer(t)
	big := strings.Repeat("a", 70<<10)
	if rec := do(t, h, "POST", "/api/admin/deepseek-web/sessions", map[string]any{"label": "A", "credential": big}, cli()); rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized: %d", rec.Code)
	}
	if len(m.List()) != 0 {
		t.Fatal("oversized body stored")
	}
}
