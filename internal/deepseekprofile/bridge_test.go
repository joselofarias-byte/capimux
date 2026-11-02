package deepseekprofile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Every test talks to httptest servers on 127.0.0.1. No real DSH or InferX
// endpoint is ever contacted.

const testSecret = "sk-test-inferx-NOT-REAL-7d1e5a" // fake value, test only

type fakeUpstream struct {
	srv    *httptest.Server
	hits   atomic.Int64
	mu     sync.Mutex
	bodies [][]byte
	auth   []string
}

func newFake(t *testing.T, h func(w http.ResponseWriter, r *http.Request, body []byte)) *fakeUpstream {
	t.Helper()
	f := &fakeUpstream{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.mu.Unlock()
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "bad path "+r.URL.Path, http.StatusNotFound)
			return
		}
		h(w, r, body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeUpstream) url() string { return f.srv.URL + "/v1" }

func (f *fakeUpstream) lastBody(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bodies) == 0 {
		t.Fatal("upstream received no request")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(f.bodies[len(f.bodies)-1], &m); err != nil {
		t.Fatalf("upstream body not JSON: %v", err)
	}
	return m
}

type logSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logSink) logf(level, msg string, kv ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(&l.buf, "%s %s %v\n", level, msg, kv)
}

func (l *logSink) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func mapEnv(m map[string]string) Env {
	return Env{
		Getenv: func(k string) string { return m[k] },
		ReadFile: func(p string) ([]byte, error) {
			if v, ok := m["file:"+p]; ok {
				return []byte(v), nil
			}
			return nil, fmt.Errorf("open %s: no such file", p)
		},
	}
}

func baseEnv(dshURL, infURL string) map[string]string {
	return map[string]string{
		EnvEnabled:           "1",
		EnvDSHURL:            dshURL,
		EnvInferXURL:         infURL,
		EnvInferXAPIKey:      testSecret,
		EnvDSHTimeout:        "2s",
		EnvInferXTimeout:     "2s",
		EnvStreamIdleTimeout: "2s",
		EnvConnectTimeout:    "1s",
	}
}

func newBridge(t *testing.T, env map[string]string) (*Bridge, *logSink) {
	t.Helper()
	cfg, err := Load(mapEnv(env))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sink := &logSink{}
	return NewBridge(cfg, sink.logf), sink
}

func serve(t *testing.T, b *Bridge, body string) (*httptest.ResponseRecorder, Outcome) {
	t.Helper()
	rec := httptest.NewRecorder()
	out := b.Serve(context.Background(), rec, []byte(body))
	return rec, out
}

const textReply = `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hola desde %s"},"finish_reason":"stop"}],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`

func okText(name string, p, c int) func(http.ResponseWriter, *http.Request, []byte) {
	return func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, textReply, name, p, c, p+c)
	}
}

func status(code int) func(http.ResponseWriter, *http.Request, []byte) {
	return func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		fmt.Fprintf(w, `{"error":{"message":"upstream %d"}}`, code)
	}
}

const simpleReq = `{"model":"deepseek-profile","messages":[{"role":"user","content":"hola"}]}`

func TestText_PrimaryAnswersAndSecondaryIsUntouched(t *testing.T) {
	dsh := newFake(t, okText("dsh", 0, 0))
	inf := newFake(t, okText("inferx", 5, 3))
	b, _ := newBridge(t, baseEnv(dsh.url(), inf.url()))

	rec, out := serve(t, b, simpleReq)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "hola desde dsh") {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if inf.hits.Load() != 0 {
		t.Fatalf("InferX was called %d times on a DSH success", inf.hits.Load())
	}
	if got := string(dsh.lastBody(t)["model"]); got != `"deepseek-chat"` {
		t.Fatalf("DSH model = %s, want deepseek-chat", got)
	}
	if dsh.auth[0] != "" {
		t.Fatal("DSH received an Authorization header although none is configured")
	}
	if out.Provider != ProviderDSH || out.Err != nil || out.UsageKnown() {
		t.Fatalf("outcome = %+v; DSH zero usage must be unknown", out)
	}
}

func TestToolCalling_PreservesToolsAndToolChoiceAndDoesNotExecute(t *testing.T) {
	tools := `[{"type":"function","function":{"name":"get_weather","description":"clima","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}]`
	choice := `{"type":"function","function":{"name":"get_weather"}}`
	req := `{"model":"deepseek-profile","messages":[{"role":"user","content":"clima en Montevideo"}],"tools":` + tools + `,"tool_choice":` + choice + `,"parallel_tool_calls":false,"temperature":0.2}`
	reply := `{"id":"t","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Montevideo\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":9,"total_tokens":29}}`
	dsh := newFake(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, reply)
	})
	inf := newFake(t, okText("inferx", 1, 1))
	b, _ := newBridge(t, baseEnv(dsh.url(), inf.url()))

	rec, out := serve(t, b, req)
	if rec.Code != 200 || out.Err != nil {
		t.Fatalf("got %d %s err=%v", rec.Code, rec.Body.String(), out.Err)
	}
	if rec.Body.String() != reply {
		t.Fatalf("tool call response was altered:\n%s", rec.Body.String())
	}
	got := dsh.lastBody(t)
	if !jsonEqual(t, got["tools"], tools) || !jsonEqual(t, got["tool_choice"], choice) {
		t.Fatalf("tools/tool_choice not preserved: %s / %s", got["tools"], got["tool_choice"])
	}
	if string(got["parallel_tool_calls"]) != "false" || string(got["temperature"]) != "0.2" {
		t.Fatalf("other fields not preserved: %v", got)
	}
	// The bridge must not run the tool nor send a follow-up turn.
	if dsh.hits.Load() != 1 || inf.hits.Load() != 0 {
		t.Fatalf("hits dsh=%d inferx=%d, want 1/0", dsh.hits.Load(), inf.hits.Load())
	}
}

func jsonEqual(t *testing.T, raw json.RawMessage, want string) bool {
	t.Helper()
	var a, b any
	if json.Unmarshal(raw, &a) != nil || json.Unmarshal([]byte(want), &b) != nil {
		return false
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return bytes.Equal(ja, jb)
}

func sseWriter(events ...string) func(http.ResponseWriter, *http.Request, []byte) {
	return func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, e := range events {
			fmt.Fprintf(w, "data: %s\n\n", e)
			fl.Flush()
		}
	}
}

func TestStreaming_TextIsForwardedChunkByChunk(t *testing.T) {
	dsh := newFake(t, sseWriter(
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"Ho"}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"la"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
		`[DONE]`))
	inf := newFake(t, okText("inferx", 1, 1))
	b, _ := newBridge(t, baseEnv(dsh.url(), inf.url()))

	rec, out := serve(t, b, `{"model":"deepseek-profile","stream":true,"messages":[{"role":"user","content":"hola"}]}`)
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	body := rec.Body.String()
	for _, want := range []string{`"content":"Ho"`, `"content":"la"`, "data: [DONE]"} {
		if !strings.Contains(body, want) {
			t.Fatalf("stream lacks %q:\n%s", want, body)
		}
	}
	if out.Err != nil || out.ResponseText != "Hola" || out.UsageKnown() || inf.hits.Load() != 0 {
		t.Fatalf("outcome %+v inferx=%d", out, inf.hits.Load())
	}
}

func TestStreaming_DeclaredToolCallSplitAcrossChunksPasses(t *testing.T) {
	dsh := newFake(t, sseWriter(
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"get_","arguments":""}}]}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"weather","arguments":"{\"city\":\"MVD\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`))
	b, _ := newBridge(t, baseEnv(dsh.url(), newFake(t, okText("i", 1, 1)).url()))
	rec, out := serve(t, b, `{"model":"deepseek-profile","stream":true,"messages":[],"tools":[{"type":"function","function":{"name":"get_weather"}}]}`)
	if out.Err != nil || !strings.Contains(rec.Body.String(), "[DONE]") {
		t.Fatalf("declared streamed tool call rejected: %v\n%s", out.Err, rec.Body.String())
	}
}

// --- Fallback policy -------------------------------------------------------

func TestFallback_OnRetryableStatusesBeforeStart(t *testing.T) {
	for _, code := range []int{429, 502, 503, 504} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			dsh := newFake(t, status(code))
			inf := newFake(t, okText("inferx", 11, 7))
			b, logs := newBridge(t, baseEnv(dsh.url(), inf.url()))
			rec, out := serve(t, b, simpleReq)
			if rec.Code != 200 || !strings.Contains(rec.Body.String(), "hola desde inferx") {
				t.Fatalf("got %d %s", rec.Code, rec.Body.String())
			}
			if dsh.hits.Load() != 1 || inf.hits.Load() != 1 {
				t.Fatalf("hits dsh=%d inferx=%d, want 1/1", dsh.hits.Load(), inf.hits.Load())
			}
			if inf.auth[0] != "Bearer "+testSecret {
				t.Fatal("InferX did not receive the configured Bearer secret")
			}
			if got := string(inf.lastBody(t)["model"]); got != `"`+DefaultInferXModel+`"` {
				t.Fatalf("InferX model = %s", got)
			}
			if out.Provider != ProviderInferX || !out.UsageKnown() || len(out.Attempts) != 2 {
				t.Fatalf("outcome %+v", out)
			}
			if strings.Contains(logs.String(), testSecret) {
				t.Fatal("secret leaked into logs")
			}
		})
	}
}

func TestFallback_OnConnectionError(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL + "/v1"
	dead.Close()
	inf := newFake(t, okText("inferx", 2, 2))
	b, _ := newBridge(t, baseEnv(deadURL, inf.url()))
	rec, out := serve(t, b, simpleReq)
	if rec.Code != 200 || inf.hits.Load() != 1 || out.Attempts[0].Kind != FailConnection {
		t.Fatalf("got %d attempts=%+v", rec.Code, out.Attempts)
	}
}

func TestFallback_OnTimeoutBeforeHeaders(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	dsh := newFake(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	inf := newFake(t, okText("inferx", 2, 2))
	env := baseEnv(dsh.url(), inf.url())
	env[EnvDSHTimeout] = "150ms"
	b, _ := newBridge(t, env)
	start := time.Now()
	rec, out := serve(t, b, simpleReq)
	if rec.Code != 200 || inf.hits.Load() != 1 || out.Attempts[0].Kind != FailTimeout {
		t.Fatalf("got %d attempts=%+v", rec.Code, out.Attempts)
	}
	if time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("timeout not honoured: %v", time.Since(start))
	}
}

func TestFallback_OnTimeoutAfterHeadersBeforeFirstByte(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	dsh := newFake(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	inf := newFake(t, sseWriter(`{"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`, `[DONE]`))
	env := baseEnv(dsh.url(), inf.url())
	env[EnvDSHTimeout] = "150ms"
	b, _ := newBridge(t, env)
	rec, out := serve(t, b, `{"model":"deepseek-profile","stream":true,"messages":[]}`)
	if rec.Code != 200 || inf.hits.Load() != 1 || out.Attempts[0].Kind != FailTimeout || !strings.Contains(rec.Body.String(), `"content":"ok"`) {
		t.Fatalf("got %d attempts=%+v body=%s", rec.Code, out.Attempts, rec.Body.String())
	}
}

func TestNoFallback_OnPermanentStatuses(t *testing.T) {
	// 400/401/403 are the required exclusions; 404/422/500 pin that only the
	// listed statuses fail over.
	for _, code := range []int{400, 401, 403, 404, 422, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			dsh := newFake(t, status(code))
			inf := newFake(t, okText("inferx", 1, 1))
			b, _ := newBridge(t, baseEnv(dsh.url(), inf.url()))
			rec, out := serve(t, b, simpleReq)
			if rec.Code != code {
				t.Fatalf("status = %d, want %d passed through", rec.Code, code)
			}
			if inf.hits.Load() != 0 {
				t.Fatalf("HTTP %d failed over to InferX", code)
			}
			if out.Err == nil || len(out.Attempts) != 1 {
				t.Fatalf("outcome %+v", out)
			}
		})
	}
}

func TestNoFallback_WhenSecondaryAlsoFails_ReturnsLastError(t *testing.T) {
	dsh := newFake(t, status(503))
	inf := newFake(t, status(429))
	b, _ := newBridge(t, baseEnv(dsh.url(), inf.url()))
	rec, out := serve(t, b, simpleReq)
	if rec.Code != 429 || len(out.Attempts) != 2 || dsh.hits.Load() != 1 || inf.hits.Load() != 1 {
		t.Fatalf("got %d attempts=%+v", rec.Code, out.Attempts)
	}
}

func TestFallbackTable(t *testing.T) {
	yes := map[int]bool{429: true, 502: true, 503: true, 504: true}
	for code := 100; code < 600; code++ {
		if Fallback(FailHTTP, code) != yes[code] {
			t.Fatalf("Fallback(HTTP %d) = %v", code, !yes[code])
		}
	}
	for _, k := range []FailureKind{FailToolBlocked, FailCut, FailInvalid, FailClientGone} {
		if Fallback(k, 503) {
			t.Fatalf("Fallback(%s) must be false", k)
		}
	}
	if !Fallback(FailConnection, 0) || !Fallback(FailTimeout, 0) {
		t.Fatal("connection errors and timeouts must fail over")
	}
}

// --- No retry after the response started ------------------------------------

func hijackClose(w http.ResponseWriter) {
	conn, _, err := w.(http.Hijacker).Hijack()
	if err == nil {
		conn.Close()
	}
}

func TestMidStreamCut_IsNotRetried(t *testing.T) {
	dsh := newFake(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"parcial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		hijackClose(w)
	})
	inf := newFake(t, okText("inferx", 1, 1))
	b, _ := newBridge(t, baseEnv(dsh.url(), inf.url()))
	rec, out := serve(t, b, `{"model":"deepseek-profile","stream":true,"messages":[]}`)
	body := rec.Body.String()
	if !strings.Contains(body, "parcial") || !strings.Contains(body, "upstream_cut") || strings.Contains(body, "[DONE]") {
		t.Fatalf("stream body:\n%s", body)
	}
	if inf.hits.Load() != 0 || !IsCut(out.Err) {
		t.Fatalf("cut stream retried (inferx=%d) or not flagged: %v", inf.hits.Load(), out.Err)
	}
}

func TestMidStreamCut_ToolCallStartedIsNotRetried(t *testing.T) {
	dsh := newFake(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"{\\\"ci\"}}]}}]}\n\n")
		w.(http.Flusher).Flush()
		hijackClose(w)
	})
	inf := newFake(t, okText("inferx", 1, 1))
	b, _ := newBridge(t, baseEnv(dsh.url(), inf.url()))
	_, out := serve(t, b, `{"model":"deepseek-profile","stream":true,"messages":[],"tools":[{"type":"function","function":{"name":"get_weather"}}]}`)
	if inf.hits.Load() != 0 || !IsCut(out.Err) {
		t.Fatalf("partial tool call retried (inferx=%d): %v", inf.hits.Load(), out.Err)
	}
}

func TestMidStreamIdleTimeout_IsNotRetried(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	dsh := newFake(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	inf := newFake(t, okText("inferx", 1, 1))
	env := baseEnv(dsh.url(), inf.url())
	env[EnvStreamIdleTimeout] = "150ms"
	b, _ := newBridge(t, env)
	rec, out := serve(t, b, `{"model":"deepseek-profile","stream":true,"messages":[]}`)
	if inf.hits.Load() != 0 || !IsCut(out.Err) || !strings.Contains(rec.Body.String(), "upstream_cut") {
		t.Fatalf("idle stream retried or not cut: inferx=%d err=%v", inf.hits.Load(), out.Err)
	}
}

func TestNonStreamCutAfterStart_IsNotRetried(t *testing.T) {
	dsh := newFake(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "500")
		io.WriteString(w, `{"choices":[{"message":{"content":"medio`)
		w.(http.Flusher).Flush()
		hijackClose(w)
	})
	inf := newFake(t, okText("inferx", 1, 1))
	b, _ := newBridge(t, baseEnv(dsh.url(), inf.url()))
	rec, out := serve(t, b, simpleReq)
	if rec.Code != 502 || inf.hits.Load() != 0 || !IsCut(out.Err) {
		t.Fatalf("got %d inferx=%d err=%v", rec.Code, inf.hits.Load(), out.Err)
	}
}

func TestClientCancelBeforeStart_DoesNotFallBack(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	dsh := newFake(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	inf := newFake(t, okText("inferx", 1, 1))
	b, _ := newBridge(t, baseEnv(dsh.url(), inf.url()))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	out := b.Serve(ctx, httptest.NewRecorder(), []byte(simpleReq))
	if inf.hits.Load() != 0 || out.Attempts[0].Kind != FailClientGone {
		t.Fatalf("client cancel failed over: %+v", out.Attempts)
	}
}

// --- Tool security -----------------------------------------------------------

func toolReply(name string) func(http.ResponseWriter, *http.Request, []byte) {
	return func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":%q,"arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, name)
	}
}

func TestSecurity_UndeclaredToolIsRejected(t *testing.T) {
	cases := map[string]string{
		"no tools declared":          `{"model":"deepseek-profile","messages":[]}`,
		"other tool declared":        `{"model":"deepseek-profile","messages":[],"tools":[{"type":"function","function":{"name":"get_weather"}}]}`,
		"tool_choice none":           `{"model":"deepseek-profile","messages":[],"tools":[{"type":"function","function":{"name":"run_shell"}}],"tool_choice":"none"}`,
		"tool_choice names another":  `{"model":"deepseek-profile","messages":[],"tools":[{"type":"function","function":{"name":"run_shell"}},{"type":"function","function":{"name":"get_weather"}}],"tool_choice":{"type":"function","function":{"name":"get_weather"}}}`,
		"declared under another key": `{"model":"deepseek-profile","messages":[],"metadata":{"tools":[{"function":{"name":"run_shell"}}]}}`,
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			dsh := newFake(t, toolReply("run_shell"))
			inf := newFake(t, okText("inferx", 1, 1))
			b, _ := newBridge(t, baseEnv(dsh.url(), inf.url()))
			rec, out := serve(t, b, req)
			if rec.Code != 502 || !strings.Contains(rec.Body.String(), "tool_not_declared") || strings.Contains(rec.Body.String(), `"arguments"`) {
				t.Fatalf("got %d %s", rec.Code, rec.Body.String())
			}
			if !IsToolBlocked(out.Err) || inf.hits.Load() != 0 || dsh.hits.Load() != 1 {
				t.Fatalf("err=%v dsh=%d inferx=%d", out.Err, dsh.hits.Load(), inf.hits.Load())
			}
		})
	}
}

func TestSecurity_DeclaredToolIsAllowed(t *testing.T) {
	dsh := newFake(t, toolReply("run_shell"))
	b, _ := newBridge(t, baseEnv(dsh.url(), newFake(t, okText("i", 1, 1)).url()))
	rec, out := serve(t, b, `{"model":"deepseek-profile","messages":[],"tools":[{"type":"function","function":{"name":"run_shell"}}],"tool_choice":"auto"}`)
	if rec.Code != 200 || out.Err != nil {
		t.Fatalf("declared tool rejected: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSecurity_UndeclaredToolInStreamIsNotForwarded(t *testing.T) {
	for name, events := range map[string][]string{
		"whole name": {
			`{"choices":[{"index":0,"delta":{"content":"pienso"}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c","type":"function","function":{"name":"run_shell","arguments":"{\"cmd\":\"rm\"}"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`, `[DONE]`,
		},
		"prefix of a declared name": {
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c","type":"function","function":{"name":"get","arguments":""}}]}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`, `[DONE]`,
		},
		"second call undeclared": {
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"a","type":"function","function":{"name":"get_weather","arguments":"{}"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"b","type":"function","function":{"name":"run_shell","arguments":"{}"}}]}}]}`, `[DONE]`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			dsh := newFake(t, sseWriter(events...))
			inf := newFake(t, okText("inferx", 1, 1))
			b, _ := newBridge(t, baseEnv(dsh.url(), inf.url()))
			rec, out := serve(t, b, `{"model":"deepseek-profile","stream":true,"messages":[],"tools":[{"type":"function","function":{"name":"get_weather"}}]}`)
			body := rec.Body.String()
			if strings.Contains(body, `\"cmd\"`) || strings.Contains(body, "[DONE]") || strings.Contains(body, `"finish_reason":"tool_calls"`) || !strings.Contains(body, "tool_not_declared") {
				t.Fatalf("undeclared tool call forwarded:\n%s", body)
			}
			if !IsToolBlocked(out.Err) || inf.hits.Load() != 0 {
				t.Fatalf("err=%v inferx=%d", out.Err, inf.hits.Load())
			}
		})
	}
}

func TestSecurity_MissingSecretDisablesSecondary(t *testing.T) {
	dsh := newFake(t, status(503))
	inf := newFake(t, okText("inferx", 1, 1))
	env := baseEnv(dsh.url(), inf.url())
	delete(env, EnvInferXAPIKey)
	cfg, err := Load(mapEnv(env))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Providers) != 1 || len(cfg.Warnings) != 1 {
		t.Fatalf("providers=%d warnings=%v", len(cfg.Providers), cfg.Warnings)
	}
	rec, out := serve(t, NewBridge(cfg, nil), simpleReq)
	if rec.Code != 503 || inf.hits.Load() != 0 || len(out.Attempts) != 1 {
		t.Fatalf("got %d inferx=%d", rec.Code, inf.hits.Load())
	}
}

func TestSecurity_SecretFromFileAndNeverInErrorsOrLogs(t *testing.T) {
	inf := newFake(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		// A hostile or buggy upstream echoing the credential back.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		fmt.Fprintf(w, `{"error":{"message":"bad token %s"}}`, r.Header.Get("Authorization"))
	})
	dsh := newFake(t, status(502))
	env := baseEnv(dsh.url(), inf.url())
	delete(env, EnvInferXAPIKey)
	env[EnvInferXAPIKeyFile] = "/run/secrets/inferx"
	env["file:/run/secrets/inferx"] = testSecret + "\n"
	b, logs := newBridge(t, env)
	rec, out := serve(t, b, simpleReq)
	if inf.auth[0] != "Bearer "+testSecret {
		t.Fatal("secret from file not used")
	}
	if rec.Code != 401 || strings.Contains(rec.Body.String(), testSecret) || !strings.Contains(rec.Body.String(), "[redactado]") {
		t.Fatalf("secret leaked to client: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(logs.String(), testSecret) || strings.Contains(fmt.Sprint(out.Err), testSecret) || strings.Contains(fmt.Sprintf("%+v", out), testSecret) {
		t.Fatal("secret leaked into logs or outcome")
	}
	// Formatting the config (a tempting debug line) must not print the key.
	if strings.Contains(fmt.Sprintf("%v %+v %#v", b.Config(), b.Config().Providers, b.Config().Providers), testSecret) {
		t.Fatal("formatting the config exposes the secret")
	}
	if !b.Config().Providers[1].HasAPIKey() {
		t.Fatal("HasAPIKey false")
	}
}

func TestSecurity_ConfigErrorsDoNotExposeSecrets(t *testing.T) {
	cases := map[string]func(map[string]string){
		"missing DSH URL":     func(m map[string]string) { delete(m, EnvDSHURL) },
		"InferX over http":    func(m map[string]string) { m[EnvInferXURL] = "http://model.example.net/v1" },
		"credentials in URL":  func(m map[string]string) { m[EnvInferXURL] = "https://user:" + testSecret + "@model.example.net/v1" },
		"query in URL":        func(m map[string]string) { m[EnvDSHURL] = "http://127.0.0.1:1/v1?key=" + testSecret },
		"unreadable key file": func(m map[string]string) { delete(m, EnvInferXAPIKey); m[EnvInferXAPIKeyFile] = "/nope/" + testSecret },
		"empty key file": func(m map[string]string) {
			delete(m, EnvInferXAPIKey)
			m[EnvInferXAPIKeyFile] = "/e"
			m["file:/e"] = " \n"
		},
		"bad timeout":          func(m map[string]string) { m[EnvInferXTimeout] = "pronto" },
		"non-positive timeout": func(m map[string]string) { m[EnvDSHTimeout] = "0s" },
		"unsupported URL kind": func(m map[string]string) { m[EnvDSHURL] = "ftp://127.0.0.1/v1" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			env := baseEnv("http://127.0.0.1:1/v1", "https://model.example.net/v1")
			mutate(env)
			_, err := Load(mapEnv(env))
			if err == nil {
				t.Fatal("expected a configuration error")
			}
			if strings.Contains(err.Error(), testSecret) {
				t.Fatalf("error exposes the secret: %v", err)
			}
		})
	}
}

func TestSecurity_RedirectIsNotFollowedWithSecret(t *testing.T) {
	var stolen atomic.Int64
	thief := newFake(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		if r.Header.Get("Authorization") != "" {
			stolen.Add(1)
		}
		w.WriteHeader(200)
	})
	inf := newFake(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		http.Redirect(w, r, thief.srv.URL+"/v1/chat/completions", http.StatusTemporaryRedirect)
	})
	dsh := newFake(t, status(503))
	b, _ := newBridge(t, baseEnv(dsh.url(), inf.url()))
	rec, _ := serve(t, b, simpleReq)
	if thief.hits.Load() != 0 || stolen.Load() != 0 || rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("redirect followed: code=%d thief=%d", rec.Code, thief.hits.Load())
	}
}

// --- Rollback / opt-in ---------------------------------------------------------

func TestRollback_DisabledByDefault(t *testing.T) {
	for _, v := range []string{"", "0", "false", "off", "no"} {
		env := baseEnv("http://127.0.0.1:1/v1", "https://model.example.net/v1")
		env[EnvEnabled] = v
		if Enabled(mapEnv(env)) {
			t.Fatalf("%q enabled the profile", v)
		}
		if _, err := Load(mapEnv(env)); err != ErrDisabled {
			t.Fatalf("%q: Load err = %v, want ErrDisabled", v, err)
		}
	}
}

func TestDefaults(t *testing.T) {
	env := map[string]string{EnvEnabled: "true", EnvDSHURL: "http://127.0.0.1:20232/v1/", EnvInferXAPIKey: testSecret}
	cfg, err := Load(mapEnv(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ModelID != DefaultModelID || len(cfg.Providers) != 2 {
		t.Fatalf("cfg %+v", cfg)
	}
	d, i := cfg.Providers[0], cfg.Providers[1]
	if d.Name != ProviderDSH || d.BaseURL != "http://127.0.0.1:20232/v1" || d.Model != DefaultDSHModel || d.Timeout != DefaultDSHTimeout {
		t.Fatalf("dsh %+v", d)
	}
	if i.Name != ProviderInferX || i.BaseURL != DefaultInferXURL || i.Model != DefaultInferXModel || i.Timeout != DefaultInferXTimeout {
		t.Fatalf("inferx %+v", i)
	}
	if cfg.StreamIdleTimeout != DefaultStreamIdleTimeout || cfg.ConnectTimeout != DefaultConnectTimeout {
		t.Fatalf("timeouts %+v", cfg)
	}
}

func TestUsageKnown(t *testing.T) {
	if (*Usage)(nil).Known() || (&Usage{}).Known() {
		t.Fatal("missing or zero usage must be unknown")
	}
	if !(&Usage{PromptTokens: 1}).Known() || !(&Usage{CompletionTokens: 1}).Known() {
		t.Fatal("non-zero usage must be known")
	}
}

func TestInvalidRequestBody(t *testing.T) {
	dsh := newFake(t, okText("dsh", 1, 1))
	b, _ := newBridge(t, baseEnv(dsh.url(), newFake(t, okText("i", 1, 1)).url()))
	rec, _ := serve(t, b, `{not json`)
	if rec.Code != 400 || dsh.hits.Load() != 0 {
		t.Fatalf("got %d dsh=%d", rec.Code, dsh.hits.Load())
	}
}

func TestFallback_OnEmpty2xxBodyBeforeStart(t *testing.T) {
	// 200 with no body at all: nothing was received, so it is a connection
	// failure before the response started.
	dsh := newFake(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { w.WriteHeader(200) })
	inf := newFake(t, okText("inferx", 1, 1))
	b, _ := newBridge(t, baseEnv(dsh.url(), inf.url()))
	rec, out := serve(t, b, simpleReq)
	if rec.Code != 200 || inf.hits.Load() != 1 || out.Attempts[0].Kind != FailConnection {
		t.Fatalf("got %d attempts=%+v", rec.Code, out.Attempts)
	}
}
