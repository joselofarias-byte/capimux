package promptinject

import (
	json "encoding/json/v2"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// KV scopes. Adding the feature adds rows only under these scopes; removing
// it (rollback) is `DELETE FROM kv WHERE scope IN ('promptInjection','comboIdentity')`.
const (
	KVScopeInjection = "promptInjection"
	KVScopeIdentity  = "comboIdentity"
)

// Settings keys. Every toggle is off by default, so an install that never
// touches them behaves exactly as before.
const (
	SettingInjectionEnabled = "promptInjectionEnabled"
	SettingIdentityEnabled  = "comboPublicIdentityEnabled"
	SettingCaptureEnabled   = "promptInjectionCaptureEnabled"
)

// SettingKeys lists the toggles so the settings guard can protect them.
var SettingKeys = []string{SettingInjectionEnabled, SettingIdentityEnabled, SettingCaptureEnabled}

// Backend is the slice of db.Repo the store needs.
type Backend interface {
	GetKVScope(scope string) (map[string]string, error)
	SetKV(scope, key, value string) error
	DeleteKV(scope, key string) error
	GetSettingsRaw() (map[string]any, error)
}

// Toggles are the independent feature switches.
type Toggles struct {
	Injection bool `json:"promptInjectionEnabled"`
	Identity  bool `json:"comboPublicIdentityEnabled"`
	Capture   bool `json:"promptInjectionCaptureEnabled"`
}

// ReadToggles reads the switches; any read error means "off".
func ReadToggles(b Backend) Toggles {
	raw, err := b.GetSettingsRaw()
	if err != nil || raw == nil {
		return Toggles{}
	}
	on := func(k string) bool { v, _ := raw[k].(bool); return v }
	return Toggles{Injection: on(SettingInjectionEnabled), Identity: on(SettingIdentityEnabled), Capture: on(SettingCaptureEnabled)}
}

// Identity is the public model name shown to clients of one combo. Routing,
// usage, logs and statistics keep the real provider/model; this only changes
// the "model" field of the response the client reads.
type Identity struct {
	Combo       string `json:"combo"`
	PublicModel string `json:"publicModel"`
	Enabled     bool   `json:"enabled"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
}

// Store caches both scopes for a short time; writes through this store
// invalidate the cache immediately.
type Store struct {
	b   Backend
	ttl time.Duration

	mu         sync.Mutex
	loadedAt   time.Time
	entries    map[string]Entry
	identities map[string]Identity
}

// NewStore returns a store over b.
func NewStore(b Backend) *Store { return &Store{b: b, ttl: 2 * time.Second} }

func (s *Store) invalidate() {
	s.mu.Lock()
	s.loadedAt = time.Time{}
	s.mu.Unlock()
}

func (s *Store) load() (map[string]Entry, map[string]Identity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loadedAt.IsZero() && time.Since(s.loadedAt) < s.ttl {
		return s.entries, s.identities, nil
	}
	rawE, err := s.b.GetKVScope(KVScopeInjection)
	if err != nil {
		return nil, nil, err
	}
	rawI, err := s.b.GetKVScope(KVScopeIdentity)
	if err != nil {
		return nil, nil, err
	}
	entries := make(map[string]Entry, len(rawE))
	for k, v := range rawE {
		var e Entry
		if json.Unmarshal([]byte(v), &e) == nil {
			entries[k] = e
		}
	}
	ids := make(map[string]Identity, len(rawI))
	for k, v := range rawI {
		var id Identity
		if json.Unmarshal([]byte(v), &id) == nil {
			ids[k] = id
		}
	}
	s.entries, s.identities, s.loadedAt = entries, ids, time.Now()
	return entries, ids, nil
}

// Entries returns all stored layers sorted by scope order then target.
func (s *Store) Entries() ([]Entry, error) {
	m, _, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(m))
	for _, e := range m {
		out = append(out, e)
	}
	rank := map[Scope]int{}
	for i, sc := range scopeOrder {
		rank[sc] = i
	}
	sort.Slice(out, func(i, j int) bool {
		if rank[out[i].Scope] != rank[out[j].Scope] {
			return rank[out[i].Scope] < rank[out[j].Scope]
		}
		return out[i].Target < out[j].Target
	})
	return out, nil
}

// Match returns the plan for req.
func (s *Store) Match(req Request) (Plan, error) {
	m, _, err := s.load()
	if err != nil {
		return Plan{}, err
	}
	return Compose(Resolve(m, req)), nil
}

// Put validates and stores e.
func (s *Store) Put(e Entry) (Entry, error) {
	e, err := e.Normalize()
	if err != nil {
		return e, err
	}
	e.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	b, err := json.Marshal(e)
	if err != nil {
		return e, err
	}
	if err := s.b.SetKV(KVScopeInjection, StorageKey(e.Scope, e.Target), string(b)); err != nil {
		return e, err
	}
	s.invalidate()
	return e, nil
}

// Delete removes one layer.
func (s *Store) Delete(scope Scope, target string) error {
	if !ValidScope(scope) {
		return fmt.Errorf("%w: alcance desconocido %q", ErrInvalid, scope)
	}
	if err := s.b.DeleteKV(KVScopeInjection, StorageKey(scope, strings.TrimSpace(target))); err != nil {
		return err
	}
	s.invalidate()
	return nil
}

// Identities returns every configured combo identity.
func (s *Store) Identities() ([]Identity, error) {
	_, m, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]Identity, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Combo < out[j].Combo })
	return out, nil
}

// IdentityFor returns the enabled public name of combo, if any.
func (s *Store) IdentityFor(combo string) (string, bool) {
	_, m, err := s.load()
	if err != nil {
		return "", false
	}
	id, ok := m[combo]
	if !ok || !id.Enabled || id.PublicModel == "" {
		return "", false
	}
	return id.PublicModel, true
}

// PutIdentity validates and stores a combo identity.
func (s *Store) PutIdentity(id Identity) (Identity, error) {
	id.Combo = strings.TrimSpace(id.Combo)
	id.PublicModel = strings.TrimSpace(id.PublicModel)
	if id.Combo == "" || len(id.Combo) > MaxTargetLen {
		return id, fmt.Errorf("%w: combo inválido", ErrInvalid)
	}
	if id.PublicModel == "" || len(id.PublicModel) > 200 || !utf8.ValidString(id.PublicModel) ||
		strings.ContainsAny(id.PublicModel, "\x00\r\n\"") {
		return id, fmt.Errorf("%w: nombre público inválido", ErrInvalid)
	}
	id.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	b, err := json.Marshal(id)
	if err != nil {
		return id, err
	}
	if err := s.b.SetKV(KVScopeIdentity, id.Combo, string(b)); err != nil {
		return id, err
	}
	s.invalidate()
	return id, nil
}

// DeleteIdentity removes a combo identity.
func (s *Store) DeleteIdentity(combo string) error {
	if err := s.b.DeleteKV(KVScopeIdentity, strings.TrimSpace(combo)); err != nil {
		return err
	}
	s.invalidate()
	return nil
}
