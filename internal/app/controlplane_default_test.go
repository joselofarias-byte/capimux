package app

import (
	"os"
	"testing"
)

// Regression: the opt-in must be OFF when the variable is not set at all.
func TestStrictControlPlaneInit_DefaultOffWhenUnset(t *testing.T) {
	t.Setenv(StrictControlPlaneInitEnvVar, "x")
	os.Unsetenv(StrictControlPlaneInitEnvVar)
	if strictControlPlaneInitEnabled() {
		t.Fatal("CAPIMUX_STRICT_CONTROLPLANE_INIT must default to OFF")
	}
}
