package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"9router/proxy/internal/controlplane/registry"
)

func TestHandleAdminRegistry_NoAuthData(t *testing.T) {
	// Setup mock state
	registry.InitRegistry(nil)
	state := registry.GetActiveState()
	state.Accounts["test-acc"] = &registry.Account{
		ID:       "test-acc",
		IsActive: true,
	}

	h := &ChatHandler{} // lightweight stub
	req, _ := http.NewRequest("GET", "/api/admin/registry", nil)
	rr := httptest.NewRecorder()

	h.HandleAdminRegistry(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", rr.Code)
	}

	var res registry.RegistryState
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	acc := res.Accounts["test-acc"]
	if acc == nil {
		t.Fatal("Expected account in response")
	}
}

func TestHandleAdminExplainRoute(t *testing.T) {
	h := &ChatHandler{}
	req, _ := http.NewRequest("GET", "/api/admin/explain-route?model=gpt-4&policy=free-first", nil)
	rr := httptest.NewRecorder()

	h.HandleAdminExplainRoute(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", rr.Code)
	}

	// Validate JSON response shape
	var res map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if res["requestedModel"] != "gpt-4" {
		t.Errorf("Expected model 'gpt-4', got %v", res["requestedModel"])
	}
	if res["policy"] != "free-first" {
		t.Errorf("Expected policy 'free-first', got %v", res["policy"])
	}
}

func TestHandleAdminExplainRoute_RejectsUnknownPolicy(t *testing.T) {
	t.Setenv(ExplainRouteStrictPolicyEnvVar, "1")
	h := &ChatHandler{}
	for _, p := range []string{"made-up", "FREE-ONLY", "free_only", "balanced;drop"} {
		req := httptest.NewRequest(http.MethodGet, "/api/admin/explain-route?model=gpt-4&policy="+url.QueryEscape(p), nil)
		rec := httptest.NewRecorder()
		h.HandleAdminExplainRoute(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("policy %q: expected 400, got %d", p, rec.Code)
		}
	}
}

func TestHandleAdminExplainRoute_AcceptsKnownAndDefaultPolicies(t *testing.T) {
	t.Setenv(ExplainRouteStrictPolicyEnvVar, "1")
	h := &ChatHandler{}
	for _, p := range []string{"", "balanced", "free-only", "free-first", "trusted-only"} {
		req := httptest.NewRequest(http.MethodGet, "/api/admin/explain-route?model=gpt-4&policy="+p, nil)
		rec := httptest.NewRecorder()
		h.HandleAdminExplainRoute(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("policy %q: expected 200, got %d", p, rec.Code)
		}
	}
}

// Regression: without the opt-in an unknown policy keeps answering 200.
func TestHandleAdminExplainRoute_UnknownPolicyDefaultUnchanged(t *testing.T) {
	t.Setenv(ExplainRouteStrictPolicyEnvVar, "")
	h := &ChatHandler{}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/explain-route?model=gpt-4&policy=made-up", nil)
	rec := httptest.NewRecorder()
	h.HandleAdminExplainRoute(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("default mode must not change the response, got %d", rec.Code)
	}
}
