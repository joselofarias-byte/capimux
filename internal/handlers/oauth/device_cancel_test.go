package oauth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// hangingDeviceClient replaces deviceClient with one whose transport never
// answers: it only returns once the request context is done, exactly like an
// upstream that accepted the connection and then stalled. No real network is
// touched. The client keeps the production 15 s timeout so that, without
// context propagation, a call is only freed after 15 s.
func hangingDeviceClient(t *testing.T) *atomic.Int64 {
	t.Helper()
	var hits atomic.Int64
	original := deviceClient
	deviceClient = &http.Client{Timeout: 15 * time.Second, Transport: devicePollFixture(func(r *http.Request) (*http.Response, error) {
		hits.Add(1)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	t.Cleanup(func() { deviceClient = original })
	return &hits
}

const deviceCancelBound = 3 * time.Second

func TestDeviceStart_HonoursRequestCancellation(t *testing.T) {
	for _, provider := range []string{"kilocode", "grok-cli", "github", "kiro", "kimi", "codebuddy-cn", "codebuddy-intl", "muse"} {
		t.Run(provider, func(t *testing.T) {
			hits := hangingDeviceClient(t)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			req := httptest.NewRequest("POST", "/api/oauth/device/start", strings.NewReader(`{"provider":"`+provider+`"}`)).WithContext(ctx)
			rec := httptest.NewRecorder()
			start := time.Now()
			NewOAuthHandler(nil).HandleDeviceStart(rec, req)
			if elapsed := time.Since(start); elapsed > deviceCancelBound {
				t.Fatalf("start kept running %v after the client went away", elapsed)
			}
			if hits.Load() == 0 {
				t.Fatal("start never reached the upstream; test is not exercising the call")
			}
			if rec.Code == http.StatusOK {
				t.Fatalf("cancelled start reported success: %s", rec.Body.String())
			}
		})
	}
}

func TestDevicePoll_HonoursRequestCancellation(t *testing.T) {
	cases := map[string]string{
		"qoder":          `{"verifier":"v","machineId":"m"}`,
		"kilocode":       `{}`,
		"grok-cli":       `{}`,
		"github":         `{}`,
		"kiro":           `{"region":"us-east-1","clientId":"c","clientSecret":"s"}`,
		"kimi":           `{"deviceId":"d"}`,
		"codebuddy-intl": `{}`,
		"muse":           `{}`,
	}
	for provider, session := range cases {
		t.Run(provider, func(t *testing.T) {
			hits := hangingDeviceClient(t)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			body := `{"provider":"` + provider + `","device_code":"code","session":` + session + `}`
			req := httptest.NewRequest("POST", "/api/oauth/device/poll", strings.NewReader(body)).WithContext(ctx)
			rec := httptest.NewRecorder()
			start := time.Now()
			NewOAuthHandler(nil).HandleDevicePoll(rec, req)
			if elapsed := time.Since(start); elapsed > deviceCancelBound {
				t.Fatalf("poll kept running %v after the client went away", elapsed)
			}
			if hits.Load() == 0 {
				t.Fatal("poll never reached the upstream; test is not exercising the call")
			}
			if strings.Contains(rec.Body.String(), `"status":"success"`) {
				t.Fatalf("cancelled poll reported success: %s", rec.Body.String())
			}
		})
	}
}

// Even when the caller never cancels (a script, a stuck proxy), one start or
// poll request has an overall budget.
func TestDeviceFlow_OverallTimeoutBoundsTheRequest(t *testing.T) {
	hangingDeviceClient(t)
	previous := deviceFlowTimeout
	deviceFlowTimeout = 200 * time.Millisecond
	t.Cleanup(func() { deviceFlowTimeout = previous })

	start := time.Now()
	rec := httptest.NewRecorder()
	NewOAuthHandler(nil).HandleDeviceStart(rec, httptest.NewRequest("POST", "/api/oauth/device/start", strings.NewReader(`{"provider":"github"}`)))
	if elapsed := time.Since(start); elapsed > deviceCancelBound {
		t.Fatalf("start ignored the overall budget: %v", elapsed)
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}

	start = time.Now()
	rec = httptest.NewRecorder()
	NewOAuthHandler(nil).HandleDevicePoll(rec, httptest.NewRequest("POST", "/api/oauth/device/poll", strings.NewReader(`{"provider":"github","device_code":"code"}`)))
	if elapsed := time.Since(start); elapsed > deviceCancelBound {
		t.Fatalf("poll ignored the overall budget: %v", elapsed)
	}
}

// Muse retries the key mint on 429 with 5 s and 10 s waits; a cancelled
// request must not sit through them.
func TestMuseMintKey_BackoffHonoursCancellation(t *testing.T) {
	var hits atomic.Int64
	original := deviceClient
	deviceClient = &http.Client{Transport: devicePollFixture(func(r *http.Request) (*http.Response, error) {
		hits.Add(1)
		return &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
	})}
	t.Cleanup(func() { deviceClient = original })

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := museMintKey(ctx, "token")
	if elapsed := time.Since(start); elapsed > deviceCancelBound {
		t.Fatalf("mint backoff ignored cancellation: %v", elapsed)
	}
	if err == nil {
		t.Fatal("cancelled mint returned no error")
	}
	if hits.Load() != 1 {
		t.Fatalf("mint attempts after cancellation = %d, want 1", hits.Load())
	}
}

// Regression: a normal, answering upstream still completes the flow.
func TestDeviceStart_AnsweringUpstreamStillWorks(t *testing.T) {
	original := deviceClient
	deviceClient = &http.Client{Transport: devicePollFixture(func(r *http.Request) (*http.Response, error) {
		if r.Context().Err() != nil {
			t.Error("request context already done on a healthy call")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: r,
			Body: io.NopCloser(strings.NewReader(`{"device_code":"dc","user_code":"UC","verification_uri":"https://github.com/login/device","interval":5,"expires_in":900}`))}, nil
	})}
	t.Cleanup(func() { deviceClient = original })
	rec := httptest.NewRecorder()
	NewOAuthHandler(nil).HandleDeviceStart(rec, httptest.NewRequest("POST", "/api/oauth/device/start", strings.NewReader(`{"provider":"github"}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"user_code":"UC"`) {
		t.Fatalf("healthy start = %d %s", rec.Code, rec.Body.String())
	}
}
