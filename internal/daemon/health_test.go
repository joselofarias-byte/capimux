package daemon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSplitHostPort(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		host   string
		port   string
		wantOK bool
	}{
		{name: "http with path", in: "http://127.0.0.1:8080/health", host: "127.0.0.1", port: "8080", wantOK: true},
		{name: "https", in: "https://localhost:443/x", host: "localhost", port: "443", wantOK: true},
		{name: "no scheme", in: "127.0.0.1:9", host: "127.0.0.1", port: "9", wantOK: true},
		{name: "empty host uses loopback", in: "http://:9090/", host: "127.0.0.1", port: "9090", wantOK: true},
		{name: "ipv6", in: "http://[::1]:8080/a", host: "::1", port: "8080", wantOK: true},
		{name: "missing port", in: "http://127.0.0.1/health", wantOK: false},
		{name: "empty", in: "", wantOK: false},
		{name: "scheme only", in: "http://", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port, ok := splitHostPort(tt.in)
			if ok != tt.wantOK || host != tt.host || port != tt.port {
				t.Fatalf("splitHostPort(%q) = %q, %q, %v; want %q, %q, %v", tt.in, host, port, ok, tt.host, tt.port, tt.wantOK)
			}
		})
	}
}

func TestPortBusy(t *testing.T) {
	t.Run("closed port", func(t *testing.T) {
		busy, who := portBusy(localURL(freePort(t)))
		if busy || who != "" {
			t.Fatalf("portBusy(closed) = %v, %q; want free", busy, who)
		}
	})

	t.Run("unparseable url", func(t *testing.T) {
		busy, who := portBusy("not a url")
		if busy || who != "" {
			t.Fatalf("portBusy(unparseable) = %v, %q; want free", busy, who)
		}
	})

	t.Run("names the live pid", func(t *testing.T) {
		isolate(t)
		port, release := holdPort(t)
		t.Cleanup(release)
		pid := livePID(t)
		writePIDFile(t, strconv.Itoa(pid))

		busy, who := portBusy(localURL(port))
		if !busy || who != "pid "+strconv.Itoa(pid)+" owns it" {
			t.Fatalf("portBusy = %v, %q; want the live pid", busy, who)
		}
	})

	t.Run("names a foreign listener", func(t *testing.T) {
		isolate(t)
		port, release := holdPort(t)
		t.Cleanup(release)

		busy, who := portBusy(localURL(port))
		if !busy || who != "started outside this binary" {
			t.Fatalf("portBusy = %v, %q; want a foreign listener", busy, who)
		}
	})

	t.Run("drops a stale pid and reports the listener as foreign", func(t *testing.T) {
		isolate(t)
		port, release := holdPort(t)
		t.Cleanup(release)
		writePIDFile(t, strconv.Itoa(deadPID(t)))

		busy, who := portBusy(localURL(port))
		if !busy || who != "started outside this binary" {
			t.Fatalf("portBusy = %v, %q; want a foreign listener", busy, who)
		}
		if _, err := os.Stat(PIDFile()); !os.IsNotExist(err) {
			t.Fatalf("stale pid file should be removed, stat err = %v", err)
		}
	})

	t.Run("empty host dials loopback", func(t *testing.T) {
		isolate(t)
		port, release := holdPort(t)
		t.Cleanup(release)

		busy, who := portBusy("http://:" + strconv.Itoa(port))
		if !busy || who != "started outside this binary" {
			t.Fatalf("portBusy(empty host) = %v, %q; want the loopback listener", busy, who)
		}
	})
}

func TestDaemonLocalURLUsesConfiguredPort(t *testing.T) {
	isolate(t)
	t.Setenv("PORT", "34567")
	if got, want := daemonLocalURL(), "http://127.0.0.1:34567"; got != want {
		t.Fatalf("daemonLocalURL() = %q, want %q", got, want)
	}
}

func TestDaemonLocalURLFallsBackWhenPortUnset(t *testing.T) {
	isolate(t)
	// LoadConfig clamps a non-positive PORT to 20130 before daemonLocalURL
	// reads it, so the defensive fallback inside daemonLocalURL is not what
	// produces this URL.
	t.Setenv("PORT", "0")
	if got, want := daemonLocalURL(), "http://127.0.0.1:20130"; got != want {
		t.Fatalf("daemonLocalURL() = %q, want %q", got, want)
	}
}

func TestIsBackgroundProcess(t *testing.T) {
	t.Setenv(EnvBackground, "")
	if IsBackgroundProcess() {
		t.Fatal("an empty marker must not count as a background process")
	}
	t.Setenv(EnvBackground, "0")
	if IsBackgroundProcess() {
		t.Fatal("value 0 must not count as a background process")
	}
	t.Setenv(EnvBackground, "1")
	if !IsBackgroundProcess() {
		t.Fatal("value 1 must count as a background process")
	}
}

func TestProbeHealthy(t *testing.T) {
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(okSrv.Close)
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	t.Cleanup(badSrv.Close)

	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "200", url: okSrv.URL, want: true},
		{name: "non-200", url: badSrv.URL, want: false},
		{name: "connection refused", url: localURL(freePort(t)) + "/health", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := probeHealthy(tt.url); got != tt.want {
				t.Fatalf("probeHealthy(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestBindFailureSeen(t *testing.T) {
	isolate(t)
	if bindFailureSeen() {
		t.Fatal("a missing log must not count as a bind failure")
	}
	if err := os.MkdirAll(Dir(), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "unrelated log", body: "server started\n", want: false},
		{name: "windows address in use", body: "bind: Only one usage of each socket address\n", want: true},
		{name: "unix address in use", body: "listen tcp 127.0.0.1:20130: bind: address already in use\n", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(LogPath(), []byte(tt.body), 0644); err != nil {
				t.Fatalf("write log: %v", err)
			}
			if got := bindFailureSeen(); got != tt.want {
				t.Fatalf("bindFailureSeen() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWaitHealthyWhenProcessAlreadyExited(t *testing.T) {
	isolate(t)
	err := waitHealthy(localURL(freePort(t)), deadPID(t))
	if err == nil || !strings.Contains(err.Error(), "exited before becoming ready") || !strings.Contains(err.Error(), LogPath()) {
		t.Fatalf("waitHealthy error = %v", err)
	}
}

func TestWaitHealthyReturnsOnceHealthAnswers(t *testing.T) {
	isolate(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != HealthPath {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	if err := waitHealthy(srv.URL, os.Getpid()); err != nil {
		t.Fatalf("waitHealthy: %v", err)
	}
}

func TestWaitHealthyReportsBindFailure(t *testing.T) {
	isolate(t)
	if err := os.MkdirAll(Dir(), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(LogPath(), []byte("listen tcp: bind: address already in use\n"), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	err := waitHealthy(localURL(freePort(t)), os.Getpid())
	if err == nil || !strings.Contains(err.Error(), "could not bind") || !strings.Contains(err.Error(), LogPath()) {
		t.Fatalf("waitHealthy error = %v", err)
	}
}

func TestWaitHealthyTimesOut(t *testing.T) {
	isolate(t)
	url := localURL(freePort(t))
	start := time.Now()
	err := waitHealthy(url, os.Getpid())
	elapsed := time.Since(start)
	if elapsed < StartupTimeout {
		t.Fatalf("waitHealthy returned after %s, want at least %s", elapsed, StartupTimeout)
	}
	if err == nil || !strings.Contains(err.Error(), "no response from "+url+HealthPath) || !strings.Contains(err.Error(), LogPath()) {
		t.Fatalf("waitHealthy error = %v", err)
	}
}

func TestWaitExitReturnsImmediatelyForDeadPID(t *testing.T) {
	start := time.Now()
	if !waitExit(deadPID(t), 5000) {
		t.Fatal("waitExit(dead) = false, want true")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("waitExit(dead) took %s", elapsed)
	}
}

func TestWaitExitTimesOutForLivePID(t *testing.T) {
	pid := livePID(t)
	start := time.Now()
	if waitExit(pid, 80) {
		t.Fatal("waitExit(live) = true, want false")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("waitExit(live) took %s", elapsed)
	}
}

func TestWaitExitReturnsWhenProcessExits(t *testing.T) {
	cmd := spawnHelper(t, "TestDaemonExitHelper", "exit")
	if !waitExit(cmd.Process.Pid, 5000) {
		t.Fatal("waitExit did not observe the helper exit")
	}
}

func TestWaitPortFreeWhenNothingListens(t *testing.T) {
	if err := waitPortFree(localURL(freePort(t))); err != nil {
		t.Fatalf("waitPortFree(closed) = %v", err)
	}
}

func TestWaitPortFreeWaitsUntilListenerCloses(t *testing.T) {
	port, release := holdPort(t)
	t.Cleanup(release)
	go func() {
		time.Sleep(200 * time.Millisecond)
		release()
	}()

	start := time.Now()
	if err := waitPortFree(localURL(port)); err != nil {
		t.Fatalf("waitPortFree: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("waitPortFree took %s after the listener closed", elapsed)
	}
}
