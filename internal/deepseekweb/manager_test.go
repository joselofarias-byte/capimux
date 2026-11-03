package deepseekweb

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// All credentials below are fake, test-only values. Nothing is contacted:
// the checker is an in-memory mock.
const (
	fakeCredA = "fake-dsh-session-A-NOT-REAL-7f3a91"
	fakeCredB = "fake-dsh-session-B-NOT-REAL-2c8e40"
	fakeCredC = "fake-dsh-session-C-NOT-REAL-rotated"
)

// mockChecker answers per credential and records what it saw.
type mockChecker struct {
	mu      sync.Mutex
	results map[string]CheckResult
	seen    map[string]string // sessionID -> credential received
	block   chan struct{}     // if set, Check waits on it
	started chan string
}

func newMock() *mockChecker {
	return &mockChecker{results: map[string]CheckResult{}, seen: map[string]string{}}
}

func (c *mockChecker) set(cred string, r CheckResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.results[cred] = r
}

func (c *mockChecker) Check(ctx context.Context, id string, cred []byte) CheckResult {
	c.mu.Lock()
	c.seen[id] = string(cred)
	r, ok := c.results[string(cred)]
	block, started := c.block, c.started
	c.mu.Unlock()
	if started != nil {
		started <- id
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return CheckResult{Status: StatusError, Detail: "timeout"}
		}
	}
	if !ok {
		return CheckResult{Status: StatusError, Detail: "credencial rechazada por el mock"}
	}
	return r
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func open(t *testing.T, dir string, chk Checker, clk *clock) *Manager {
	t.Helper()
	o := Options{Dir: dir, Checker: chk}
	if clk != nil {
		o.Now = clk.now
	}
	m, err := Open(o)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func mustCreate(t *testing.T, m *Manager, label, cred string) SessionInfo {
	t.Helper()
	s, err := m.Create(CreateInput{Label: label, Credential: []byte(cred)})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func assertNoSecrets(t *testing.T, what string, data []byte, secrets ...string) {
	t.Helper()
	for _, s := range secrets {
		if strings.Contains(string(data), s) {
			t.Fatalf("%s contains a session credential", what)
		}
	}
}

func TestCredentialEncryptedAtRestWithPrivatePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), DirName)
	m := open(t, dir, newMock(), nil)
	mustCreate(t, m, "cuenta A", fakeCredA)

	raw, err := os.ReadFile(filepath.Join(dir, stateFile))
	if err != nil {
		t.Fatal(err)
	}
	assertNoSecrets(t, "sessions.json", raw, fakeCredA)
	if !strings.Contains(string(raw), "sealedCredential") {
		t.Fatal("no sealed credential stored")
	}
	if runtime.GOOS == "windows" {
		return
	}
	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, stateFile): 0o600, filepath.Join(dir, "secret.key"): 0o600} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != want {
			t.Fatalf("%s mode %o, want %o", filepath.Base(path), st.Mode().Perm(), want)
		}
	}
}

func TestSameCredentialEncryptsDifferentlyAndSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	chk := newMock()
	chk.set(fakeCredA, CheckResult{Status: StatusConnected})
	m := open(t, dir, chk, nil)
	a := mustCreate(t, m, "A1", fakeCredA)
	b := mustCreate(t, m, "A2", fakeCredA)
	if m.st.Sessions[0].Sealed == m.st.Sessions[1].Sealed {
		t.Fatal("identical ciphertexts: nonce reuse")
	}
	m2 := open(t, dir, chk, nil)
	for _, id := range []string{a.ID, b.ID} {
		if s, err := m2.Check(context.Background(), id); err != nil || s.Status != StatusConnected {
			t.Fatalf("after reopen: %v %v", s.Status, err)
		}
	}
}

func TestResponsesNeverCarryTheCredential(t *testing.T) {
	m := open(t, t.TempDir(), newMock(), nil)
	s := mustCreate(t, m, "A", fakeCredA)
	got, _ := m.Get(s.ID)
	for name, v := range map[string]any{"create": s, "get": got, "list": m.List()} {
		b, _ := json.Marshal(v)
		assertNoSecrets(t, name, b, fakeCredA)
		if name != "list" && (!strings.Contains(string(b), CredentialMask) || strings.Contains(string(b), "sealed")) {
			t.Fatalf("%s: credential not masked or ciphertext exposed: %s", name, b)
		}
	}
}

func TestCiphertextIsBoundToItsSession(t *testing.T) {
	dir := t.TempDir()
	chk := newMock()
	chk.set(fakeCredA, CheckResult{Status: StatusConnected})
	chk.set(fakeCredB, CheckResult{Status: StatusConnected})
	m := open(t, dir, chk, nil)
	a := mustCreate(t, m, "A", fakeCredA)
	b := mustCreate(t, m, "B", fakeCredB)
	// Tamper: copy A's ciphertext into B on disk.
	var st state
	raw, _ := os.ReadFile(filepath.Join(dir, stateFile))
	_ = json.Unmarshal(raw, &st)
	st.Sessions[1].Sealed = st.Sessions[0].Sealed
	raw, _ = json.Marshal(st)
	if err := os.WriteFile(filepath.Join(dir, stateFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	m2 := open(t, dir, chk, nil)
	if s, _ := m2.Check(context.Background(), b.ID); s.Status != StatusError {
		t.Fatalf("swapped ciphertext accepted: %v", s.Status)
	}
	if chk.seen[b.ID] == fakeCredA {
		t.Fatal("A's credential was decrypted under B")
	}
	if s, _ := m2.Check(context.Background(), a.ID); s.Status != StatusConnected {
		t.Fatalf("A affected by B's tampering: %v", s.Status)
	}
}

func TestWrongKeyCannotDecrypt(t *testing.T) {
	dir := t.TempDir()
	m := open(t, dir, newMock(), nil)
	s := mustCreate(t, m, "A", fakeCredA)
	if err := os.WriteFile(filepath.Join(dir, "secret.key"), []byte("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	chk := newMock()
	m2 := open(t, dir, chk, nil)
	got, _ := m2.Check(context.Background(), s.ID)
	if got.Status != StatusError || chk.seen[s.ID] != "" {
		t.Fatalf("decrypted with the wrong key: %v", got.Status)
	}
}

func TestMultipleSessionsAreIsolated(t *testing.T) {
	chk := newMock()
	chk.set(fakeCredA, CheckResult{Status: StatusConnected})
	chk.set(fakeCredB, CheckResult{Status: StatusExpired, Detail: "vencida"})
	m := open(t, t.TempDir(), chk, nil)
	a := mustCreate(t, m, "A", fakeCredA)
	b := mustCreate(t, m, "B", fakeCredB)
	sa, _ := m.Check(context.Background(), a.ID)
	sb, _ := m.Check(context.Background(), b.ID)
	if sa.Status != StatusConnected || sb.Status != StatusExpired {
		t.Fatalf("statuses %v %v", sa.Status, sb.Status)
	}
	if chk.seen[a.ID] != fakeCredA || chk.seen[b.ID] != fakeCredB {
		t.Fatal("checker received another session's credential")
	}
	if err := m.Delete(b.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Get(a.ID); got.Status != StatusConnected || got.Rev != sa.Rev {
		t.Fatalf("deleting B changed A: %+v", got)
	}
	if _, err := m.Get(b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("B still present")
	}
}

func TestExpiryMarksSessionExpiredAndClosesFallback(t *testing.T) {
	clk := &clock{t: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)}
	chk := newMock()
	exp := clk.now().Add(time.Hour)
	chk.set(fakeCredA, CheckResult{Status: StatusConnected, ExpiresAt: &exp})
	m := open(t, t.TempDir(), chk, clk)
	a := mustCreate(t, m, "A", fakeCredA)
	if _, err := m.SetFallback(true); err != nil {
		t.Fatal(err)
	}
	if s, _ := m.Check(context.Background(), a.ID); s.Status != StatusConnected || s.ExpiresAt == nil {
		t.Fatalf("check: %+v", s)
	}
	if !m.FallbackAllowed() {
		t.Fatal("connected session + toggle should allow fallback")
	}
	clk.add(time.Hour)
	if s, _ := m.Get(a.ID); s.Status != StatusExpired {
		t.Fatalf("status after expiry: %v", s.Status)
	}
	if m.FallbackAllowed() {
		t.Fatal("expired session still allows the paid fallback")
	}
	// Admin-provided expiry in the past: expired from the start.
	past := clk.now().Add(-time.Minute)
	b, _ := m.Create(CreateInput{Label: "B", Credential: []byte(fakeCredB), ExpiresAt: &past})
	if b.Status != StatusExpired {
		t.Fatalf("past expiry not honoured: %v", b.Status)
	}
}

func TestRotationReencryptsResetsStatusAndUsesNewCredential(t *testing.T) {
	dir := t.TempDir()
	chk := newMock()
	chk.set(fakeCredA, CheckResult{Status: StatusConnected})
	chk.set(fakeCredC, CheckResult{Status: StatusConnected})
	m := open(t, dir, chk, nil)
	a := mustCreate(t, m, "A", fakeCredA)
	a, _ = m.Check(context.Background(), a.ID)
	oldSealed := m.st.Sessions[0].Sealed
	if _, err := m.Update(a.ID, UpdateInput{Rev: a.Rev - 1, Credential: []byte(fakeCredC)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale rev accepted: %v", err)
	}
	r, err := m.Update(a.ID, UpdateInput{Rev: a.Rev, Credential: []byte(fakeCredC)})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusUnknown || r.LastCheck != nil || m.st.Sessions[0].Sealed == oldSealed {
		t.Fatalf("rotation did not reset status or re-encrypt: %+v", r)
	}
	if _, err := m.Check(context.Background(), a.ID); err != nil || chk.seen[a.ID] != fakeCredC {
		t.Fatal("check did not use the rotated credential")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, stateFile))
	assertNoSecrets(t, "sessions.json", raw, fakeCredA, fakeCredC)
}

func TestStaleCheckDoesNotOverwriteRotatedCredential(t *testing.T) {
	chk := newMock()
	chk.set(fakeCredA, CheckResult{Status: StatusConnected})
	chk.block, chk.started = make(chan struct{}), make(chan string, 1)
	m := open(t, t.TempDir(), chk, nil)
	a := mustCreate(t, m, "A", fakeCredA)
	done := make(chan error, 1)
	go func() { _, err := m.Check(context.Background(), a.ID); done <- err }()
	waitStarted(t, chk.started)
	if _, err := m.Update(a.ID, UpdateInput{Rev: a.Rev, Credential: []byte(fakeCredC)}); err != nil {
		t.Fatal(err)
	}
	close(chk.block)
	if err := <-done; !errors.Is(err, ErrConflict) {
		t.Fatalf("stale check result: %v", err)
	}
	if s, _ := m.Get(a.ID); s.Status != StatusUnknown {
		t.Fatalf("stale check wrote status %v for the new credential", s.Status)
	}
}

func TestConcurrentChecksSameSessionAreSerializedOthersIndependent(t *testing.T) {
	chk := newMock()
	chk.set(fakeCredA, CheckResult{Status: StatusConnected})
	chk.set(fakeCredB, CheckResult{Status: StatusConnected})
	chk.block, chk.started = make(chan struct{}), make(chan string, 2)
	m := open(t, t.TempDir(), chk, nil)
	a := mustCreate(t, m, "A", fakeCredA)
	b := mustCreate(t, m, "B", fakeCredB)
	errs := make(chan error, 2)
	go func() { _, err := m.Check(context.Background(), a.ID); errs <- err }()
	waitStarted(t, chk.started)
	if _, err := m.Check(context.Background(), a.ID); !errors.Is(err, ErrCheckInProgress) {
		t.Fatalf("second concurrent check of A: %v", err)
	}
	go func() { _, err := m.Check(context.Background(), b.ID); errs <- err }()
	waitStarted(t, chk.started) // B's check runs while A's is still blocked
	// Edits and reads are not blocked by a running check.
	if _, err := m.Update(b.ID, UpdateInput{Rev: b.Rev, Label: ptr("B2")}); err != nil {
		t.Fatal(err)
	}
	close(chk.block)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrentEditsUnderRace(t *testing.T) {
	m := open(t, t.TempDir(), newMock(), nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := m.Create(CreateInput{Label: "S", Credential: []byte(fakeCredA)})
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = m.Check(context.Background(), s.ID)
			_ = m.List()
			_, _ = m.SetFallback(i%2 == 0)
		}(i)
	}
	wg.Wait()
	if len(m.List()) != 8 {
		t.Fatalf("lost sessions: %d", len(m.List()))
	}
}

func TestFallbackRequiresToggleAndConnectedSession(t *testing.T) {
	chk := newMock()
	chk.set(fakeCredA, CheckResult{Status: StatusConnected})
	m := open(t, t.TempDir(), chk, nil)
	if m.FallbackAllowed() || m.Fallback().Requested {
		t.Fatal("fallback on by default")
	}
	a := mustCreate(t, m, "A", fakeCredA)
	if _, err := m.Check(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	if m.FallbackAllowed() {
		t.Fatal("connected session alone enabled the paid fallback (no explicit toggle)")
	}
	fs, _ := m.SetFallback(true)
	if !fs.Effective || !m.FallbackAllowed() {
		t.Fatal("toggle + connected session should be effective")
	}
	cur, _ := m.Get(a.ID)
	off := false
	if _, err := m.Update(a.ID, UpdateInput{Rev: cur.Rev, Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if m.FallbackAllowed() {
		t.Fatal("disabled session still allows fallback")
	}
	if _, err := m.Check(context.Background(), a.ID); !errors.Is(err, ErrSessionDisabled) {
		t.Fatalf("check of disabled session: %v", err)
	}
	_ = m.Delete(a.ID)
	if fs := m.Fallback(); fs.Effective || !fs.Requested {
		t.Fatalf("after delete: %+v", fs)
	}
}

func TestDefaultCheckerNeverConnectsSoFallbackStaysOff(t *testing.T) {
	m := open(t, t.TempDir(), nil, nil)
	a := mustCreate(t, m, "A", fakeCredA)
	_, _ = m.SetFallback(true)
	s, err := m.Check(context.Background(), a.ID)
	if err != nil || s.Status != StatusUnknown || !strings.Contains(s.LastError, "auditoría") {
		t.Fatalf("default checker: %+v %v", s, err)
	}
	if m.FallbackAllowed() {
		t.Fatal("pending-audit checker enabled the paid fallback")
	}
}

func TestCheckDetailIsRedactedAndCapped(t *testing.T) {
	chk := newMock()
	chk.set(fakeCredA, CheckResult{Status: StatusError, Detail: "upstream dijo: " + fakeCredA + " " + strings.Repeat("x", 500)})
	m := open(t, t.TempDir(), chk, nil)
	a := mustCreate(t, m, "A", fakeCredA)
	s, _ := m.Check(context.Background(), a.ID)
	if strings.Contains(s.LastError, fakeCredA) || len([]rune(s.LastError)) > maxDetailRunes+1 {
		t.Fatalf("detail not redacted/capped: %q", s.LastError)
	}
}

func TestValidation(t *testing.T) {
	m := open(t, t.TempDir(), newMock(), nil)
	for name, in := range map[string]CreateInput{
		"empty label":   {Label: " ", Credential: []byte(fakeCredA)},
		"long label":    {Label: strings.Repeat("a", MaxLabelRunes+1), Credential: []byte(fakeCredA)},
		"newline label": {Label: "a\nb", Credential: []byte(fakeCredA)},
		"empty cred":    {Label: "a", Credential: []byte("  ")},
		"huge cred":     {Label: "a", Credential: make([]byte, MaxCredentialBytes+1)},
	} {
		if _, err := m.Create(in); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestLegacyPermissionsAreTightened(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := t.TempDir()
	m := open(t, dir, newMock(), nil)
	mustCreate(t, m, "A", fakeCredA)
	_ = os.Chmod(dir, 0o755)
	_ = os.Chmod(filepath.Join(dir, "secret.key"), 0o644)
	_ = os.Chmod(filepath.Join(dir, stateFile), 0o644)
	open(t, dir, newMock(), nil)
	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "secret.key"): 0o600, filepath.Join(dir, stateFile): 0o600} {
		st, _ := os.Stat(path)
		if st.Mode().Perm() != want {
			t.Fatalf("%s not tightened: %o", filepath.Base(path), st.Mode().Perm())
		}
	}
}

func TestExternalKeyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	keyFile := filepath.Join(t.TempDir(), "k")
	if err := os.WriteFile(keyFile, []byte("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := Open(Options{Dir: dir, KeyFile: keyFile}); err == nil {
		t.Fatal("world-readable external key accepted")
	}
	_ = os.Chmod(keyFile, 0o600)
	m, err := Open(Options{Dir: dir, KeyFile: keyFile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "secret.key")); err == nil {
		t.Fatal("local key created despite external custody")
	}
	if err := m.RotateKey(); err == nil {
		t.Fatal("rotating an external key should be refused")
	}
}

func TestRotateKeyReencryptsEverything(t *testing.T) {
	dir := t.TempDir()
	chk := newMock()
	chk.set(fakeCredA, CheckResult{Status: StatusConnected})
	chk.set(fakeCredB, CheckResult{Status: StatusConnected})
	m := open(t, dir, chk, nil)
	a := mustCreate(t, m, "A", fakeCredA)
	b := mustCreate(t, m, "B", fakeCredB)
	oldKey, _ := os.ReadFile(filepath.Join(dir, "secret.key"))
	if err := m.RotateKey(); err != nil {
		t.Fatal(err)
	}
	newKey, _ := os.ReadFile(filepath.Join(dir, "secret.key"))
	if string(oldKey) == string(newKey) {
		t.Fatal("key not rotated")
	}
	m2 := open(t, dir, chk, nil)
	for id, want := range map[string]string{a.ID: fakeCredA, b.ID: fakeCredB} {
		if s, _ := m2.Check(context.Background(), id); s.Status != StatusConnected || chk.seen[id] != want {
			t.Fatalf("after key rotation %s: %v", id, s.Status)
		}
	}
}

func TestDefaultGateCreatesNothingWithoutState(t *testing.T) {
	base := t.TempDir()
	SetDefault(nil)
	SetDataDirResolver(func() string { return base })
	t.Cleanup(func() { SetDefault(nil); SetDataDirResolver(nil) })
	if DefaultFallbackAllowed() {
		t.Fatal("fallback allowed with no state")
	}
	if _, err := os.Stat(filepath.Join(base, DirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("gate created the sessions directory")
	}
}

func ptr[T any](v T) *T { return &v }

func waitStarted(t *testing.T, ch chan string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("checker never started (credential could not be decrypted?)")
	}
}
