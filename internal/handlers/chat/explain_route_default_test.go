package chat

import (
	"os"
	"testing"
)

// Regression: the opt-in must be OFF when the variable is not set at all.
func TestExplainRouteStrictPolicy_DefaultOffWhenUnset(t *testing.T) {
	t.Setenv(ExplainRouteStrictPolicyEnvVar, "x")
	os.Unsetenv(ExplainRouteStrictPolicyEnvVar)
	if explainRouteStrictPolicy() {
		t.Fatal("CAPIMUX_EXPLAIN_ROUTE_STRICT_POLICY must default to OFF")
	}
}
