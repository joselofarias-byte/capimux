// Package combohealth tracks the recent health of each provider/model so the
// opt-in "health" combo strategy can try healthy members first.
//
// It never removes a member: an unhealthy one is only moved to the end, so a
// combo keeps its full availability. Only failures that say something about
// the provider count (429, 408, 5xx, transport errors, empty completions);
// client errors such as 400 do not.
//
// Inspired by the health-based balancing of 9Router Pro (MIT, © decolua and
// contributors); reimplemented in Go, no code copied.
package combohealth

import (
	"math"
	"sort"
	"sync"
	"time"
)

const (
	alpha          = 0.3 // EWMA weight of the newest outcome
	openAfter      = 3   // consecutive failures that open the circuit
	baseCooldown   = 30 * time.Second
	maxCooldown    = 5 * time.Minute
	latencyBuckets = 0.1 // success scores within this band tie, then latency decides
)

type stat struct {
	success     float64 // EWMA of success (1) / failure (0)
	latencyMs   float64 // EWMA of successful latency
	samples     int64
	consecFails int
	lastFail    time.Time
	lastSuccess time.Time
}

// Tracker is safe for concurrent use.
type Tracker struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[string]*stat
}

// New returns an empty tracker. now may be nil.
func New(now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	return &Tracker{now: now, m: map[string]*stat{}}
}

// Default is the process-wide tracker used by the chat handlers.
var Default = New(nil)

// Key is the tracker key for a provider/model pair.
func Key(provider, model string) string { return provider + "/" + model }

// Success records a served request and its latency.
func (t *Tracker) Success(key string, latency time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.get(key)
	s.success = s.success*(1-alpha) + alpha
	ms := float64(latency.Milliseconds())
	if s.latencyMs == 0 {
		s.latencyMs = ms
	} else {
		s.latencyMs = s.latencyMs*(1-alpha) + ms*alpha
	}
	s.samples++
	s.consecFails = 0
	s.lastSuccess = t.now()
}

// Failure records a provider-side failure.
func (t *Tracker) Failure(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.get(key)
	s.success *= 1 - alpha
	s.samples++
	s.consecFails++
	s.lastFail = t.now()
}

func (t *Tracker) get(key string) *stat {
	s := t.m[key]
	if s == nil {
		// Optimistic start: an unknown member is assumed healthy so it gets
		// tried and earns a real score.
		s = &stat{success: 1}
		t.m[key] = s
	}
	return s
}

func cooldown(consec int) time.Duration {
	d := baseCooldown
	for i := openAfter; i < consec && d < maxCooldown; i++ {
		d *= 2
	}
	if d > maxCooldown {
		d = maxCooldown
	}
	return d
}

// open reports whether the member's circuit is open (recent run of failures
// still inside its cooldown).
func (s *stat) open(now time.Time) bool {
	return s.consecFails >= openAfter && now.Sub(s.lastFail) < cooldown(s.consecFails)
}

// Order returns members sorted healthiest first. keyOf maps a member to its
// tracker key ("" = unknown, treated as healthy). The sort is stable, so
// equally healthy members keep the combo's configured order.
func (t *Tracker) Order(members []string, keyOf func(string) string) []string {
	type row struct {
		m       string
		idx     int
		open    bool
		band    int
		latency float64
	}
	now := t.now()
	rows := make([]row, len(members))
	t.mu.Lock()
	for i, m := range members {
		r := row{m: m, idx: i, band: int(1 / latencyBuckets)}
		if k := keyOf(m); k != "" {
			if s, ok := t.m[k]; ok {
				r.open = s.open(now)
				r.band = int(math.Round(s.success / latencyBuckets))
				r.latency = s.latencyMs
			}
		}
		rows[i] = r
	}
	t.mu.Unlock()
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.open != b.open {
			return !a.open
		}
		if a.band != b.band {
			return a.band > b.band
		}
		if a.latency != b.latency && a.latency > 0 && b.latency > 0 {
			return a.latency < b.latency
		}
		return a.idx < b.idx
	})
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.m
	}
	return out
}

// Entry is a read-only view of one member's health.
type Entry struct {
	Key         string  `json:"key"`
	Success     float64 `json:"success"`
	LatencyMs   int64   `json:"latencyMs"`
	Samples     int64   `json:"samples"`
	ConsecFails int     `json:"consecutiveFailures"`
	Open        bool    `json:"circuitOpen"`
	LastFail    string  `json:"lastFailure,omitempty"`
	LastSuccess string  `json:"lastSuccess,omitempty"`
}

// Snapshot returns every tracked member, sorted by key.
func (t *Tracker) Snapshot() []Entry {
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Entry, 0, len(t.m))
	for k, s := range t.m {
		e := Entry{Key: k, Success: float64(int(s.success*1000)) / 1000, LatencyMs: int64(s.latencyMs), Samples: s.samples, ConsecFails: s.consecFails, Open: s.open(now)}
		if !s.lastFail.IsZero() {
			e.LastFail = s.lastFail.UTC().Format(time.RFC3339)
		}
		if !s.lastSuccess.IsZero() {
			e.LastSuccess = s.lastSuccess.UTC().Format(time.RFC3339)
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Reset forgets every member (tests and the admin "reset" action).
func (t *Tracker) Reset() {
	t.mu.Lock()
	t.m = map[string]*stat{}
	t.mu.Unlock()
}
