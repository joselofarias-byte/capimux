package executor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/providers"
	"9router/proxy/internal/proxy"
)

const chatBody = `{"model":"demo","messages":[{"role":"user","content":"hi"}]}`
const okCompletion = `{"id":"chatcmpl-test","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
const codexSSE = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: [DONE]\n\n"

func execReq(base string, client *http.Client, body string, stream bool) *Request {
	return &Request{
		Ctx:      context.Background(),
		Client:   client,
		Config:   &providers.ProviderConfig{BaseURL: base, AuthHeader: "Authorization", AuthScheme: "bearer"},
		APIKey:   "sk-test",
		Body:     []byte(body),
		IsStream: stream,
	}
}

func TestExecutorRegistryOneLiners(t *testing.T) {
	if Default() == nil {
		t.Fatal("default executor")
	}
	if !IsGeminiNative(&providers.ProviderConfig{Format: "gemini-native"}) || IsGeminiNative(&providers.ProviderConfig{}) {
		t.Fatal("IsGeminiNative")
	}
}

func TestForwardIflowUpstreamFailuresAndSuccess(t *testing.T) {
	var hits int
	var sawStream, sawSig bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("x-iflow-signature") != "" && r.Header.Get("session-id") != "" {
			sawSig = true
		}
		if r.Header.Get("Accept") == "text/event-stream" {
			sawStream = true
		}
		switch hits {
		case 1:
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"down"}`))
		case 2:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`not-json`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(okCompletion))
		}
	}))
	defer srv.Close()

	rec := httptest.NewRecorder()
	err := ForwardIflow(rec, execReq(srv.URL, srv.Client(), chatBody, false))
	var ue *proxy.UpstreamError
	if !errors.As(err, &ue) || ue.StatusCode != http.StatusBadGateway {
		t.Fatalf("502 err=%v", err)
	}
	rec = httptest.NewRecorder()
	err = ForwardIflow(rec, execReq(srv.URL, srv.Client(), chatBody, false))
	if !errors.As(err, &ue) || ue.StatusCode != http.StatusBadGateway {
		t.Fatalf("invalid 200 err=%v", err)
	}
	rec = httptest.NewRecorder()
	if err := ForwardIflow(rec, execReq(srv.URL, srv.Client(), chatBody, true)); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !sawSig || !sawStream {
		t.Fatalf("sig=%v stream=%v", sawSig, sawStream)
	}

	if err := ForwardIflow(httptest.NewRecorder(), execReq(srv.URL, srv.Client(), "{", false)); err == nil || hits != 3 {
		t.Fatalf("invalid body err=%v hits=%d", err, hits)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancel()
	req := execReq(srv.URL, srv.Client(), chatBody, false)
	req.Ctx = ctx
	if err := ForwardIflow(httptest.NewRecorder(), req); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout err=%v", err)
	}
	req = execReq(srv.URL, &http.Client{Transport: errTransport("connection reset by peer")}, chatBody, false)
	if err := ForwardIflow(httptest.NewRecorder(), req); err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("cut err=%v", err)
	}
}

func TestForwardKimchiStripsAndFailsOver(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		if got == nil {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"down"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okCompletion))
	}))
	defer srv.Close()

	body := `{"model":"demo","system":"be brief","anthropic_version":"2023-06-01","thinking":{"type":"enabled"},"messages":[{"role":"user","content":"hi","reasoning_content":"secret"}]}`
	rec := httptest.NewRecorder()
	if err := ForwardKimchi(rec, execReq(srv.URL, srv.Client(), body, false)); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["anthropic_version"]; ok {
		t.Fatalf("anthropic field leaked: %v", got)
	}
	if _, ok := got["thinking"]; ok || got["system"] != nil {
		t.Fatalf("system/thinking leaked: %v", got)
	}
	msgs, _ := got["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatalf("messages=%v", got["messages"])
	}

	if err := ForwardKimchi(httptest.NewRecorder(), execReq(srv.URL, srv.Client(), "{", false)); err == nil {
		t.Fatal("invalid json forwarded")
	}
	cut := execReq(srv.URL, &http.Client{Transport: errTransport("connection reset by peer")}, body, false)
	if err := ForwardKimchi(httptest.NewRecorder(), cut); err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("cut err=%v", err)
	}
}

func TestForwardAzureStatusTimeoutAndCut(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if !strings.Contains(r.URL.Path, "/openai/deployments/dep/chat/completions") || r.URL.Query().Get("api-version") != "2024-test" {
			t.Errorf("url=%s", r.URL)
		}
		if r.Header.Get("api-key") != "sk-test" {
			t.Errorf("api-key=%q", r.Header.Get("api-key"))
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"azure"}`))
	}))
	defer srv.Close()
	t.Setenv("AZURE_ENDPOINT", srv.URL)
	t.Setenv("AZURE_DEPLOYMENT", "dep")
	t.Setenv("AZURE_API_VERSION", "2024-test")

	err := ForwardAzure(httptest.NewRecorder(), execReq(srv.URL, srv.Client(), chatBody, false))
	var ue *proxy.UpstreamError
	if !errors.As(err, &ue) || ue.StatusCode != http.StatusInternalServerError || !strings.Contains(string(ue.Body), "azure") {
		t.Fatalf("err=%v", err)
	}
	req := execReq(srv.URL, srv.Client(), chatBody, false)
	req.Ctx = expired()
	if err := ForwardAzure(httptest.NewRecorder(), req); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout err=%v", err)
	}
	req = execReq("http://127.0.0.1:1", &http.Client{Transport: errBodyTransport(http.StatusBadGateway)}, chatBody, false)
	err = ForwardAzure(httptest.NewRecorder(), req)
	if !errors.As(err, &ue) || string(ue.Body) != "failed to read error body" {
		t.Fatalf("read err=%v", err)
	}
	if hits != 1 {
		t.Fatalf("hits=%d", hits)
	}
}

func TestForwardMuseAndGrokFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/responses") {
			t.Errorf("path=%s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(codexSSE))
	}))
	defer srv.Close()

	if err := ForwardMuse(httptest.NewRecorder(), execReq(srv.URL, srv.Client(), "{", false)); err == nil {
		t.Fatal("muse accepted invalid json")
	}
	if err := ForwardMuse(httptest.NewRecorder(), execReq(srv.URL, srv.Client(), `{"messages":[]}`, false)); err == nil || !strings.Contains(err.Error(), "missing model") {
		t.Fatalf("missing model err=%v", err)
	}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"muse down"}`))
	}))
	defer down.Close()
	err := ForwardMuse(httptest.NewRecorder(), execReq(down.URL, down.Client(), `{"model":"muse/spark","messages":[{"role":"user","content":"hi"}]}`, false))
	var ue *proxy.UpstreamError
	if !errors.As(err, &ue) || ue.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("503 err=%v", err)
	}
	rec := httptest.NewRecorder()
	if err := ForwardMuse(rec, execReq(srv.URL+"/chat/completions", srv.Client(), `{"model":"muse/spark","messages":[{"role":"user","content":"hi"}]}`, false)); err != nil {
		t.Fatalf("success err=%v body=%s", err, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hello") {
		t.Fatalf("body=%s", rec.Body.String())
	}

	if err := ForwardGrokCLI(httptest.NewRecorder(), execReq(srv.URL, srv.Client(), "{", false)); err == nil {
		t.Fatal("grok accepted invalid json")
	}
	err = ForwardGrokCLI(httptest.NewRecorder(), execReq(down.URL, down.Client(), chatBody, false))
	if !errors.As(err, &ue) || ue.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("grok 503 err=%v", err)
	}
	req := execReq(srv.URL, &http.Client{Transport: errTransport("connection reset by peer")}, chatBody, false)
	if err := ForwardGrokCLI(httptest.NewRecorder(), req); err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("grok cut err=%v", err)
	}
}

func TestForwardGeminiExecutorUpstream(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"gemini down"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}]}`))
	}))
	defer srv.Close()
	req := execReq(srv.URL, srv.Client(), chatBody, false)
	req.ModelName = "gemini-2.0-flash"
	err := ForwardGemini(httptest.NewRecorder(), req)
	var ue *proxy.UpstreamError
	if !errors.As(err, &ue) || ue.StatusCode != http.StatusBadGateway {
		t.Fatalf("502 err=%v", err)
	}
	rec := httptest.NewRecorder()
	req = execReq(srv.URL, srv.Client(), chatBody, false)
	req.ModelName = "gemini-2.0-flash"
	if err := ForwardGemini(rec, req); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Body.String(), "hi") {
		t.Fatalf("body=%s", rec.Body.String())
	}
	req = execReq(srv.URL, srv.Client(), chatBody, false)
	req.Ctx = expired()
	req.ModelName = "gemini-2.0-flash"
	if err := ForwardGemini(httptest.NewRecorder(), req); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout err=%v", err)
	}
	req = execReq(srv.URL, &http.Client{Transport: errTransport("connection reset by peer")}, chatBody, false)
	req.ModelName = "gemini-2.0-flash"
	if err := ForwardGemini(httptest.NewRecorder(), req); err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("cut err=%v", err)
	}
}

func TestForwardCommandcodeErrorCutAndInvalid(t *testing.T) {
	if err := ForwardCommandcode(httptest.NewRecorder(), execReq("http://127.0.0.1:1", &http.Client{Transport: errTransport("connection reset by peer")}, "{", false)); err == nil {
		t.Fatal("invalid json")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"cc"}`))
	}))
	defer srv.Close()
	err := ForwardCommandcode(httptest.NewRecorder(), execReq(srv.URL, srv.Client(), chatBody, false))
	var ue *proxy.UpstreamError
	if !errors.As(err, &ue) || ue.StatusCode != http.StatusBadGateway {
		t.Fatalf("502 err=%v", err)
	}
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{\"type\":\"error\",\"error\":{\"message\":\"nope\",\"type\":\"server_error\"}}\n"))
	}))
	defer errSrv.Close()
	err = ForwardCommandcode(httptest.NewRecorder(), execReq(errSrv.URL, errSrv.Client(), chatBody, false))
	if !errors.As(err, &ue) {
		t.Fatalf("event error=%v", err)
	}
	req := execReq(srv.URL, &http.Client{Transport: errTransport("connection reset by peer")}, chatBody, false)
	if err := ForwardCommandcode(httptest.NewRecorder(), req); err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("cut err=%v", err)
	}
}

type errRoundTrip struct{ msg string }

func (e errRoundTrip) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New(e.msg)
}

func errTransport(msg string) http.RoundTripper { return errRoundTrip{msg: msg} }

type errBodyRoundTrip struct{ status int }

func (e errBodyRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: e.status,
		Header:     make(http.Header),
		Body:       io.NopCloser(errReader{}),
		Request:    r,
	}, nil
}

func errBodyTransport(status int) http.RoundTripper { return errBodyRoundTrip{status: status} }

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func expired() context.Context {
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
	cancel()
	return ctx
}
