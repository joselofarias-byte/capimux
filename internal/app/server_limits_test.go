package app_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/app"
	"9router/proxy/internal/config"
	"go.uber.org/fx"
)

type limitsLifecycle struct{}

func (limitsLifecycle) Append(fx.Hook) {}

func limitsServer(handler http.Handler) *http.Server {
	return app.ProvideServer(app.ServerParams{
		Lifecycle: limitsLifecycle{}, Config: &config.Config{Host: "127.0.0.1", Port: 20130}, Handler: handler,
	})
}

func TestServerConnectionLimits(t *testing.T) {
	server := limitsServer(http.NotFoundHandler())
	if server.ReadHeaderTimeout != 10*time.Second || server.IdleTimeout != 120*time.Second || server.MaxHeaderBytes != 1<<20 {
		t.Fatalf("unexpected limits: header=%v idle=%v bytes=%d", server.ReadHeaderTimeout, server.IdleTimeout, server.MaxHeaderBytes)
	}
	if server.ReadTimeout != 0 || server.WriteTimeout != 0 {
		t.Fatal("whole-request deadlines would truncate long-lived streams")
	}
}

// Port of upstream #132: header/idle bounds must not become a write deadline.
func TestServerLimitsPreserveSSE(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := range 6 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(450 * time.Millisecond):
			}
			_, _ = fmt.Fprintf(w, "data: %d\n\n", i)
			w.(http.Flusher).Flush()
		}
	})
	server := httptest.NewUnstartedServer(handler)
	server.Config = limitsServer(handler)
	server.Start()
	defer server.Close()
	client := server.Client()
	client.Timeout = 8 * time.Second
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.Header.Get("Content-Type") != "text/event-stream" || !strings.Contains(string(body), "data: 5\n\n") {
		t.Fatalf("truncated SSE: %s", body)
	}
}
