package discovery

import (
	"os"
	"testing"
)

// Regression: both discovery opt-ins must be OFF when the variables are not set at all.
func TestAuditFlags_DefaultOffWhenUnset(t *testing.T) {
	for _, name := range []string{StrictPricingEnvVar, PreserveAccountsEnvVar} {
		t.Setenv(name, "x")
		os.Unsetenv(name)
	}
	if StrictPricingEnabled() {
		t.Error("CAPIMUX_DISCOVERY_STRICT_PRICING must default to OFF")
	}
	if PreserveAccountsEnabled() {
		t.Error("CAPIMUX_DISCOVERY_PRESERVE_ACCOUNTS must default to OFF")
	}
}
