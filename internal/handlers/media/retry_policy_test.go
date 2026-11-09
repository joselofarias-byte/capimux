package media

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"9router/proxy/internal/db"
	"9router/proxy/internal/providers"
)

const agImageOK = `{"response":{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="}}]}}]}}`

// Image generation is non-idempotent: after an ambiguous failure on the first
// account the second one must NOT generate again; after a clear rejection
// (4xx) it may.
func TestAntigravityImageNeverRetriesAmbiguousFailures(t *testing.T) {
	cases := []struct {
		name       string
		first      func(http.ResponseWriter)
		wantSecond int
		wantStatus int
	}{
		{"503 no rota", func(w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) }, 0, http.StatusBadGateway},
		{"500 no rota", func(w http.ResponseWriter) { w.WriteHeader(http.StatusInternalServerError) }, 0, http.StatusBadGateway},
		{"corte tras enviar no rota", func(http.ResponseWriter) { panic(http.ErrAbortHandler) }, 0, http.StatusBadGateway},
		{"200 ilegible no rota", func(w http.ResponseWriter) { w.WriteHeader(http.StatusOK); _, _ = w.Write([]byte("{")) }, 0, http.StatusBadGateway},
		{"429 rota", func(w http.ResponseWriter) { w.WriteHeader(http.StatusTooManyRequests) }, 1, http.StatusOK},
		{"401 rota", func(w http.ResponseWriter) { w.WriteHeader(http.StatusUnauthorized) }, 1, http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var mu sync.Mutex
			hits := map[string]int{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				key := r.Header.Get("Authorization")
				mu.Lock()
				hits[key]++
				mu.Unlock()
				if key == "Bearer ag-1" {
					c.first(w)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(agImageOK))
			}))
			defer upstream.Close()

			ag := providers.KnownProviders["antigravity"]
			origBase := ag.BaseURL
			ag.BaseURL = upstream.URL
			providers.KnownProviders["antigravity"] = ag
			defer func() {
				ag.BaseURL = origBase
				providers.KnownProviders["antigravity"] = ag
			}()

			database, cleanup := setupMultimodalTestDB(t)
			defer cleanup()
			for i, key := range []string{"ag-1", "ag-2"} {
				data, _ := json.Marshal(map[string]any{"apiKey": key, "projectId": "proj"})
				if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES (?, 'antigravity', 'oauth', ?, ?, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
					"conn-"+key, key, i+1, string(data)); err != nil {
					t.Fatal(err)
				}
			}
			handler := newTestMediaHandler(db.NewRepo(database))
			req := httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(`{"model":"antigravity/gemini-3.1-flash-image","prompt":"un faro al atardecer"}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.HandleImages(rec, req)

			mu.Lock()
			defer mu.Unlock()
			if hits["Bearer ag-1"] != 1 {
				t.Fatalf("la primera cuenta recibió %d intentos (hits=%v)", hits["Bearer ag-1"], hits)
			}
			if hits["Bearer ag-2"] != c.wantSecond {
				t.Fatalf("segunda cuenta: %d intentos, se esperaban %d", hits["Bearer ag-2"], c.wantSecond)
			}
			if rec.Code != c.wantStatus {
				t.Fatalf("status = %d, se esperaba %d: %s", rec.Code, c.wantStatus, rec.Body.String())
			}
		})
	}
}
