package deepseekprofile

import (
	"io"
	"net/http"
	"sync/atomic"
	"testing"
)

// The InferX fallback gate (panel toggle + connected DeepSeek Web session).
// Mocks only: httptest upstreams, an in-memory gate.

func TestGate_ClosedBlocksPaidFallback(t *testing.T) {
	for _, code := range []int{429, 502, 503, 504} {
		dsh := newFake(t, status(code))
		inf := newFake(t, okText("inferx", 1, 1))
		b, _ := newBridge(t, baseEnv(dsh.url(), inf.url()))
		b.SetFallbackGate(func() bool { return false })
		rec, out := serve(t, b, simpleReq)
		if inf.hits.Load() != 0 || rec.Code != code || out.Provider != ProviderDSH {
			t.Fatalf("HTTP %d with closed gate: status %d inferx=%d provider=%s", code, rec.Code, inf.hits.Load(), out.Provider)
		}
	}
}

func TestGate_ClosedBlocksFallbackOnConnectionError(t *testing.T) {
	inf := newFake(t, okText("inferx", 1, 1))
	b, _ := newBridge(t, baseEnv("http://127.0.0.1:1/v1", inf.url()))
	b.SetFallbackGate(func() bool { return false })
	rec, _ := serve(t, b, simpleReq)
	if inf.hits.Load() != 0 || rec.Code < 500 {
		t.Fatalf("closed gate: status %d inferx=%d", rec.Code, inf.hits.Load())
	}
}

func TestGate_OpenAllowsFallbackAndIsReadLive(t *testing.T) {
	dsh := newFake(t, status(503))
	inf := newFake(t, okText("inferx", 1, 1))
	b, _ := newBridge(t, baseEnv(dsh.url(), inf.url()))
	var open atomic.Bool
	var calls atomic.Int64
	b.SetFallbackGate(func() bool { calls.Add(1); return open.Load() })
	if _, out := serve(t, b, simpleReq); out.Provider != ProviderDSH || inf.hits.Load() != 0 {
		t.Fatal("fallback before the gate opened")
	}
	open.Store(true) // panel change, no restart
	if _, out := serve(t, b, simpleReq); out.Provider != ProviderInferX || inf.hits.Load() != 1 {
		t.Fatalf("open gate did not fall back: %s", out.Provider)
	}
	if calls.Load() != 2 {
		t.Fatalf("gate consulted %d times", calls.Load())
	}
}

func TestGate_NotConsultedOnSuccessOrPermanentErrors(t *testing.T) {
	inf := newFake(t, okText("inferx", 1, 1))
	for _, h := range []func(http.ResponseWriter, *http.Request, []byte){okText("dsh", 1, 1), status(401), status(400)} {
		dsh := newFake(t, h)
		b, _ := newBridge(t, baseEnv(dsh.url(), inf.url()))
		var calls atomic.Int64
		b.SetFallbackGate(func() bool { calls.Add(1); return true })
		serve(t, b, simpleReq)
		if calls.Load() != 0 || inf.hits.Load() != 0 {
			t.Fatalf("gate consulted=%d inferx=%d", calls.Load(), inf.hits.Load())
		}
	}
}

func TestGate_OpenStillNeverRetriesAStartedStream(t *testing.T) {
	dsh := newFake(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"parcial\"}}]}\n\n")
		w.(http.Flusher).Flush()
		hijackClose(w)
	})
	inf := newFake(t, okText("inferx", 1, 1))
	b, _ := newBridge(t, baseEnv(dsh.url(), inf.url()))
	b.SetFallbackGate(func() bool { return true })
	_, out := serve(t, b, `{"model":"deepseek-profile","stream":true,"messages":[]}`)
	if inf.hits.Load() != 0 || !IsCut(out.Err) {
		t.Fatalf("started stream retried with open gate: inferx=%d", inf.hits.Load())
	}
}
