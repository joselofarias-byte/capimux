package media

import (
	"database/sql"
	json "encoding/json/v2"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"9router/proxy/internal/providers"
)

// errReader fails every read so handlers can exercise their body-read branches
// without touching a socket.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read boom") }

func (errReader) Error() string { return "read boom" }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// rewriteTransport sends every request to dest and keeps the original path.
// Callers use it so a handler whose URL is a real provider host still dials
// the loopback httptest server and never the public API.
type rewriteTransport struct {
	dest  *url.URL
	base  http.RoundTripper
	hosts []string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.hosts = append(t.hosts, req.URL.Host)
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.dest.Scheme
	clone.URL.Host = t.dest.Host
	clone.Host = t.dest.Host
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

// newLoopbackClient rewrites every host onto rawURL (an httptest server).
// The returned transport records each host the caller asked for, before the
// rewrite, so a test can see api.vercel.com without dialing it.
func newLoopbackClient(srv interface {
	Client() *http.Client
}, rawURL string) (*http.Client, *rewriteTransport) {
	dest, err := url.Parse(rawURL)
	if err != nil {
		panic(err)
	}
	rt := &rewriteTransport{dest: dest, base: srv.Client().Transport}
	return &http.Client{Transport: rt, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}, rt
}

// swapDirectClient replaces the package-level direct fallback for the test and
// puts the previous client back afterwards. Tests that can fail the first hop
// of doDirectOrClient must point this at a loopback client or a transport that
// errors without dialing, so the retry never leaves the machine.
func swapDirectClient(t *testing.T, client *http.Client) {
	t.Helper()
	prev := directHTTPClient
	directHTTPClient = client
	t.Cleanup(func() { directHTTPClient = prev })
}

func blockDirectDial(t *testing.T) {
	t.Helper()
	swapDirectClient(t, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("offline fake: direct dial blocked")
	})})
}

func resetVoiceCaches(t *testing.T) {
	t.Helper()
	clearVoiceCaches()
	t.Cleanup(clearVoiceCaches)
}

func clearVoiceCaches() {
	voiceCacheMu.Lock()
	edgeVoiceCache = voiceCacheEntry{}
	elevenVoiceCache = map[string]voiceCacheEntry{}
	minimaxVoiceCache = map[string]voiceCacheEntry{}
	deepgramVoiceCache = voiceCacheEntry{}
	inworldVoiceCache = voiceCacheEntry{}
	localVoiceCache = voiceCacheEntry{}
	voiceCacheMu.Unlock()
}

func resetTTSTokens(t *testing.T) {
	t.Helper()
	clearTTSTokens()
	t.Cleanup(clearTTSTokens)
}

func clearTTSTokens() {
	bingTokenMu.Lock()
	bingKey, bingToken, bingCookie = "", "", ""
	bingTokenTime = time.Time{}
	bingTokenMu.Unlock()
	googleTTSMu.Lock()
	googleFSid, googleBl = "", ""
	googleTTSTime = time.Time{}
	googleTTSMu.Unlock()
}

func withProvider(t *testing.T, id string, mutate func(*providers.ProviderConfig)) {
	t.Helper()
	orig := providers.KnownProviders[id]
	cfg := orig
	mutate(&cfg)
	providers.KnownProviders[id] = cfg
	t.Cleanup(func() { providers.KnownProviders[id] = orig })
}

func seedConn(t *testing.T, database *sql.DB, id, provider, apiKey, baseURL string, priority int) {
	t.Helper()
	data := map[string]any{}
	if apiKey != "" {
		data["apiKey"] = apiKey
	}
	if baseURL != "" {
		data["baseUrl"] = baseURL
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal conn data: %v", err)
	}
	_, err = database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		(?, ?, 'apikey', ?, ?, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
		id, provider, provider+" "+id, priority, string(raw))
	if err != nil {
		t.Fatalf("seed connection %s: %v", id, err)
	}
}

func ensureTable(t *testing.T, database *sql.DB, query string) {
	t.Helper()
	if _, err := database.Exec(query); err != nil {
		t.Fatalf("schema: %v", err)
	}
}

func ensureProxyPools(t *testing.T, database *sql.DB) {
	t.Helper()
	ensureTable(t, database, `CREATE TABLE IF NOT EXISTS proxyPools (
		id TEXT PRIMARY KEY,
		isActive INTEGER DEFAULT 1,
		testStatus TEXT,
		data TEXT NOT NULL,
		createdAt TEXT NOT NULL,
		updatedAt TEXT NOT NULL
	)`)
}

func ensureCombos(t *testing.T, database *sql.DB) {
	t.Helper()
	ensureTable(t, database, `CREATE TABLE IF NOT EXISTS combos (
		id TEXT PRIMARY KEY,
		name TEXT UNIQUE NOT NULL,
		kind TEXT,
		models TEXT NOT NULL,
		createdAt TEXT NOT NULL,
		updatedAt TEXT NOT NULL
	)`)
}

func ensureSettings(t *testing.T, database *sql.DB) {
	t.Helper()
	ensureTable(t, database, `CREATE TABLE IF NOT EXISTS settings (
		id INTEGER PRIMARY KEY,
		data TEXT NOT NULL
	)`)
}

func ensureUsageTables(t *testing.T, database *sql.DB) {
	t.Helper()
	ensureTable(t, database, `CREATE TABLE IF NOT EXISTS usageHistory (
		timestamp TEXT,
		provider TEXT,
		model TEXT,
		connectionId TEXT,
		apiKey TEXT,
		endpoint TEXT,
		promptTokens INTEGER,
		completionTokens INTEGER,
		cost REAL,
		status TEXT,
		tokens TEXT,
		meta TEXT
	)`)
	ensureTable(t, database, `CREATE TABLE IF NOT EXISTS requestDetails (
		id TEXT PRIMARY KEY,
		timestamp TEXT,
		provider TEXT,
		model TEXT,
		connectionId TEXT,
		status TEXT,
		data TEXT
	)`)
	ensureTable(t, database, `CREATE TABLE IF NOT EXISTS usageDaily (
		dateKey TEXT PRIMARY KEY,
		data TEXT NOT NULL
	)`)
}

func putSettings(t *testing.T, database *sql.DB, data string) {
	t.Helper()
	ensureSettings(t, database)
	if _, err := database.Exec(`INSERT INTO settings (id, data) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET data = excluded.data`, data); err != nil {
		t.Fatalf("settings: %v", err)
	}
}

func writeExec(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

const fakePythonScript = `#!/bin/sh
here=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
if [ "$1" = "--version" ]; then
  echo "Python 3.12.1"
  exit 0
fi
if [ "$2" = "pip" ] && [ "$3" = "show" ]; then
  echo "Name: headroom-ai"
  exit 0
fi
if [ "$2" = "pip" ] && [ "$3" = "list" ]; then
  printf '%s\n' '[{"name":"headroom-ai","version":"1.2.3"},{"name":"tree-sitter","version":"0.20"},{"name":"torch","version":"2"}]'
  exit 0
fi
if [ "$3" = "install" ] && [ -f "$here/fail-install" ]; then
  echo "install failed" >&2
  exit 3
fi
exit 0
`

const fakeHeadroomSleep = `#!/bin/sh
printf '%s\n' "$*"
exec sleep 60
`

const fakeHeadroomExit7 = `#!/bin/sh
exit 7
`

// installFakeHeadroom puts a headroom binary and a sibling python3 on PATH.
// FindPython310 probes that sibling before /usr/bin, so the real interpreter
// is not executed.
func installFakeHeadroom(t *testing.T, headroomScript string) string {
	t.Helper()
	dir := t.TempDir()
	writeExec(t, dir, "headroom", headroomScript)
	writeExec(t, dir, "python3", fakePythonScript)
	// Keep the system directories on PATH so `which` itself can be executed.
	// The fake directory stays first, and FindPython310 probes the sibling of
	// that headroom binary before /usr/bin.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}
