package headroom

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"9router/proxy/internal/proc"
)

func releaseChild(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	t.Cleanup(func() {
		if cmd.Process == nil {
			return
		}
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
}

func cleanupRecordedPid(t *testing.T, path string) {
	t.Helper()
	t.Cleanup(func() {
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || pid <= 0 {
			return
		}
		proc.Terminate(pid, 500)
	})
}

func startSleep(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("sleep: %v", err)
	}
	releaseChild(t, cmd)
	return cmd
}

func TestLocalStartMissingBinary(t *testing.T) {
	if got := FindHeadroomBinary(); got != "" {
		t.Fatalf("FindHeadroomBinary() = %q; refusing to spawn a real CLI", got)
	}
	_, err := StartHeadroomProxy(t.TempDir(), 8787, false, true)
	if !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("StartHeadroomProxy() error = %v", err)
	}
	if CodeOf(err) != "NOT_INSTALLED" {
		t.Fatalf("CodeOf = %q", CodeOf(err))
	}
}

func TestLocalStartAlreadyRunning(t *testing.T) {
	installFakeCLI(t, false)
	data := t.TempDir()
	child := startSleep(t)
	if err := writePid(data, child.Process.Pid); err != nil {
		t.Fatal(err)
	}
	got, err := StartHeadroomProxy(data, 1, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if !got.AlreadyRunning || got.PID != child.Process.Pid {
		t.Fatalf("start = %+v, want already running pid %d", got, child.Process.Pid)
	}
	if _, err := os.Stat(filepath.Join(headroomDir(data), "proxy.log")); !os.IsNotExist(err) {
		t.Fatal("already-running start spawned a new proxy")
	}
}

func TestLocalStartEarlyExit(t *testing.T) {
	installFakeCLI(t, false)
	tests := []struct {
		name     string
		port     int
		code     bool
		kompress bool
		exitCode string
		wantArgs string
	}{
		{
			name:     "invalid low port adds both flags",
			port:     0,
			code:     true,
			kompress: false,
			exitCode: "0",
			wantArgs: "proxy --port 8787 --code-aware --disable-kompress",
		},
		{
			name:     "invalid high port keeps defaults",
			port:     65536,
			code:     false,
			kompress: true,
			exitCode: "0",
			wantArgs: "proxy --port 8787",
		},
		{
			name:     "top valid port with code aware",
			port:     65535,
			code:     true,
			kompress: true,
			exitCode: "7",
			wantArgs: "proxy --port 65535 --code-aware",
		},
		{
			name:     "kompress disabled",
			port:     8787,
			code:     false,
			kompress: false,
			exitCode: "7",
			wantArgs: "proxy --port 8787 --disable-kompress",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HEADROOM_FAKE_CODE", tt.exitCode)
			data := t.TempDir()
			_, err := StartHeadroomProxy(data, tt.port, tt.code, tt.kompress)
			if !errors.Is(err, ErrEarlyExit) {
				t.Fatalf("error = %v", err)
			}
			if !strings.Contains(err.Error(), "code="+tt.exitCode) {
				t.Fatalf("error = %v, want code=%s", err, tt.exitCode)
			}
			if CodeOf(err) != "EARLY_EXIT" {
				t.Fatalf("CodeOf = %q", CodeOf(err))
			}
			if readPid(data) != 0 {
				t.Fatal("pid file survived an early exit")
			}
			if got := GetHeadroomLogTail(data, 5); got != tt.wantArgs {
				t.Fatalf("proxy log = %q, want %q", got, tt.wantArgs)
			}
		})
	}
}

func TestLocalStartDirectoryErrors(t *testing.T) {
	installFakeCLI(t, false)

	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := StartHeadroomProxy(file, 8787, false, true); err == nil || !strings.Contains(err.Error(), "create headroom dir") {
		t.Fatalf("mkdir error = %v", err)
	}

	blocked := t.TempDir()
	if err := os.MkdirAll(filepath.Join(blocked, "headroom", "proxy.log"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := StartHeadroomProxy(blocked, 8787, false, true); err == nil || !strings.Contains(err.Error(), "open proxy.log") {
		t.Fatalf("open log error = %v", err)
	}
}

func TestLocalStartSpawnError(t *testing.T) {
	dir := t.TempDir()
	writeExe(t, filepath.Join(dir, "headroom"), "#!/no/such/headroom-interpreter\nexit 0\n")
	prependPath(t, dir)
	requireFakeHeadroom(t, dir)

	data := t.TempDir()
	_, err := StartHeadroomProxy(data, 8787, false, true)
	if err == nil || !strings.Contains(err.Error(), "spawn headroom proxy") {
		t.Fatalf("spawn error = %v", err)
	}
	if readPid(data) != 0 {
		t.Fatal("spawn failure wrote a pid file")
	}
}

func TestLocalStartWritePidFailure(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("HEADROOM_FAKE_PIDFILE", pidPath)
	cleanupRecordedPid(t, pidPath)
	installFakeCLI(t, true)

	data := t.TempDir()
	if err := os.MkdirAll(filepath.Join(data, "headroom", "proxy.pid"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := StartHeadroomProxy(data, 8787, false, true)
	if err == nil || !strings.Contains(err.Error(), "write pid file") {
		t.Fatalf("write pid error = %v", err)
	}
}

func TestLocalStartStaysUpThenStop(t *testing.T) {
	// StartupTimeout is an 8s const. The stay-up branch is that timer, so
	// this test waits it out on a local sleep and then stops that process.
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("HEADROOM_FAKE_PIDFILE", pidPath)
	cleanupRecordedPid(t, pidPath)
	installFakeCLI(t, true)

	data := t.TempDir()
	got, err := StartHeadroomProxy(data, 8787, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.AlreadyRunning || got.PID <= 0 {
		t.Fatalf("start = %+v", got)
	}
	if !proc.Alive(got.PID) {
		t.Fatal("proxy was not alive after the startup wait")
	}
	if readPid(data) != got.PID {
		t.Fatalf("pid file = %d, want %d", readPid(data), got.PID)
	}
	if log := GetHeadroomLogTail(data, 5); log != "proxy --port 8787 --code-aware --disable-kompress" {
		t.Fatalf("proxy log = %q", log)
	}

	stopped, err := StopHeadroomProxy(data)
	if err != nil {
		t.Fatal(err)
	}
	if !stopped.Stopped || stopped.PID != got.PID {
		t.Fatalf("stop = %+v", stopped)
	}
	if proc.Alive(got.PID) {
		t.Fatal("proxy still alive after stop")
	}
	if readPid(data) != 0 {
		t.Fatal("pid file survived stop")
	}
}

func TestLocalStopNotRunning(t *testing.T) {
	got, err := StopHeadroomProxy(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got.Stopped || got.Reason != "not_running" {
		t.Fatalf("stop = %+v", got)
	}

	data := t.TempDir()
	if err := writePid(data, 1<<30); err != nil {
		t.Fatal(err)
	}
	got, err = StopHeadroomProxy(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stopped || got.Reason != "not_running" {
		t.Fatalf("dead pid stop = %+v", got)
	}
}

func TestLocalStopSleep(t *testing.T) {
	data := t.TempDir()
	child := startSleep(t)
	if err := writePid(data, child.Process.Pid); err != nil {
		t.Fatal(err)
	}
	got, err := StopHeadroomProxy(data)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Stopped || got.PID != child.Process.Pid {
		t.Fatalf("stop = %+v", got)
	}
	if proc.Alive(child.Process.Pid) {
		t.Fatal("sleep still alive")
	}
	if readPid(data) != 0 {
		t.Fatal("pid file survived stop")
	}
}

func TestLocalRestart(t *testing.T) {
	installFakeCLI(t, false)
	t.Setenv("HEADROOM_FAKE_CODE", "0")

	idle := t.TempDir()
	if _, err := RestartHeadroomProxy(idle, 8787, false, true); !errors.Is(err, ErrEarlyExit) {
		t.Fatalf("restart idle error = %v", err)
	}

	data := t.TempDir()
	child := startSleep(t)
	if err := writePid(data, child.Process.Pid); err != nil {
		t.Fatal(err)
	}
	_, err := RestartHeadroomProxy(data, 9000, true, true)
	if !errors.Is(err, ErrEarlyExit) {
		t.Fatalf("restart running error = %v", err)
	}
	if proc.Alive(child.Process.Pid) {
		t.Fatal("restart left the previous pid alive")
	}
	if got := GetHeadroomLogTail(data, 5); got != "proxy --port 9000 --code-aware" {
		t.Fatalf("restart log = %q", got)
	}
}

func TestLocalInstallAndUninstall(t *testing.T) {
	dir := installFakeCLI(t, false)
	pylog := filepath.Join(dir, "py.log")
	t.Setenv("HEADROOM_FAKE_PYLOG", pylog)
	t.Setenv("HEADROOM_FAKE_LIST_JSON", `[{"name":"headroom-ai","version":"9.9.9"},{"name":"tree-sitter-language-pack","version":"1"},{"name":"huggingface-hub","version":"2"}]`)
	requireFakePython(t, dir)
	requireFakeHeadroom(t, dir)

	data := t.TempDir()
	got, err := InstallHeadroomExtras(data, []string{"ml", "nope", "code"})
	if err != nil {
		t.Fatal(err)
	}
	if got["success"] != true || got["code"] != 0 || got["spec"] != "headroom-ai[proxy,ml,code]" {
		t.Fatalf("install = %#v", got)
	}
	if got["installed"] != true || got["version"] != "9.9.9" {
		t.Fatalf("install status = %#v", got)
	}
	extras, ok := got["extras"].(Extras)
	if !ok || extras != (Extras{Code: true, ML: true}) {
		t.Fatalf("install extras = %#v", got["extras"])
	}
	logBody, err := os.ReadFile(pylog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logBody), "-m pip install --upgrade headroom-ai[proxy,ml,code]") {
		t.Fatalf("fake pip log = %q", logBody)
	}
	if tail := GetInstallLogTail(data, 10); !strings.Contains(tail, "pip install") {
		t.Fatalf("install.log tail = %q", tail)
	}

	removed, err := UninstallHeadroomExtras(data, []string{"code", "ml"})
	if err != nil {
		t.Fatal(err)
	}
	if removed["success"] != true || removed["code"] != 0 {
		t.Fatalf("uninstall = %#v", removed)
	}
	pkgs, ok := removed["removed"].([]string)
	if !ok {
		t.Fatalf("removed = %#v", removed["removed"])
	}
	wantRemoved := []string{"huggingface-hub", "torch", "tree-sitter", "tree-sitter-language-pack"}
	if strings.Join(pkgs, ",") != strings.Join(wantRemoved, ",") {
		t.Fatalf("removed = %v, want %v", pkgs, wantRemoved)
	}
	if !strings.Contains(string(mustRead(t, pylog)), "-m pip uninstall -y huggingface-hub torch tree-sitter tree-sitter-language-pack") {
		t.Fatalf("uninstall was not run by the fake python: %s", mustRead(t, pylog))
	}

	_, err = UninstallHeadroomExtras(t.TempDir(), []string{"nope", "CODE"})
	if !errors.Is(err, ErrInvalidExtras) {
		t.Fatalf("invalid extras error = %v", err)
	}
	if CodeOf(err) != "INVALID_EXTRAS" {
		t.Fatalf("CodeOf after invalid extras = %q", CodeOf(err))
	}

	t.Setenv("HEADROOM_FAKE_PIP_CODE", "3")
	if _, err := InstallHeadroomExtras(t.TempDir(), []string{"code"}); !errors.Is(err, ErrInstallFailed) {
		t.Fatalf("install failure = %v", err)
	} else if CodeOf(err) != "INSTALL_FAILED" {
		t.Fatalf("CodeOf = %q", CodeOf(err))
	}
	if _, err := UninstallHeadroomExtras(t.TempDir(), []string{"ml"}); !errors.Is(err, ErrUninstallFailed) {
		t.Fatalf("uninstall failure = %v", err)
	} else if CodeOf(err) != "UNINSTALL_FAILED" {
		t.Fatalf("CodeOf = %q", CodeOf(err))
	}

	blocked := t.TempDir()
	if err := os.MkdirAll(filepath.Join(blocked, "headroom", "install.log"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallHeadroomExtras(blocked, []string{"code"}); err == nil || errors.Is(err, ErrInstallFailed) {
		t.Fatalf("install log dir error = %v", err)
	}
	if _, err := UninstallHeadroomExtras(blocked, []string{"ml"}); err == nil || errors.Is(err, ErrUninstallFailed) {
		t.Fatalf("uninstall log dir error = %v", err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
