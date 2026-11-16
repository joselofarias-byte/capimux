package oauth

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func jwtEmailCov4th(email string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"` + email + `","exp":2000000000}`))
	return header + "." + payload + ".sig"
}

func routeHappyCov4th(r *http.Request) *http.Response {
	switch r.URL.Host {
	case "api.kilo.ai":
		if r.Method == http.MethodPost {
			return jsonBodyCov4th(http.StatusOK, `{"code":"kilo-code","verificationUrl":"https://app.kilo.ai/device"}`)
		}
		if strings.HasPrefix(r.URL.Path, "/api/device-auth/codes/") {
			return jsonBodyCov4th(http.StatusOK, `{"status":"approved","token":"kilo-token-cov4th","userEmail":"kilo@example.test"}`)
		}
		return jsonBodyCov4th(http.StatusOK, `{"organizations":[{"id":"org-cov4th"}]}`)
	case "auth.x.ai":
		if strings.Contains(r.URL.Path, "/device/code") {
			return jsonBodyCov4th(http.StatusOK, `{"device_code":"grok-code","user_code":"ABCD","verification_uri":"https://auth.x.ai/device"}`)
		}
		return jsonBodyCov4th(http.StatusOK, `{"access_token":"grok-access-cov4th","refresh_token":"grok-refresh-cov4th"}`)
	case "cli-chat-proxy.grok.com":
		return jsonBodyCov4th(http.StatusOK, `{"email":"grok@example.test","name":"Grok User"}`)
	case "github.com":
		if strings.Contains(r.URL.Path, "/device/code") {
			return jsonBodyCov4th(http.StatusOK, `{"device_code":"gh-code","user_code":"GH-1","verification_uri":"https://github.com/login/device"}`)
		}
		return jsonBodyCov4th(http.StatusOK, `{"access_token":"gh-access-cov4th","refresh_token":"gh-refresh-cov4th"}`)
	case "api.github.com":
		if strings.Contains(r.URL.Path, "copilot") {
			return jsonBodyCov4th(http.StatusOK, `{"token":"copilot-cov4th","expires_at":"2099-01-01T00:00:00Z"}`)
		}
		return jsonBodyCov4th(http.StatusOK, `{"id":42,"login":"octo","name":"Octo","email":"octo@example.test"}`)
	case "oidc.us-east-1.amazonaws.com":
		switch r.URL.Path {
		case "/client/register":
			return jsonBodyCov4th(http.StatusOK, `{"clientId":"cid-cov4th","clientSecret":"csec-cov4th"}`)
		case "/device_authorization":
			return jsonBodyCov4th(http.StatusOK, `{"deviceCode":"kiro-dev","userCode":"KIRO","verificationUri":"https://view.awsapps.com/start","verificationUriComplete":"https://view.awsapps.com/start#u","expiresIn":300,"interval":2}`)
		default:
			return jsonBodyCov4th(http.StatusOK, `{"accessToken":"`+jwtEmailCov4th("kiro@example.test")+`","refreshToken":"kiro-refresh-cov4th"}`)
		}
	case "codewhisperer.us-east-1.amazonaws.com":
		if r.URL.Path == "/ListAvailableProfiles" {
			return jsonBodyCov4th(http.StatusOK, `{"profiles":[{"arn":"  arn:aws:codewhisperer:us-east-1:1:profile/cov4th  "}]}`)
		}
		return jsonBodyCov4th(http.StatusOK, `{"models":[]}`)
	case "auth.kimi.com":
		if strings.Contains(r.URL.Path, "device_authorization") {
			return jsonBodyCov4th(http.StatusOK, `{"device_code":"kimi-code","user_code":"KIMI","verification_uri":"https://auth.kimi.com/device"}`)
		}
		return jsonBodyCov4th(http.StatusOK, `{"access_token":"kimi-access-cov4th","refresh_token":"kimi-refresh-cov4th"}`)
	case "copilot.tencent.com":
		if strings.Contains(r.URL.Path, "/auth/state") {
			return jsonBodyCov4th(http.StatusOK, `{"code":0,"data":{"state":"cb-state","authUrl":"https://copilot.tencent.com/auth"}}`)
		}
		return jsonBodyCov4th(http.StatusOK, `{"code":0,"data":{"accessToken":"cb-access-cov4th","refreshToken":"cb-refresh-cov4th"}}`)
	case "www.codebuddy.ai":
		if strings.Contains(r.URL.Path, "/auth/state") {
			return jsonBodyCov4th(http.StatusOK, `{"code":0,"data":{"state":"cbi-state","authUrl":"https://www.codebuddy.ai/auth"}}`)
		}
		return jsonBodyCov4th(http.StatusOK, `{"code":0,"data":{"accessToken":"cbi-access-cov4th","refreshToken":"cbi-refresh-cov4th"}}`)
	case "auth.meta.com":
		if strings.Contains(r.URL.Path, "authorization") {
			return jsonBodyCov4th(http.StatusOK, `{"device_code":"muse-code","user_code":"MUSE","verification_uri":"https://auth.meta.com/device"}`)
		}
		return jsonBodyCov4th(http.StatusOK, `{"access_token":"muse-account-cov4th"}`)
	case "api.meta.ai":
		return jsonBodyCov4th(http.StatusOK, `{"api_key":"muse-key-cov4th","user_email":"Muse@Example.Test","subs_tier_name":"pro","is_subs_active":true}`)
	case "openapi.qoder.sh":
		if strings.Contains(r.URL.Path, "userinfo") {
			return jsonBodyCov4th(http.StatusOK, `{"name":"Qoder User","email":"qoder@example.test","organization_id":"org-q"}`)
		}
		return jsonBodyCov4th(http.StatusOK, `{"token":"qoder-token-cov4th","refresh_token":"qoder-rt","user_id":"uid-q"}`)
	case "openapi.qoder.com.cn":
		if strings.Contains(r.URL.Path, "userinfo") {
			return jsonBodyCov4th(http.StatusOK, `{"username":"qcn"}`)
		}
		return jsonBodyCov4th(http.StatusOK, `{"token":"qoder-cn-token-cov4th","user_id":"uid-cn"}`)
	case "prod.us-east-1.auth.desktop.kiro.dev":
		if strings.Contains(r.URL.Path, "refreshToken") {
			return jsonBodyCov4th(http.StatusOK, `{"accessToken":"kiro-social-access-cov4th","refreshToken":"kiro-social-refresh-cov4th","expiresIn":1200}`)
		}
		return jsonBodyCov4th(http.StatusOK, `{"accessToken":"kiro-exchange-access-cov4th","refreshToken":"kiro-exchange-refresh-cov4th","expiresIn":900,"profileArn":"arn:aws:codewhisperer:us-east-1:1:profile/social"}`)
	case "oauth2.googleapis.com":
		return jsonBodyCov4th(http.StatusOK, `{"access_token":"ag-access-cov4th","refresh_token":"ag-refresh-cov4th","expires_in":3600,"token_type":"Bearer","scope":"email","id_token":"`+jwtEmailCov4th("ag@example.test")+`"}`)
	case "www.googleapis.com":
		return jsonBodyCov4th(http.StatusOK, `{"email":"gemini@example.test"}`)
	case "api.cline.bot":
		return jsonBodyCov4th(http.StatusOK, `{"success":true,"data":{"accessToken":"cline-access-cov4th","refreshToken":"cline-refresh-cov4th"}}`)
	case "api.anthropic.com":
		return jsonBodyCov4th(http.StatusOK, `{"access_token":"claude-access-cov4th","refresh_token":"claude-refresh-cov4th","expires_in":3600}`)
	case "auth.openai.com":
		claims := `{"exp":2000000000,"email":"codex@example.test","https://api.openai.com/auth":{"chatgpt_account_id":"acct-cov4th","chatgpt_plan_type":"plus"}}`
		idTok := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".sig"
		return jsonBodyCov4th(http.StatusOK, `{"access_token":"codex-access-cov4th","refresh_token":"codex-refresh-cov4th","expires_in":3600,"id_token":"`+idTok+`"}`)
	case "iflow.cn":
		if strings.Contains(r.URL.Path, "getUserInfo") {
			return jsonBodyCov4th(http.StatusOK, `{"success":true,"data":{"apiKey":"iflow-key-cov4th","email":"iflow@example.test","nickname":"IFlow"}}`)
		}
		return jsonBodyCov4th(http.StatusOK, `{"access_token":"iflow-access-cov4th","refresh_token":"iflow-refresh-cov4th","expires_in":60}`)
	case "platform.iflow.cn":
		if r.Method == http.MethodGet {
			return jsonBodyCov4th(http.StatusOK, `{"success":true,"data":{"name":"iflow-name"}}`)
		}
		return jsonBodyCov4th(http.StatusOK, `{"success":true,"data":{"apiKey":"iflow-cookie-key-cov4th","name":"iflow-name","expireTime":"2099-01-01"}}`)
	default:
		return jsonBodyCov4th(http.StatusBadGateway, `{"error":"unhandled"}`)
	}
}

func TestDeviceStartHappyCov4th(t *testing.T) {
	installUpstreamCov4th(t, func(r *http.Request) (*http.Response, error) {
		resp := routeHappyCov4th(r)
		resp.Request = r
		return resp, nil
	})
	handler := NewOAuthHandler(nil)
	cases := []struct {
		name string
		body string
		want string
	}{
		{"qoder-cn local", `{"provider":"qoder-cn"}`, "qoder.com.cn/device/selectAccounts"},
		{"kimi alias", `{"provider":"kimi-coding"}`, "kimi-code"},
		{"kilocode", `{"provider":"kilocode"}`, "kilo-code"},
		{"grok-cli", `{"provider":"grok-cli"}`, "grok-code"},
		{"github", `{"provider":"github"}`, "gh-code"},
		{"kiro idc", `{"provider":"kiro","region":"us-east-1","authMethod":"idc","startUrl":"https://example.test/start"}`, "kiro-dev"},
		{"kimi", `{"provider":"kimi"}`, "kimi-code"},
		{"codebuddy-cn", `{"provider":"codebuddy-cn"}`, "cb-state"},
		{"codebuddy-intl", `{"provider":"codebuddy-intl"}`, "cbi-state"},
		{"muse", `{"provider":"muse"}`, "muse-code"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := callOAuthCov4th(handler.HandleDeviceStart, http.MethodPost, "/api/oauth/device/start", tc.body)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDevicePollAuthorizedCov4th(t *testing.T) {
	installUpstreamCov4th(t, func(r *http.Request) (*http.Response, error) {
		resp := routeHappyCov4th(r)
		resp.Request = r
		return resp, nil
	})
	repo := repoCov4th(t)
	handler := NewOAuthHandler(repo)
	cases := []struct {
		name string
		body string
	}{
		{"qoder", `{"provider":"qoder","device_code":"nonce-1","session":{"verifier":"ver-1","machineId":"mach-1"}}`},
		{"qoder-cn", `{"provider":"qoder-cn","device_code":"nonce-cn","session":{"verifier":"ver-cn","machineId":"mach-cn"}}`},
		{"kilocode", `{"provider":"kilocode","device_code":"kilo-code"}`},
		{"grok-cli", `{"provider":"grok-cli","device_code":"grok-code"}`},
		{"github", `{"provider":"github","device_code":"gh-code"}`},
		{"kiro", `{"provider":"kiro","device_code":"kiro-dev","session":{"clientId":"cid-cov4th","clientSecret":"csec-cov4th","region":"us-east-1","authMethod":"builder-id"}}`},
		{"kimi", `{"provider":"kimi","device_code":"kimi-code","session":{"deviceId":"dev-kimi"}}`},
		{"codebuddy-cn", `{"provider":"codebuddy-cn","device_code":"cb-state"}`},
		{"codebuddy-intl", `{"provider":"codebuddy-intl","device_code":"cbi-state"}`},
		{"muse", `{"provider":"muse","device_code":"muse-code"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := countConnsCov4th(t, repo)
			rec := callOAuthCov4th(handler.HandleDevicePoll, http.MethodPost, "/api/oauth/device/poll", tc.body)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"authorized"`) {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
			if countConnsCov4th(t, repo) != before+1 {
				t.Fatalf("connection was not stored")
			}
			again := callOAuthCov4th(handler.HandleDevicePoll, http.MethodPost, "/api/oauth/device/poll", tc.body)
			if again.Code != http.StatusOK || !strings.Contains(again.Body.String(), `"status":"authorized"`) {
				t.Fatalf("update status %d body %s", again.Code, again.Body.String())
			}
			if countConnsCov4th(t, repo) != before+1 {
				t.Fatalf("second poll inserted a duplicate row")
			}
		})
	}
}

func TestDeviceStartFailuresCov4th(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		status int
		resp   string
		err    error
	}{
		{"kilo transport", `{"provider":"kilocode"}`, 0, "", errors.New("dial refused")},
		{"kilo 429", `{"provider":"kilocode"}`, http.StatusTooManyRequests, `{}`, nil},
		{"kilo 500", `{"provider":"kilocode"}`, http.StatusInternalServerError, `{}`, nil},
		{"grok 500", `{"provider":"grok-cli"}`, http.StatusInternalServerError, `{}`, nil},
		{"github 500", `{"provider":"github"}`, http.StatusInternalServerError, `{}`, nil},
		{"kimi 500", `{"provider":"kimi"}`, http.StatusInternalServerError, `{}`, nil},
		{"muse 500", `{"provider":"muse"}`, http.StatusInternalServerError, `{}`, nil},
		{"muse missing code", `{"provider":"muse"}`, http.StatusOK, `{"user_code":"x"}`, nil},
		{"codebuddy code", `{"provider":"codebuddy-cn"}`, http.StatusOK, `{"code":7,"msg":"nope"}`, nil},
		{"kiro register", `{"provider":"kiro","region":"us-east-1"}`, http.StatusInternalServerError, `{}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			installUpstreamCov4th(t, func(r *http.Request) (*http.Response, error) {
				if tc.err != nil {
					return nil, tc.err
				}
				return jsonBodyCov4th(tc.status, tc.resp), nil
			})
			rec := callOAuthCov4th(NewOAuthHandler(nil).HandleDeviceStart, http.MethodPost, "/api/oauth/device/start", tc.body)
			if rec.Code != http.StatusBadGateway {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDevicePollFailuresCov4th(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		status int
		resp   string
		want   string
	}{
		{"qoder missing verifier", `{"provider":"qoder","device_code":"n","session":{}}`, http.StatusOK, `{}`, `"status":"error"`},
		{"qoder pending", `{"provider":"qoder","device_code":"n","session":{"verifier":"v"}}`, http.StatusAccepted, ``, `"status":"pending"`},
		{"qoder bad json", `{"provider":"qoder","device_code":"n","session":{"verifier":"v"}}`, http.StatusOK, `not-json`, `"status":"error"`},
		{"qoder no token", `{"provider":"qoder","device_code":"n","session":{"verifier":"v"}}`, http.StatusOK, `{"user_id":"u"}`, `"status":"error"`},
		{"kilo denied", `{"provider":"kilocode","device_code":"c"}`, http.StatusForbidden, ``, `"status":"error"`},
		{"kilo expired", `{"provider":"kilocode","device_code":"c"}`, http.StatusGone, ``, `"status":"error"`},
		{"kilo 500", `{"provider":"kilocode","device_code":"c"}`, http.StatusInternalServerError, `{}`, `"status":"error"`},
		{"kilo bad json", `{"provider":"kilocode","device_code":"c"}`, http.StatusOK, `{`, `"status":"error"`},
		{"kilo not approved", `{"provider":"kilocode","device_code":"c"}`, http.StatusOK, `{"status":"pending"}`, `"status":"pending"`},
		{"grok pending", `{"provider":"grok-cli","device_code":"c"}`, http.StatusOK, `{"error":"authorization_pending"}`, `"status":"pending"`},
		{"grok slow", `{"provider":"grok-cli","device_code":"c"}`, http.StatusOK, `{"error":"slow_down"}`, `"status":"pending"`},
		{"grok denied", `{"provider":"grok-cli","device_code":"c"}`, http.StatusOK, `{"error":"access_denied","error_description":"no"}`, `"status":"error"`},
		{"github pending", `{"provider":"github","device_code":"c"}`, http.StatusOK, `{"error":"authorization_pending"}`, `"status":"pending"`},
		{"github denied", `{"provider":"github","device_code":"c"}`, http.StatusOK, `{"error":"access_denied","error_description":"no"}`, `"status":"error"`},
		{"kimi pending", `{"provider":"kimi","device_code":"c","session":{"deviceId":"d"}}`, http.StatusOK, `{"error":"slow_down"}`, `"status":"pending"`},
		{"kimi empty", `{"provider":"kimi","device_code":"c"}`, http.StatusOK, `{}`, `"status":"pending"`},
		{"kiro bad region", `{"provider":"kiro","device_code":"c","session":{"region":"nope"}}`, http.StatusOK, `{}`, `"status":"error"`},
		{"kiro pending", `{"provider":"kiro","device_code":"c","session":{"region":"us-east-1"}}`, http.StatusOK, `{"error":"authorization_pending"}`, `"status":"pending"`},
		{"codebuddy pending", `{"provider":"codebuddy-cn","device_code":"st"}`, http.StatusOK, `{"code":11217}`, `"status":"pending"`},
		{"codebuddy http", `{"provider":"codebuddy-intl","device_code":"st"}`, http.StatusBadRequest, `{}`, `"status":"error"`},
		{"codebuddy bad json", `{"provider":"codebuddy-cn","device_code":"st"}`, http.StatusOK, `nope`, `"status":"error"`},
		{"codebuddy msg", `{"provider":"codebuddy-cn","device_code":"st"}`, http.StatusOK, `{"code":9,"msg":"failed"}`, `"status":"error"`},
		{"muse pending", `{"provider":"muse","device_code":"c"}`, http.StatusOK, `{"error":"authorization_pending"}`, `"status":"pending"`},
		{"muse denied", `{"provider":"muse","device_code":"c"}`, http.StatusOK, `{"error":"access_denied","error_description":"no"}`, `"status":"error"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			installUpstreamCov4th(t, func(r *http.Request) (*http.Response, error) {
				return jsonBodyCov4th(tc.status, tc.resp), nil
			})
			rec := callOAuthCov4th(NewOAuthHandler(nil).HandleDevicePoll, http.MethodPost, "/api/oauth/device/poll", tc.body)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMuseMintBranchesCov4th(t *testing.T) {
	t.Run("inactive", func(t *testing.T) {
		installUpstreamCov4th(t, func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "api.meta.ai" {
				return jsonBodyCov4th(http.StatusOK, `{"is_subs_active":false}`), nil
			}
			return jsonBodyCov4th(http.StatusOK, `{"access_token":"muse-account-cov4th"}`), nil
		})
		rec := callOAuthCov4th(NewOAuthHandler(nil).HandleDevicePoll, http.MethodPost, "/api/oauth/device/poll", `{"provider":"muse","device_code":"c"}`)
		if !strings.Contains(rec.Body.String(), "inactive") {
			t.Fatalf("body %s", rec.Body.String())
		}
	})
	t.Run("payment", func(t *testing.T) {
		installUpstreamCov4th(t, func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "api.meta.ai" {
				return jsonBodyCov4th(http.StatusOK, `{"require_payment":true,"action_url":"https://example.test/pay"}`), nil
			}
			return jsonBodyCov4th(http.StatusOK, `{"access_token":"muse-account-cov4th"}`), nil
		})
		rec := callOAuthCov4th(NewOAuthHandler(nil).HandleDevicePoll, http.MethodPost, "/api/oauth/device/poll", `{"provider":"muse","device_code":"c"}`)
		if !strings.Contains(rec.Body.String(), "subscription required") {
			t.Fatalf("body %s", rec.Body.String())
		}
	})
	t.Run("no api key", func(t *testing.T) {
		installUpstreamCov4th(t, func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "api.meta.ai" {
				return jsonBodyCov4th(http.StatusOK, `{"user_email":"a@b.c"}`), nil
			}
			return jsonBodyCov4th(http.StatusOK, `{"access_token":"muse-account-cov4th"}`), nil
		})
		rec := callOAuthCov4th(NewOAuthHandler(nil).HandleDevicePoll, http.MethodPost, "/api/oauth/device/poll", `{"provider":"muse","device_code":"c"}`)
		if !strings.Contains(rec.Body.String(), "no api_key") {
			t.Fatalf("body %s", rec.Body.String())
		}
	})
	t.Run("status error text", func(t *testing.T) {
		installUpstreamCov4th(t, func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "api.meta.ai" {
				return jsonBodyCov4th(http.StatusBadRequest, `{"title":"Nope","detail":"bad key","require_payment_action_url":"https://example.test/fix"}`), nil
			}
			return jsonBodyCov4th(http.StatusOK, `{"access_token":"muse-account-cov4th"}`), nil
		})
		rec := callOAuthCov4th(NewOAuthHandler(nil).HandleDevicePoll, http.MethodPost, "/api/oauth/device/poll", `{"provider":"muse","device_code":"c"}`)
		if !strings.Contains(rec.Body.String(), "Nope") {
			t.Fatalf("body %s", rec.Body.String())
		}
	})
	t.Run("cancel during retry", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		installUpstreamCov4th(t, func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "api.meta.ai" {
				cancel()
				return jsonBodyCov4th(http.StatusTooManyRequests, `{"title":"slow"}`), nil
			}
			return jsonBodyCov4th(http.StatusOK, `{"access_token":"muse-account-cov4th"}`), nil
		})
		req := httptestNewCov4th(ctx, `{"provider":"muse","device_code":"c"}`)
		rec := httptest.NewRecorder()
		NewOAuthHandler(nil).HandleDevicePoll(rec, req)
		if rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), `"status":"authorized"`) {
			t.Fatalf("cancelled mint reported success: %s", rec.Body.String())
		}
	})
	t.Run("retry then key", func(t *testing.T) {
		var hits int
		installUpstreamCov4th(t, func(r *http.Request) (*http.Response, error) {
			if r.URL.Host == "api.meta.ai" {
				hits++
				if hits == 1 {
					return jsonBodyCov4th(http.StatusTooManyRequests, `{}`), nil
				}
				return jsonBodyCov4th(http.StatusOK, `{"api_key":"muse-retry-cov4th","user_email":"retry@example.test"}`), nil
			}
			return jsonBodyCov4th(http.StatusOK, `{"access_token":"muse-account-cov4th"}`), nil
		})
		start := time.Now()
		rec := callOAuthCov4th(NewOAuthHandler(nil).HandleDevicePoll, http.MethodPost, "/api/oauth/device/poll", `{"provider":"muse","device_code":"c"}`)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"authorized"`) {
			t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
		}
		if time.Since(start) < 4*time.Second {
			t.Fatal("retry returned before the mint backoff")
		}
	})
}

func httptestNewCov4th(ctx context.Context, body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/api/oauth/device/poll", strings.NewReader(body)).WithContext(ctx)
}

func TestDeviceJSONHelpersCov4th(t *testing.T) {
	if firstLine("alpha\nbeta", 3) != "alp" {
		t.Fatal("firstLine truncate")
	}
	if strVal(map[string]any{"a": "  "}, "a", "b") != "" || strVal(map[string]any{"b": " ok "}, "a", "b") != "ok" {
		t.Fatal("strVal")
	}
	if _, _, err := postJSON(context.Background(), "http://127.0.0.1", make(chan int), nil); err == nil {
		t.Fatal("postJSON accepted an unmarshalable body")
	}
	if _, _, err := postJSON(context.Background(), "http://[::1", map[string]int{"a": 1}, nil); err == nil {
		t.Fatal("postJSON accepted a bad URL")
	}
	if _, _, err := postForm(context.Background(), "http://[::1", nil, nil); err == nil {
		t.Fatal("postForm accepted a bad URL")
	}
	if _, _, err := getJSON(context.Background(), "http://[::1", nil); err == nil {
		t.Fatal("getJSON accepted a bad URL")
	}

	installUpstreamCov4th(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/raw":
			return jsonBodyCov4th(http.StatusOK, "not-json"), nil
		case "/badjson":
			return jsonBodyCov4th(http.StatusOK, "not-json"), nil
		case "/read":
			return &http.Response{StatusCode: http.StatusOK, Body: errBodyCov4th{}, Request: r}, nil
		default:
			return nil, errors.New("transport")
		}
	})
	if _, _, err := postJSON(context.Background(), "http://127.0.0.1/badjson", map[string]string{"a": "b"}, nil); err == nil {
		t.Fatal("postJSON accepted invalid JSON")
	}
	raw, status, err := postForm(context.Background(), "http://127.0.0.1/raw", nil, map[string]string{"X-Test": "1"})
	if err != nil || status != http.StatusOK || raw["_raw"] == nil {
		t.Fatalf("postForm raw = %v err %v status %d", raw, err, status)
	}
	if _, _, err := getJSON(context.Background(), "http://127.0.0.1/badjson", nil); err == nil {
		t.Fatal("getJSON accepted invalid JSON")
	}
	if _, status, err := postJSON(context.Background(), "http://127.0.0.1/read", map[string]string{"a": "b"}, nil); err == nil || status != http.StatusOK {
		t.Fatalf("postJSON read err=%v status=%d", err, status)
	}
	if _, status, err := postForm(context.Background(), "http://127.0.0.1/read", nil, nil); err == nil || status != http.StatusOK {
		t.Fatalf("postForm read err=%v status=%d", err, status)
	}
	if _, status, err := getJSON(context.Background(), "http://127.0.0.1/read", nil); err == nil || status != http.StatusOK {
		t.Fatalf("getJSON read err=%v status=%d", err, status)
	}
	if _, _, err := postJSON(context.Background(), "http://127.0.0.1/down", map[string]string{"a": "b"}, nil); err == nil {
		t.Fatal("postJSON ignored transport error")
	}
}

func TestSaveDeviceConnectionExpiryCov4th(t *testing.T) {
	repo := repoCov4th(t)
	handler := NewOAuthHandler(repo)
	empty := handler.saveDeviceConnection("github", deviceTokens{})
	if empty.id != "" {
		t.Fatalf("empty token stored as %s", empty.id)
	}
	saved := handler.saveDeviceConnection("github", deviceTokens{
		access: "gh-direct-cov4th", refresh: "rt", email: "direct@example.test",
		name: "Named", expiresIn: 30, extra: map[string]any{"copilotToken": "copilot-cov4th"},
	})
	if saved.email != "direct@example.test" || saved.id == "" {
		t.Fatalf("saved = %+v", saved)
	}
	again := handler.saveDeviceConnection("github", deviceTokens{access: "gh-direct-cov4th", expiresIn: 30})
	if again.id != saved.id {
		t.Fatalf("update id = %s, want %s", again.id, saved.id)
	}
	if countConnsCov4th(t, repo) != 1 {
		t.Fatal("expiry save did not upsert one row")
	}
}

func TestKiroProfileArnEdgesCov4th(t *testing.T) {
	cases := []struct {
		name string
		fn   devicePollFixture
	}{
		{"transport", func(*http.Request) (*http.Response, error) { return nil, errors.New("down") }},
		{"bad json", func(*http.Request) (*http.Response, error) {
			return jsonBodyCov4th(http.StatusOK, "nope"), nil
		}},
		{"blank arn", func(*http.Request) (*http.Response, error) {
			return jsonBodyCov4th(http.StatusOK, `{"profiles":[{"arn":"  "}]}`), nil
		}},
		{"read error", func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: errBodyCov4th{}, Request: r}, nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			installUpstreamCov4th(t, tc.fn)
			if got := kiroProfileArn(context.Background(), "tok-cov4th"); got != "" {
				t.Fatalf("arn = %q", got)
			}
		})
	}
}

func TestRedirectHeadersCov4th(t *testing.T) {
	handler := NewOAuthHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/oauth/windsurf/authorize", nil)
	req.Host = ""
	req.Header.Set("X-Forwarded-Host", " dash.example.test , ignored")
	req.Header.Set("X-Forwarded-Proto", "HTTPS, http")
	rec := httptest.NewRecorder()
	handler.HandleWindsurfAuthorize(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "https://dash.example.test/callback") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/oauth/windsurf/authorize", nil)
	req.Host = ""
	req.Header.Set("X-Forwarded-Host", " , ")
	req.Header.Set("X-Forwarded-Proto", "ftp")
	rec = httptest.NewRecorder()
	handler.HandleWindsurfAuthorize(rec, req)
	if !strings.Contains(rec.Body.String(), "http://localhost:8080/callback") {
		t.Fatalf("body %s", rec.Body.String())
	}
}
