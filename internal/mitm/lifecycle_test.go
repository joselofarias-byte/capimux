package mitm

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHandleConn_DropsNonTLS(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	s := &Server{}
	done := make(chan struct{})
	go func() {
		s.handleConn(server)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleConn blocked on a connection that is not TLS")
	}
}

func TestStop_ClosesActivePipe(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	s := &Server{active: map[net.Conn]struct{}{server: {}}, running: true}
	readDone := make(chan error, 1)
	go func() {
		var buf [1]byte
		_, err := client.Read(buf[:])
		readDone <- err
	}()
	s.Stop()
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("read succeeded after Stop closed the connection")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not close the active connection")
	}
	if s.IsRunning() {
		t.Fatal("IsRunning after Stop")
	}
	s.Stop()
}

func TestResponseWriter_HeadersAndImplicitStatus(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })

	errCh := make(chan error, 1)
	go func() {
		w := newResponseWriter(server)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Test", "unit")
		w.WriteHeader(http.StatusCreated)
		w.WriteHeader(http.StatusBadGateway)
		_, err := w.Write([]byte(`{"ok":true}`))
		errCh <- err
		server.Close()
	}()

	resp, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("body %q", body)
	}
	if resp.Header.Get("Content-Type") != "application/json" || resp.Header.Get("X-Test") != "unit" {
		t.Fatalf("headers %v", resp.Header)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("write: %v", err)
	}

	client2, server2 := net.Pipe()
	t.Cleanup(func() { client2.Close(); server2.Close() })
	go func() {
		w := newResponseWriter(server2)
		_, _ = w.Write([]byte("hi"))
		server2.Close()
	}()
	resp, err = http.ReadResponse(bufio.NewReader(client2), nil)
	if err != nil {
		t.Fatalf("implicit status: %v", err)
	}
	defer resp.Body.Close()
	body, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "hi" {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}
}

func TestAcceptLoop_ContinuesAfterAcceptError(t *testing.T) {
	neutralizeEnv(t)
	guardSystem(t)
	dir := t.TempDir()
	srv, err := NewServer(dir)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := &errOnceListener{Listener: inner}
	srv.mu.Lock()
	srv.listener = ln
	srv.running = true
	srv.mu.Unlock()
	go srv.acceptLoop()
	t.Cleanup(func() { srv.Stop() })

	deadline := time.Now().Add(2 * time.Second)
	for ln.accepts.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if ln.accepts.Load() < 2 {
		t.Fatalf("acceptLoop stopped after the injected error, accepts=%d", ln.accepts.Load())
	}
}

type errOnceListener struct {
	net.Listener
	accepts atomic.Int32
	failed  atomic.Bool
}

func (l *errOnceListener) Accept() (net.Conn, error) {
	l.accepts.Add(1)
	if l.failed.CompareAndSwap(false, true) {
		return nil, errors.New("injected accept failure")
	}
	return l.Listener.Accept()
}

func TestStart_FailsClosedWhenPrivilegedPortIsUnavailable(t *testing.T) {
	neutralizeEnv(t)
	guardSystem(t)
	dir := t.TempDir()
	srv, err := NewServer(dir)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	err = srv.Start()
	if err == nil {
		srv.Stop()
		return
	}
	if srv.IsRunning() {
		srv.Stop()
		t.Fatal("IsRunning is true after Start failed")
	}
	if !strings.Contains(err.Error(), "listen") {
		t.Fatalf("error %v", err)
	}
}

func TestStart_AlreadyRunning(t *testing.T) {
	neutralizeEnv(t)
	guardSystem(t)
	srv, err := NewServer(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	srv.running = true
	srv.mu.Unlock()
	err = srv.Start()
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("Start error %v", err)
	}
}

func TestNewServer_FailsWhenBaseIsNotADirectory(t *testing.T) {
	neutralizeEnv(t)
	guardSystem(t)
	base := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(base, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewServer(base); err == nil {
		t.Fatal("NewServer accepted a file as the base directory")
	}
}

func TestManagerEnable_StopsBeforeDNSWhenDirectoryCannotBeCreated(t *testing.T) {
	neutralizeEnv(t)
	guardSystem(t)
	base := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(base, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := NewManager(base).Enable()
	if err == nil || !strings.Contains(err.Error(), "create mitm dir") {
		t.Fatalf("Enable error %v", err)
	}
}

func TestManagerPaths(t *testing.T) {
	dir := t.TempDir()
	if got := ConfigFilePath(dir); !strings.HasSuffix(got, filepath.Join("mitm", "config.json")) {
		t.Fatalf("ConfigFilePath %s", got)
	}
	logDir := LogDir(dir)
	info, err := os.Stat(logDir)
	if err != nil || !info.IsDir() {
		t.Fatalf("LogDir %s: %v", logDir, err)
	}

	base := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(base, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LogDir(base); !strings.HasSuffix(got, filepath.Join("mitm", "logs")) {
		t.Fatalf("LogDir on a file base returned %s", got)
	}
}

func TestHandledDomainsMatchInterceptList(t *testing.T) {
	listed := map[string]bool{}
	for _, domain := range AllDomains() {
		listed[domain] = true
		if getHandler(domain) == nil {
			t.Errorf("intercepted domain %s has no handler", domain)
		}
	}
	for host := range toolHandlers {
		if !listed[host] {
			t.Errorf("handler host %s is missing from InterceptedDomains", host)
		}
	}
	if getHandler("example.com") != nil {
		t.Fatal("example.com is intercepted")
	}
	if getHandler("api.cursor.com:8443") != nil {
		t.Fatal("only the :443 suffix is stripped")
	}
}
