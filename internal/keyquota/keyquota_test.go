package keyquota

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	json "encoding/json/v2"

	"9router/proxy/internal/db"
)

type fakeBackend struct {
	mu      sync.Mutex
	kv      map[string]string
	buckets map[string]map[int64][2]int64
	writes  int
}

func newFake() *fakeBackend {
	return &fakeBackend{kv: map[string]string{}, buckets: map[string]map[int64][2]int64{}}
}

func (f *fakeBackend) GetKVScope(scope string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{}
	for k, v := range f.kv {
		if s, key, ok := strings.Cut(k, "\x00"); ok && s == scope {
			out[key] = v
		}
	}
	return out, nil
}
func (f *fakeBackend) SetKV(scope, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kv[scope+"\x00"+key] = value
	return nil
}
func (f *fakeBackend) DeleteKV(scope, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.kv, scope+"\x00"+key)
	return nil
}
func (f *fakeBackend) EnsureApiKeyUsageSchema() error { return nil }
func (f *fakeBackend) AddApiKeyUsage(keyID string, hour, tokens, requests int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.buckets[keyID] == nil {
		f.buckets[keyID] = map[int64][2]int64{}
	}
	b := f.buckets[keyID][hour]
	f.buckets[keyID][hour] = [2]int64{b[0] + tokens, b[1] + requests}
	f.writes++
	return nil
}
func (f *fakeBackend) GetApiKeyUsageBuckets(since int64) ([]db.ApiKeyUsageBucket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []db.ApiKeyUsageBucket
	for k, m := range f.buckets {
		for h, v := range m {
			if h >= since {
				out = append(out, db.ApiKeyUsageBucket{KeyID: k, Hour: h, Tokens: v[0], Requests: v[1]})
			}
		}
	}
	return out, nil
}
func (f *fakeBackend) PruneApiKeyUsage(before int64) error { return nil }
func (f *fakeBackend) total(key string) (tokens, reqs int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, v := range f.buckets[key] {
		tokens += v[0]
		reqs += v[1]
	}
	return
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

func setup(t *testing.T, lim Limits) (*Limiter, *fakeBackend, *clock) {
	t.Helper()
	b := newFake()
	c := &clock{t: time.Date(2026, 10, 30, 12, 30, 0, 0, time.UTC)}
	l := New(b, c.now)
	if err := l.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if _, err := l.SetLimits("k1", lim); err != nil {
		t.Fatal(err)
	}
	return l, b, c
}

func TestOffByDefaultTracksNothing(t *testing.T) {
	b := newFake()
	l := New(b, nil)
	if _, err := l.SetLimits("k1", Limits{RPM: 1}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		tk, d := l.Acquire("k1")
		if d != nil || tk != nil {
			t.Fatalf("apagado: ticket=%v denial=%v", tk, d)
		}
	}
	if b.writes != 0 {
		t.Fatalf("apagado escribió %d buckets", b.writes)
	}
}

func TestConcurrencyLimit(t *testing.T) {
	l, _, _ := setup(t, Limits{Concurrency: 2})
	a, _ := l.Acquire("k1")
	b, _ := l.Acquire("k1")
	if _, d := l.Acquire("k1"); d == nil || d.Limit != "concurrency" {
		t.Fatalf("tercera simultánea admitida: %+v", d)
	}
	a.Release()
	a.Release() // idempotent: must not free a second slot
	c, d := l.Acquire("k1")
	if d != nil {
		t.Fatalf("tras liberar: %+v", d)
	}
	if _, d := l.Acquire("k1"); d == nil {
		t.Fatal("Release doble liberó dos lugares")
	}
	b.Release()
	c.Release()
}

func TestRPMWithRetryAfter(t *testing.T) {
	l, _, c := setup(t, Limits{RPM: 3})
	for i := 0; i < 3; i++ {
		tk, d := l.Acquire("k1")
		if d != nil {
			t.Fatalf("solicitud %d: %+v", i, d)
		}
		tk.Release()
		c.add(10 * time.Second)
	}
	_, d := l.Acquire("k1")
	if d == nil || d.Limit != "rpm" {
		t.Fatalf("cuarta solicitud en el minuto: %+v", d)
	}
	if d.RetryAfter != 30*time.Second {
		t.Fatalf("RetryAfter = %v, se esperaba 30s", d.RetryAfter)
	}
	c.add(31 * time.Second)
	if _, d := l.Acquire("k1"); d != nil {
		t.Fatalf("tras la ventana: %+v", d)
	}
	// Other keys are independent.
	if _, d := l.Acquire("k2"); d != nil {
		t.Fatalf("otra key afectada: %+v", d)
	}
}

func TestTPM(t *testing.T) {
	l, _, c := setup(t, Limits{TPM: 100})
	tk, _ := l.Acquire("k1")
	tk.AddTokens(60)
	tk.AddTokens(40)
	tk.Release()
	_, d := l.Acquire("k1")
	if d == nil || d.Limit != "tpm" || d.RetryAfter != time.Minute {
		t.Fatalf("tpm: %+v", d)
	}
	c.add(61 * time.Second)
	if _, d := l.Acquire("k1"); d != nil {
		t.Fatalf("tpm tras la ventana: %+v", d)
	}
}

func TestRollingTokenWindows(t *testing.T) {
	l, _, c := setup(t, Limits{Tokens24h: 1000, Tokens7d: 1500})
	tk, _ := l.Acquire("k1")
	tk.AddTokens(1000)
	tk.Release()
	_, d := l.Acquire("k1")
	if d == nil || d.Limit != "tokens24h" {
		t.Fatalf("24h: %+v", d)
	}
	if d.RetryAfter <= 0 || d.RetryAfter > 24*time.Hour {
		t.Fatalf("RetryAfter 24h fuera de rango: %v", d.RetryAfter)
	}
	c.add(d.RetryAfter + time.Second)
	tk, d = l.Acquire("k1")
	if d != nil {
		t.Fatalf("tras 24h: %+v", d)
	}
	tk.AddTokens(500)
	tk.Release()
	_, d = l.Acquire("k1")
	if d == nil || d.Limit != "tokens7d" {
		t.Fatalf("7d (1000 de ayer + 500 hoy): %+v", d)
	}
}

func TestUsageSurvivesRestart(t *testing.T) {
	l, b, c := setup(t, Limits{Tokens24h: 50})
	tk, _ := l.Acquire("k1")
	tk.AddTokens(50)
	tk.Release()
	// A new limiter over the same storage (process restart) still enforces.
	l2 := New(b, c.now)
	if _, d := l2.Acquire("k1"); d == nil || d.Limit != "tokens24h" {
		t.Fatalf("tras reinicio: %+v", d)
	}
}

func TestValidation(t *testing.T) {
	l := New(newFake(), nil)
	for _, lim := range []Limits{{RPM: -1}, {Concurrency: -2}, {TPM: -5}, {Tokens30d: maxTokens + 1}, {RPM: maxRPM + 1}} {
		if _, err := l.SetLimits("k", lim); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v aceptado: %v", lim, err)
		}
	}
	if _, err := l.SetLimits(toggleKey, Limits{RPM: 1}); !errors.Is(err, ErrInvalid) {
		t.Error("se aceptó el id reservado del interruptor")
	}
	if _, err := l.SetLimits("", Limits{RPM: 1}); !errors.Is(err, ErrInvalid) {
		t.Error("se aceptó un id vacío")
	}
}

// Many goroutines race for a concurrency limit of 5: never more than 5 in
// flight, and every token is counted exactly once in memory and on disk.
func TestConcurrentAdmissionIsAtomic(t *testing.T) {
	l, b, _ := setup(t, Limits{Concurrency: 5})
	var inflight, maxSeen, admitted atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 400; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tk, d := l.Acquire("k1")
			if d != nil {
				return
			}
			admitted.Add(1)
			n := inflight.Add(1)
			for {
				m := maxSeen.Load()
				if n <= m || maxSeen.CompareAndSwap(m, n) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			tk.AddTokens(3)
			inflight.Add(-1)
			tk.Release()
		}()
	}
	wg.Wait()
	if maxSeen.Load() > 5 {
		t.Fatalf("hubo %d solicitudes simultáneas con límite 5", maxSeen.Load())
	}
	_, _, usage := l.Snapshot()
	u := usage["k1"]
	if u.Inflight != 0 {
		t.Fatalf("quedaron %d en vuelo", u.Inflight)
	}
	if u.Tokens24h != 3*admitted.Load() || u.Requests24h != admitted.Load() {
		t.Fatalf("memoria: tokens=%d requests=%d, admitidas=%d", u.Tokens24h, u.Requests24h, admitted.Load())
	}
	tok, req := b.total("k1")
	if tok != 3*admitted.Load() || req != admitted.Load() {
		t.Fatalf("persistido: tokens=%d requests=%d, admitidas=%d", tok, req, admitted.Load())
	}
}

func TestMiddleware(t *testing.T) {
	b := newFake()
	l := For(b)
	_ = l.SetEnabled(true)
	_, _ = l.SetLimits("k1", Limits{RPM: 1})
	calls := 0
	h := Middleware(b, func(*http.Request) string { return "k1" })(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method == http.MethodPost && TicketFrom(r.Context()) == nil {
			t.Error("sin ticket en el contexto")
		}
		AddTokens(r.Context(), 7)
		w.WriteHeader(200)
	}))
	do := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader("{}")))
		return rec
	}
	if rec := do("POST", "/v1/chat/completions"); rec.Code != 200 {
		t.Fatalf("primera = %d", rec.Code)
	}
	rec := do("POST", "/v1/chat/completions")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("segunda = %d retry=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
	var oa struct {
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &oa); err != nil || oa.Error.Type != "rate_limit_error" || oa.Error.Code != "api_key_rpm_exceeded" {
		t.Fatalf("cuerpo OpenAI: %s", rec.Body.String())
	}
	rec = do("POST", "/v1/messages")
	var an struct {
		Type  string `json:"type"`
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &an); err != nil || an.Type != "error" || an.Error.Type != "rate_limit_error" {
		t.Fatalf("cuerpo Anthropic: %s", rec.Body.String())
	}
	// GET (model listing) is not limited.
	if rec := do("GET", "/v1/models"); rec.Code != 200 {
		t.Fatalf("GET = %d", rec.Code)
	}
	if calls != 2 {
		t.Fatalf("el handler corrió %d veces, se esperaban 2", calls)
	}
	if tok, _ := b.total("k1"); tok != 7 {
		t.Fatalf("tokens persistidos = %d", tok)
	}
	// Without a ticket AddTokens is a harmless no-op.
	AddTokens(context.Background(), 5)
}
