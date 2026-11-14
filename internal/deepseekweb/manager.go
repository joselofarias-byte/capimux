package deepseekweb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const stateFile = "sessions.json"

type storedSession struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	Enabled    bool       `json:"enabled"`
	Sealed     string     `json:"sealedCredential"` // AES-256-GCM, never plaintext
	Status     Status     `json:"status"`
	LastCheck  *time.Time `json:"lastCheck,omitempty"`
	LastError  string     `json:"lastError,omitempty"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	Rev        int64      `json:"rev"`
	CredentRev int64      `json:"credentialRev"` // bumps on every credential rotation
}

type state struct {
	Version        int              `json:"version"`
	FallbackInferX bool             `json:"fallbackInferX"`
	Sessions       []*storedSession `json:"sessions"`
}

// Options configure a Manager.
type Options struct {
	Dir         string  // DATA_DIR/deepseek-web
	KeyFile     string  // optional external key (EnvKeyFile)
	Checker     Checker // nil = PendingAuditChecker
	Now         func() time.Time
	CheckBudget time.Duration // per check; 0 = DefaultCheckBudget
}

// Manager owns the sessions. Safe for concurrent use.
type Manager struct {
	dir     string
	sealer  *sealer
	checker Checker
	now     func() time.Time
	budget  time.Duration

	external bool // key held outside dir (EnvKeyFile): RotateKey is refused

	mu       sync.RWMutex
	st       state
	checking map[string]bool
}

// Open loads (or initialises) the store. The encryption key is created on
// first use; nothing is contacted over the network.
func Open(o Options) (*Manager, error) {
	if o.Dir == "" {
		return nil, errors.New("directorio de sesiones vacío")
	}
	key, err := loadOrCreateKey(o.Dir, o.KeyFile)
	if err != nil {
		return nil, err
	}
	s, err := newSealer(key)
	if err != nil {
		return nil, err
	}
	m := &Manager{dir: o.Dir, sealer: s, checker: o.Checker, now: o.Now, budget: o.CheckBudget, checking: map[string]bool{}, external: o.KeyFile != ""}
	if m.checker == nil {
		m.checker = PendingAuditChecker()
	}
	if m.now == nil {
		m.now = time.Now
	}
	if m.budget <= 0 {
		m.budget = DefaultCheckBudget
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) path() string { return filepath.Join(m.dir, stateFile) }

func (m *Manager) load() error {
	m.st = state{Version: 1}
	b, err := os.ReadFile(m.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("no se pudo leer el estado de sesiones")
	}
	tightenPrivateFile(m.path())
	var st state
	if err := json.Unmarshal(b, &st); err != nil {
		return errors.New("estado de sesiones corrupto")
	}
	for _, s := range st.Sessions {
		if s == nil || s.ID == "" || !s.Status.valid() {
			return errors.New("estado de sesiones corrupto")
		}
	}
	st.Version = 1
	m.st = st
	return nil
}

// persist writes the state atomically (temp file 0600 + rename). Caller holds mu.
func (m *Manager) persist() error {
	if err := ensurePrivateDir(m.dir); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m.st, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(m.dir, ".sessions-*.tmp")
	if err != nil {
		return errors.New("no se pudo guardar el estado de sesiones")
	}
	tmp := f.Name()
	_ = f.Chmod(0o600)
	_, werr := f.Write(b)
	serr := f.Sync()
	cerr := f.Close()
	if werr != nil || serr != nil || cerr != nil {
		_ = os.Remove(tmp)
		return errors.New("no se pudo guardar el estado de sesiones")
	}
	if err := os.Rename(tmp, m.path()); err != nil {
		_ = os.Remove(tmp)
		return errors.New("no se pudo guardar el estado de sesiones")
	}
	return nil
}

func (m *Manager) find(id string) (int, *storedSession) {
	for i, s := range m.st.Sessions {
		if s.ID == id {
			return i, s
		}
	}
	return -1, nil
}

func (m *Manager) effectiveStatus(s *storedSession) Status {
	if s.ExpiresAt != nil && !m.now().Before(*s.ExpiresAt) {
		return StatusExpired
	}
	return s.Status
}

func (m *Manager) info(s *storedSession) SessionInfo {
	out := SessionInfo{ID: s.ID, Label: s.Label, Enabled: s.Enabled, HasCredential: s.Sealed != "",
		Status: m.effectiveStatus(s), LastError: s.LastError, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, Rev: s.Rev}
	if out.HasCredential {
		out.Credential = CredentialMask
	}
	if s.LastCheck != nil {
		t := *s.LastCheck
		out.LastCheck = &t
	}
	if s.ExpiresAt != nil {
		t := *s.ExpiresAt
		out.ExpiresAt = &t
	}
	return out
}

func validLabel(l string) (string, error) {
	l = strings.TrimSpace(l)
	if l == "" || utf8.RuneCountInString(l) > MaxLabelRunes || strings.ContainsAny(l, "\r\n\t") {
		return "", ErrInvalidLabel
	}
	return l, nil
}

func validCredential(c []byte) error {
	if len(strings.TrimSpace(string(c))) == 0 {
		return ErrEmptyCredential
	}
	if len(c) > MaxCredentialBytes {
		return ErrCredentialTooBig
	}
	return nil
}

func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "dsw_" + hex.EncodeToString(b), nil
}

// List returns all sessions (secret-free), oldest first.
func (m *Manager) List() []SessionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]SessionInfo, 0, len(m.st.Sessions))
	for _, s := range m.st.Sessions {
		out = append(out, m.info(s))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Get returns one session (secret-free).
func (m *Manager) Get(id string) (SessionInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, s := m.find(id)
	if s == nil {
		return SessionInfo{}, ErrNotFound
	}
	return m.info(s), nil
}

// CreateInput is what the admin submits for a new session.
type CreateInput struct {
	Label      string
	Credential []byte
	ExpiresAt  *time.Time
}

// Create adds a session. It starts enabled with status unknown: it is never
// considered connected until a check says so.
func (m *Manager) Create(in CreateInput) (SessionInfo, error) {
	label, err := validLabel(in.Label)
	if err != nil {
		return SessionInfo{}, err
	}
	if err := validCredential(in.Credential); err != nil {
		return SessionInfo{}, err
	}
	id, err := newID()
	if err != nil {
		return SessionInfo{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.st.Sessions) >= MaxSessions {
		return SessionInfo{}, ErrTooManySessions
	}
	sealed, err := m.sealer.seal(id, in.Credential)
	if err != nil {
		return SessionInfo{}, err
	}
	now := m.now().UTC()
	s := &storedSession{ID: id, Label: label, Enabled: true, Sealed: sealed, Status: StatusUnknown,
		ExpiresAt: utcPtr(in.ExpiresAt), CreatedAt: now, UpdatedAt: now, Rev: 1, CredentRev: 1}
	m.st.Sessions = append(m.st.Sessions, s)
	if err := m.persist(); err != nil {
		m.st.Sessions = m.st.Sessions[:len(m.st.Sessions)-1]
		return SessionInfo{}, err
	}
	return m.info(s), nil
}

// UpdateInput changes a session. Nil fields are left as they are. Rev must
// match the current revision (optimistic concurrency).
type UpdateInput struct {
	Rev          int64
	Label        *string
	Credential   []byte // non-nil = rotate the credential
	Enabled      *bool
	ExpiresAt    *time.Time
	ClearExpires bool
}

// Update edits a session. Rotating the credential re-encrypts it with a fresh
// nonce, resets the status to unknown and invalidates in-flight checks.
func (m *Manager) Update(id string, in UpdateInput) (SessionInfo, error) {
	var label string
	var err error
	if in.Label != nil {
		if label, err = validLabel(*in.Label); err != nil {
			return SessionInfo{}, err
		}
	}
	if in.Credential != nil {
		if err := validCredential(in.Credential); err != nil {
			return SessionInfo{}, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var sealed string
	if in.Credential != nil {
		if sealed, err = m.sealer.seal(id, in.Credential); err != nil {
			return SessionInfo{}, err
		}
	}
	_, s := m.find(id)
	if s == nil {
		return SessionInfo{}, ErrNotFound
	}
	if in.Rev != s.Rev {
		return SessionInfo{}, ErrConflict
	}
	prev := *s
	if in.Label != nil {
		s.Label = label
	}
	if in.Credential != nil {
		s.Sealed = sealed
		s.CredentRev++
		s.Status, s.LastCheck, s.LastError = StatusUnknown, nil, ""
	}
	if in.Enabled != nil {
		s.Enabled = *in.Enabled
	}
	if in.ClearExpires {
		s.ExpiresAt = nil
	} else if in.ExpiresAt != nil {
		s.ExpiresAt = utcPtr(in.ExpiresAt)
	}
	s.Rev++
	s.UpdatedAt = m.now().UTC()
	if err := m.persist(); err != nil {
		*s = prev
		return SessionInfo{}, err
	}
	return m.info(s), nil
}

// Delete removes a session and its ciphertext.
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, s := m.find(id)
	if s == nil {
		return ErrNotFound
	}
	prev := m.st.Sessions
	m.st.Sessions = append(append([]*storedSession{}, prev[:i]...), prev[i+1:]...)
	if err := m.persist(); err != nil {
		m.st.Sessions = prev
		return err
	}
	delete(m.checking, id)
	return nil
}

// Check runs the health check for one session. Only one check per session
// runs at a time; checks of different sessions are independent. The result is
// discarded if the credential was rotated or the session deleted meanwhile.
func (m *Manager) Check(ctx context.Context, id string) (SessionInfo, error) {
	m.mu.Lock()
	_, s := m.find(id)
	if s == nil {
		m.mu.Unlock()
		return SessionInfo{}, ErrNotFound
	}
	if !s.Enabled {
		m.mu.Unlock()
		return SessionInfo{}, ErrSessionDisabled
	}
	if m.checking[id] {
		m.mu.Unlock()
		return SessionInfo{}, ErrCheckInProgress
	}
	m.checking[id] = true
	sealed, credRev, sl := s.Sealed, s.CredentRev, m.sealer
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.checking, id)
		m.mu.Unlock()
	}()

	var res CheckResult
	cred, err := sl.open(id, sealed)
	if err != nil {
		res = CheckResult{Status: StatusError, Detail: errCorrupt.Error()}
	} else {
		cctx, cancel := context.WithTimeout(ctx, m.budget)
		res = m.checker.Check(cctx, id, cred)
		if cctx.Err() != nil && res.Status == StatusConnected {
			res = CheckResult{Status: StatusError, Detail: "tiempo de comprobación agotado"}
		}
		cancel()
	}
	if !res.Status.valid() {
		res.Status = StatusUnknown
	}
	res.Detail = sanitizeDetail(res.Detail, cred)
	for i := range cred {
		cred[i] = 0
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	_, s = m.find(id)
	if s == nil {
		return SessionInfo{}, ErrNotFound
	}
	if s.CredentRev != credRev {
		return m.info(s), ErrConflict
	}
	prev := *s
	now := m.now().UTC()
	s.Status, s.LastCheck = res.Status, &now
	s.LastError = ""
	if res.Status != StatusConnected {
		s.LastError = res.Detail
	}
	if res.ExpiresAt != nil {
		s.ExpiresAt = utcPtr(res.ExpiresAt)
	}
	s.Rev++
	if err := m.persist(); err != nil {
		*s = prev
		return SessionInfo{}, err
	}
	return m.info(s), nil
}

// FallbackState describes the InferX fallback toggle.
type FallbackState struct {
	Requested bool   `json:"requested"` // the explicit admin toggle
	Effective bool   `json:"effective"` // requested AND a connected session exists
	Reason    string `json:"reason,omitempty"`
}

// Fallback reports the toggle and whether it is effective right now.
func (m *Manager) Fallback() FallbackState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.fallbackLocked()
}

func (m *Manager) fallbackLocked() FallbackState {
	fs := FallbackState{Requested: m.st.FallbackInferX}
	if !fs.Requested {
		fs.Reason = "respaldo InferX desactivado (requiere habilitación explícita)"
		return fs
	}
	for _, s := range m.st.Sessions {
		if s.Enabled && m.effectiveStatus(s) == StatusConnected {
			fs.Effective = true
			return fs
		}
	}
	fs.Reason = "sin sesión DeepSeek Web conectada: el respaldo InferX no se usa"
	return fs
}

// SetFallback stores the explicit toggle. Enabling it does not make it
// effective by itself: a connected web session is still required.
func (m *Manager) SetFallback(enabled bool) (FallbackState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prev := m.st.FallbackInferX
	m.st.FallbackInferX = enabled
	if err := m.persist(); err != nil {
		m.st.FallbackInferX = prev
		return FallbackState{}, err
	}
	return m.fallbackLocked(), nil
}

// FallbackAllowed is the gate the DeepSeek profile bridge consults before
// moving to InferX. It is read live, so panel changes apply without restart.
func (m *Manager) FallbackAllowed() bool {
	if m == nil {
		return false
	}
	return m.Fallback().Effective
}

// RotateKey re-encrypts every credential under a new random key stored in
// dir/secret.key. Not available with an external key file.
func (m *Manager) RotateKey() error {
	if m.external {
		return errors.New("la clave es externa (" + EnvKeyFile + "): rotarla fuera de Capimux")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	newKey := make([]byte, keySize)
	if _, err := rand.Read(newKey); err != nil {
		return err
	}
	ns, err := newSealer(newKey)
	if err != nil {
		return err
	}
	resealed := make([]string, len(m.st.Sessions))
	for i, s := range m.st.Sessions {
		pt, err := m.sealer.open(s.ID, s.Sealed)
		if err != nil {
			return err
		}
		resealed[i], err = ns.seal(s.ID, pt)
		for j := range pt {
			pt[j] = 0
		}
		if err != nil {
			return err
		}
	}
	keyPath := filepath.Join(m.dir, "secret.key")
	tmpKey := keyPath + ".new"
	if err := writePrivate(tmpKey, newKey); err != nil {
		return err
	}
	old := make([]string, len(m.st.Sessions))
	for i, s := range m.st.Sessions {
		old[i], s.Sealed = s.Sealed, resealed[i]
	}
	if err := m.persist(); err != nil {
		for i, s := range m.st.Sessions {
			s.Sealed = old[i]
		}
		_ = os.Remove(tmpKey)
		return err
	}
	if err := os.Rename(tmpKey, keyPath); err != nil {
		return errors.New("credenciales recifradas pero no se pudo reemplazar la clave; restaurar secret.key.new manualmente")
	}
	m.sealer = ns
	return nil
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
