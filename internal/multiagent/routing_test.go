package multiagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRoutingStrategies(t *testing.T) {
	tests := []struct {
		name, strategy string
		failure        error
		want           int
	}{
		{"parallel retains all", "parallel", nil, 3},
		{"manual first only", "manual", nil, 1},
		{"priority success stops", "priority", nil, 1},
		{"fallback on rate limit", "fallback", &upstreamStatusError{429}, 3},
		{"priority on server error", "priority", &upstreamStatusError{503}, 3},
		{"no retry auth", "fallback", &upstreamStatusError{401}, 1},
		{"no retry timeout", "fallback", context.DeadlineExceeded, 1},
		{"no retry transport", "fallback", errors.New("secret transport detail"), 1},
		{"no retry empty output", "fallback", errEmptyResponse, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			invoke := func(context.Context, string, string) (string, error) { calls.Add(1); return "ok", tt.failure }
			req := Request{Prompt: "hello", Models: []string{"a/1", "b/2", "c/3"}, Strategy: tt.strategy}
			got := dispatch(context.Background(), req, 2, time.Second, invoke, &atomic.Uint64{})
			if len(got) != tt.want || int(calls.Load()) != tt.want {
				t.Fatalf("results %d calls %d want %d", len(got), calls.Load(), tt.want)
			}
			for _, r := range got {
				if strings.Contains(r.Error, "secret") {
					t.Fatal("error detail leaked")
				}
			}
		})
	}
}

func TestRoundRobin(t *testing.T) {
	var sequence atomic.Uint64
	req := Request{Prompt: "x", Models: []string{"p/a", "q/b"}, Strategy: "round-robin"}
	invoke := func(_ context.Context, m, p string) (string, error) { return m, nil }
	for i, want := range []string{"p/a", "q/b", "p/a"} {
		got := dispatch(context.Background(), req, 1, time.Second, invoke, &sequence)
		if len(got) != 1 || got[0].Model != want {
			t.Fatalf("iteration %d: %+v", i, got)
		}
	}
}

func TestRequestLimitsAndMetadata(t *testing.T) {
	tests := []struct {
		name, task, strategy string
		count                int
		want                 int
	}{
		{"ten models", "programming", "parallel", 10, 200},
		{"eleven rejected", "review", "fallback", 11, 400},
		{"legacy single", "", "", 1, 200},
		{"unknown task", "unknown", "manual", 2, 400},
		{"unknown strategy", "analysis", "unknown", 2, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			models := make([]string, tt.count)
			for i := range models {
				models[i] = fmt.Sprintf("provider/model-%d", i)
			}
			t.Setenv("CAPIMUX_MULTIAGENT_ALLOWED_MODELS", strings.Join(models, ","))
			payload, _ := json.Marshal(Request{Prompt: "x", Models: models, Task: tt.task, Strategy: tt.strategy, DryRun: true})
			h := Handler(func(context.Context, string, string) (string, error) { t.Fatal("dry run dispatched"); return "", nil })
			rec := httptest.NewRecorder()
			h(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(payload))))
			if rec.Code != tt.want {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHostFallbackStatusAndPrivacy(t *testing.T) {
	var calls []string
	invoke := HostInvoker(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		calls = append(calls, request.Model)
		if len(calls) == 1 {
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":"Bearer private-key"}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	})
	got := dispatch(context.Background(), Request{Models: []string{"a", "b", "c"}, Strategy: "fallback"}, 1, time.Second, invoke, &atomic.Uint64{})
	if len(got) != 2 || len(calls) != 2 || got[1].Output != "ok" {
		t.Fatalf("results %+v calls %v", got, calls)
	}
	payload, _ := json.Marshal(got)
	if strings.Contains(string(payload), "private-key") {
		t.Fatal("provider body leaked")
	}
}

func TestFallbackDeadlineWinsOverHTTPStatus(t *testing.T) {
	var calls int
	invoke := func(ctx context.Context, model, prompt string) (string, error) {
		calls++
		<-ctx.Done()
		return "", &upstreamStatusError{503}
	}
	got := dispatch(context.Background(), Request{Models: []string{"a", "b"}, Strategy: "fallback"}, 1, time.Millisecond, invoke, &atomic.Uint64{})
	if calls != 1 || len(got) != 1 || got[0].Error != "timed out" {
		t.Fatalf("calls %d results %+v", calls, got)
	}
}
