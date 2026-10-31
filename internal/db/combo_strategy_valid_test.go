package db

import "testing"

func TestIsValidComboStrategy(t *testing.T) {
	for _, ok := range []string{"fallback", "first-model", "round-robin", "roundrobin", "sticky", "capacity", "fusion"} {
		if !IsValidComboStrategy(ok) {
			t.Errorf("IsValidComboStrategy(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "random", "Round-Robin", "fusión", "weighted"} {
		if IsValidComboStrategy(bad) {
			t.Errorf("IsValidComboStrategy(%q) = true, want false", bad)
		}
	}
}
