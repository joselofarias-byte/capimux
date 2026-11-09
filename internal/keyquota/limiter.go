package keyquota

import (
	"fmt"
	"sync"
	"time"

	"9router/proxy/internal/db"
	"9router/proxy/internal/log"
)

// Backend is the persistence the limiter needs; *db.Repo implements it.
type Backend interface {
	GetKVScope(scope string) (map[string]string, error)
	SetKV(scope, key, value string) error
	DeleteKV(scope, key string) error
	EnsureApiKeyUsageSchema() error
	AddApiKeyUsage(keyID string, hour, tokens, requests int64) error
	GetApiKeyUsageBuckets(sinceHour int64) ([]db.ApiKeyUsageBucket, error)
	PruneApiKeyUsage(beforeHour int64) error
}

const (
	minute     = time.Minute
	retention  = 30 * 24 // hours kept in memory and on disk
	configTTL  = 2 * time.Second
	pruneEvery = time.Hour
)

type tokEvent struct {
	at     time.Time
	tokens int64
}

type keyState struct {
	inflight int
	reqs     []time.Time // admissions in the last minute
	toks     []tokEvent  // token records in the last minute
	tokens   map[int64]int64
	requests map[int64]int64
}

// Limiter holds every key's live counters. All admission decisions and
// counter updates happen under one mutex, so concurrent requests can never
// overshoot a limit between the check and the increment.
type Limiter struct {
	b   Backend
	now func() time.Time

	mu        sync.Mutex
	keys      map[string]*keyState
	loaded    bool
	limits    map[string]Limits
	enabled   bool
	cfgAt     time.Time
	lastPrune time.Time
}

// New returns a limiter over b. now may be nil (time.Now).
func New(b Backend, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{b: b, now: now, keys: map[string]*keyState{}}
}

var (
	sharedMu sync.Mutex
	shared   = map[Backend]*Limiter{}
)

// For returns the process-wide limiter of a backend, so the middleware and
// the admin API see the same counters.
func For(b Backend) *Limiter {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if l, ok := shared[b]; ok {
		return l
	}
	l := New(b, nil)
	shared[b] = l
	return l
}

func hourOf(t time.Time) int64 { return t.Unix() / 3600 }

// refreshLocked reloads limits and the switch when the cache expired, and the
// hourly buckets once per process. Caller holds l.mu.
func (l *Limiter) refreshLocked(now time.Time) {
	if !l.loaded {
		if err := l.b.EnsureApiKeyUsageSchema(); err != nil {
			log.Warn("keyquota", "schema failed", "error", err)
		} else if buckets, err := l.b.GetApiKeyUsageBuckets(hourOf(now) - retention); err != nil {
			log.Warn("keyquota", "load usage failed", "error", err)
		} else {
			for _, bk := range buckets {
				st := l.stateLocked(bk.KeyID)
				st.tokens[bk.Hour] += bk.Tokens
				st.requests[bk.Hour] += bk.Requests
			}
			l.loaded = true
		}
	}
	if l.limits != nil && now.Sub(l.cfgAt) < configTTL {
		return
	}
	raw, err := l.b.GetKVScope(ScopeLimits)
	if err != nil {
		log.Warn("keyquota", "load limits failed", "error", err)
		if l.limits == nil {
			l.limits = map[string]Limits{}
		}
		return
	}
	limits := make(map[string]Limits, len(raw))
	enabled := false
	for k, v := range raw {
		if k == toggleKey {
			enabled = v == "true"
			continue
		}
		if lim, ok := decodeLimits(v); ok {
			limits[k] = lim
		}
	}
	l.limits, l.enabled, l.cfgAt = limits, enabled, now
}

func (l *Limiter) invalidate() {
	l.mu.Lock()
	l.limits = nil
	l.mu.Unlock()
}

func (l *Limiter) stateLocked(keyID string) *keyState {
	st := l.keys[keyID]
	if st == nil {
		st = &keyState{tokens: map[int64]int64{}, requests: map[int64]int64{}}
		l.keys[keyID] = st
	}
	return st
}

func (st *keyState) pruneLocked(now time.Time) {
	cut := now.Add(-minute)
	i := 0
	for i < len(st.reqs) && !st.reqs[i].After(cut) {
		i++
	}
	st.reqs = st.reqs[i:]
	j := 0
	for j < len(st.toks) && !st.toks[j].at.After(cut) {
		j++
	}
	st.toks = st.toks[j:]
	oldest := hourOf(now) - retention
	for h := range st.tokens {
		if h < oldest {
			delete(st.tokens, h)
		}
	}
	for h := range st.requests {
		if h < oldest {
			delete(st.requests, h)
		}
	}
}

// windowHours: a rolling window of N hours covers the current partial hour
// plus the N-1 previous full hours (hour-granular accounting).
func sumSince(m map[int64]int64, now time.Time, hours int64) int64 {
	from := hourOf(now) - hours + 1
	var s int64
	for h, v := range m {
		if h >= from {
			s += v
		}
	}
	return s
}

func (st *keyState) tpmLocked() int64 {
	var s int64
	for _, e := range st.toks {
		s += e.tokens
	}
	return s
}

// Denial explains a refused admission.
type Denial struct {
	Limit      string // rpm, tpm, concurrency, tokens24h, tokens7d, tokens30d
	Message    string // Spanish, safe to return to the client
	RetryAfter time.Duration
}

// Ticket is an admitted request. Release must be called exactly once (extra
// calls are no-ops).
type Ticket struct {
	l      *Limiter
	keyID  string
	once   sync.Once
	mu     sync.Mutex
	tokens int64
}

// AddTokens accumulates tokens consumed by the request.
func (t *Ticket) AddTokens(n int64) {
	if t == nil || n <= 0 {
		return
	}
	t.mu.Lock()
	t.tokens += n
	t.mu.Unlock()
}

// Enabled reports the feature switch.
func (l *Limiter) Enabled() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refreshLocked(l.now())
	return l.enabled
}

// Acquire admits a request for keyID or explains why not. With the feature
// off it returns (nil, nil): nothing is tracked.
func (l *Limiter) Acquire(keyID string) (*Ticket, *Denial) {
	if keyID == "" {
		return nil, nil
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refreshLocked(now)
	if !l.enabled {
		return nil, nil
	}
	st := l.stateLocked(keyID)
	st.pruneLocked(now)
	if lim, ok := l.limits[keyID]; ok && !lim.Zero() {
		if d := st.checkLocked(lim, now); d != nil {
			return nil, d
		}
	}
	st.inflight++
	st.reqs = append(st.reqs, now)
	return &Ticket{l: l, keyID: keyID}, nil
}

func (st *keyState) checkLocked(lim Limits, now time.Time) *Denial {
	if lim.Concurrency > 0 && st.inflight >= lim.Concurrency {
		return &Denial{Limit: "concurrency", RetryAfter: time.Second,
			Message: fmt.Sprintf("Límite de solicitudes simultáneas de esta API key alcanzado (%d).", lim.Concurrency)}
	}
	if lim.RPM > 0 && len(st.reqs) >= lim.RPM {
		at := st.reqs[len(st.reqs)-lim.RPM].Add(minute)
		return &Denial{Limit: "rpm", RetryAfter: at.Sub(now),
			Message: fmt.Sprintf("Límite de solicitudes por minuto de esta API key alcanzado (%d).", lim.RPM)}
	}
	if lim.TPM > 0 {
		if used := st.tpmLocked(); used >= lim.TPM {
			wait := minute
			for _, e := range st.toks {
				used -= e.tokens
				if used < lim.TPM {
					wait = e.at.Add(minute).Sub(now)
					break
				}
			}
			return &Denial{Limit: "tpm", RetryAfter: wait,
				Message: fmt.Sprintf("Límite de tokens por minuto de esta API key alcanzado (%d).", lim.TPM)}
		}
	}
	for _, w := range []struct {
		name  string
		hours int64
		limit int64
		label string
	}{
		{"tokens24h", 24, lim.Tokens24h, "24 horas"},
		{"tokens7d", 7 * 24, lim.Tokens7d, "7 días"},
		{"tokens30d", 30 * 24, lim.Tokens30d, "30 días"},
	} {
		if w.limit <= 0 {
			continue
		}
		used := sumSince(st.tokens, now, w.hours)
		if used < w.limit {
			continue
		}
		return &Denial{Limit: w.name, RetryAfter: periodWait(st.tokens, now, w.hours, used, w.limit),
			Message: fmt.Sprintf("Cuota de tokens de %s de esta API key agotada (%d).", w.label, w.limit)}
	}
	return nil
}

// periodWait estimates when enough old hourly buckets leave the window for
// usage to drop below the limit.
func periodWait(m map[int64]int64, now time.Time, hours, used, limit int64) time.Duration {
	from := hourOf(now) - hours + 1
	cur := hourOf(now)
	for h := from; h <= cur; h++ {
		used -= m[h]
		if used < limit {
			// Bucket h leaves the window when hour h+hours starts.
			return time.Unix((h+hours)*3600, 0).Sub(now)
		}
	}
	return time.Duration(hours) * time.Hour
}

// Release ends the request: frees the concurrency slot and records tokens.
func (t *Ticket) Release() {
	if t == nil {
		return
	}
	t.once.Do(func() {
		t.mu.Lock()
		tokens := t.tokens
		t.mu.Unlock()
		l := t.l
		now := l.now()
		h := hourOf(now)
		l.mu.Lock()
		st := l.stateLocked(t.keyID)
		if st.inflight > 0 {
			st.inflight--
		}
		if tokens > 0 {
			st.toks = append(st.toks, tokEvent{at: now, tokens: tokens})
			st.tokens[h] += tokens
		}
		st.requests[h]++
		prune := now.Sub(l.lastPrune) > pruneEvery
		if prune {
			l.lastPrune = now
		}
		l.mu.Unlock()
		if err := l.b.AddApiKeyUsage(t.keyID, h, tokens, 1); err != nil {
			log.Warn("keyquota", "persist usage failed", "error", err)
		}
		if prune {
			if err := l.b.PruneApiKeyUsage(h - retention); err != nil {
				log.Warn("keyquota", "prune usage failed", "error", err)
			}
		}
	})
}

// Usage is a key's live counters.
type Usage struct {
	Inflight    int   `json:"inflight"`
	RPM         int   `json:"rpm"`
	TPM         int64 `json:"tpm"`
	Tokens24h   int64 `json:"tokens24h"`
	Tokens7d    int64 `json:"tokens7d"`
	Tokens30d   int64 `json:"tokens30d"`
	Requests24h int64 `json:"requests24h"`
	Requests7d  int64 `json:"requests7d"`
	Requests30d int64 `json:"requests30d"`
}

// Snapshot returns the switch, every configured limit and every key's usage.
func (l *Limiter) Snapshot() (bool, map[string]Limits, map[string]Usage) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refreshLocked(now)
	limits := make(map[string]Limits, len(l.limits))
	for k, v := range l.limits {
		limits[k] = v
	}
	usage := make(map[string]Usage, len(l.keys))
	for k, st := range l.keys {
		st.pruneLocked(now)
		usage[k] = Usage{
			Inflight:    st.inflight,
			RPM:         len(st.reqs),
			TPM:         st.tpmLocked(),
			Tokens24h:   sumSince(st.tokens, now, 24),
			Tokens7d:    sumSince(st.tokens, now, 7*24),
			Tokens30d:   sumSince(st.tokens, now, 30*24),
			Requests24h: sumSince(st.requests, now, 24),
			Requests7d:  sumSince(st.requests, now, 7*24),
			Requests30d: sumSince(st.requests, now, 30*24),
		}
	}
	return l.enabled, limits, usage
}

// SetLimits stores (or, for zero limits, deletes) a key's limits.
func (l *Limiter) SetLimits(keyID string, lim Limits) (Limits, error) {
	if keyID == "" || keyID == toggleKey || len(keyID) > 256 {
		return Limits{}, fmt.Errorf("%w: id de API key inválido", ErrInvalid)
	}
	if err := lim.Validate(); err != nil {
		return Limits{}, err
	}
	defer l.invalidate()
	if lim.Zero() {
		return Limits{}, l.b.DeleteKV(ScopeLimits, keyID)
	}
	raw, err := encodeLimits(lim)
	if err != nil {
		return Limits{}, err
	}
	if err := l.b.SetKV(ScopeLimits, keyID, raw); err != nil {
		return Limits{}, err
	}
	out, _ := decodeLimits(raw)
	return out, nil
}

// DeleteLimits removes a key's limits.
func (l *Limiter) DeleteLimits(keyID string) error {
	if keyID == "" || keyID == toggleKey {
		return fmt.Errorf("%w: id de API key inválido", ErrInvalid)
	}
	defer l.invalidate()
	return l.b.DeleteKV(ScopeLimits, keyID)
}

// SetEnabled turns the feature on or off.
func (l *Limiter) SetEnabled(on bool) error {
	defer l.invalidate()
	if !on {
		return l.b.DeleteKV(ScopeLimits, toggleKey)
	}
	return l.b.SetKV(ScopeLimits, toggleKey, "true")
}
