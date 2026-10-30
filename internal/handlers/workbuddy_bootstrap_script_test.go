package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"9router/proxy/internal/auth"
	"9router/proxy/internal/db"
)

const (
	wbClientKey  = "wb-client-synthetic-key"
	wbUpstreamKy = "wb-upstream-synthetic-key"
)

// wbEnv is a real router (SQLite, auth, SetupServerRouter) behind a local
// HTTP server, with /chat/completions answered by a stub so the script's
// final probe never leaves the machine.
type wbEnv struct {
	repo     *db.Repo
	srv      *httptest.Server
	dataDir  string
	probeHdr string
}

func newWorkbuddyEnv(t *testing.T, settings map[string]any) *wbEnv {
	t.Helper()
	for _, tool := range []string{"bash", "jq", "curl", "sha256sum"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	if runtime.GOOS == "windows" {
		t.Skip("bash script")
	}
	if _, err := exec.LookPath("pkg"); err == nil {
		t.Skip("Termux pkg on PATH: the script would install packages")
	}
	env := &wbEnv{dataDir: t.TempDir()}
	t.Setenv("JWT_SECRET", "workbuddy-script-test-secret")
	t.Setenv("DATA_DIR", env.dataDir)
	database, cleanup := setupTestDB(t)
	t.Cleanup(cleanup)
	if err := db.EnsureCoreSchema(database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO apiKeys (id, key, name, isActive, createdAt) VALUES ('wb', ?, 'wb', 1, '2026-01-01T00:00:00Z')`, wbClientKey); err != nil {
		t.Fatal(err)
	}
	env.repo = db.NewRepo(database)
	all := map[string]any{"requireLogin": true}
	for k, v := range settings {
		all[k] = v
	}
	if err := env.repo.UpdateSettingsRaw(all); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	SetupServerRouter(r, env.repo, nil)
	env.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/chat/completions" {
			env.probeHdr = req.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"model":"gpt-5.6-luna","choices":[{"message":{"role":"assistant","content":"WB_OK"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
			return
		}
		r.ServeHTTP(w, req)
	}))
	t.Cleanup(env.srv.Close)
	return env
}

// run executes scripts/workbuddy-termux-bootstrap.sh as a user would, with
// the upstream key on stdin and a stub codebuddy CLI so nothing is installed.
func (e *wbEnv) run(t *testing.T, extraEnv ...string) (string, error) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codebuddy"), []byte("#!/bin/sh\necho 0.0.0-test\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	cmd := exec.Command("bash", filepath.Join("..", "..", "scripts", "workbuddy-termux-bootstrap.sh"))
	cmd.Env = append([]string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + home,
		"ROUTER_BASE_URL=" + e.srv.URL,
		"DATA_DIR=" + e.dataDir,
	}, extraEnv...)
	cmd.Stdin = strings.NewReader(wbUpstreamKy + "\n")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	output := out.String()
	for _, secret := range []string{wbUpstreamKy, wbClientKey, auth.CLIToken()} {
		if strings.Contains(output, secret) {
			t.Fatalf("the script printed a credential:\n%s", output)
		}
	}
	return output, err
}

func (e *wbEnv) workbuddyRows(t *testing.T) []map[string]any {
	t.Helper()
	rows, err := e.repo.RawDB().Query(`SELECT data FROM providerConnections WHERE provider = 'codebuddy-intl'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			t.Fatal(err)
		}
		m := map[string]any{}
		_ = json.Unmarshal([]byte(data), &m)
		out = append(out, m)
	}
	return out
}

// The bootstrap imports with the local admin token, so it works even when an
// admin closed client-key imports, and still probes with the client key.
func TestWorkbuddyBootstrapImportsWithAdminToken(t *testing.T) {
	e := newWorkbuddyEnv(t, map[string]any{"allowClientCredentialImport": false})
	out, err := e.run(t, "NINEROUTER_API_KEY="+wbClientKey)
	if err != nil {
		t.Fatalf("bootstrap failed: %v\n%s", err, out)
	}
	for _, want := range []string{"Conexión creada: codebuddy-intl-import-", "Probe OK"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	rows := e.workbuddyRows(t)
	if len(rows) != 1 || rows[0]["apiKey"] != wbUpstreamKy {
		t.Fatalf("stored rows = %v", rows)
	}
	if e.probeHdr != "Bearer "+wbClientKey {
		t.Errorf("probe Authorization = %q, want the client key", e.probeHdr)
	}
}

// Without the admin token (another DATA_DIR) the import is refused and the
// script says how to fix it; a client API key alone does not import.
func TestWorkbuddyBootstrapWithoutAdminTokenFailsClearly(t *testing.T) {
	e := newWorkbuddyEnv(t, map[string]any{"allowClientCredentialImport": false})
	out, err := e.run(t, "NINEROUTER_API_KEY="+wbClientKey, "DATA_DIR="+t.TempDir())
	if err == nil {
		t.Fatalf("bootstrap succeeded without the admin token:\n%s", out)
	}
	if !strings.Contains(out, "token de administrador") {
		t.Errorf("missing guidance:\n%s", out)
	}
	if rows := e.workbuddyRows(t); len(rows) != 0 {
		t.Fatalf("a connection was stored: %v", rows)
	}
}

// Admin token files readable by others are refused before any request uses
// them.
func TestWorkbuddyBootstrapRefusesOpenTokenFiles(t *testing.T) {
	e := newWorkbuddyEnv(t, nil)
	_ = auth.CLIToken() // the router creates the files on first use
	if err := os.Chmod(filepath.Join(e.dataDir, "auth", "cli-secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := e.run(t)
	if err == nil || !strings.Contains(out, "permisos demasiado abiertos") {
		t.Fatalf("expected a permissions error, err=%v:\n%s", err, out)
	}
	if rows := e.workbuddyRows(t); len(rows) != 0 {
		t.Fatalf("a connection was stored: %v", rows)
	}
}
