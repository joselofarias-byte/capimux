package dashboard

import (
	"bufio"
	"context"
	"database/sql"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"9router/proxy/internal/providers"
	"9router/proxy/internal/proxy/executor"
)

func TestCustomModelsHTTPDiscovery(t *testing.T) {
	tests := []struct {
		name           string
		upstreamStatus int
		body           string
		wantStatus     int
		wantID         string
	}{
		{"dynamic data envelope", 200, `{"data":[{"id":"tenant/model-new:revision"}]}`, 200, "tenant/model-new:revision"},
		{"alternate models envelope", 200, `{"models":[{"id":"other-model"}]}`, 200, "other-model"},
		{"empty catalog", 200, `{"data":[]}`, 200, ""},
		{"unauthorized body is not exposed", 401, `{"error":{"message":"sk-private-model-test"}}`, 401, ""},
		{"rate limited body is not exposed", 429, `{"error":{"message":"sk-private-model-test"}}`, 429, ""},
		{"provider failure body is not exposed", 503, `{"error":{"message":"sk-private-model-test"}}`, 503, ""},
		{"invalid JSON body is not exposed", 200, `sk-private-model-test`, 502, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
					t.Errorf("probe = %s %s, want GET /v1/models", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer sk-private-model-test" {
					t.Error("missing expected upstream Bearer authentication")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.upstreamStatus)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer upstream.Close()
			repo, cleanup := setupNodeTestDB(t)
			defer cleanup()
			seedCustomModelsHTTPConnection(t, repo.RawDB(), upstream.URL+"/v1/")
			router := setupTestRouter(repo)
			for range 2 {
				req := httptest.NewRequest(http.MethodGet, "/api/providers/custom-http/models", nil)
				req = req.WithContext(withProbeClient(req.Context(), upstream.Client()))
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				if rec.Code != tt.wantStatus {
					t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
				}
				if strings.Contains(rec.Body.String(), "sk-private-model-test") {
					t.Fatal("model endpoint exposed upstream credentials/body")
				}
				if tt.wantStatus != http.StatusOK {
					continue
				}
				var out struct {
					Models []struct {
						ID string `json:"id"`
					} `json:"models"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
					t.Fatal(err)
				}
				if tt.wantID == "" {
					if len(out.Models) != 0 {
						t.Fatalf("models = %+v, want empty", out.Models)
					}
				} else if len(out.Models) != 1 || out.Models[0].ID != tt.wantID {
					t.Fatalf("models = %+v, want exact ID %q", out.Models, tt.wantID)
				}
			}
			if calls.Load() != 2 {
				t.Errorf("probes = %d, want one per discovery request", calls.Load())
			}
		})
	}
}

func TestCustomModelsHTTPCancellation(t *testing.T) {
	started, aborted := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(aborted)
	}))
	defer upstream.Close()
	repo, cleanup := setupNodeTestDB(t)
	defer cleanup()
	seedCustomModelsHTTPConnection(t, repo.RawDB(), upstream.URL+"/v1")
	router := setupTestRouter(repo)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/providers/custom-http/models", nil)
	req = req.WithContext(withProbeClient(ctx, upstream.Client()))
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); router.ServeHTTP(rec, req) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("catalog probe did not start")
	}
	cancel()
	select {
	case <-aborted:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream request was not canceled")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("catalog handler did not return after cancellation")
	}
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

func seedCustomModelsHTTPConnection(t *testing.T, conn *sql.DB, baseURL string) {
	t.Helper()
	data, err := json.Marshal(struct {
		APIKey               string            `json:"apiKey"`
		ProviderSpecificData map[string]string `json:"providerSpecificData"`
	}{"sk-private-model-test", map[string]string{"baseUrl": baseURL, "apiType": "chat", "prefix": "custom"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(`INSERT INTO providerConnections
 (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt)
 VALUES ('custom-http', 'openai-compatible-chat-http', 'apikey', 'Custom HTTP', 1, 1, ?, '2026-01-01', '2026-01-01')`, string(data))
	if err != nil {
		t.Fatal(err)
	}
}

func TestCustomProviderSSEHTTPCancellation(t *testing.T) {
	const frame = `data: {"id":"fixture","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_fixture","type":"function","function":{"name":"inspect","arguments":"{}"}}]}}]}`
	aborted := make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost {
			t.Errorf("unexpected streaming request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-private-stream-test" {
			t.Error("missing streaming Bearer authentication")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(frame + "\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(aborted)
	}))
	defer upstream.Close()
	done := make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		_ = executor.ForwardOpenAI(w, &executor.Request{
			Ctx: r.Context(), Client: upstream.Client(),
			Config:   &providers.ProviderConfig{BaseURL: upstream.URL + "/v1/chat/completions"},
			APIKey:   "sk-private-stream-test",
			Body:     []byte(`{"model":"arbitrary-model","messages":[{"role":"user","content":"fixture"}],"stream":true}`),
			IsStream: true,
		})
	}))
	defer gateway.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := gateway.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("response = %d %q, want SSE", res.StatusCode, res.Header.Get("Content-Type"))
	}
	line, err := bufio.NewReader(res.Body).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(line) != frame {
		t.Fatalf("SSE frame changed: %q", line)
	}
	cancel()
	select {
	case <-aborted:
	case <-time.After(3 * time.Second):
		t.Fatal("stream upstream did not observe client cancellation")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream forwarding did not terminate")
	}
	if calls.Load() != 1 {
		t.Errorf("stream calls = %d, want no replay", calls.Load())
	}
}
