package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"9router/proxy/internal/db"
)

// A failure outside RetryableStatusCodes (400, 404, 422, 500, a bare network
// error) used to "continue" the combo's per-entry account loop without
// excluding the account, so the strategy picked the very same account again
// and the request was re-sent up to ten times per model entry: ten 400s for
// one malformed request, ten hits on a 500ing account while a healthy one
// sat unused. Each account must be tried at most once per entry and pass.
func TestComboNonRetryableFailureTriesEachAccountOnce(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		accounts int
	}{
		{"400 single account", http.StatusBadRequest, 1},
		{"422 two accounts", http.StatusUnprocessableEntity, 2},
		{"500 two accounts", http.StatusInternalServerError, 2},
	}
	for _, tc := range cases {
		for _, endpoint := range []string{"chat", "messages"} {
			t.Run(tc.name+"/"+endpoint, func(t *testing.T) {
				var mu sync.Mutex
				perKey := map[string]int{}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					perKey[r.Header.Get("Authorization")]++
					mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(`{"error":{"message":"nope","type":"invalid_request_error"}}`))
				}))
				defer srv.Close()

				database, cleanup := setupChatTestDB(t)
				defer cleanup()
				if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
					t.Fatal(err)
				}
				seedPooledConns(t, database, tc.accounts, srv.URL)
				h := NewChatHandler(db.NewRepo(database))
				models := []string{"deepseek/deepseek-chat"}
				modelsJSON, _ := json.Marshal(models)
				if _, err := database.Exec(`INSERT INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES ('combo-nr', 'combo-nr', 'fallback', ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`, string(modelsJSON)); err != nil {
					t.Fatal(err)
				}

				rec := httptest.NewRecorder()
				if endpoint == "chat" {
					h.handleComboFallback(context.Background(), rec, []byte(`{"model":"combo-nr","messages":[{"role":"user","content":"hi"}]}`), models, "fallback", false, false, "combo-nr", 0)
				} else {
					req := map[string]any{"model": "deepseek-chat", "max_tokens": 10, "messages": []map[string]any{{"role": "user", "content": "hi"}}}
					h.handleMessagesComboFallback(context.Background(), rec, req, models, "fallback", false, "combo-nr", 0)
				}

				if rec.Code != tc.status {
					t.Errorf("client status = %d, want the upstream %d (body %s)", rec.Code, tc.status, rec.Body.String())
				}
				mu.Lock()
				defer mu.Unlock()
				if len(perKey) != tc.accounts {
					t.Errorf("accounts reached = %v, want all %d tried", perKey, tc.accounts)
				}
				for i := 1; i <= tc.accounts; i++ {
					key := "Bearer sk-pool-" + strconv.Itoa(i)
					if perKey[key] != 1 {
						t.Errorf("%s hit %d times, want exactly once (all hits %v)", key, perKey[key], perKey)
					}
				}
			})
		}
	}
}
