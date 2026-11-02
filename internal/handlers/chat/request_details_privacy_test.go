package chat

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"9router/proxy/internal/db"
	"9router/proxy/internal/translator"
)

func TestRequestDetailsCaptureOptIn(t *testing.T) {
	t.Setenv("CAPIMUX_CAPTURE_REQUEST_CONTENT", "")
	if captureRequestDetailContent() {
		t.Fatal("request content must not be recorded by default")
	}
	t.Setenv("CAPIMUX_CAPTURE_REQUEST_CONTENT", "false")
	if captureRequestDetailContent() {
		t.Fatal("explicit false must disable content capture")
	}
	t.Setenv("CAPIMUX_CAPTURE_REQUEST_CONTENT", "garbage")
	if captureRequestDetailContent() {
		t.Fatal("invalid flag must fail closed")
	}
	t.Setenv("CAPIMUX_CAPTURE_REQUEST_CONTENT", "true")
	if !captureRequestDetailContent() {
		t.Fatal("explicit opt-in must enable content capture")
	}
}

func TestLogFailureDefaultDoesNotPersistPromptsOrUpstreamErrors(t *testing.T) {
	t.Setenv("CAPIMUX_CAPTURE_REQUEST_CONTENT", "")
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	handler := NewChatHandler(repo)
	handler.LogFailure(
		&UsageLogInfo{Provider: "openai", Model: "grok004", ConnectionID: "test"},
		nil,
		errors.New("grok004-sensitive-upstream-error-canary"),
		12,
		[]byte(`{"model":"grok004","messages":[{"role":"user","content":"grok004-private-prompt-canary"}]}`),
		nil,
	)
	var raw string
	if err := database.QueryRow(`SELECT data FROM requestDetails WHERE model = 'grok004'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{"grok004-private-prompt-canary", "grok004-sensitive-upstream-error-canary"} {
		if strings.Contains(raw, canary) {
			t.Fatalf("sensitive content persisted: %s", canary)
		}
	}
	if !strings.Contains(raw, "request failed") {
		t.Fatalf("missing safe failure summary: %s", raw)
	}
}

func TestLogUsageDefaultDoesNotPersistSuccessfulPromptContent(t *testing.T) {
	t.Setenv("CAPIMUX_CAPTURE_REQUEST_CONTENT", "")
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	handler := NewChatHandler(repo)
	handler.LogUsage(
		&UsageLogInfo{Provider: "openai", Model: "grok004-success", ConnectionID: "test"},
		&translator.OpenAIUsage{PromptTokens: 10, CompletionTokens: 5},
		12,
		[]byte(`{"model":"grok004-success","messages":[{"role":"user","content":"grok004-private-success-canary"}]}`),
		nil,
	)
	var raw string
	if err := database.QueryRow(`SELECT data FROM requestDetails WHERE model = 'grok004-success'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "grok004-private-success-canary") {
		t.Fatal("successful request content persisted by default")
	}
	if !strings.Contains(raw, "prompt_tokens") {
		t.Fatalf("usage metrics missing after privacy control: %s", raw)
	}
}

// With capture off, a failure keeps the upstream's machine identifiers (status,
// error.type, error.code) for diagnosis, and never its free-text message.
func TestLogFailureDefaultKeepsErrorTypeAndCodeWithoutContent(t *testing.T) {
	t.Setenv("CAPIMUX_CAPTURE_REQUEST_CONTENT", "")
	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	handler := NewChatHandler(db.NewRepo(database))
	cases := []struct {
		model, body, wantType, wantCode string
	}{
		{"grok004-ids", `{"error":{"message":"grok004-message-canary","type":"invalid_request_error","code":"context_length_exceeded"}}`, "invalid_request_error", "context_length_exceeded"},
		{"grok004-numeric", `{"error":{"message":"grok004-message-canary","type":"rate_limit","code":429}}`, "rate_limit", "429"},
		// Free text in type/code is dropped: it could echo the prompt.
		{"grok004-freetext", `{"error":{"message":"m","type":"grok004 prompt canary with spaces","code":"x"}}`, "", "x"},
	}
	for _, tc := range cases {
		handler.LogFailure(
			&UsageLogInfo{Provider: "openai", Model: tc.model, ConnectionID: "test"},
			nil,
			&upstreamError{StatusCode: 400, Body: []byte(tc.body)},
			12, nil, nil,
		)
		var raw string
		if err := database.QueryRow(`SELECT data FROM requestDetails WHERE model = ?`, tc.model).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var rec struct {
			Response struct {
				Error     string `json:"error"`
				Status    int    `json:"status"`
				ErrorType string `json:"errorType"`
				ErrorCode string `json:"errorCode"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			t.Fatal(err)
		}
		if rec.Response.ErrorType != tc.wantType || rec.Response.ErrorCode != tc.wantCode || rec.Response.Status != 400 {
			t.Errorf("%s: type=%q code=%q status=%d, want %q %q 400", tc.model, rec.Response.ErrorType, rec.Response.ErrorCode, rec.Response.Status, tc.wantType, tc.wantCode)
		}
		if strings.Contains(raw, "canary") {
			t.Errorf("%s: free text persisted with capture off: %s", tc.model, raw)
		}
		if rec.Response.Error != "request failed" {
			t.Errorf("%s: error = %q, want the safe summary", tc.model, rec.Response.Error)
		}
	}
}
