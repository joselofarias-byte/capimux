package headroom

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Local stand-ins for the headroom CLI and its Python. They never call the
// real interpreter, pip, or the network. Production discovery prefers the
// interpreter that sits next to the headroom binary, so a sibling that
// answers `pip show headroom-ai` returns before /usr/bin is walked.

const fakePythonScript = `#!/bin/sh
printf '%s\n' "$*" >> "${HEADROOM_FAKE_PYLOG:-/dev/null}"
if [ "$1" = "--version" ]; then
  printf '%s\n' "${HEADROOM_FAKE_PYVER:-Python 3.12.0}"
  exit 0
fi
if [ "$1" = "-m" ] && [ "$2" = "pip" ]; then
  case "$3" in
    show)
      if [ "${HEADROOM_FAKE_SHOW:-ok}" = "fail" ]; then
        exit 1
      fi
      printf '%s\n' "Name: headroom-ai"
      exit 0
      ;;
    list)
      if [ "${HEADROOM_FAKE_LIST:-ok}" = "fail" ]; then
        exit 1
      fi
      printf '%s\n' "${HEADROOM_FAKE_LIST_JSON:-[]}"
      exit 0
      ;;
    install|uninstall)
      printf '%s\n' "pip $3"
      exit "${HEADROOM_FAKE_PIP_CODE:-0}"
      ;;
  esac
fi
printf '%s\n' "unexpected python args: $*" >&2
exit 2
`

const fakeHeadroomExitScript = `#!/bin/sh
printf '%s\n' "$*"
exit "${HEADROOM_FAKE_CODE:-0}"
`

const fakeHeadroomStayScript = `#!/bin/sh
printf '%s\n' "$*"
if [ -n "${HEADROOM_FAKE_PIDFILE:-}" ]; then
  printf '%s\n' "$$" > "$HEADROOM_FAKE_PIDFILE"
fi
exec sleep 120
`

func writeExe(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func prependPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// installFakeCLI puts a headroom binary and a sibling python3 on PATH.
// stay keeps the proxy process alive; exit makes it quit immediately.
func installFakeCLI(t *testing.T, stay bool) (dir string) {
	t.Helper()
	dir = t.TempDir()
	body := fakeHeadroomExitScript
	if stay {
		body = fakeHeadroomStayScript
	}
	writeExe(t, filepath.Join(dir, "headroom"), body)
	writeExe(t, filepath.Join(dir, "python3"), fakePythonScript)
	prependPath(t, dir)
	return dir
}

func mustBeInside(t *testing.T, path, dir, what string) {
	t.Helper()
	rel, err := filepath.Rel(dir, path)
	if err != nil || path == "" || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		t.Fatalf("%s resolved to %q, outside the fake dir %s", what, path, dir)
	}
}

// requireFakePython fails the test before any install if discovery did not
// stop on the sibling interpreter.
func requireFakePython(t *testing.T, dir string) string {
	t.Helper()
	got := FindPython310()
	mustBeInside(t, got, dir, "FindPython310")
	return got
}

func requireFakeHeadroom(t *testing.T, dir string) string {
	t.Helper()
	got := FindHeadroomBinary()
	mustBeInside(t, got, dir, "FindHeadroomBinary")
	return got
}

func TestLocalWhichCommandAndPaths(t *testing.T) {
	if whichCommand() != "which" {
		t.Fatalf("whichCommand() = %q, want which", whichCommand())
	}

	t.Setenv("HOME", "")
	for _, dir := range extraBinDirs() {
		if strings.HasSuffix(dir, ".local/bin") {
			t.Fatalf("empty HOME still added %q", dir)
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	var sawLocal bool
	for _, dir := range extraBinDirs() {
		if dir == filepath.Join(home, ".local/bin") {
			sawLocal = true
		}
	}
	if !sawLocal {
		t.Fatal("expected ~/.local/bin when HOME is set")
	}

	t.Setenv("PATH", "")
	if got := extendedPath(); strings.Contains(got, "::") || strings.HasSuffix(got, ":") {
		t.Fatalf("empty PATH was appended: %q", got)
	}
	t.Setenv("PATH", "/custom/bin")
	if !strings.Contains(extendedPath(), "/custom/bin") {
		t.Fatalf("extendedPath() = %q, missing /custom/bin", extendedPath())
	}
}

func TestLocalRunOutput(t *testing.T) {
	out, err := runOutput(context.Background(), "echo", "headroom-local")
	if err != nil {
		t.Fatalf("echo: %v", err)
	}
	if out != "headroom-local" {
		t.Fatalf("echo output = %q", out)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runOutput(ctx, "sleep", "5"); err == nil {
		t.Fatal("canceled runOutput succeeded")
	}
	if _, err := runOutput(context.Background(), "headroom-no-such-binary"); err == nil {
		t.Fatal("missing binary succeeded")
	}
}

func TestLocalFindHeadroomBinary(t *testing.T) {
	if got := FindHeadroomBinary(); got != "" {
		t.Fatalf("FindHeadroomBinary() = %q on a machine with no headroom CLI", got)
	}

	dir := installFakeCLI(t, false)
	got := requireFakeHeadroom(t, dir)
	if filepath.Base(got) != "headroom" {
		t.Fatalf("binary name = %q", filepath.Base(got))
	}
}

func TestLocalPythonCandidates(t *testing.T) {
	if FindHeadroomBinary() != "" {
		t.Fatal("a headroom binary is already on PATH")
	}
	without := pythonCandidates()
	if len(without) == 0 {
		t.Fatal("expected bare candidates")
	}
	for _, c := range without[:3] {
		if strings.Contains(c, "headroom") {
			t.Fatalf("candidate %q looks like a headroom sibling before any binary is installed", c)
		}
	}

	dir := installFakeCLI(t, false)
	with := pythonCandidates()
	want := []string{
		filepath.Join(dir, "python3"),
		filepath.Join(dir, "python3.13"),
		filepath.Join(dir, "python"),
	}
	if len(with) < len(want) {
		t.Fatalf("candidates = %v", with)
	}
	for i, w := range want {
		if with[i] != w {
			t.Fatalf("candidate[%d] = %q, want %q", i, with[i], w)
		}
	}
}

func TestLocalFindPython310Sibling(t *testing.T) {
	dir := installFakeCLI(t, false)
	got := requireFakePython(t, dir)
	if filepath.Base(got) != "python3" {
		t.Fatalf("FindPython310() = %q, want the sibling python3", got)
	}
}

func TestLocalFindPython310SkipsBadCandidates(t *testing.T) {
	dir := t.TempDir()
	// No digits in the version line, then a Python older than 3.10.
	// The third sibling is 3.10 and can see headroom-ai, so the scan stops
	// before extraBinDirs reaches /usr/bin/python3.
	writeExe(t, filepath.Join(dir, "python3"), `#!/bin/sh
if [ "$1" = "--version" ]; then printf '%s\n' "Python"; exit 0; fi
exit 1
`)
	writeExe(t, filepath.Join(dir, "python3.13"), `#!/bin/sh
if [ "$1" = "--version" ]; then printf '%s\n' "Python 2.7.18"; exit 0; fi
exit 1
`)
	writeExe(t, filepath.Join(dir, "python"), `#!/bin/sh
if [ "$1" = "--version" ]; then printf '%s\n' "Python 3.10.0"; exit 0; fi
if [ "$1" = "-m" ] && [ "$2" = "pip" ] && [ "$3" = "show" ]; then exit 0; fi
exit 1
`)
	writeExe(t, filepath.Join(dir, "headroom"), fakeHeadroomExitScript)
	prependPath(t, dir)

	got := requireFakePython(t, dir)
	if filepath.Base(got) != "python" {
		t.Fatalf("FindPython310() = %q, want the sibling python that reports 3.10", got)
	}
}

func TestLocalFindPython310SkipsUnrunnable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "python3"), []byte("not executable"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeExe(t, filepath.Join(dir, "python3.13"), fakePythonScript)
	writeExe(t, filepath.Join(dir, "headroom"), fakeHeadroomExitScript)
	prependPath(t, dir)

	got := requireFakePython(t, dir)
	if filepath.Base(got) != "python3.13" {
		t.Fatalf("FindPython310() = %q, want python3.13", got)
	}
}

func TestLocalFindPython310ShowFailsThenNextSibling(t *testing.T) {
	dir := t.TempDir()
	writeExe(t, filepath.Join(dir, "python3"), `#!/bin/sh
if [ "$1" = "--version" ]; then printf '%s\n' "Python 3.11.4"; exit 0; fi
exit 1
`)
	writeExe(t, filepath.Join(dir, "python3.13"), fakePythonScript)
	writeExe(t, filepath.Join(dir, "headroom"), fakeHeadroomExitScript)
	prependPath(t, dir)

	got := requireFakePython(t, dir)
	if filepath.Base(got) != "python3.13" {
		t.Fatalf("FindPython310() = %q, want python3.13 after pip show failed on python3", got)
	}
}

func TestLocalCodeOf(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", err: nil, want: ""},
		{name: "unknown", err: errors.New("other"), want: ""},
		{name: "not installed", err: ErrNotInstalled, want: "NOT_INSTALLED"},
		{name: "no python", err: ErrNoPython, want: "NO_PYTHON"},
		{name: "invalid extras", err: ErrInvalidExtras, want: "INVALID_EXTRAS"},
		{name: "install", err: fmt.Errorf("pip: %w", ErrInstallFailed), want: "INSTALL_FAILED"},
		{name: "uninstall", err: ErrUninstallFailed, want: "UNINSTALL_FAILED"},
		{name: "stop", err: ErrStopFailed, want: "STOP_FAILED"},
		{name: "early exit", err: fmt.Errorf("start: %w", ErrEarlyExit), want: "EARLY_EXIT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CodeOf(tt.err); got != tt.want {
				t.Fatalf("CodeOf() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLocalParsePortFromURL(t *testing.T) {
	tests := []struct {
		raw  string
		want int
	}{
		{raw: "http://localhost:8787", want: 8787},
		{raw: "http://localhost:8787/health", want: 8787},
		{raw: "http://localhost:65535", want: 65535},
		{raw: "http://localhost", want: 0},
		{raw: "http://localhost:abc", want: 0},
		{raw: "http://localhost:0", want: 0},
		{raw: "http://localhost:65536", want: 0},
		{raw: "http://[::1", want: 0},
		{raw: "%", want: 0},
		{raw: "", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if got := ParsePortFromURL(tt.raw); got != tt.want {
				t.Fatalf("ParsePortFromURL(%q) = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}

func TestLocalIsLoopbackRejectsMalformed(t *testing.T) {
	for _, raw := range []string{"http://[::1", "%", "http://a b"} {
		if IsLoopbackHeadroomUrl(raw) {
			t.Fatalf("IsLoopbackHeadroomUrl(%q) = true for a URL that does not parse", raw)
		}
	}
}

func TestLocalStrOrNil(t *testing.T) {
	if strOrNil("") != nil {
		t.Fatalf("strOrNil empty = %#v", strOrNil(""))
	}
	if strOrNil("headroom") != "headroom" {
		t.Fatalf("strOrNil = %#v", strOrNil("headroom"))
	}
}

func TestLocalParsePipListMarkers(t *testing.T) {
	second := ParsePipList([]byte(`[
		{"name":"Headroom-AI","version":"9.9.9"},
		{"name":"tree-sitter-language-pack","version":"1.0.0"},
		{"name":"huggingface-hub","version":"0.20.0"}
	]`))
	if !second.Installed || second.Version != "9.9.9" || !second.Extras.Code || !second.Extras.ML {
		t.Fatalf("second markers: %+v", second)
	}

	dup := ParsePipList([]byte(`[
		{"name":"headroom-ai","version":"first"},
		{"name":"headroom-ai","version":"second"}
	]`))
	if dup.Version != "first" || dup.Extras.Code || dup.Extras.ML {
		t.Fatalf("duplicate version: %+v", dup)
	}

	if ParsePipList(nil).Installed {
		t.Fatal("nil list reported installed")
	}
	if ParsePipList([]byte(`[]`)).Installed {
		t.Fatal("empty list reported installed")
	}
}

func TestLocalProbeProxyRunning(t *testing.T) {
	if ProbeProxyRunning("") {
		t.Fatal("empty URL reported running")
	}
	if ProbeProxyRunning("http://[::1") {
		t.Fatal("malformed URL reported running")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	if ProbeProxyRunning("http://" + addr) {
		t.Fatal("closed loopback port reported running")
	}

	code := http.StatusNoContent
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)

	if !ProbeProxyRunning(srv.URL + "/") {
		t.Fatal("204 should count as running")
	}
	if gotPath != "/health" {
		t.Fatalf("health path = %q", gotPath)
	}

	code = 299
	if !ProbeProxyRunning(srv.URL) {
		t.Fatal("299 should count as running")
	}
	code = http.StatusMultipleChoices
	if ProbeProxyRunning(srv.URL) {
		t.Fatal("300 should not count as running")
	}
	code = http.StatusBadGateway
	if ProbeProxyRunning(srv.URL) {
		t.Fatal("502 should not count as running")
	}
}

func TestLocalGetInstalledExtrasAndStatus(t *testing.T) {
	dir := installFakeCLI(t, false)
	pylog := filepath.Join(dir, "py.log")
	t.Setenv("HEADROOM_FAKE_PYLOG", pylog)
	t.Setenv("HEADROOM_FAKE_LIST_JSON", `[{"name":"headroom-ai","version":"4.5.6"},{"name":"torch","version":"2.0.0"}]`)
	requireFakePython(t, dir)
	requireFakeHeadroom(t, dir)

	st := GetInstalledHeadroomExtras("")
	if !st.Installed || st.Version != "4.5.6" || st.Extras.Code || !st.Extras.ML {
		t.Fatalf("auto extras = %+v", st)
	}

	explicit := GetInstalledHeadroomExtras(filepath.Join(dir, "python3"))
	if explicit.Version != "4.5.6" || !explicit.Extras.ML {
		t.Fatalf("explicit extras = %+v", explicit)
	}

	t.Setenv("HEADROOM_FAKE_LIST", "fail")
	if failed := GetInstalledHeadroomExtras(filepath.Join(dir, "python3")); failed.Installed {
		t.Fatalf("failed pip list = %+v", failed)
	}
	t.Setenv("HEADROOM_FAKE_LIST", "ok")
	t.Setenv("HEADROOM_FAKE_LIST_JSON", "not-json")
	if bad := GetInstalledHeadroomExtras(filepath.Join(dir, "python3")); bad.Installed {
		t.Fatalf("invalid pip list = %+v", bad)
	}

	missing := GetInstalledHeadroomExtras(filepath.Join(dir, "no-such-python"))
	if missing.Installed || missing.Version != "" {
		t.Fatalf("missing python = %+v", missing)
	}

	t.Setenv("HEADROOM_FAKE_LIST_JSON", `[{"name":"headroom-ai","version":"4.5.6"},{"name":"torch","version":"2.0.0"}]`)
	code := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)

	up := GetHeadroomStatus(srv.URL)
	if up["installed"] != true || up["running"] != true || up["localUrl"] != true || up["canStart"] != true {
		t.Fatalf("status up = %#v", up)
	}
	if up["path"] != filepath.Join(dir, "headroom") || up["python"] != filepath.Join(dir, "python3") {
		t.Fatalf("paths = %#v", up)
	}
	if up["version"] != "4.5.6" {
		t.Fatalf("version = %#v", up["version"])
	}
	extras, ok := up["extras"].(Extras)
	if !ok || extras != (Extras{ML: true}) {
		t.Fatalf("extras = %#v", up["extras"])
	}

	code = http.StatusServiceUnavailable
	down := GetHeadroomStatus(srv.URL)
	if down["running"] != false || down["canStart"] != true {
		t.Fatalf("status down = %#v", down)
	}

	// Empty URL does not dial. localUrl is false, so the proxy must not be startable.
	idle := GetHeadroomStatus("")
	if idle["running"] != false || idle["localUrl"] != false || idle["canStart"] != false || idle["installed"] != true {
		t.Fatalf("status empty url = %#v", idle)
	}
}

func TestLocalFilterValidExtras(t *testing.T) {
	got := filterValidExtras([]string{"nope", "code", "CODE", "code", "ml", ""})
	want := []string{"code", "code", "ml"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("filterValidExtras = %v, want %v", got, want)
	}
	if got := filterValidExtras(nil); len(got) != 0 {
		t.Fatalf("nil extras = %#v", got)
	}
}

func TestLocalRunPip(t *testing.T) {
	dir := t.TempDir()
	py := filepath.Join(dir, "python3")
	writeExe(t, py, `#!/bin/sh
printf '%s\n' "out:$*"
printf '%s\n' "err:$*" >&2
exit "${1:-0}"
`)

	code, err := runPip(dir, py, []string{"0"})
	if err != nil || code != 0 {
		t.Fatalf("runPip success = %d, %v", code, err)
	}
	logPath := filepath.Join(dir, "headroom", "install.log")
	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "out:0") || !strings.Contains(string(body), "err:0") {
		t.Fatalf("install.log = %q", body)
	}

	code, err = runPip(dir, py, []string{"4"})
	if err != nil || code != 4 {
		t.Fatalf("runPip exit = %d, %v", code, err)
	}
	body, err = os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "out:0") {
		t.Fatal("install.log was not truncated on the second run")
	}

	fileData := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(fileData, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runPip(fileData, py, nil); err == nil {
		t.Fatal("runPip on a file data dir succeeded")
	}

	blocked := t.TempDir()
	if err := os.MkdirAll(filepath.Join(blocked, "headroom", "install.log"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runPip(blocked, py, nil); err == nil {
		t.Fatal("runPip with install.log as a directory succeeded")
	}

	if _, err := runPip(t.TempDir(), filepath.Join(dir, "missing-python"), []string{"-m", "pip"}); err == nil {
		t.Fatal("runPip with a missing python succeeded")
	}
}

func TestLocalLogTail(t *testing.T) {
	if logTail(filepath.Join(t.TempDir(), "missing.log"), 5) != "" {
		t.Fatal("missing log was not empty")
	}
	dir := t.TempDir()
	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, fmt.Sprintf("line-%d", i))
	}
	payload := strings.Join(lines, "\r\n") + "\r\n\r\n"
	if err := os.WriteFile(filepath.Join(dir, "proxy.log"), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	// GetHeadroomLogTail reads <data>/headroom/proxy.log. The helper above
	// writes next to dir, so place the file where the exported API looks.
	data := t.TempDir()
	if err := os.MkdirAll(filepath.Join(data, "headroom"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "headroom", "proxy.log"), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "headroom", "install.log"), []byte("install-a\n\ninstall-b\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := GetHeadroomLogTail(data, 0)
	wantLines := lines[len(lines)-15:]
	if got != strings.Join(wantLines, "\n") {
		t.Fatalf("default tail =\n%s\nwant\n%s", got, strings.Join(wantLines, "\n"))
	}
	if GetHeadroomLogTail(data, -3) != got {
		t.Fatal("negative maxLines did not use the default of 15")
	}
	if GetHeadroomLogTail(data, 2) != "line-19\nline-20" {
		t.Fatalf("tail 2 = %q", GetHeadroomLogTail(data, 2))
	}
	if GetInstallLogTail(data, 10) != "install-a\ninstall-b" {
		t.Fatalf("install tail = %q", GetInstallLogTail(data, 10))
	}
	if GetHeadroomLogTail(t.TempDir(), 4) != "" {
		t.Fatal("missing proxy log was not empty")
	}
	if logTail(filepath.Join(dir, "proxy.log"), 2) != "line-19\nline-20" {
		t.Fatalf("direct tail = %q", logTail(filepath.Join(dir, "proxy.log"), 2))
	}
}

func TestLocalPidFile(t *testing.T) {
	if readPid(t.TempDir()) != 0 {
		t.Fatal("missing pid file was not zero")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(headroomDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidFile(dir), []byte("  no\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if readPid(dir) != 0 {
		t.Fatalf("garbage pid = %d", readPid(dir))
	}
	if err := writePid(dir, 4242); err != nil {
		t.Fatal(err)
	}
	if readPid(dir) != 4242 {
		t.Fatalf("readPid = %d", readPid(dir))
	}
	clearPid(dir)
	if readPid(dir) != 0 {
		t.Fatal("pid file survived clearPid")
	}
	clearPid(dir)

	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writePid(file, 1); err == nil {
		t.Fatal("writePid on a file data dir succeeded")
	}

	if GetManagedPid(t.TempDir()) != 0 {
		t.Fatal("missing managed pid")
	}
	if GetManagedPid(dir) != 0 {
		t.Fatal("cleared pid file still looked alive")
	}
	alive := t.TempDir()
	if err := writePid(alive, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if GetManagedPid(alive) != os.Getpid() {
		t.Fatalf("GetManagedPid = %d, want %d", GetManagedPid(alive), os.Getpid())
	}
	dead := t.TempDir()
	if err := writePid(dead, 1<<30); err != nil {
		t.Fatal(err)
	}
	if GetManagedPid(dead) != 0 {
		t.Fatalf("dead pid reported as %d", GetManagedPid(dead))
	}
}
