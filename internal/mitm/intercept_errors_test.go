package mitm

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInterceptTLS_UpstreamConnectionRefused(t *testing.T) {
	routerPortMu.Lock()
	defer routerPortMu.Unlock()
	for _, addr := range []string{"127.0.0.1:20130", "[::1]:20130"} {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			if strings.HasPrefix(addr, "[") {
				continue
			}
			t.Fatalf("port 20130 is busy (%s): %v", addr, err)
		}
		ln.Close()
	}

	rig := newRig(t)
	status, _, body, err := rig.exchange(t, "api.cursor.com", "api.cursor.com", "/v1/chat/completions", []byte(`{"model":"m"}`), 5*time.Second)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if status != http.StatusBadGateway {
		t.Fatalf("status %d body %s", status, body)
	}
	if !bytes.Contains(body, []byte(`"type":"mitm_error"`)) || !bytes.Contains(body, []byte("connection refused")) {
		t.Fatalf("body %s", body)
	}
}

func TestInterceptTLS_UpstreamConnectionCutBeforeResponse(t *testing.T) {
	rig := newRig(t)
	listeners := listenRouter(t)
	for _, ln := range listeners {
		go func(ln net.Listener) {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				conn.Close()
			}
		}(ln)
	}

	status, _, body, err := rig.exchange(t, "api.cursor.com", "api.cursor.com", "/v1/chat/completions", []byte(`{"model":"m"}`), 5*time.Second)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if status != http.StatusBadGateway {
		t.Fatalf("status %d body %s", status, body)
	}
	if !bytes.Contains(body, []byte(`"type":"mitm_error"`)) {
		t.Fatalf("body %s", body)
	}
}

func TestInterceptTLS_UpstreamGarbageIsBadGateway(t *testing.T) {
	rig := newRig(t)
	listeners := listenRouter(t)
	for _, ln := range listeners {
		go func(ln net.Listener) {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				go func(conn net.Conn) {
					defer conn.Close()
					_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
					req, err := http.ReadRequest(bufio.NewReader(conn))
					if err != nil {
						return
					}
					_, _ = io.Copy(io.Discard, req.Body)
					_, _ = conn.Write([]byte("not an http response\r\n"))
				}(conn)
			}
		}(ln)
	}

	status, _, body, err := rig.exchange(t, "chatgpt.com", "chatgpt.com", "/v1/responses", []byte(`{"model":"m"}`), 5*time.Second)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if status != http.StatusBadGateway {
		t.Fatalf("status %d body %s", status, body)
	}
	if !bytes.Contains(body, []byte(`"type":"mitm_error"`)) {
		t.Fatalf("body %s", body)
	}
}

func TestInterceptTLS_TruncatedUpstreamBody(t *testing.T) {
	rig := newRig(t)
	listeners := listenRouter(t)
	for _, ln := range listeners {
		go serveTruncated(ln)
	}

	status, _, body, err := rig.exchange(t, "api.cursor.com", "api.cursor.com", "/v1/chat/completions", []byte(`{"model":"m"}`), 5*time.Second)
	if err != nil || status != http.StatusBadGateway || !bytes.Contains(body, []byte(`"type":"mitm_error"`)) {
		t.Fatalf("truncated upstream: err=%v status=%d body=%q", err, status, body)
	}
}

func serveTruncated(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func(conn net.Conn) {
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			req, err := http.ReadRequest(bufio.NewReader(conn))
			if err != nil {
				return
			}
			_, _ = io.Copy(io.Discard, req.Body)
			_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 64\r\nConnection: close\r\n\r\n{\"x\":")
		}(conn)
	}
}

func TestInterceptTLS_DropsOversizedBody(t *testing.T) {
	rig := newRig(t)
	cap := &routerCapture{}
	startRouter(t, cap.serve(http.StatusOK, "application/json", `{"ok":true}`))

	body := bytes.Repeat([]byte("a"), maxBodyBounds+1)
	_, _, _, err := rig.exchange(t, "api.cursor.com", "api.cursor.com", "/v1/chat/completions", body, 30*time.Second)
	if err == nil {
		t.Fatal("oversized body produced an HTTP response")
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("server hung on an oversized body instead of closing")
	}
	if hits, _, _, _, _ := cap.snapshot(); hits != 0 {
		t.Fatalf("oversized body was forwarded, hits=%d", hits)
	}
}

func TestInterceptTLS_ForwardsBodyAtLimit(t *testing.T) {
	rig := newRig(t)
	got := make(chan []byte, 1)
	startRouter(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	prefix := []byte(`{"model":"m","pad":"`)
	suffix := []byte(`"}`)
	pad := maxBodyBounds - len(prefix) - len(suffix)
	body := make([]byte, 0, maxBodyBounds)
	body = append(body, prefix...)
	body = append(body, bytes.Repeat([]byte("a"), pad)...)
	body = append(body, suffix...)
	if len(body) != maxBodyBounds {
		t.Fatalf("fixture length %d, want %d", len(body), maxBodyBounds)
	}

	status, _, respBody, err := rig.exchange(t, "api.cursor.com", "api.cursor.com", "/v1/chat/completions", body, 30*time.Second)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if status != http.StatusOK || !bytes.Contains(respBody, []byte(`{"ok":true}`)) {
		t.Fatalf("status %d body %s", status, respBody)
	}
	select {
	case fwd := <-got:
		if len(fwd) < maxBodyBounds-64 || !bytes.Contains(fwd, []byte("cursor/m")) {
			t.Fatalf("upstream body len %d, missing forwarded model", len(fwd))
		}
	case <-time.After(time.Second):
		t.Fatal("upstream was not called")
	}
}

func TestInterceptTLS_UnknownSNIIsNotIntercepted(t *testing.T) {
	rig := newRig(t)
	cap := &routerCapture{}
	startRouter(t, cap.serve(http.StatusOK, "application/json", `{"ok":true}`))

	if _, err := rig.dial(t, "example.com", 3*time.Second); err == nil {
		t.Fatal("handshake succeeded for a host that is not intercepted")
	}
	if _, err := rig.dial(t, "", 3*time.Second); err == nil {
		t.Fatal("handshake succeeded with an empty SNI")
	}
	if hits, _, _, _, _ := cap.snapshot(); hits != 0 {
		t.Fatalf("non-intercepted handshake reached upstream, hits=%d", hits)
	}
	if _, err := os.Stat(filepath.Join(CertDir(rig.dir), "example.com.pem")); !os.IsNotExist(err) {
		t.Fatal("minted a leaf cert for a non-intercepted host")
	}
}

func TestInterceptTLS_HostHeaderOutsideListIsDropped(t *testing.T) {
	rig := newRig(t)
	cap := &routerCapture{}
	startRouter(t, cap.serve(http.StatusOK, "application/json", `{"ok":true}`))

	_, _, _, err := rig.exchange(t, "api.cursor.com", "evil.example", "/v1/chat/completions", []byte(`{"model":"m"}`), 3*time.Second)
	if err == nil {
		t.Fatal("mismatched Host produced an HTTP response")
	}
	if hits, _, _, _, _ := cap.snapshot(); hits != 0 {
		t.Fatalf("mismatched Host was forwarded, hits=%d", hits)
	}
}

func TestInterceptTLS_PlaintextCONNECTIsNotATunnel(t *testing.T) {
	rig := newRig(t)
	cap := &routerCapture{}
	startRouter(t, cap.serve(http.StatusOK, "application/json", `{"ok":true}`))

	conn, err := net.DialTimeout("tcp", rig.addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	if _, err := io.WriteString(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	buf := make([]byte, 512)
	n, _ := conn.Read(buf)
	if bytes.Contains(bytes.ToLower(buf[:n]), []byte("connection established")) {
		t.Fatalf("plaintext CONNECT was accepted: %q", buf[:n])
	}
	if hits, _, _, _, _ := cap.snapshot(); hits != 0 {
		t.Fatalf("plaintext CONNECT reached upstream, hits=%d", hits)
	}
}

func TestInterceptTLS_CONNECTAfterTLSDoesNotOpenATunnel(t *testing.T) {
	rig := newRig(t)
	cap := &routerCapture{}
	startRouter(t, cap.serve(http.StatusOK, "application/json", `{"ok":true}`))

	// The request target is example.com:443. ReadRequest uses that as Host
	// (server.go:169), which is not intercepted, so the connection is dropped.
	raw := []byte("CONNECT example.com:443 HTTP/1.1\r\nHost: api.cursor.com\r\nContent-Length: 0\r\n\r\n")
	status, body, err := rig.exchangeRaw(t, "api.cursor.com", raw, 5*time.Second)
	if err == nil && (status == http.StatusOK || bytes.Contains(bytes.ToLower(body), []byte("connection established"))) {
		t.Fatalf("CONNECT opened a tunnel: status %d body %s", status, body)
	}
	if hits, _, _, _, _ := cap.snapshot(); hits != 0 {
		t.Fatalf("CONNECT was forwarded, hits=%d", hits)
	}
}

func TestInterceptTLS_ClientCloseMidBodyIsNotForwarded(t *testing.T) {
	rig := newRig(t)
	cap := &routerCapture{}
	startRouter(t, cap.serve(http.StatusOK, "application/json", `{"ok":true}`))

	conn, err := rig.dial(t, "api.cursor.com", 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	fmt.Fprintf(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: api.cursor.com\r\nContent-Length: %d\r\n\r\npartial", maxBodyBounds)
	conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rig.srv.mu.Lock()
		n := len(rig.srv.active)
		rig.srv.mu.Unlock()
		if n == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	rig.srv.mu.Lock()
	left := len(rig.srv.active)
	rig.srv.mu.Unlock()
	if left != 0 {
		t.Fatalf("connection still active after client close: %d", left)
	}
	if hits, _, _, _, _ := cap.snapshot(); hits != 0 {
		t.Fatalf("partial body was forwarded, hits=%d", hits)
	}
}

func TestInterceptTLS_StopClosesBlockedConnection(t *testing.T) {
	rig := newRig(t)
	conn, err := rig.dial(t, "api.cursor.com", 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "POST /v1/chat/completions HTTP/1.1\r\nHost: api.cursor.com\r\n"); err != nil {
		t.Fatalf("write: %v", err)
	}

	done := make(chan struct{})
	go func() {
		rig.srv.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop hung while a request was blocked in ReadRequest")
	}
	if rig.srv.IsRunning() {
		t.Fatal("IsRunning after Stop")
	}
}
