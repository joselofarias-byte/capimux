package mitm

import (
	"bufio"
	"bytes"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// routerPortMu serializes tests that bind the hardcoded router port.
// FetchRouter always dials http://localhost:20130 (handlers/base.go).
var routerPortMu sync.Mutex

const systemCAPath = "/usr/local/share/ca-certificates/9router-mitm-root.crt"

func neutralizeEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
		"http_proxy", "https_proxy", "all_proxy", "no_proxy",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("NO_INSTALL_CA", "1")
}

func guardSystem(t *testing.T) {
	t.Helper()
	hostsBefore, err := os.ReadFile("/etc/hosts")
	if err != nil {
		t.Fatalf("read /etc/hosts: %v", err)
	}
	caBefore, caErr := os.ReadFile(systemCAPath)
	t.Cleanup(func() {
		hostsAfter, err := os.ReadFile("/etc/hosts")
		if err != nil {
			t.Errorf("read /etc/hosts: %v", err)
			return
		}
		if !bytes.Equal(hostsBefore, hostsAfter) {
			t.Error("/etc/hosts changed; MITM tests must not install DNS redirects")
		}
		caAfter, caErr2 := os.ReadFile(systemCAPath)
		beforeMissing := os.IsNotExist(caErr)
		afterMissing := os.IsNotExist(caErr2)
		if beforeMissing && afterMissing {
			return
		}
		if beforeMissing != afterMissing || !bytes.Equal(caBefore, caAfter) {
			t.Error("system trust anchor 9router-mitm-root.crt changed")
		}
	})
}

type mitmRig struct {
	dir  string
	srv  *Server
	addr string
	ca   *x509.Certificate
	key  crypto.PrivateKey
}

func newRig(t *testing.T) *mitmRig {
	t.Helper()
	neutralizeEnv(t)
	guardSystem(t)
	if !strings.HasSuffix(os.Args[0], ".test") {
		t.Fatal("refusing to start MITM rig: test binary name does not end in .test")
	}
	dir := t.TempDir()
	ca, key, err := EnsureRootCA(dir)
	if err != nil {
		t.Fatalf("EnsureRootCA: %v", err)
	}
	srv, err := NewServer(dir)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", leafTLSConfig(dir, ca, key))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv.mu.Lock()
	srv.listener = ln
	srv.running = true
	srv.mu.Unlock()
	go srv.acceptLoop()
	t.Cleanup(func() { srv.Stop() })
	return &mitmRig{dir: dir, srv: srv, addr: ln.Addr().String(), ca: ca, key: key}
}

func leafTLSConfig(baseDir string, caCert *x509.Certificate, caKey crypto.PrivateKey) *tls.Config {
	return &tls.Config{
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			if getHandler(hello.ServerName) == nil {
				return nil, fmt.Errorf("domain not intercepted: %s", hello.ServerName)
			}
			leafCert, leafKey, err := GetOrCreateLeafCert(baseDir, hello.ServerName, caCert, caKey)
			if err != nil {
				return nil, fmt.Errorf("get leaf cert for %s: %w", hello.ServerName, err)
			}
			pair, err := tls.X509KeyPair(certToPEM(leafCert), privateKeyToPEM(leafKey))
			if err != nil {
				return nil, fmt.Errorf("X509KeyPair for %s: %w", hello.ServerName, err)
			}
			return &pair, nil
		},
	}
}

func (r *mitmRig) dial(t *testing.T, serverName string, timeout time.Duration) (*tls.Conn, error) {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(r.ca)
	dialer := &net.Dialer{Timeout: timeout}
	return tls.DialWithDialer(dialer, "tcp", r.addr, &tls.Config{
		ServerName: serverName,
		RootCAs:    pool,
	})
}

func (r *mitmRig) exchange(t *testing.T, serverName, hostHeader, path string, body []byte, timeout time.Duration) (int, http.Header, []byte, error) {
	t.Helper()
	conn, err := r.dial(t, serverName, timeout)
	if err != nil {
		return 0, nil, nil, err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, "https://"+serverName+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Host = hostHeader
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer unit-test")
	req.Header.Set("User-Agent", "unit-test-agent")
	if err := req.Write(conn); err != nil {
		return 0, nil, nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Clone(), respBody, readErr
}

func closeIdleDefaultConns() {
	if tr, ok := http.DefaultTransport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
}

func listenRouter(t *testing.T) []net.Listener {
	t.Helper()
	routerPortMu.Lock()
	t.Cleanup(func() { routerPortMu.Unlock() })
	var listeners []net.Listener
	for _, addr := range []string{"127.0.0.1:20130", "[::1]:20130"} {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			if strings.HasPrefix(addr, "[") && (strings.Contains(err.Error(), "address family") || strings.Contains(err.Error(), "cannot assign")) {
				continue
			}
			t.Fatalf("listen %s: %v", addr, err)
		}
		listeners = append(listeners, ln)
		t.Cleanup(func() { _ = ln.Close() })
	}
	if len(listeners) == 0 {
		t.Fatal("no loopback listener on port 20130")
	}
	t.Cleanup(closeIdleDefaultConns)
	closeIdleDefaultConns()
	return listeners
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

func serveFixed(ln net.Listener, payload string) {
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
			fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(payload), payload)
		}(conn)
	}
}
