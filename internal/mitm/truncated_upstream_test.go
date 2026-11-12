package mitm

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/log"
	"9router/proxy/internal/mitm/handlers"
)

// TestInterceptTLS_TruncatedUpstreamBody was ported from
// cursor/mitm-intercept-coverage-65e0. The skip that documented the bug is
// now a hard failure: the intercepted client must see HTTP 502 mitm_error.
func TestInterceptTLS_TruncatedUpstreamBody(t *testing.T) {
	rig := newRig(t)
	listeners := listenRouter(t)
	for _, ln := range listeners {
		go serveTruncated(ln)
	}

	status, _, body, err := rig.exchange(t, "api.cursor.com", "api.cursor.com", "/v1/chat/completions", []byte(`{"model":"m"}`), 5*time.Second)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if status != http.StatusBadGateway || !bytes.Contains(body, []byte(`"type":"mitm_error"`)) || !bytes.Contains(body, []byte(`"code":502`)) {
		t.Fatalf("status %d body %s", status, body)
	}
	if bytes.Contains(body, []byte(`{"x":`)) || bytes.Contains(body, []byte("Bearer")) || bytes.Contains(body, []byte("unit-test")) {
		t.Fatal("502 body leaked the partial upstream body or request credentials")
	}
}

func TestInterceptTLS_CompleteJSONBody(t *testing.T) {
	const payload = `{"id":"resp-1","ok":true}`
	rig := newRig(t)
	listeners := listenRouter(t)
	for _, ln := range listeners {
		go serveFixed(ln, payload)
	}

	status, _, body, err := rig.exchange(t, "api.cursor.com", "api.cursor.com", "/v1/chat/completions", []byte(`{"model":"m"}`), 5*time.Second)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if status != http.StatusOK || string(body) != payload {
		t.Fatalf("status %d body %q", status, body)
	}
}

func TestHandleCursor_TruncatedUpstreamAfterHeaderAborts(t *testing.T) {
	neutralizeEnv(t)
	guardSystem(t)
	log.ClearConsoleLogs()
	prev := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("Content-Type", "application/json")
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        header,
			ContentLength: 64,
			Body:          io.NopCloser(&shortRead{n: 4}),
			Request:       r,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = prev })

	client, server := tlsPair(t)
	rw := newResponseWriter(server)
	rw.WriteHeader(http.StatusOK)
	if !rw.WroteHeader() {
		t.Fatal("status line was not committed")
	}

	req := httptest.NewRequest(http.MethodPost, "https://api.cursor.com/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer super-secret-token-value")
	handlers.HandleCursor(rw, req, []byte(`{"model":"m","password":"p@ss-not-a-key"}`))

	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(client), nil)
	if isTimeout(err) {
		t.Fatal("timed out waiting for the client connection to be cut")
	}
	if err == nil {
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if isTimeout(readErr) {
			t.Fatal("timed out reading the client body")
		}
		if readErr == nil {
			t.Fatalf("completed HTTP %d with %d bytes after a truncated upstream body", resp.StatusCode, len(body))
		}
	}
	assertTruncationLog(t)
}

func assertTruncationLog(t *testing.T) {
	t.Helper()
	found := false
	for _, line := range log.ConsoleLogs() {
		if !strings.Contains(line, "upstream JSON truncated after headers were sent") {
			continue
		}
		found = true
		lower := strings.ToLower(line)
		if strings.Contains(line, "super-secret-token-value") || strings.Contains(line, "p@ss-not-a-key") || strings.Contains(lower, "bearer") || strings.Contains(lower, "authorization") || strings.Contains(line, `{"x":`) {
			t.Fatal("truncation log included credential material or an upstream body")
		}
	}
	if !found {
		t.Fatal("expected a truncation log after the connection was cut")
	}
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type shortRead struct {
	n   int
	did int
}

func (e *shortRead) Read(p []byte) (int, error) {
	if e.did >= e.n {
		return 0, io.ErrUnexpectedEOF
	}
	if len(p) > e.n-e.did {
		p = p[:e.n-e.did]
	}
	for i := range p {
		p[i] = 'x'
	}
	e.did += len(p)
	return len(p), nil
}

func tlsPair(t *testing.T) (client, server *tls.Conn) {
	t.Helper()
	dir := t.TempDir()
	ca, key, err := EnsureRootCA(dir)
	if err != nil {
		t.Fatalf("EnsureRootCA: %v", err)
	}
	leaf, leafKey, err := GetOrCreateLeafCert(dir, "api.cursor.com", ca, key)
	if err != nil {
		t.Fatalf("leaf: %v", err)
	}
	pair, err := tls.X509KeyPair(certToPEM(leaf), privateKeyToPEM(leafKey))
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	type accepted struct {
		conn *tls.Conn
		err  error
	}
	ch := make(chan accepted, 1)
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			ch <- accepted{err: err}
			return
		}
		tc := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{pair}})
		if err := tc.Handshake(); err != nil {
			_ = raw.Close()
			ch <- accepted{err: err}
			return
		}
		ch <- accepted{conn: tc}
	}()

	rawClient, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	client = tls.Client(rawClient, &tls.Config{ServerName: "api.cursor.com", RootCAs: pool})
	if err := client.Handshake(); err != nil {
		t.Fatalf("client handshake: %v", err)
	}
	got := <-ch
	if got.err != nil {
		t.Fatalf("server handshake: %v", got.err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = got.conn.Close()
	})
	return client, got.conn
}
