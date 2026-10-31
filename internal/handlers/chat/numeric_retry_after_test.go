package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"9router/proxy/internal/db"
)

// Rate-limit bodies that spell the wait as a JSON number. Before the fix a
// single numeric field made the whole decode fail and the wait was lost.
func TestRetryAfterWait_NumericBodyFields(t *testing.T) {
	cases := []struct {
		body string
		want time.Duration
	}{
		{`{"retry_after":30}`, 30 * time.Second},
		{`{"retryAfter":45}`, 45 * time.Second},
		{`{"error":{"message":"rate limited","retry_after":20}}`, 20 * time.Second},
		{`{"error":{"message":"m","retryAfter":12}}`, 12 * time.Second},
		{`{"retry_after":1.5}`, 1500 * time.Millisecond},
		// The exact shape passRetry.writeError sends to clients: a chained
		// Capimux/9router upstream whose Retry-After header was stripped.
		{`{"error":{"message":"quota (reset after 1m 30s)","retry_after":90,"reset_at":"` + time.Now().UTC().Add(90*time.Second).Format(time.RFC3339) + `"}}`, 90 * time.Second},
		// A numeric field next to a string one no longer hides the string.
		{`{"retry_after":7,"resetsAt":"x"}`, 7 * time.Second},
		{`{"retry_after":"30"}`, 30 * time.Second},
	}
	for _, tc := range cases {
		got, ok := retryAfterWait(&upstreamError{StatusCode: 429, Header: http.Header{}, Body: []byte(tc.body)})
		if !ok || got < tc.want-time.Second || got > tc.want+time.Second {
			t.Errorf("%s -> %v,%v want ~%v", tc.body, got, ok, tc.want)
		}
	}
	for _, body := range []string{`{"retry_after":0}`, `{"retry_after":-5}`, `{"retry_after":null}`, `{"retry_after":true}`, `{"error":{"message":"m"}}`, `not json`} {
		if got, ok := retryAfterWait(&upstreamError{StatusCode: 429, Header: http.Header{}, Body: []byte(body)}); ok {
			t.Errorf("%s -> %v, want no wait", body, got)
		}
	}
}

// A huge delta-seconds used to overflow time.Duration into a negative value,
// which read as "no wait": the exhausted account was re-picked two seconds
// later. It is now held to maxResetCooldown like every other cooldown.
func TestRetryAfterWait_HugeSecondsAreClampedNotDropped(t *testing.T) {
	huge := strconv.Itoa(10_000_000_000)
	for name, ue := range map[string]*upstreamError{
		"header":      {StatusCode: 429, Header: http.Header{"Retry-After": {huge}}},
		"body string": {StatusCode: 429, Header: http.Header{}, Body: []byte(`{"retry_after":"` + huge + `"}`)},
		"body number": {StatusCode: 429, Header: http.Header{}, Body: []byte(`{"retry_after":` + huge + `}`)},
	} {
		got, ok := retryAfterWait(ue)
		if !ok || got != maxResetCooldown {
			t.Errorf("%s: %v,%v want %v", name, got, ok, maxResetCooldown)
		}
		if sec := retryableCooldownSec(429, 2*time.Second, ue); sec != int(maxResetCooldown/time.Second) {
			t.Errorf("%s: cooldown %ds, want %ds", name, sec, int(maxResetCooldown/time.Second))
		}
	}
}

// End to end through the account fallback: a 429 whose only wait is a
// numeric body field parks the account for that window instead of the 2s
// base backoff.
func TestRateLimitNumericBodyParksAccountForTheNamedWindow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"Rate limit reached","type":"rate_limit_error","retry_after":45}}`))
	}))
	defer srv.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	if _, err := database.Exec(`DELETE FROM providerConnections WHERE id IN ('conn-1', 'conn-2')`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}
	seedConnDB(t, database, "deepseek", "conn-num", "sk-num", srv.URL)
	h := NewChatHandler(db.NewRepo(database))

	body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)
	if err := h.handleAccountFallback(context.Background(), httptest.NewRecorder(), "deepseek", "deepseek-chat", "", body, false, false, "/v1/chat/completions"); err == nil {
		t.Fatal("expected the 429 to be returned as an error")
	}
	var rawData string
	if err := database.QueryRow(`SELECT data FROM providerConnections WHERE id = 'conn-num'`).Scan(&rawData); err != nil {
		t.Fatalf("read connection row: %v", err)
	}
	until, ok := db.ConnectionCooldownUntil(rawData)
	if !ok {
		t.Fatal("expected an account-scoped cooldown after a 429")
	}
	if left := time.Until(until); left < 40*time.Second || left > 45*time.Second {
		t.Errorf("account parked for %v, want the 45s the upstream named", left.Round(time.Second))
	}
}
