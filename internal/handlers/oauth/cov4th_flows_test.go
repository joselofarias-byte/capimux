package oauth

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"9router/proxy/internal/models"
)

func encryptMimoCov4th(t *testing.T, verifier, plain string) string {
	t.Helper()
	privDer, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(verifier, "mimo-x25519:"))
	if err != nil {
		t.Fatal(err)
	}
	privAny, err := x509.ParsePKCS8PrivateKey(privDer)
	if err != nil {
		t.Fatal(err)
	}
	priv, ok := privAny.(*ecdh.PrivateKey)
	if !ok {
		t.Fatal("verifier was not an X25519 key")
	}
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := eph.ECDH(priv.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	key := sha256.Sum256(secret)
	nonce := []byte("0123456789ab")
	ct, tag := aesGCMSealForTest(t, key[:], nonce, []byte(plain))
	raw := append(append(append([]byte{}, nonce...), eph.PublicKey().Bytes()...), append(ct, tag...)...)
	return base64.StdEncoding.EncodeToString(raw)
}

func TestFreebuffConnectionHelpersCov4th(t *testing.T) {
	if freebuffConnectionToken(nil) != "" {
		t.Fatal("nil connection")
	}
	if freebuffConnectionToken(&models.ProviderConnection{ID: "bad", Data: "{"}) != "" {
		t.Fatal("invalid data")
	}
	if freebuffConnectionToken(&models.ProviderConnection{Data: `{"accessToken":"at-cov4th"}`}) != "at-cov4th" {
		t.Fatal("access token")
	}
	if freebuffConnectionToken(&models.ProviderConnection{Data: `{"apiKey":"key-cov4th"}`}) != "key-cov4th" {
		t.Fatal("api key")
	}
	if conn, err := NewOAuthHandler(nil).freebuffConnection("fb-cov4th"); conn != nil || err != nil {
		t.Fatalf("nil repo conn=%v err=%v", conn, err)
	}

	repo := repoCov4th(t)
	now := currentTimestamp()
	data := `{"authToken":"auth-cov4th","providerSpecificData":{"keep":true}}`
	if _, err := repo.RawDB().Exec(`INSERT INTO providerConnections (id, provider, authType, name, isActive, data, createdAt, updatedAt) VALUES ('fb-cov4th', 'freebuff', 'oauth', 'FB', 1, ?, ?, ?)`, data, now, now); err != nil {
		t.Fatal(err)
	}
	handler := NewOAuthHandler(repo)
	got, err := handler.freebuffConnection("")
	if err != nil || got == nil || got.ID != "fb-cov4th" || freebuffConnectionToken(got) != "auth-cov4th" {
		t.Fatalf("list conn=%v err=%v", got, err)
	}
	byID, err := handler.freebuffConnection("fb-cov4th")
	if err != nil || byID == nil {
		t.Fatalf("by id conn=%v err=%v", byID, err)
	}
	missing, err := handler.freebuffConnection("missing-cov4th")
	if err != nil || missing != nil {
		t.Fatalf("missing conn=%v err=%v", missing, err)
	}
	updateConnectionFreebuffModel(nil, byID, "model-cov4th")
	updateConnectionFreebuffModel(repo, &models.ProviderConnection{ID: "fb-cov4th", Data: "{"}, "model-cov4th")
	updateConnectionFreebuffModel(repo, byID, "model-cov4th")
	refreshed, err := handler.freebuffConnection("fb-cov4th")
	if err != nil || refreshed == nil || !strings.Contains(refreshed.Data, "model-cov4th") {
		t.Fatalf("updated conn=%v err=%v", refreshed, err)
	}
	updateConnectionFreebuffModel(repo, refreshed, "model-cov4th")
	updateConnectionFreebuffModel(repo, refreshed, "")
	cleared, err := handler.freebuffConnection("fb-cov4th")
	if err != nil || cleared == nil || strings.Contains(cleared.Data, "model-cov4th") {
		t.Fatalf("cleared conn=%v err=%v", cleared, err)
	}
}

func TestMimoExchangeCov4th(t *testing.T) {
	if len(x25519SPKIPrefix()) != 12 {
		t.Fatal("unexpected SPKI prefix")
	}
	repo := repoCov4th(t)
	handler := NewOAuthHandler(repo)
	rec := callOAuthCov4th(handler.HandleMimoAuthorize, http.MethodGet, "/api/oauth/xiaomi-mimo/authorize?key_name=desk", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("authorize %d %s", rec.Code, rec.Body.String())
	}
	verifier := betweenCov4th(t, rec.Body.String(), `"codeVerifier":"`, `"`)
	enc := encryptMimoCov4th(t, verifier, `{"uid":"u-cov4th","sk":"sk-cov4th","url":"https://api.example.test/v1"}`)

	body := `{"code":"https://platform.example.test/cb?u=` + urlQueryEscape(enc) + `","codeVerifier":"` + verifier + `"}`
	got := callOAuthCov4th(handler.HandleMimoExchange, http.MethodPost, "/api/oauth/xiaomi-mimo/exchange", body)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"provider":"xiaomi-mimo"`) {
		t.Fatalf("exchange %d %s", got.Code, got.Body.String())
	}
	again := callOAuthCov4th(handler.HandleMimoExchange, http.MethodPost, "/api/oauth/xiaomi-mimo/exchange", `{"code":"https://platform.example.test/cb?u=`+urlQueryEscape(enc)+`","codeVerifier":"`+verifier+`","name":"Desk"}`)
	if again.Code != http.StatusOK || countConnsCov4th(t, repo) != 1 {
		t.Fatalf("update %d rows %d", again.Code, countConnsCov4th(t, repo))
	}

	rawEnc := base64.RawURLEncoding.EncodeToString(mustStdDecodeCov4th(t, enc))
	if _, err := mimoDecryptCallback(verifier, `{"u":"`+rawEnc+`"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := mimoDecryptCallback("nope", enc); err == nil {
		t.Fatal("bad verifier was accepted")
	}
	shortBlob := base64.StdEncoding.EncodeToString([]byte("short"))
	if _, err := mimoDecryptCallback(verifier, "u="+urlQueryEscape(shortBlob)); err == nil {
		t.Fatal("short payload was accepted")
	}
	missingBlob := encryptMimoCov4th(t, verifier, `{"uid":"only"}`)
	missing := callOAuthCov4th(handler.HandleMimoExchange, http.MethodPost, "/api/oauth/xiaomi-mimo/exchange", `{"code":"https://platform.example.test/cb?u=`+urlQueryEscape(missingBlob)+`","codeVerifier":"`+verifier+`"}`)
	if missing.Code != http.StatusBadGateway {
		t.Fatalf("missing sk %d %s", missing.Code, missing.Body.String())
	}
}

func mustStdDecodeCov4th(t *testing.T, s string) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func betweenCov4th(t *testing.T, s, start, end string) string {
	t.Helper()
	i := strings.Index(s, start)
	if i < 0 {
		t.Fatalf("missing %q in %s", start, s)
	}
	rest := s[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		t.Fatalf("missing end %q", end)
	}
	return rest[:j]
}

func TestWindsurfExchangeCov4th(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "missing-key"):
			http.Error(w, "no", http.StatusBadGateway)
		case strings.Contains(r.URL.Path, "RegisterUser"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"apiKey":"ws-key-cov4th","api_server_url":"` + srv.URL + `"}`))
		case strings.Contains(r.URL.Path, "GetOneTimeAuthToken"):
			_, _ = w.Write([]byte(`{"authToken":"once-cov4th"}`))
		default:
			_, _ = w.Write([]byte(`{"user":{"email":"wind@example.test","name":"Wind"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	orig := windsurfRegisterBase
	windsurfRegisterBase = srv.URL
	t.Cleanup(func() { windsurfRegisterBase = orig })

	repo := repoCov4th(t)
	handler := NewOAuthHandler(repo)
	bad := callOAuthCov4th(handler.HandleWindsurfExchange, http.MethodPost, "/api/oauth/windsurf/exchange", `{}`)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("missing code %d", bad.Code)
	}
	denied := callOAuthCov4th(handler.HandleWindsurfExchange, http.MethodPost, "/api/oauth/windsurf/exchange", `{"code":"https://cb.example.test/?error=access_denied&error_description=no"}`)
	if denied.Code != http.StatusBadRequest {
		t.Fatalf("denied %d %s", denied.Code, denied.Body.String())
	}
	mismatch := callOAuthCov4th(handler.HandleWindsurfExchange, http.MethodPost, "/api/oauth/windsurf/exchange", `{"code":"https://cb.example.test/?access_token=jwt-cov4th&state=other","state":"mine"}`)
	if mismatch.Code != http.StatusBadRequest {
		t.Fatalf("mismatch %d %s", mismatch.Code, mismatch.Body.String())
	}
	ok := callOAuthCov4th(handler.HandleWindsurfExchange, http.MethodPost, "/api/oauth/windsurf/exchange", `{"code":"#access_token=firebase-cov4th&state=mine","state":"mine","name":"Blade"}`)
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), "wind@example.test") {
		t.Fatalf("exchange %d %s", ok.Code, ok.Body.String())
	}
	updated := callOAuthCov4th(handler.HandleWindsurfExchange, http.MethodPost, "/api/oauth/windsurf/exchange", `{"code":"sk-ws-cov4th-imported"}`)
	if updated.Code != http.StatusOK {
		t.Fatalf("import %d %s", updated.Code, updated.Body.String())
	}
	if countConnsCov4th(t, repo) != 2 {
		t.Fatalf("rows = %d", countConnsCov4th(t, repo))
	}

	windsurfRegisterBase = srv.URL + "/missing-key"
	// A 404 from the register host is a gateway error.
	fail := callOAuthCov4th(handler.HandleWindsurfExchange, http.MethodPost, "/api/oauth/windsurf/exchange", `{"code":"firebase-plain-cov4th"}`)
	if fail.Code != http.StatusBadGateway {
		t.Fatalf("register fail %d %s", fail.Code, fail.Body.String())
	}
}

func TestTraeGuidanceAndExchangeCov4th(t *testing.T) {
	var mode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "down":
			http.Error(w, "no", http.StatusBadGateway)
		case "badjson":
			_, _ = w.Write([]byte("not-json"))
		case "nohost":
			_, _ = w.Write([]byte(`{"Result":{}}`))
		case "sg":
			if strings.Contains(r.URL.Path, "GetUserInfo") {
				_, _ = w.Write([]byte(`{"Result":{"Email":"trae@example.test","ScreenName":"Trae","AIRegion":"Singapore","Region":"SG","TenantID":"ten","UserID":"u"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"Result":{"AccessToken":"trae-access-cov4th","RefreshToken":"trae-refresh-cov4th"}}`))
		default:
			if strings.Contains(r.URL.Path, "GetLoginGuidance") {
				_, _ = w.Write([]byte(`{"data":{"loginHost":"login.example.test"}}`))
				return
			}
			if strings.Contains(r.URL.Path, "GetUserInfo") {
				_, _ = w.Write([]byte(`{"Result":{"email":"cn@example.test","AIRegion":"China"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"accessToken":"trae-access-cov4th"}`))
		}
	}))
	t.Cleanup(srv.Close)
	origG, origO := append([]string(nil), traeGuidanceURLs...), append([]string(nil), traeAPIOrigins...)
	t.Cleanup(func() {
		traeGuidanceURLs = origG
		traeAPIOrigins = origO
	})
	traeGuidanceURLs = []string{srv.URL + "/GetLoginGuidance", srv.URL + "/second"}
	traeAPIOrigins = []string{srv.URL}

	handler := NewOAuthHandler(repoCov4th(t))
	mode = "down"
	if rec := callOAuthCov4th(handler.HandleTraeAuthorize, http.MethodGet, "/api/oauth/trae/authorize?state=trace-cov4th", ""); rec.Code != http.StatusBadGateway {
		t.Fatalf("down %d %s", rec.Code, rec.Body.String())
	}
	mode = "badjson"
	if rec := callOAuthCov4th(handler.HandleTraeAuthorize, http.MethodGet, "/api/oauth/trae/authorize", ""); rec.Code != http.StatusBadGateway {
		t.Fatalf("bad json %d", rec.Code)
	}
	mode = "nohost"
	if rec := callOAuthCov4th(handler.HandleTraeAuthorize, http.MethodGet, "/api/oauth/trae/authorize", ""); rec.Code != http.StatusBadGateway {
		t.Fatalf("no host %d", rec.Code)
	}
	mode = ""
	ok := callOAuthCov4th(handler.HandleTraeAuthorize, http.MethodGet, "/api/oauth/trae/authorize?state=trace-cov4th", "")
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), "https://login.example.test/authorization") {
		t.Fatalf("authorize %d %s", ok.Code, ok.Body.String())
	}

	missing := callOAuthCov4th(handler.HandleTraeExchange, http.MethodPost, "/api/oauth/trae/exchange", `{}`)
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing %d", missing.Code)
	}
	denied := callOAuthCov4th(handler.HandleTraeExchange, http.MethodPost, "/api/oauth/trae/exchange", `{"code":"error=access_denied&error_description=no"}`)
	if denied.Code != http.StatusBadRequest {
		t.Fatalf("denied %d %s", denied.Code, denied.Body.String())
	}
	noRefresh := callOAuthCov4th(handler.HandleTraeExchange, http.MethodPost, "/api/oauth/trae/exchange", `{"code":"loginHost=https://api.trae.ai&token=cloud"}`)
	if noRefresh.Code != http.StatusBadRequest {
		t.Fatalf("no refresh %d %s", noRefresh.Code, noRefresh.Body.String())
	}
	mode = "down"
	failed := callOAuthCov4th(handler.HandleTraeExchange, http.MethodPost, "/api/oauth/trae/exchange", `{"code":"refreshToken=rt-cov4th&loginHost=https://api.trae.ai&access_token=cloud"}`)
	if failed.Code != http.StatusBadGateway {
		t.Fatalf("exchange down %d %s", failed.Code, failed.Body.String())
	}
	mode = "sg"
	sg := callOAuthCov4th(handler.HandleTraeExchange, http.MethodPost, "/api/oauth/trae/exchange", `{"code":"?refresh_token=rt-cov4th&login_host=https://api.trae.ai&token=cloud","name":"SG"}`)
	if sg.Code != http.StatusOK || !strings.Contains(sg.Body.String(), "trae@example.test") {
		t.Fatalf("sg %d %s", sg.Code, sg.Body.String())
	}
	mode = ""
	cn := callOAuthCov4th(handler.HandleTraeExchange, http.MethodPost, "/api/oauth/trae/exchange", `{"code":"Bearer trae-access-cov4th"}`)
	if cn.Code != http.StatusOK {
		t.Fatalf("paste %d %s", cn.Code, cn.Body.String())
	}
	if _, _, _, err := parseTraeCallback("#refreshToken=rt&loginHost=h&x-cloudide-token=c"); err != nil {
		t.Fatal(err)
	}
	if got := traeStr(map[string]any{"Result": map[string]any{"Email": " a "}}, "Result", "Email"); got != "a" {
		t.Fatal(got)
	}
	if traeStr(map[string]any{"Result": "x"}, "Result", "Email") != "" {
		t.Fatal("non-object path")
	}
}

func TestKimchiGitlabIflowCov4th(t *testing.T) {
	var validateStatus int
	var userStatus int
	var userBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/me") {
			w.WriteHeader(userStatus)
			_, _ = w.Write([]byte(userBody))
			return
		}
		if strings.Contains(r.URL.Path, "/api/v4/user") {
			if r.Header.Get("Private-Token") == "bad" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte("nope\nsecret"))
				return
			}
			if r.Header.Get("Private-Token") == "weird" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("not-json"))
				return
			}
			_, _ = w.Write([]byte(`{"username":"gl","name":"Git Lab","email":"gl@example.test","public_email":""}`))
			return
		}
		w.WriteHeader(validateStatus)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	origV, origU, origA := kimchiValidate, kimchiUserInfo, kimchiAppBase
	kimchiValidate = srv.URL + "/validate"
	kimchiUserInfo = srv.URL + "/me"
	kimchiAppBase = "https://app.example.test"
	t.Cleanup(func() {
		kimchiValidate, kimchiUserInfo, kimchiAppBase = origV, origU, origA
	})

	repo := repoCov4th(t)
	handler := NewOAuthHandler(repo)
	authz := callOAuthCov4th(handler.HandleKimchiAuthorize, http.MethodGet, "/api/oauth/kimchi/authorize?state=st-cov4th", "")
	if authz.Code != http.StatusOK || !strings.Contains(authz.Body.String(), "https://app.example.test/cli-auth") {
		t.Fatalf("authorize %d %s", authz.Code, authz.Body.String())
	}
	validateStatus, userStatus = http.StatusUnauthorized, http.StatusOK
	if rec := callOAuthCov4th(handler.HandleKimchiExchange, http.MethodPost, "/api/oauth/kimchi/exchange", `{"code":"kimchi-token-cov4th"}`); rec.Code != http.StatusBadGateway {
		t.Fatalf("validate %d %s", rec.Code, rec.Body.String())
	}
	validateStatus, userStatus, userBody = http.StatusOK, http.StatusOK, `{"id":7,"username":"kim","email":"","name":"Kim"}`
	ok := callOAuthCov4th(handler.HandleKimchiExchange, http.MethodPost, "/api/oauth/kimchi/exchange", `{"code":"kimchi-token-cov4th"}`)
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), "kimchi-user-7") {
		t.Fatalf("kimchi %d %s", ok.Code, ok.Body.String())
	}
	named := callOAuthCov4th(handler.HandleKimchiExchange, http.MethodPost, "/api/oauth/kimchi/exchange", `{"code":"kimchi-token-cov4th","name":"Alias"}`)
	if named.Code != http.StatusOK || countConnsCov4th(t, repo) != 1 {
		t.Fatalf("kimchi update %d rows %d", named.Code, countConnsCov4th(t, repo))
	}

	gl := callOAuthCov4th(handler.HandleGitlabPAT, http.MethodPost, "/api/oauth/gitlab/pat", `{"token":"glpat-cov4th","baseUrl":"`+srv.URL+`"}`)
	if gl.Code != http.StatusOK || !strings.Contains(gl.Body.String(), "gl@example.test") {
		t.Fatalf("gitlab %d %s", gl.Code, gl.Body.String())
	}
	denied := callOAuthCov4th(handler.HandleGitlabPAT, http.MethodPost, "/api/oauth/gitlab/pat", `{"token":"bad","baseUrl":"`+srv.URL+`"}`)
	if denied.Code != http.StatusUnauthorized || !strings.Contains(denied.Body.String(), "nope") {
		t.Fatalf("gitlab denied %d %s", denied.Code, denied.Body.String())
	}
	weird := callOAuthCov4th(handler.HandleGitlabPAT, http.MethodPost, "/api/oauth/gitlab/pat", `{"token":"weird","baseUrl":"`+srv.URL+`"}`)
	if weird.Code != http.StatusBadGateway {
		t.Fatalf("gitlab json %d %s", weird.Code, weird.Body.String())
	}
	down := callOAuthCov4th(handler.HandleGitlabPAT, http.MethodPost, "/api/oauth/gitlab/pat", `{"token":"glpat-cov4th","baseUrl":"http://127.0.0.1:1"}`)
	if down.Code != http.StatusBadGateway {
		t.Fatalf("gitlab dial %d %s", down.Code, down.Body.String())
	}

	installUpstreamCov4th(t, func(r *http.Request) (*http.Response, error) {
		return routeHappyCov4th(r), nil
	})
	cookie := callOAuthCov4th(handler.HandleIflowCookie, http.MethodPost, "/api/oauth/iflow/cookie", `{"cookie":"BXAuth=abc","name":""}`)
	if cookie.Code != http.StatusOK || !strings.Contains(cookie.Body.String(), `"provider":"iflow"`) {
		t.Fatalf("iflow %d %s", cookie.Code, cookie.Body.String())
	}
}

func TestIflowCookieFailuresCov4th(t *testing.T) {
	steps := []struct {
		name string
		fn   devicePollFixture
		want int
	}{
		{"get 500", func(r *http.Request) (*http.Response, error) {
			return jsonBodyCov4th(http.StatusBadGateway, "upstream down"), nil
		}, http.StatusBadGateway},
		{"get rejected", func(r *http.Request) (*http.Response, error) {
			return jsonBodyCov4th(http.StatusOK, `{"success":false,"message":"no"}`), nil
		}, http.StatusBadRequest},
		{"missing name", func(r *http.Request) (*http.Response, error) {
			return jsonBodyCov4th(http.StatusOK, `{"success":true,"data":{}}`), nil
		}, http.StatusBadRequest},
		{"post 500", func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPost {
				return jsonBodyCov4th(http.StatusBadGateway, "post down"), nil
			}
			return jsonBodyCov4th(http.StatusOK, `{"success":true,"data":{"name":"n"}}`), nil
		}, http.StatusBadGateway},
		{"post rejected", func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPost {
				return jsonBodyCov4th(http.StatusOK, `{"success":false,"message":"no"}`), nil
			}
			return jsonBodyCov4th(http.StatusOK, `{"success":true,"data":{"name":"n"}}`), nil
		}, http.StatusBadRequest},
		{"missing key", func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPost {
				return jsonBodyCov4th(http.StatusOK, `{"success":true,"data":{}}`), nil
			}
			return jsonBodyCov4th(http.StatusOK, `{"success":true,"data":{"name":"n"}}`), nil
		}, http.StatusBadRequest},
		{"transport", func(*http.Request) (*http.Response, error) {
			return nil, http.ErrServerClosed
		}, http.StatusBadGateway},
	}
	for _, tc := range steps {
		t.Run(tc.name, func(t *testing.T) {
			installUpstreamCov4th(t, tc.fn)
			rec := callOAuthCov4th(NewOAuthHandler(nil).HandleIflowCookie, http.MethodPost, "/api/oauth/iflow/cookie", `{"cookie":"BXAuth=abc"}`)
			if rec.Code != tc.want {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestCursorAutoImportCov4th(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	handler := NewOAuthHandler(repoCov4th(t))
	missing := callOAuthCov4th(handler.HandleCursorAutoImport, http.MethodGet, "/api/oauth/cursor/auto-import", "")
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing %d %s", missing.Code, missing.Body.String())
	}
	dir := filepath.Join(home, ".config", "Cursor", "User", "globalStorage")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", filepath.Join(dir, "state.vscdb"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE itemTable (key TEXT, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("c", 64)
	if _, err := database.Exec(`INSERT INTO itemTable (key, value) VALUES ('cursorAuth/accessToken', ?), ('storage.serviceMachineId', '12345678123412341234123456789012')`, token); err != nil {
		t.Fatal(err)
	}
	database.Close()
	ok := callOAuthCov4th(handler.HandleCursorAutoImport, http.MethodGet, "/api/oauth/cursor/auto-import", "")
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), `"provider":"cursor"`) {
		t.Fatalf("import %d %s", ok.Code, ok.Body.String())
	}
	again := callOAuthCov4th(handler.HandleCursorAutoImport, http.MethodGet, "/api/oauth/cursor/auto-import", "")
	if again.Code != http.StatusOK {
		t.Fatalf("update %d %s", again.Code, again.Body.String())
	}
}

func TestKiroCacheAndAPIKeyCov4th(t *testing.T) {
	if normalizeKiroProfileArn("") != "" {
		t.Fatal("empty arn")
	}
	if got := normalizeKiroProfileArn("arn:aws:s3:us-west-2:1:bucket"); got != "arn:aws:s3:us-west-2:1:bucket" {
		t.Fatal(got)
	}
	if readKiroClientRegistration("") != nil {
		t.Fatal("empty hash")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	handler := NewOAuthHandler(repoCov4th(t))
	none := callOAuthCov4th(handler.HandleKiroAutoImport, http.MethodGet, "/api/oauth/kiro/auto-import", "")
	if none.Code != http.StatusOK || !strings.Contains(none.Body.String(), `"found":false`) {
		t.Fatalf("none %d %s", none.Code, none.Body.String())
	}
	cache := filepath.Join(home, ".aws", "sso", "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(cache, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("notes.txt", "skip")
	write("bad.json", "{")
	write("kiro-auth-token.json", `{"refreshToken":"nope"}`)
	write("partial.json", `{"clientId":"only"}`)
	if readKiroClientRegistration("partial") != nil {
		t.Fatal("partial registration")
	}
	write("other.json", `{"refreshToken":"aorAAAAAGfake-cov4th","region":"us-west-2","authMethod":"idc","clientIdHash":"hashcov4th"}`)
	write("hashcov4th.json", `{"clientId":"cid-cov4th","clientSecret":"csec-cov4th"}`)
	profile := filepath.Join(home, ".config", "Kiro", "User", "globalStorage", "kiro.kiroagent")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "profile.json"), []byte(`{"arn":"arn:aws:codewhisperer:eu-west-1:1:profile/cov4th"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	found := callOAuthCov4th(handler.HandleKiroAutoImport, http.MethodGet, "/api/oauth/kiro/auto-import", "")
	if found.Code != http.StatusOK || !strings.Contains(found.Body.String(), `"found":true`) || !strings.Contains(found.Body.String(), "us-east-1") || !strings.Contains(found.Body.String(), "cid-cov4th") {
		t.Fatalf("found %d %s", found.Code, found.Body.String())
	}

	installUpstreamCov4th(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "codewhisperer.us-east-1.amazonaws.com" && r.Header.Get("Authorization") == "Bearer bad-key" {
			return jsonBodyCov4th(http.StatusForbidden, `{"message":"no"}`), nil
		}
		return routeHappyCov4th(r), nil
	})
	bad := callOAuthCov4th(handler.HandleKiroAPIKey, http.MethodPost, "/api/oauth/kiro/api-key", `{"apiKey":"bad-key","region":"eu-west-1"}`)
	if bad.Code != http.StatusBadGateway {
		t.Fatalf("bad key %d %s", bad.Code, bad.Body.String())
	}
	key := jwtEmailCov4th("kiro-key@example.test")
	ok := callOAuthCov4th(handler.HandleKiroAPIKey, http.MethodPost, "/api/oauth/kiro/api-key", `{"apiKey":"`+key+`"}`)
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), "kiro-key@example.test") {
		t.Fatalf("api key %d %s", ok.Code, ok.Body.String())
	}
	again := callOAuthCov4th(handler.HandleKiroAPIKey, http.MethodPost, "/api/oauth/kiro/api-key", `{"apiKey":"`+key+`"}`)
	if again.Code != http.StatusOK {
		t.Fatalf("api key update %d %s", again.Code, again.Body.String())
	}

	imported := callOAuthCov4th(handler.HandleKiroImport, http.MethodPost, "/api/oauth/kiro/import", `{"refreshToken":"refresh-cov4th","authMethod":"social"}`)
	if imported.Code != http.StatusOK {
		t.Fatalf("import %d %s", imported.Code, imported.Body.String())
	}
	idc := callOAuthCov4th(handler.HandleKiroImport, http.MethodPost, "/api/oauth/kiro/import", `{"refreshToken":"refresh-idc-cov4th","clientId":"cid","clientSecret":"csec","region":"us-east-1","profileArn":"arn:aws:codewhisperer:eu-west-1:1:profile/p"}`)
	if idc.Code != http.StatusOK {
		t.Fatalf("idc %d %s", idc.Code, idc.Body.String())
	}
	badJSON := callOAuthCov4th(handler.HandleKiroImportCliProxy, http.MethodPost, "/api/oauth/kiro/import-cli-proxy", `{"auth":"not-json"}`)
	if badJSON.Code != http.StatusBadRequest {
		t.Fatalf("cli %d %s", badJSON.Code, badJSON.Body.String())
	}
	cli := `{"json":"{\"auth_method\":\"external_idp\",\"access_token\":\"at-cov4th\",\"refresh_token\":\"rt-cov4th\",\"client_id\":\"cid\",\"token_endpoint\":\"https://login.microsoftonline.com/common/oauth2/v2.0/token\",\"profile_arn\":\"arn:aws:codewhisperer:us-east-1:1:profile/p\",\"scopes\":[\"openid\",\"email\"],\"email\":\"kiro-cli@example.test\",\"expires_in\":120}"}`
	cliRec := callOAuthCov4th(handler.HandleKiroImportCliProxy, http.MethodPost, "/api/oauth/kiro/import-cli-proxy", cli)
	if cliRec.Code != http.StatusOK || !strings.Contains(cliRec.Body.String(), "kiro-cli@example.test") {
		t.Fatalf("cli proxy %d %s", cliRec.Code, cliRec.Body.String())
	}
}

func TestZedUserinfoCov4th(t *testing.T) {
	if zedOrgID(map[string]any{"default_organization_id": "d"}) != "d" {
		t.Fatal("default id")
	}
	if zedOrgID(map[string]any{"defaultOrganizationId": "c"}) != "c" {
		t.Fatal("camel id")
	}
	if zedOrgID(map[string]any{"organizations": []any{map[string]any{"id": "a"}, map[string]any{"id": "b", "is_personal": true}}}) != "b" {
		t.Fatal("personal org")
	}
	if zedOrgID(map[string]any{"organizations": []any{"nope", map[string]any{"id": "only"}}}) != "only" {
		t.Fatal("first org")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" || r.Header.Get(zedSystemHeader) == "" {
			http.Error(w, "missing", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"email":"zed@example.test","display_name":"Zed User","organizations":[{"id":"org-cov4th","is_personal":true}]}`))
	}))
	t.Cleanup(srv.Close)
	orig := zedCloudBase
	zedCloudBase = srv.URL
	t.Cleanup(func() { zedCloudBase = orig })

	handler := NewOAuthHandler(repoCov4th(t))
	authz := callOAuthCov4th(handler.HandleZedAuthorize, http.MethodGet, "/api/oauth/zed/authorize?port=4000", "")
	verifier := betweenCov4th(t, authz.Body.String(), `"codeVerifier":"`, `"`)
	systemID := betweenCov4th(t, authz.Body.String(), `"systemId":"`, `"`)
	pemBytes, err := decodeZedVerifier(verifier)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		t.Fatal("zed verifier was not a PEM key")
	}
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &priv.PublicKey, []byte("zed-token-cov4th"), nil)
	if err != nil {
		t.Fatal(err)
	}
	cb := "user_id=u1&access_token=" + base64.RawURLEncoding.EncodeToString(cipher)
	body := `{"code":"` + cb + `","codeVerifier":"` + verifier + `","systemId":"` + systemID + `"}`
	got := callOAuthCov4th(handler.HandleZedExchange, http.MethodPost, "/api/oauth/zed/exchange", body)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "zed@example.test") || !strings.Contains(got.Body.String(), "org-cov4th") {
		t.Fatalf("zed %d %s", got.Code, got.Body.String())
	}
	updated := callOAuthCov4th(handler.HandleZedExchange, http.MethodPost, "/api/oauth/zed/exchange", body)
	if updated.Code != http.StatusOK {
		t.Fatalf("zed update %d %s", updated.Code, updated.Body.String())
	}
}
