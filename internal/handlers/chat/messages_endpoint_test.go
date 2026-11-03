package chat

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9router/proxy/internal/db"
)

// Regression for #130 (chat.go): a single-model /v1/messages request used to
// pass the endpoint "/v1/v1/messages", so usage history recorded a doubled
// prefix. The upstream URL never depended on this value; only the recorded
// endpoint (and the claudeNative check, which accepts both) did.
func TestHandleMessagesSingleModel_RecordsV1MessagesEndpoint(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-1","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`))
	}))
	defer upstream.Close()

	data, _ := json.Marshal(map[string]any{"apiKey": "sk-test-deepseek-key", "baseUrl": upstream.URL})
	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		('conn-endpoint', 'deepseek', 'apikey', 'Mock DeepSeek', 0, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`, string(data)); err != nil {
		t.Fatalf("insert connection: %v", err)
	}

	handler := NewChatHandler(db.NewRepo(database))
	body := `{"model":"deepseek/deepseek-chat","messages":[{"role":"user","content":"hello"}],"max_tokens":100,"stream":false}`
	rec := httptest.NewRecorder()
	handler.HandleMessages(rec, httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var endpoint string
	if err := database.QueryRow(`SELECT endpoint FROM usageHistory WHERE connectionId = 'conn-endpoint' ORDER BY rowid DESC LIMIT 1`).Scan(&endpoint); err != nil {
		t.Fatalf("query usageHistory: %v", err)
	}
	if endpoint != "/v1/messages" {
		t.Fatalf("recorded endpoint = %q, want /v1/messages", endpoint)
	}
}
