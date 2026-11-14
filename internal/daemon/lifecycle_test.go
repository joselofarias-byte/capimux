package daemon

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/auth"
	"9router/proxy/internal/proc"
)

func TestRequestGracefulStopAcceptsLocalShutdown(t *testing.T) {
	isolate(t)
	token := auth.CLIToken()

	var method, path, contentType, gotToken, body string
	port := serveLocal(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		contentType = r.Header.Get("Content-Type")
		gotToken = r.Header.Get(auth.CLITokenHeader)
		payload, _ := io.ReadAll(io.LimitReader(r.Body, 32))
		body = string(payload)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bytes.Repeat([]byte("x"), 5000))
	}))
	t.Setenv("PORT", strconv.Itoa(port))

	if !requestGracefulStop() {
		t.Fatal("local shutdown endpoint should accept the CLI token")
	}
	if method != http.MethodPost || path != shutdownAPI {
		t.Fatalf("request = %s %s, want POST %s", method, path, shutdownAPI)
	}
	if contentType != "application/json" || body != "{}" {
		t.Fatalf("request content-type = %q, body = %q", contentType, body)
	}
	if gotToken != token {
		t.Fatal("shutdown request did not carry this machine's CLI token")
	}
}

func TestRequestGracefulStopRejectsNonOK(t *testing.T) {
	isolate(t)
	_ = auth.CLIToken()
	port := serveLocal(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Setenv("PORT", strconv.Itoa(port))

	if requestGracefulStop() {
		t.Fatal("a non-200 shutdown response must be treated as a refusal")
	}
}

func TestRequestGracefulStopFailsWhenUnreachable(t *testing.T) {
	isolate(t)
	t.Setenv("PORT", strconv.Itoa(freePort(t)))
	if requestGracefulStop() {
		t.Fatal("an unreachable shutdown endpoint must fail closed")
	}
}

func TestStartRefusesWhenOurDaemonOwnsPort(t *testing.T) {
	isolate(t)
	port, release := holdPort(t)
	t.Cleanup(release)
	pid := livePID(t)
	writePIDFile(t, strconv.Itoa(pid))
	url := localURL(port)

	res, err := Start(url, nil)
	if !errors.Is(err, ErrAlreadyRunning) || err.Error() != ErrAlreadyRunning.Error() {
		t.Fatalf("Start error = %v, want the bare ErrAlreadyRunning", err)
	}
	if !res.Existing || res.PID != pid || res.URL != url {
		t.Fatalf("Start result = %+v, want existing pid %d at %s", res, pid, url)
	}
	data, readErr := os.ReadFile(PIDFile())
	if readErr != nil || strings.TrimSpace(string(data)) != strconv.Itoa(pid) {
		t.Fatalf("pid file = %q, err = %v; want it left untouched", data, readErr)
	}
}

func TestStartRefusesForeignListener(t *testing.T) {
	isolate(t)
	port, release := holdPort(t)
	t.Cleanup(release)
	url := localURL(port)

	res, err := Start(url, nil)
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Start error = %v, want ErrAlreadyRunning", err)
	}
	if !res.Existing || res.PID != 0 || res.URL != url {
		t.Fatalf("Start result = %+v", res)
	}
	if !strings.Contains(err.Error(), url) || !strings.Contains(err.Error(), "started outside this binary") {
		t.Fatalf("Start error = %v", err)
	}
	if _, statErr := os.Stat(PIDFile()); !os.IsNotExist(statErr) {
		t.Fatalf("foreign listener must not create a pid file, stat err = %v", statErr)
	}
}

func TestStartTreatsStalePIDAsForeignListener(t *testing.T) {
	isolate(t)
	port, release := holdPort(t)
	t.Cleanup(release)
	writePIDFile(t, strconv.Itoa(deadPID(t)))
	url := localURL(port)

	res, err := Start(url, nil)
	if !errors.Is(err, ErrAlreadyRunning) || res.PID != 0 || !res.Existing {
		t.Fatalf("Start = %+v, %v; want a foreign-listener error", res, err)
	}
	if _, statErr := os.Stat(PIDFile()); !os.IsNotExist(statErr) {
		t.Fatalf("stale pid file should be cleared, stat err = %v", statErr)
	}
}

func TestStartFailsWhenRunDirCannotBeCreated(t *testing.T) {
	base := t.TempDir()
	blocker := filepath.Join(base, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	t.Setenv("DATA_DIR", blocker)

	_, err := Start(localURL(freePort(t)), nil)
	if err == nil || !strings.Contains(err.Error(), "create run dir") {
		t.Fatalf("Start error = %v, want create run dir", err)
	}
}

func TestStartFailsWhenLogPathIsDirectory(t *testing.T) {
	isolate(t)
	if err := os.MkdirAll(LogPath(), 0755); err != nil {
		t.Fatalf("mkdir log path: %v", err)
	}

	_, err := Start(localURL(freePort(t)), nil)
	if err == nil || !strings.Contains(err.Error(), "open") {
		t.Fatalf("Start error = %v, want log open failure", err)
	}
}

func TestStartFailsWhenPIDFileIsDirectory(t *testing.T) {
	isolate(t)
	if err := os.MkdirAll(PIDFile(), 0755); err != nil {
		t.Fatalf("mkdir pid path: %v", err)
	}
	port := freePort(t)
	t.Setenv(helperModeEnv, "exit")
	t.Setenv("9ROUTER_TEST_PORT", strconv.Itoa(port))

	res, err := Start(localURL(port), []string{"-test.run=^TestDaemonExitHelper$"})
	t.Cleanup(func() { stopPID(t, res.PID) })
	if err == nil || !strings.Contains(err.Error(), "write pid file") {
		t.Fatalf("Start error = %v, want write pid file", err)
	}
}

func TestStartClearsPIDWhenChildExitsEarly(t *testing.T) {
	isolate(t)
	port := freePort(t)
	t.Setenv(helperModeEnv, "exit")
	t.Setenv("9ROUTER_TEST_PORT", strconv.Itoa(port))

	res, err := Start(localURL(port), []string{"-test.run=^TestDaemonExitHelper$"})
	t.Cleanup(func() { stopPID(t, res.PID) })
	if err == nil || !strings.Contains(err.Error(), "exited before becoming ready") || !strings.Contains(err.Error(), LogPath()) {
		t.Fatalf("Start error = %v", err)
	}
	if _, statErr := os.Stat(PIDFile()); !os.IsNotExist(statErr) {
		t.Fatalf("pid file should be cleared, stat err = %v", statErr)
	}
}

func TestStartReportsBindFailureAndClearsPID(t *testing.T) {
	isolate(t)
	if err := os.MkdirAll(Dir(), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(LogPath(), []byte("listen tcp: bind: address already in use\n"), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	port := freePort(t)
	t.Setenv(helperModeEnv, "sleep")
	t.Setenv("9ROUTER_TEST_PORT", strconv.Itoa(port))

	res, err := Start(localURL(port), []string{"-test.run=^TestDaemonSleepHelper$"})
	t.Cleanup(func() { stopPID(t, res.PID) })
	if err == nil || !strings.Contains(err.Error(), "could not bind") {
		t.Fatalf("Start error = %v\nlog:\n%s", err, LogTail(40))
	}
	if _, statErr := os.Stat(PIDFile()); !os.IsNotExist(statErr) {
		t.Fatalf("pid file should be cleared, stat err = %v", statErr)
	}
	if res.PID <= 0 || !proc.Alive(res.PID) {
		t.Fatalf("bind failure should still name the live child, pid=%d", res.PID)
	}
}

func TestStartDetachesHealthyDaemon(t *testing.T) {
	dir := isolate(t)
	port := freePort(t)
	url := localURL(port)
	argsFile := filepath.Join(dir, "child-args.txt")
	t.Setenv(helperModeEnv, "serve-health")
	t.Setenv("9ROUTER_TEST_PORT", strconv.Itoa(port))
	t.Setenv("9ROUTER_ARGS_FILE", argsFile)

	res, err := Start(url, []string{
		"-d=false",
		"--background",
		"-test.run=^TestDaemonServeHealthHelper$",
		"-test.v",
	})
	t.Cleanup(func() { stopPID(t, res.PID) })
	if err != nil {
		t.Fatalf("Start: %v\nlog:\n%s", err, LogTail(40))
	}
	if res.Existing || res.PID <= 0 || res.PID == os.Getpid() || res.URL != url {
		t.Fatalf("Start result = %+v, want a fresh child serving %s", res, url)
	}
	if got := RunningPID(); got != res.PID {
		t.Fatalf("RunningPID() = %d, want %d", got, res.PID)
	}

	report, readErr := os.ReadFile(argsFile)
	if readErr != nil {
		t.Fatalf("read child args: %v", readErr)
	}
	lines := strings.Split(strings.TrimRight(string(report), "\n"), "\n")
	if len(lines) < 2 || lines[len(lines)-1] != EnvBackground+"=1" {
		t.Fatalf("child report = %q, want the background marker on the last line", report)
	}
	flags := strings.Join(lines[1:len(lines)-1], "\n")
	if flags != "-test.run=^TestDaemonServeHealthHelper$\n-test.v" {
		t.Fatalf("forwarded flags = %q", flags)
	}

	again, againErr := Start(url, []string{"-test.run=^TestDaemonServeHealthHelper$"})
	if again.PID != 0 && again.PID != res.PID {
		t.Cleanup(func() { stopPID(t, again.PID) })
	}
	if !errors.Is(againErr, ErrAlreadyRunning) || !again.Existing || again.PID != res.PID || again.URL != url {
		t.Fatalf("second Start = %+v, %v; want the live daemon", again, againErr)
	}
}

func TestStopKillsWhenShutdownUnreachable(t *testing.T) {
	isolate(t)
	t.Setenv("PORT", strconv.Itoa(freePort(t)))
	pid := livePID(t)
	writePIDFile(t, strconv.Itoa(pid))

	res, err := Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !res.Stopped || res.PID != pid || res.Reason != "" {
		t.Fatalf("Stop result = %+v, want stopped pid %d", res, pid)
	}
	if _, statErr := os.Stat(PIDFile()); !os.IsNotExist(statErr) {
		t.Fatalf("pid file still present, stat err = %v", statErr)
	}
	if proc.Alive(pid) {
		t.Fatal("process still alive after Stop")
	}
}

func TestStopUsesGracefulShutdown(t *testing.T) {
	dir := isolate(t)
	port := freePort(t)
	portStr := strconv.Itoa(port)
	marker := filepath.Join(dir, "shutdown-marker")
	t.Setenv("PORT", portStr)
	t.Setenv("9ROUTER_TEST_PORT", portStr)
	t.Setenv("9ROUTER_SHUTDOWN_MARKER", marker)
	_ = auth.CLIToken()

	cmd := spawnHelper(t, "TestDaemonShutdownHelper", "shutdown")
	waitListen(t, net.JoinHostPort("127.0.0.1", portStr))
	writePIDFile(t, strconv.Itoa(cmd.Process.Pid))

	res, err := Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !res.Stopped || res.PID != cmd.Process.Pid {
		t.Fatalf("Stop result = %+v, want stopped pid %d", res, cmd.Process.Pid)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("graceful shutdown was not requested: %v", statErr)
	}
	if _, statErr := os.Stat(PIDFile()); !os.IsNotExist(statErr) {
		t.Fatalf("pid file still present, stat err = %v", statErr)
	}
	if proc.Alive(cmd.Process.Pid) {
		t.Fatal("daemon still alive after the shutdown endpoint accepted")
	}
}

func TestStopFallsBackWhenProcessIgnoresShutdown(t *testing.T) {
	dir := isolate(t)
	port := freePort(t)
	portStr := strconv.Itoa(port)
	marker := filepath.Join(dir, "shutdown-marker")
	t.Setenv("PORT", portStr)
	t.Setenv("9ROUTER_TEST_PORT", portStr)
	t.Setenv("9ROUTER_SHUTDOWN_MARKER", marker)

	cmd := spawnHelper(t, "TestDaemonStickyHelper", "sticky")
	waitListen(t, net.JoinHostPort("127.0.0.1", portStr))
	writePIDFile(t, strconv.Itoa(cmd.Process.Pid))

	start := time.Now()
	res, err := Stop()
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !res.Stopped || res.PID != cmd.Process.Pid {
		t.Fatalf("Stop result = %+v, want the sticky pid %d stopped", res, cmd.Process.Pid)
	}
	if elapsed < stopGraceMS*time.Millisecond-200*time.Millisecond {
		t.Fatalf("Stop returned after %s; a 200 that leaves the process up must wait the grace window", elapsed)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("shutdown endpoint was not called: %v", statErr)
	}
	if proc.Alive(cmd.Process.Pid) {
		t.Fatal("process still alive after the force path")
	}
}

func TestRestartStartsWhenNothingRuns(t *testing.T) {
	isolate(t)
	port := freePort(t)
	url := localURL(port)
	t.Setenv(helperModeEnv, "serve-health")
	t.Setenv("9ROUTER_TEST_PORT", strconv.Itoa(port))

	res, err := Restart(url, []string{"-test.run=^TestDaemonServeHealthHelper$"})
	t.Cleanup(func() { stopPID(t, res.PID) })
	if err != nil {
		t.Fatalf("Restart: %v\nlog:\n%s", err, LogTail(40))
	}
	if res.Existing || res.PID <= 0 || res.URL != url {
		t.Fatalf("Restart result = %+v", res)
	}
	if got := RunningPID(); got != res.PID {
		t.Fatalf("RunningPID() = %d, want %d", got, res.PID)
	}
}

func TestRestartReplacesLiveDaemon(t *testing.T) {
	dir := isolate(t)
	port := freePort(t)
	portStr := strconv.Itoa(port)
	url := localURL(port)
	marker := filepath.Join(dir, "shutdown-marker")
	t.Setenv("PORT", portStr)
	t.Setenv("9ROUTER_TEST_PORT", portStr)
	t.Setenv("9ROUTER_SHUTDOWN_MARKER", marker)
	_ = auth.CLIToken()

	old := spawnHelper(t, "TestDaemonShutdownHelper", "shutdown")
	waitListen(t, net.JoinHostPort("127.0.0.1", portStr))
	writePIDFile(t, strconv.Itoa(old.Process.Pid))

	t.Setenv(helperModeEnv, "serve-health")
	res, err := Restart(url, []string{"-test.run=^TestDaemonServeHealthHelper$"})
	t.Cleanup(func() { stopPID(t, res.PID) })
	if err != nil {
		t.Fatalf("Restart: %v\nlog:\n%s", err, LogTail(40))
	}
	if res.Existing || res.PID == old.Process.Pid || res.PID <= 0 || res.URL != url {
		t.Fatalf("Restart result = %+v, old pid %d", res, old.Process.Pid)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("graceful shutdown was not requested: %v", statErr)
	}
	if proc.Alive(old.Process.Pid) {
		t.Fatal("old daemon still alive")
	}

	resp, getErr := healthClient.Get(url + HealthPath)
	if getErr != nil {
		t.Fatalf("health of replacement: %v", getErr)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("replacement health = %d, want 200", resp.StatusCode)
	}
}

func TestRestartReportsPortStillBusy(t *testing.T) {
	isolate(t)
	// The shutdown request must not land on whatever is really bound to the
	// configured default. Stop dials PORT; the port we hold is the restart URL.
	t.Setenv("PORT", strconv.Itoa(freePort(t)))
	pid := livePID(t)
	writePIDFile(t, strconv.Itoa(pid))

	held, release := holdPort(t)
	defer release()

	start := time.Now()
	res, err := Restart(localURL(held), nil)
	if err == nil {
		t.Cleanup(func() { stopPID(t, res.PID) })
		t.Fatal("Restart should fail while a foreign listener keeps the port")
	}
	if time.Since(start) < StopTimeout-time.Second {
		t.Fatalf("Restart returned after %s, want it to wait about %s", time.Since(start), StopTimeout)
	}
	if !strings.Contains(err.Error(), "still in use") || res.PID != pid {
		t.Fatalf("Restart = %+v, %v; want the old pid and a port-busy error", res, err)
	}
	if proc.Alive(pid) {
		t.Fatal("old daemon should have been stopped before the port wait")
	}
}
