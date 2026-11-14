package mitm

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestInterceptTLS_ForwardsEveryHandledHost(t *testing.T) {
	rig := newRig(t)
	cap := &routerCapture{}
	startRouter(t, cap.serve(http.StatusOK, "application/json", `{"ok":true}`))

	tests := []struct {
		name      string
		sni       string
		path      string
		body      string
		wantPath  string
		wantModel string
		wantUA    string
	}{
		{
			name: "cursor", sni: "api.cursor.com", path: "/v1/chat/completions",
			body: `{"model":"gpt"}`, wantPath: "/v1/chat/completions", wantModel: "cursor/gpt", wantUA: "unit-test-agent",
		},
		{
			name: "cursor-api2", sni: "api2.cursor.com", path: "/v1/chat/completions",
			body: `{"model":"gpt"}`, wantPath: "/v1/chat/completions", wantModel: "cursor/gpt", wantUA: "unit-test-agent",
		},
		{
			name: "codex", sni: "chatgpt.com", path: "/v1/responses",
			body: `{"model":"gpt-5"}`, wantPath: "/v1/responses", wantModel: "codex/gpt-5", wantUA: "unit-test-agent",
		},
		{
			name: "codex-api", sni: "api.chatgpt.com", path: "/v1/responses",
			body: `{"model":"codex/already"}`, wantPath: "/v1/responses", wantModel: "codex/already", wantUA: "unit-test-agent",
		},
		{
			name: "copilot", sni: "api.githubcopilot.com", path: "/v1/chat/completions",
			body: `{"model":"gpt"}`, wantPath: "/v1/chat/completions", wantModel: "github/gpt", wantUA: "unit-test-agent",
		},
		{
			name: "kiro", sni: "runtime.us-east-1.kiro.dev", path: "/v1/chat/completions",
			body: `{"model":"gpt"}`, wantPath: "/v1/chat/completions", wantModel: "kiro/gpt", wantUA: "unit-test-agent",
		},
		{
			name: "antigravity-catalog", sni: "cloudcode-pa.googleapis.com", path: "/v1internal:fetchAvailableModels",
			body: `{"model":"gemini","metadata":{"ideVersion":"2.11.0"}}`, wantPath: "/v1/chat/completions", wantModel: "antigravity/gemini", wantUA: "unit-test-agent",
		},
		{
			name: "antigravity-daily-generate", sni: "daily-cloudcode-pa.googleapis.com", path: "/v1internal:streamGenerateContent?alt=sse",
			body: `{"model":"gemini","metadata":{"ideVersion":"2.11.0"}}`, wantPath: "/v1/chat/completions", wantModel: "antigravity/gemini", wantUA: "antigravity/1.23.2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, _, _, _, _ := cap.snapshot()
			status, _, respBody, err := rig.exchange(t, tt.sni, tt.sni, tt.path, []byte(tt.body), 5*time.Second)
			if err != nil {
				t.Fatalf("exchange: %v", err)
			}
			if status != http.StatusOK {
				t.Fatalf("status %d body %s", status, respBody)
			}
			hits, path, fwd, ua, auth := cap.snapshot()
			if hits != before+1 {
				t.Fatalf("upstream hits = %d, want %d", hits, before+1)
			}
			if path != tt.wantPath {
				t.Fatalf("upstream path %q, want %q", path, tt.wantPath)
			}
			if auth != "Bearer unit-test" {
				t.Fatalf("upstream auth %q", auth)
			}
			if ua != tt.wantUA {
				t.Fatalf("upstream UA %q, want %q", ua, tt.wantUA)
			}
			var parsed map[string]any
			if err := json.Unmarshal(fwd, &parsed); err != nil {
				t.Fatalf("upstream json: %v body %s", err, fwd)
			}
			if parsed["model"] != tt.wantModel {
				t.Fatalf("model %v, want %s", parsed["model"], tt.wantModel)
			}
			if tt.name == "antigravity-daily-generate" || tt.name == "antigravity-catalog" {
				meta, _ := parsed["metadata"].(map[string]any)
				wantVersion := "2.11.0"
				if tt.name == "antigravity-daily-generate" {
					wantVersion = "1.23.2"
				}
				if meta["ideVersion"] != wantVersion {
					t.Fatalf("ideVersion %v, want %s", meta["ideVersion"], wantVersion)
				}
			}
			if !bytes.Contains(respBody, []byte(`{"ok":true}`)) {
				t.Fatalf("client body %q", respBody)
			}
		})
	}
}

func TestInterceptTLS_LeafCertIsCachedAndChainedToTempCA(t *testing.T) {
	rig := newRig(t)
	startRouter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	first, err := rig.dial(t, "api.cursor.com", 5*time.Second)
	if err != nil {
		t.Fatalf("first handshake: %v", err)
	}
	state := first.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		t.Fatal("no peer certificate")
	}
	leaf := state.PeerCertificates[0]
	if err := leaf.CheckSignatureFrom(rig.ca); err != nil {
		t.Fatalf("leaf not signed by temp CA: %v", err)
	}
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "api.cursor.com" {
		t.Fatalf("DNSNames %v", leaf.DNSNames)
	}
	first.Close()

	certPath := filepath.Join(CertDir(rig.dir), "api.cursor.com.pem")
	rel, err := filepath.Rel(rig.dir, certPath)
	if err != nil || len(rel) >= 2 && rel[0] == '.' && rel[1] == '.' {
		t.Fatalf("leaf cert escaped temp dir: %s", certPath)
	}

	second, err := rig.dial(t, "api.cursor.com", 5*time.Second)
	if err != nil {
		t.Fatalf("second handshake: %v", err)
	}
	defer second.Close()
	again := second.ConnectionState().PeerCertificates[0]
	if !leaf.Equal(again) {
		t.Fatal("second handshake minted a new leaf instead of using the cache")
	}
}

func TestInterceptTLS_StreamsSSE(t *testing.T) {
	rig := newRig(t)
	startRouter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: one\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = w.Write([]byte("data: two\n\n"))
	}))

	status, hdr, body, err := rig.exchange(t, "api.cursor.com", "api.cursor.com", "/v1/chat/completions", []byte(`{"model":"m"}`), 5*time.Second)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if hdr.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content-type %q", hdr.Get("Content-Type"))
	}
	if !bytes.Contains(body, []byte("data: one\n\n")) || !bytes.Contains(body, []byte("data: two\n\n")) {
		t.Fatalf("body %q", body)
	}
}

func TestInterceptTLS_ForwardsUpstream5xx(t *testing.T) {
	rig := newRig(t)
	startRouter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"unavailable"}`))
	}))

	status, _, body, err := rig.exchange(t, "api.githubcopilot.com", "api.githubcopilot.com", "/v1/chat/completions", []byte(`{"model":"m"}`), 5*time.Second)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status %d body %s", status, body)
	}
	if !bytes.Contains(body, []byte(`"unavailable"`)) {
		t.Fatalf("body %s", body)
	}
}

func TestInterceptTLS_InvalidJSONIs400(t *testing.T) {
	rig := newRig(t)
	cap := &routerCapture{}
	startRouter(t, cap.serve(http.StatusOK, "application/json", `{"ok":true}`))

	status, _, body, err := rig.exchange(t, "chatgpt.com", "chatgpt.com", "/v1/responses", []byte("not-json"), 5*time.Second)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if status != http.StatusBadRequest {
		t.Fatalf("status %d body %s", status, body)
	}
	if !bytes.Contains(body, []byte(`"type":"mitm_error"`)) || !bytes.Contains(body, []byte("invalid Codex request body")) {
		t.Fatalf("body %s", body)
	}
	if hits, _, _, _, _ := cap.snapshot(); hits != 0 {
		t.Fatalf("invalid JSON was forwarded, hits=%d", hits)
	}
}

func TestInterceptTLS_HTTP10UsesSNIWhenHostMissing(t *testing.T) {
	rig := newRig(t)
	cap := &routerCapture{}
	startRouter(t, cap.serve(http.StatusOK, "application/json", `{"ok":true}`))

	raw := []byte("POST /v1/chat/completions HTTP/1.0\r\nContent-Length: 13\r\nContent-Type: application/json\r\n\r\n{\"model\":\"m\"}")
	status, body, err := rig.exchangeRaw(t, "api.cursor.com", raw, 5*time.Second)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status %d body %s", status, body)
	}
	hits, path, fwd, _, _ := cap.snapshot()
	if hits != 1 || path != "/v1/chat/completions" {
		t.Fatalf("hits=%d path=%s", hits, path)
	}
	var parsed map[string]any
	if err := json.Unmarshal(fwd, &parsed); err != nil {
		t.Fatalf("forwarded json: %v", err)
	}
	if parsed["model"] != "cursor/m" {
		t.Fatalf("model %v", parsed["model"])
	}
}

func TestInterceptTLS_HostPortSuffixStillHandled(t *testing.T) {
	rig := newRig(t)
	cap := &routerCapture{}
	startRouter(t, cap.serve(http.StatusOK, "application/json", `{"ok":true}`))

	status, _, body, err := rig.exchange(t, "api.cursor.com", "API.CURSOR.COM:443", "/v1/chat/completions", []byte(`{"model":"m"}`), 5*time.Second)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status %d body %s", status, body)
	}
	if hits, _, _, _, _ := cap.snapshot(); hits != 1 {
		t.Fatalf("hits=%d", hits)
	}
}
