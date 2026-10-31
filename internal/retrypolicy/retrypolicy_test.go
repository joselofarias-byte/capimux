package retrypolicy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNonIdempotentMedia(t *testing.T) {
	for _, e := range []string{"/v1/images/generations", "/images/edits", "/v1/videos/generations", "/videos/extensions"} {
		if !NonIdempotentMedia(e) {
			t.Errorf("%s debería ser no idempotente", e)
		}
	}
	for _, e := range []string{"/v1/chat/completions", "/v1/embeddings", "/v1/audio/transcriptions", "/v1/search", "/v1/videos/abc"} {
		if NonIdempotentMedia(e) {
			t.Errorf("%s no debería ser no idempotente", e)
		}
	}
}

func TestBeforeSendRealErrors(t *testing.T) {
	// Connection refused: a listener that is closed before dialing.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	_, refused := http.Post("http://"+addr+"/x", "application/json", nil)
	if refused == nil || !BeforeSend(refused) {
		t.Fatalf("conexión rechazada debería ser antes de enviar: %v", refused)
	}

	// Connection dropped after the request was received: ambiguous.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }))
	defer srv.Close()
	_, dropped := http.Post(srv.URL, "application/json", nil)
	if dropped == nil || BeforeSend(dropped) {
		t.Fatalf("corte tras enviar NO debería contar como antes de enviar: %v", dropped)
	}

	// Timeout after the request was sent: ambiguous.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, slow.URL, nil)
	_, timedOut := http.DefaultClient.Do(req)
	if timedOut == nil || BeforeSend(timedOut) {
		t.Fatalf("timeout tras enviar NO debería contar como antes de enviar: %v", timedOut)
	}

	if BeforeSend(nil) || BeforeSend(io.ErrUnexpectedEOF) || BeforeSend(errors.New("x")) {
		t.Fatal("errores genéricos no son 'antes de enviar'")
	}
	if !BeforeSend(fmt.Errorf("envuelto: %w", &net.DNSError{Err: "no such host", Name: "x.invalid"})) {
		t.Fatal("DNS envuelto debería ser antes de enviar")
	}
}

func TestMayRotate(t *testing.T) {
	cases := []struct {
		nonIdem bool
		status  int
		err     error
		want    bool
	}{
		{false, 500, nil, true},
		{false, 0, io.ErrUnexpectedEOF, true},
		{true, 400, nil, true},
		{true, 401, nil, true},
		{true, 429, nil, true},
		{true, 500, nil, false},
		{true, 503, nil, false},
		{true, 0, io.ErrUnexpectedEOF, false},
		{true, 0, &net.OpError{Op: "dial", Err: errors.New("refused")}, true},
		{true, 0, &net.OpError{Op: "read", Err: errors.New("reset")}, false},
	}
	for i, c := range cases {
		if got := MayRotate(c.nonIdem, c.status, c.err); got != c.want {
			t.Errorf("caso %d: MayRotate(%v,%d,%v)=%v, se esperaba %v", i, c.nonIdem, c.status, c.err, got, c.want)
		}
	}
}

func TestStop(t *testing.T) {
	base := errors.New("ambigua")
	s := Stop(base)
	if !IsStop(s) || !errors.Is(s, base) || s.Error() != "ambigua" {
		t.Fatal("Stop debe envolver y marcar")
	}
	if IsStop(base) || Stop(nil) != nil || IsStop(fmt.Errorf("x: %w", errors.New("y"))) {
		t.Fatal("IsStop falso positivo")
	}
	if !IsStop(fmt.Errorf("ctx: %w", s)) {
		t.Fatal("IsStop debe ver a través de envolturas")
	}
}
