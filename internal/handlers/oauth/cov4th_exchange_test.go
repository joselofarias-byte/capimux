package oauth

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/auth"
)

func TestOIDCAndPKCEExchangeCov4th(t *testing.T) {
	var doc string
	var status int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(doc))
	}))
	t.Cleanup(srv.Close)

	status, doc = http.StatusOK, `{"authorization_endpoint":"https://auth.example.test/authorize","token_endpoint":"https://auth.example.test/token"}`
	got, err := discoverOIDCEndpoints(srv.URL)
	if err != nil || got.authorizeURL != "https://auth.example.test/authorize" || got.tokenURL != "https://auth.example.test/token" {
		t.Fatalf("discovery %+v err %v", got, err)
	}
	status = http.StatusBadGateway
	if _, err := discoverOIDCEndpoints(srv.URL); err == nil {
		t.Fatal("bad status was accepted")
	}
	status, doc = http.StatusOK, "not-json"
	if _, err := discoverOIDCEndpoints(srv.URL); err == nil {
		t.Fatal("bad document was accepted")
	}
	status, doc = http.StatusOK, `{"authorization_endpoint":"http://auth.example.test/authorize","token_endpoint":"https://auth.example.test/token"}`
	if _, err := discoverOIDCEndpoints(srv.URL); err == nil {
		t.Fatal("http endpoint was accepted")
	}
	if _, err := discoverOIDCEndpoints("http://127.0.0.1:1"); err == nil {
		t.Fatal("closed port was accepted")
	}

	cfg := pkceProviders["xai"]
	orig := cfg.discoverIssuer
	cfg.discoverIssuer = srv.URL
	t.Cleanup(func() { cfg.discoverIssuer = orig })
	status, doc = http.StatusOK, `{"authorization_endpoint":"https://auth.example.test/authorize","token_endpoint":"https://auth.example.test/token"}`
	authz := callOAuthCov4th(NewOAuthHandler(nil).HandlePKCEAuthorize, http.MethodGet, "/api/oauth/pkce/authorize?provider=xai", "")
	if authz.Code != http.StatusOK || !strings.Contains(authz.Body.String(), "https://auth.example.test/authorize") {
		t.Fatalf("xai authorize %d %s", authz.Code, authz.Body.String())
	}
	failed := exchangeFail(http.StatusBadRequest, "missing %s", "code")
	if failed.Error() != "missing code" {
		t.Fatal(failed.Error())
	}
	bad := callOAuthCov4th(NewOAuthHandler(nil).HandlePKCEExchange, http.MethodPost, "/api/oauth/pkce/exchange", `{"provider":"nope","code":"c","codeVerifier":"v"}`)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad provider %d %s", bad.Code, bad.Body.String())
	}
	xai := callOAuthCov4th(NewOAuthHandler(repoCov4th(t)).HandlePKCEExchange, http.MethodPost, "/api/oauth/pkce/exchange", `{"provider":"xai","code":"code-cov4th","codeVerifier":"ver-cov4th"}`)
	if xai.Code != http.StatusBadGateway {
		t.Fatalf("xai exchange %d %s", xai.Code, xai.Body.String())
	}

	installHappyOrDialCov4th(t)
	repo := repoCov4th(t)
	handler := NewOAuthHandler(repo)
	claude := callOAuthCov4th(handler.HandlePKCEExchange, http.MethodPost, "/api/oauth/pkce/exchange", `{"provider":"claude","code":"code-cov4th#state","codeVerifier":"ver-cov4th","state":"st"}`)
	if claude.Code != http.StatusOK || !strings.Contains(claude.Body.String(), `"provider":"claude"`) {
		t.Fatalf("claude %d %s", claude.Code, claude.Body.String())
	}
	codex := callOAuthCov4th(handler.HandlePKCEExchange, http.MethodPost, "/api/oauth/pkce/exchange", `{"provider":"codex","code":"code-cov4th","codeVerifier":"ver-cov4th","name":"Desk"}`)
	if codex.Code != http.StatusOK || !strings.Contains(codex.Body.String(), "codex@example.test") {
		t.Fatalf("codex %d %s", codex.Code, codex.Body.String())
	}

	glSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/v4/user") {
			_, _ = w.Write([]byte(`{"email":"gl@example.test","username":"gl"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"gl-access-cov4th","refresh_token":"gl-refresh-cov4th","expires_in":30}`))
	}))
	t.Cleanup(glSrv.Close)
	gl := callOAuthCov4th(handler.HandlePKCEExchange, http.MethodPost, "/api/oauth/pkce/exchange", `{"provider":"gitlab","code":"code-cov4th","codeVerifier":"ver-cov4th","baseUrl":"`+glSrv.URL+`","clientId":"cid-cov4th","clientSecret":"sec-cov4th"}`)
	if gl.Code != http.StatusOK || !strings.Contains(gl.Body.String(), "gl@example.test") {
		t.Fatalf("gitlab %d %s", gl.Code, gl.Body.String())
	}
}

func TestProviderExchangesCov4th(t *testing.T) {
	var agCalls int
	installUpstreamCov4th(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "oauth2.googleapis.com" {
			agCalls++
			if agCalls > 1 {
				return jsonBodyCov4th(http.StatusOK, `{"access_token":"ag-access-cov4th","expires_in":10,"token_type":"Bearer","id_token":"`+jwtEmailCov4th("ag@example.test")+`"}`), nil
			}
		}
		if r.URL.Host == "api.cline.bot" && r.URL.Query().Get("fail") == "1" {
			return jsonBodyCov4th(http.StatusBadGateway, `{"success":false,"error":"nope"}`), nil
		}
		return routeHappyCov4th(r), nil
	})
	repo := repoCov4th(t)
	handler := NewOAuthHandler(repo)

	if rec := callOAuthCov4th(handler.HandleAntigravityExchange, http.MethodGet, "/api/oauth/antigravity/exchange", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method %d", rec.Code)
	}
	if rec := callOAuthCov4th(handler.HandleAntigravityExchange, http.MethodPost, "/api/oauth/antigravity/exchange", `{`); rec.Code != http.StatusBadRequest {
		t.Fatalf("json %d", rec.Code)
	}
	if rec := callOAuthCov4th(handler.HandleAntigravityExchange, http.MethodPost, "/api/oauth/antigravity/exchange", `{"code":"","redirectUri":"http://localhost/cb"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("code %d", rec.Code)
	}
	if rec := callOAuthCov4th(handler.HandleAntigravityExchange, http.MethodPost, "/api/oauth/antigravity/exchange", `{"code":"abc","redirectUri":"javascript:alert(1)"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("redirect %d", rec.Code)
	}
	ag := callOAuthCov4th(handler.HandleAntigravityExchange, http.MethodPost, "/api/oauth/antigravity/exchange", `{"code":"https://cb.example.test/?code=4%252Fcov4th","redirectUri":"http://localhost:8080/callback"}`)
	if ag.Code != http.StatusOK || !strings.Contains(ag.Body.String(), "ag@example.test") {
		t.Fatalf("ag %d %s", ag.Code, ag.Body.String())
	}
	ag2 := callOAuthCov4th(handler.HandleAntigravityExchange, http.MethodPost, "/api/oauth/antigravity/exchange", `{"code":"code=plain-cov4th&x=1","redirectUri":"http://localhost:8080/callback"}`)
	if ag2.Code != http.StatusOK {
		t.Fatalf("ag update %d %s", ag2.Code, ag2.Body.String())
	}
	var stored string
	if err := repo.RawDB().QueryRow("SELECT data FROM providerConnections WHERE provider = 'antigravity'").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored, "ag-refresh-cov4th") {
		t.Fatal("refresh token was dropped on update")
	}

	cline := callOAuthCov4th(handler.HandleClineExchange, http.MethodPost, "/api/oauth/cline/exchange", `{"provider":"clinepass","code":"code-cov4th","codeVerifier":"ver-cov4th","redirectUri":"http://localhost:8080/callback","name":"Pass"}`)
	if cline.Code != http.StatusOK || !strings.Contains(cline.Body.String(), `"provider":"clinepass"`) {
		t.Fatalf("cline %d %s", cline.Code, cline.Body.String())
	}
	if rec := callOAuthCov4th(handler.HandleClineExchange, http.MethodPost, "/api/oauth/cline/exchange", `{"provider":"nope","code":"c","codeVerifier":"v"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("cline provider %d", rec.Code)
	}
	if rec := callOAuthCov4th(handler.HandleClineExchange, http.MethodPost, "/api/oauth/cline/exchange", `{"provider":"cline","code":"code-cov4th"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("cline verifier %d %s", rec.Code, rec.Body.String())
	}

	gem := callOAuthCov4th(handler.HandleAuthCodeExchange, http.MethodPost, "/api/oauth/authcode/exchange", `{"provider":"gemini-cli","code":"code-cov4th","redirectUri":"http://localhost:8080/callback"}`)
	if gem.Code != http.StatusOK || !strings.Contains(gem.Body.String(), "gemini@example.test") {
		t.Fatalf("gemini %d %s", gem.Code, gem.Body.String())
	}
	iflow := callOAuthCov4th(handler.HandleAuthCodeExchange, http.MethodPost, "/api/oauth/authcode/exchange", `{"provider":"iflow","code":"code-cov4th","redirect_uri":"http://localhost:8080/callback"}`)
	if iflow.Code != http.StatusOK || !strings.Contains(iflow.Body.String(), "iflow@example.test") {
		t.Fatalf("iflow %d %s", iflow.Code, iflow.Body.String())
	}

	social := callOAuthCov4th(handler.HandleOAuthKiroSocialExchange, http.MethodPost, "/api/oauth/kiro/social-exchange", `{"code":"code-cov4th","codeVerifier":"ver-cov4th","provider":"google"}`)
	if social.Code != http.StatusOK || !strings.Contains(social.Body.String(), `"provider":"kiro"`) {
		t.Fatalf("social %d %s", social.Code, social.Body.String())
	}
	if rec := callOAuthCov4th(handler.HandleOAuthKiroSocialExchange, http.MethodPost, "/api/oauth/kiro/social-exchange", `{"code":"","codeVerifier":""}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("social missing %d", rec.Code)
	}
	if rec := callOAuthCov4th(handler.HandleOAuthKiroSocialExchange, http.MethodPost, "/api/oauth/kiro/social-exchange", `{"code":"c","codeVerifier":"v","provider":"twitter"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("social provider %d", rec.Code)
	}
	if truncateForError([]byte("short")) != "short" || len(truncateForError(bytes.Repeat([]byte("a"), 250))) != 200 {
		t.Fatal("truncateForError")
	}
}

func TestCodexBulkPriorityCov4th(t *testing.T) {
	repo := repoCov4th(t)
	handler := NewOAuthHandler(repo)
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/codex/bulk-import", strings.NewReader(`{"tokens":[{"accessToken":""},{"accessToken":"bulk-cov4th","name":"Named"}]}`))
	req = req.WithContext(auth.WithClientKeyWrite(req.Context()))
	rec := httptest.NewRecorder()
	handler.HandleOAuthCodexBulkImport(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"count":1`) {
		t.Fatalf("bulk %d %s", rec.Code, rec.Body.String())
	}
	var priority sql.NullInt64
	if err := repo.RawDB().QueryRow("SELECT priority FROM providerConnections WHERE provider = 'codex'").Scan(&priority); err != nil {
		t.Fatal(err)
	}
	if !priority.Valid || priority.Int64 < 1 {
		t.Fatalf("priority = %+v", priority)
	}
}

func TestCodexCLIOfflineCov4th(t *testing.T) {
	t.Cleanup(drainCodexSessionsCov4th)
	handler := NewOAuthHandler(repoCov4th(t))

	t.Run("not found", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("PATH", home)
		t.Setenv("CODEX_LOGIN_BIN", home)
		rec := callOAuthCov4th(handler.HandleCodexCLIStart, http.MethodPost, "/api/oauth/codex/cli-login/start", `{"name":"Missing"}`)
		if !strings.Contains(rec.Body.String(), `"reason":"codex_not_found"`) {
			t.Fatalf("body %s", rec.Body.String())
		}
	})

	t.Run("start failed", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "codex")
		if err := os.WriteFile(path, []byte{0x7f, 'E', 'L', 'F', 0}, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("CODEX_LOGIN_BIN", path)
		rec := callOAuthCov4th(handler.HandleCodexCLIStart, http.MethodPost, "/api/oauth/codex/cli-login/start", `{}`)
		if !strings.Contains(rec.Body.String(), `"reason":"start_failed"`) {
			t.Fatalf("body %s", rec.Body.String())
		}
	})

	t.Run("login exited", func(t *testing.T) {
		writeCodexBinCov4th(t, "#!/bin/sh\nexit 0\n")
		rec := callOAuthCov4th(handler.HandleCodexCLIStart, http.MethodPost, "/api/oauth/codex/cli-login/start", `{}`)
		if !strings.Contains(rec.Body.String(), `"reason":"login_exited"`) {
			t.Fatalf("body %s", rec.Body.String())
		}
	})

	t.Run("success and status", func(t *testing.T) {
		payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":2000000000,"email":"cli@example.test"}`))
		access := "eyJhbGciOiJub25lIn0." + payload + ".sig"
		script := "#!/bin/sh\necho waiting\nprintf '\\033[31mhttps://auth.openai.com/oauth/authorize?cov4th=1\\033[0m\\n'\ncat > \"$CODEX_HOME/auth.json\" <<EOF\n{\"tokens\":{\"access_token\":\"" + access + "\",\"refresh_token\":\"rt-cli-cov4th\",\"id_token\":\"" + access + "\"}}\nEOF\nsleep 0.3\nexit 0\n"
		writeCodexBinCov4th(t, script)
		rec := callOAuthCov4th(handler.HandleCodexCLIStart, http.MethodPost, "/api/oauth/codex/cli-login/start", `{"name":"CLI"}`)
		var body struct {
			Success   bool   `json:"success"`
			SessionID string `json:"sessionId"`
			AuthURL   string `json:"authUrl"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || !body.Success || !strings.HasPrefix(body.AuthURL, "https://auth.openai.com/") {
			t.Fatalf("start %s", rec.Body.String())
		}
		status := waitCodexStatusCov4th(t, handler, body.SessionID)
		if !strings.Contains(status, `"status":"done"`) || !strings.Contains(status, "cli@example.test") {
			t.Fatalf("status %s", status)
		}
		unknown := callOAuthCov4th(handler.HandleCodexCLIStatus, http.MethodGet, "/api/oauth/codex/cli-login/status?id="+body.SessionID, "")
		if !strings.Contains(unknown.Body.String(), `"status":"unknown"`) {
			t.Fatalf("second status %s", unknown.Body.String())
		}
	})

	t.Run("import failed", func(t *testing.T) {
		// Sleep after the URL so the scanner wins the select. A CLI that exits
		// in the same instant is a separate race, covered by the skipped test.
		writeCodexBinCov4th(t, "#!/bin/sh\necho https://auth.openai.com/oauth/authorize?missing=1\nsleep 0.3\nexit 0\n")
		rec := callOAuthCov4th(handler.HandleCodexCLIStart, http.MethodPost, "/api/oauth/codex/cli-login/start", `{"name":"Nope"}`)
		var body struct {
			SessionID string `json:"sessionId"`
			Success   bool   `json:"success"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || !body.Success {
			t.Fatalf("start %s", rec.Body.String())
		}
		status := waitCodexStatusCov4th(t, handler, body.SessionID)
		if !strings.Contains(status, `"status":"error"`) {
			t.Fatalf("status %s", status)
		}
	})

	t.Run("reuse and cancel", func(t *testing.T) {
		writeCodexBinCov4th(t, "#!/bin/sh\necho https://auth.openai.com/oauth/authorize?reused=1\nsleep 60\n")
		first := callOAuthCov4th(handler.HandleCodexCLIStart, http.MethodPost, "/api/oauth/codex/cli-login/start", `{"name":"Reuse"}`)
		var body struct {
			SessionID string `json:"sessionId"`
			Success   bool   `json:"success"`
		}
		if err := json.Unmarshal(first.Body.Bytes(), &body); err != nil || !body.Success {
			t.Fatalf("first %s", first.Body.String())
		}
		t.Cleanup(func() { codexCLILogins.cancel(body.SessionID) })
		second := callOAuthCov4th(handler.HandleCodexCLIStart, http.MethodPost, "/api/oauth/codex/cli-login/start", `{}`)
		if !strings.Contains(second.Body.String(), `"reused":true`) {
			t.Fatalf("second %s", second.Body.String())
		}
		cancelled := callOAuthCov4th(handler.HandleCodexCLICancel, http.MethodPost, "/api/oauth/codex/cli-login/cancel?id="+body.SessionID, "")
		if !strings.Contains(cancelled.Body.String(), `"success":true`) {
			t.Fatalf("cancel %s", cancelled.Body.String())
		}
		empty := callOAuthCov4th(handler.HandleCodexCLICancel, http.MethodPost, "/api/oauth/codex/cli-login/cancel", "")
		if !strings.Contains(empty.Body.String(), `"success":false`) {
			t.Fatalf("empty cancel %s", empty.Body.String())
		}
		missing := callOAuthCov4th(handler.HandleCodexCLIStatus, http.MethodGet, "/api/oauth/codex/cli-login/status", "")
		if missing.Code != http.StatusBadRequest {
			t.Fatalf("missing id %d", missing.Code)
		}
	})
}

func TestCodexCLIFastExitDropsURLCov4th(t *testing.T) {
	t.Skip("decisión de José Luis: no se corrige en este PR. internal/handlers/oauth/codex_cli.go:116 el select entre urlCh y exitCh no es determinista. Reproducción offline: CODEX_LOGIN_BIN hace echo de https://auth.openai.com/oauth/authorize?x=1 y sale al instante; POST /api/oauth/codex/cli-login/start. Esperado: success true con sessionId, y finishCodexCLILogin importa auth.json o informa que no pudo. Observado con go test -race -count=5: success false y reason login_exited, porque exitCh gana el select, cancel() cancela el contexto y finishCodexCLILogin (codex_cli.go:148) trata ctx.Err()!=nil como timeout sin leer las credenciales.")
}

func TestCodexCLIURLTimeoutCov4th(t *testing.T) {
	t.Cleanup(drainCodexSessionsCov4th)
	writeCodexBinCov4th(t, "#!/bin/sh\nsleep 60\n")
	start := time.Now()
	rec := callOAuthCov4th(NewOAuthHandler(nil).HandleCodexCLIStart, http.MethodPost, "/api/oauth/codex/cli-login/start", `{}`)
	if !strings.Contains(rec.Body.String(), `"reason":"url_timeout"`) {
		t.Fatalf("body %s", rec.Body.String())
	}
	if time.Since(start) < 15*time.Second {
		t.Fatal("url timeout returned early")
	}
}

func TestReadCodexCLIAuthErrorsCov4th(t *testing.T) {
	dir := t.TempDir()
	if _, err := readCodexCLIAuth(dir); err == nil {
		t.Fatal("missing file")
	}
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), codexCLIAuthMaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCodexCLIAuth(dir); err == nil {
		t.Fatal("large file")
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCodexCLIAuth(dir); err == nil {
		t.Fatal("bad json")
	}
	if err := os.WriteFile(path, []byte(`{"tokens":{"access_token":"  "}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCodexCLIAuth(dir); err == nil {
		t.Fatal("blank token")
	}
}

func writeCodexBinCov4th(t *testing.T, script string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_LOGIN_BIN", path)
}

func drainCodexSessionsCov4th() {
	codexCLILogins.mu.Lock()
	ids := make([]string, 0, len(codexCLILogins.sessions))
	for id := range codexCLILogins.sessions {
		ids = append(ids, id)
	}
	codexCLILogins.mu.Unlock()
	for _, id := range ids {
		codexCLILogins.cancel(id)
	}
}

func waitCodexStatusCov4th(t *testing.T, handler *OAuthHandler, id string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		rec := callOAuthCov4th(handler.HandleCodexCLIStatus, http.MethodGet, "/api/oauth/codex/cli-login/status?id="+id, "")
		last = rec.Body.String()
		if strings.Contains(last, `"status":"error"`) || strings.Contains(last, `"status":"done"`) {
			return last
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("status stayed pending: %s", last)
	return last
}
