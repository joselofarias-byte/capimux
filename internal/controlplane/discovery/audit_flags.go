package discovery

import (
	"os"
	"strings"
)

// StrictPricingEnvVar enables cost-authoritative free-tier classification
// (audit phase 1, #98 A1). Off by default so existing catalogs keep their
// current classification until the owner opts in.
const StrictPricingEnvVar = "CAPIMUX_DISCOVERY_STRICT_PRICING"

// PreserveAccountsEnvVar makes discovery publish snapshots without replacing
// the in-memory account map (audit phase 1, #98 A3). Off by default.
const PreserveAccountsEnvVar = "CAPIMUX_DISCOVERY_PRESERVE_ACCOUNTS"

func envFlagOn(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// StrictPricingEnabled reports whether CAPIMUX_DISCOVERY_STRICT_PRICING is on.
func StrictPricingEnabled() bool { return envFlagOn(StrictPricingEnvVar) }

// PreserveAccountsEnabled reports whether CAPIMUX_DISCOVERY_PRESERVE_ACCOUNTS is on.
func PreserveAccountsEnabled() bool { return envFlagOn(PreserveAccountsEnvVar) }
