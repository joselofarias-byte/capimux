package mitm

import (
	"bufio"
	"bytes"
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"errors"
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

// neutralizeEnv clears proxy variables and forces the CA installer off.
// installRootCA also no-ops when os.Args[0] ends in ".test" (cert.go:173).
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

// guardSystem fails the test if it rewrites the hosts file or the system trust anchor.
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

// mitmRig is a loopback TLS interceptor. It runs the production acceptLoop and
// handleConn. Server.Start binds 127.0.0.1:443 (server.go:106), which this
// process cannot do, so the certificate callback matches that function and the
// listener is 127.0.0.1:0.
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

// leafTLSConfig is the GetCertificate policy in Server.Start (server.go:83-100).
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

// exchangeRaw writes a prebuilt request and reads one HTTP response.
func (r *mitmRig) exchangeRaw(t *testing.T, serverName string, raw []byte, timeout time.Duration) (int, []byte, error) {
	t.Helper()
	conn, err := r.dial(t, serverName, timeout)
	if err != nil {
		return 0, nil, err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	if _, err := conn.Write(raw); err != nil {
		return 0, nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	return resp.StatusCode, body, readErr
}

type routerCapture struct {
	mu   sync.Mutex
	hits int
	path string
	body []byte
	ua   string
	auth string
}

func (c *routerCapture) snapshot() (hits int, path string, body []byte, ua, auth string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.path, append([]byte(nil), c.body...), c.ua, c.auth
}

func (c *routerCapture) serve(status int, contentType, respBody string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.hits++
		c.path = r.URL.RequestURI()
		c.body = body
		c.ua = r.Header.Get("User-Agent")
		c.auth = r.Header.Get("Authorization")
		c.mu.Unlock()
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	})
}

// listenRouter binds the port FetchRouter dials, on both loopback families.
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
	return listeners
}

func startRouter(t *testing.T, h http.Handler) {
	t.Helper()
	listeners := listenRouter(t)
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	for _, ln := range listeners {
		go func(ln net.Listener) {
			err := srv.Serve(ln)
			if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
				return
			}
		}(ln)
	}
	waitRouter(t, listeners[0].Addr().String())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
}

func waitRouter(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		last = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("router did not accept: %v", last)
}
