package chat

import (
	"net/http"
	"os"
	"strings"

	"9router/proxy/internal/controlplane/registry"
	"9router/proxy/internal/controlplane/routing"
	"9router/proxy/internal/controlplane/trust"
	"9router/proxy/internal/handlerutil"
)

// HandleAdminRegistry returns the active snapshot payload in memory.
func (h *ChatHandler) HandleAdminRegistry(w http.ResponseWriter, r *http.Request) {
	state := registry.GetActiveState()
	if state == nil {
		handlerutil.WriteJSONError(w, http.StatusServiceUnavailable, "Registry not initialized")
		return
	}

	// AuthData has been safely removed from the Registry schema,
	// so the snapshot payload is safe to return directly.
	handlerutil.WriteJSON(w, http.StatusOK, state)
}

// HandleAdminExplainRoute explains routing logic for a model and policy.
func (h *ChatHandler) HandleAdminExplainRoute(w http.ResponseWriter, r *http.Request) {
	model := r.URL.Query().Get("model")
	policyParam := r.URL.Query().Get("policy")
	if model == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "Missing 'model' parameter")
		return
	}

	policy := routing.PolicyBalanced
	if policyParam != "" {
		policy = routing.Policy(policyParam)
		// With CAPIMUX_EXPLAIN_ROUTE_STRICT_POLICY=1 (audit phase 1, #103, off
		// by default) reject invented policies instead of explaining a route
		// no request can take.
		switch policy {
		case routing.PolicyBalanced, routing.PolicyFreeOnly, routing.PolicyFreeFirst, routing.PolicyTrusted:
		default:
			if explainRouteStrictPolicy() {
				handlerutil.WriteJSONError(w, http.StatusBadRequest, "Unsupported 'policy' parameter")
				return
			}
		}
	}

	// For explanation, we just instantiate the Engine directly (in production, passed down)
	engine := &routing.Engine{
		TrustManager: trust.NewManager(),
	}

	candidates := engine.SelectCandidates(model, policy)

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"requestedModel": model,
		"policy":         policy,
		"candidates":     candidates,
	})
}

// ExplainRouteStrictPolicyEnvVar turns on policy validation in explain-route.
const ExplainRouteStrictPolicyEnvVar = "CAPIMUX_EXPLAIN_ROUTE_STRICT_POLICY"

func explainRouteStrictPolicy() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(ExplainRouteStrictPolicyEnvVar))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
