package chat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"9router/proxy/internal/db"
)

// A dead first account must not be the last attempt: a cut connection and a
// 200 that is not JSON both move the request to the next account, and neither
// attempt leaves the loopback upstreams.
func TestAccountFallbackCutAndInvalidBodyUsesNextAccount(t *testing.T) {
	cases := []struct {
		name string
		fail func(http.ResponseWriter, *http.Request)
	}{
		{
			name: "connection cut",
			fail: func(w http.ResponseWriter, r *http.Request) {
				hj, ok := w.(http.Hijacker)
				if !ok {
					t.Errorf("no hijacker")
					return
				}
				conn, _, err := hj.Hijack()
				if err != nil {
					t.Errorf("hijack: %v", err)
					return
				}
				conn.Close()
			},
		},
		{
			name: "invalid body",
			fail: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"choices":`))
			},
		},
		{
			name: "error envelope",
			fail: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"error":{"message":"upstream exploded"}}`))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var failedHits, healthyHits atomic.Int32
			failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				failedHits.Add(1)
				tc.fail(w, r)
			}))
			defer failed.Close()
			healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				healthyHits.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"healthy","choices":[{"message":{"role":"assistant","content":"recovered"}}]}`))
			}))
			defer healthy.Close()

			database, cleanup := setupChatTestDB(t)
			defer cleanup()
			if _, err := database.Exec(`DELETE FROM providerConnections WHERE provider = 'deepseek'`); err != nil {
				t.Fatal(err)
			}
			firstID := fmt.Sprintf("cut-first-%s", tc.name)
			secondID := fmt.Sprintf("cut-second-%s", tc.name)
			seedConnDB(t, database, "deepseek", firstID, "sk-first", failed.URL)
			seedConnDB(t, database, "deepseek", secondID, "sk-second", healthy.URL)
			if _, err := database.Exec(`UPDATE providerConnections SET priority = 2 WHERE id = ?`, secondID); err != nil {
				t.Fatal(err)
			}
			h := NewChatHandler(db.NewRepo(database))
			rec := httptest.NewRecorder()
			err := h.handleAccountFallback(context.Background(), rec, "deepseek", "deepseek-chat", "", []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`), false, false, "/v1/chat/completions")
			if err != nil || healthyHits.Load() != 1 || !strings.Contains(rec.Body.String(), "recovered") {
				t.Fatalf("failover missing: err=%v healthy=%d failed=%d body=%s", err, healthyHits.Load(), failedHits.Load(), rec.Body.String())
			}
			if failedHits.Load() != 1 {
				t.Fatalf("failed account hits=%d", failedHits.Load())
			}
		})
	}
}
