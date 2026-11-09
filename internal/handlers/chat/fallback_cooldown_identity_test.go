package chat

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"9router/proxy/internal/db"
)

// keyRecorder counts the bearer keys a fake upstream received.
type keyRecorder struct {
	mu   sync.Mutex
	seen []string
}

func (k *keyRecorder) add(r *http.Request) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.seen = append(k.seen, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
}

func (k *keyRecorder) list() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string(nil), k.seen...)
}

// twoAccounts seeds conn-a (first by priority, put in an account cooldown)
// and conn-b (healthy) for deepseek against srv.
func twoAccountsFirstCooling(t *testing.T, srvURL string) (*sql.DB, *ChatHandler, time.Time, func()) {
	t.Helper()
	database, cleanup := setupChatTestDB(t)
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}
	seedConnDB(t, database, "deepseek", "conn-a", "sk-a", srvURL)
	seedConnDB(t, database, "deepseek", "conn-b", "sk-b", srvURL)
	if _, err := database.Exec(`UPDATE providerConnections SET priority = CASE id WHEN 'conn-a' THEN 1 ELSE 2 END`); err != nil {
		t.Fatalf("order connections: %v", err)
	}
	repo := db.NewRepo(database)
	until := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Second)
	if err := repo.LockConnectionRateLimit("conn-a", until, 1, http.StatusTooManyRequests, "quota exhausted"); err != nil {
		t.Fatalf("cool down conn-a: %v", err)
	}
	return database, NewChatHandler(repo), until, cleanup
}

func cooldownOf(t *testing.T, database *sql.DB, id string) (time.Time, bool) {
	t.Helper()
	var raw string
	if err := database.QueryRow(`SELECT data FROM providerConnections WHERE id = ?`, id).Scan(&raw); err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return db.ConnectionCooldownUntil(raw)
}

var cooldownTestBody = []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)

// The account loop walks every active row, including one in cooldown. For
// that row getBestConnection falls through to the strategy and hands back a
// different, healthy account. The forward then ran with the healthy
// account's credentials but the cooled account's ID, so a success "proved"
// the exhausted account usable and cleared its cooldown: the next request
// went straight back to the account whose quota was spent.
func TestAccountFallback_SuccessOnAnotherAccountKeepsCooldown(t *testing.T) {
	rec := &keyRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.add(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()
	database, h, until, cleanup := twoAccountsFirstCooling(t, srv.URL)
	defer cleanup()

	w := httptest.NewRecorder()
	if err := h.handleAccountFallback(context.Background(), w, "deepseek", "deepseek-chat", "", cooldownTestBody, false, false, "/v1/chat/completions"); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if got := rec.list(); len(got) != 1 || got[0] != "sk-b" {
		t.Fatalf("upstream saw keys %v, want exactly the healthy account once", got)
	}
	left, ok := cooldownOf(t, database, "conn-a")
	if !ok || !left.Equal(until) {
		t.Fatalf("conn-a cooldown = %v,%v after conn-b served the request; want it kept until %v", left, ok, until)
	}
}

// Same mix-up on a transient failure: every cooled row re-selected the same
// healthy account (no exclusions were passed), so one client request hit
// that account once per cooled row plus once for itself.
func TestAccountFallback_TransientFailureTriesEachAccountOnce(t *testing.T) {
	rec := &keyRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.add(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"internal","type":"server_error"}}`))
	}))
	defer srv.Close()
	_, h, _, cleanup := twoAccountsFirstCooling(t, srv.URL)
	defer cleanup()

	err := h.handleAccountFallback(context.Background(), httptest.NewRecorder(), "deepseek", "deepseek-chat", "", cooldownTestBody, false, false, "/v1/chat/completions")
	if err == nil {
		t.Fatal("expected the 500 to surface")
	}
	if got := rec.list(); len(got) != 1 || got[0] != "sk-b" {
		t.Fatalf("upstream saw keys %v, want the one healthy account exactly once", got)
	}
}

// Regression: with no cooldown at all both accounts stay usable in order,
// and a retryable failure on the first still fails over to the second.
func TestAccountFallback_RetryableFailoverStillReachesNextAccount(t *testing.T) {
	rec := &keyRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.add(r)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.Header.Get("Authorization"), "sk-a") {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()
	database, h, _, cleanup := twoAccountsFirstCooling(t, srv.URL)
	defer cleanup()
	if err := h.Repo.ClearConnectionRateLimit("conn-a"); err != nil {
		t.Fatal(err)
	}

	if err := h.handleAccountFallback(context.Background(), httptest.NewRecorder(), "deepseek", "deepseek-chat", "", cooldownTestBody, false, false, "/v1/chat/completions"); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if got := rec.list(); len(got) != 2 || got[0] != "sk-a" || got[1] != "sk-b" {
		t.Fatalf("upstream saw keys %v, want sk-a then sk-b", got)
	}
	if _, ok := cooldownOf(t, database, "conn-a"); !ok {
		t.Fatal("the 429 on conn-a should have parked it")
	}
	if _, ok := cooldownOf(t, database, "conn-b"); ok {
		t.Fatal("conn-b served the request and must not be in cooldown")
	}
}

// Negative: when every account is cooling down no request leaves at all.
func TestAccountFallback_AllAccountsCoolingSendsNothing(t *testing.T) {
	rec := &keyRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.add(r)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	_, h, until, cleanup := twoAccountsFirstCooling(t, srv.URL)
	defer cleanup()
	if err := h.Repo.LockConnectionRateLimit("conn-b", until, 1, http.StatusTooManyRequests, "quota exhausted"); err != nil {
		t.Fatal(err)
	}
	if err := h.handleAccountFallback(context.Background(), httptest.NewRecorder(), "deepseek", "deepseek-chat", "", cooldownTestBody, false, false, "/v1/chat/completions"); err == nil {
		t.Fatal("expected an error when every account is cooling down")
	}
	if got := rec.list(); len(got) != 0 {
		t.Fatalf("upstream saw keys %v while every account was cooling down", got)
	}
}
