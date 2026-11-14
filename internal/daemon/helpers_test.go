package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"9router/proxy/internal/auth"
	"9router/proxy/internal/proc"
)

// The payloads below are re-execed children of this test binary. They stand in
// for the real gateway: a local listener or a process that exits on its own.
// Nothing here calls tailscale, systemd, launchd, or sudo.

const helperModeEnv = "9ROUTER_DAEMON_HELPER"

func TestDaemonServeHealthHelper(t *testing.T) {
	if os.Getenv(helperModeEnv) != "serve-health" {
		t.Skip("helper payload, only meaningful as a spawned child")
	}
	if path := os.Getenv("9ROUTER_ARGS_FILE"); path != "" {
		report := strings.Join(os.Args, "\n") + "\n" + EnvBackground + "=" + os.Getenv(EnvBackground) + "\n"
		if err := os.WriteFile(path, []byte(report), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "write args file: %v\n", err)
			os.Exit(1)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc(HealthPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	listenOrExit(mux)
}

func TestDaemonShutdownHelper(t *testing.T) {
	if os.Getenv(helperModeEnv) != "shutdown" {
		t.Skip("helper payload, only meaningful as a spawned child")
	}
	marker := os.Getenv("9ROUTER_SHUTDOWN_MARKER")
	mux := http.NewServeMux()
	mux.HandleFunc(shutdownAPI, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get(auth.CLITokenHeader) != auth.CLIToken() {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if marker != "" {
			if err := os.WriteFile(marker, []byte("ok"), 0644); err != nil {
				http.Error(w, "marker", http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
		// Leave the handler so the response is flushed, then exit. Stop watches
		// the pid, not the socket, so the process itself has to go away.
		go func() {
			time.Sleep(100 * time.Millisecond)
			os.Exit(0)
		}()
	})
	listenOrExit(mux)
}

func TestDaemonStickyHelper(t *testing.T) {
	if os.Getenv(helperModeEnv) != "sticky" {
		t.Skip("helper payload, only meaningful as a spawned child")
	}
	marker := os.Getenv("9ROUTER_SHUTDOWN_MARKER")
	mux := http.NewServeMux()
	mux.HandleFunc(shutdownAPI, func(w http.ResponseWriter, r *http.Request) {
		if marker != "" {
			_ = os.WriteFile(marker, []byte("ok"), 0644)
		}
		w.WriteHeader(http.StatusOK)
	})
	listenOrExit(mux)
}

func TestDaemonSleepHelper(t *testing.T) {
	if os.Getenv(helperModeEnv) != "sleep" {
		t.Skip("helper payload, only meaningful as a spawned child")
	}
	time.Sleep(45 * time.Second)
}

func TestDaemonExitHelper(t *testing.T) {
	if os.Getenv(helperModeEnv) != "exit" {
		t.Skip("helper payload, only meaningful as a spawned child")
	}
}

func listenOrExit(h http.Handler) {
	addr := net.JoinHostPort("127.0.0.1", os.Getenv("9ROUTER_TEST_PORT"))
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 2 * time.Second}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "helper listen %s: %v\n", addr, err)
		os.Exit(1)
	}
}

func localURL(port int) string {
	return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return port
}

// holdPort accepts and drops connections so a dial loop cannot fill the
// listen backlog and start failing open.
func holdPort(t *testing.T) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	var once sync.Once
	port := ln.Addr().(*net.TCPAddr).Port
	return port, func() { once.Do(func() { _ = ln.Close() }) }
}

func serveLocal(t *testing.T, h http.Handler) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 2 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

func waitListen(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("nothing accepted connections on %s", addr)
}

func stopPID(t *testing.T, pid int) {
	t.Helper()
	if pid <= 0 {
		return
	}
	_ = proc.ForceKill(pid)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !proc.Alive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("pid %d still alive after kill", pid)
}

// spawnHelper re-execs this test binary and runs exactly one payload. The mode
// is set on the child only, so a later Start in the same test can inherit a
// different mode from the parent environment.
func spawnHelper(t *testing.T, testName, mode string) *exec.Cmd {
	t.Helper()
	env := make([]string, 0, len(os.Environ())+1)
	prefix := helperModeEnv + "="
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, prefix+mode)

	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$")
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper %s: %v", testName, err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
		if t.Failed() && out.Len() > 0 {
			t.Logf("helper %s output:\n%s", testName, out.String())
		}
	})
	return cmd
}
