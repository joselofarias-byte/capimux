package providers

import (
	"net/http"
	"testing"
)

// Audit #131 (OpenCode inference v2): a 403 must be classified on its own
// terms, not as quota, rate limit or a transient 5xx.
func TestClassifyError_403IsAuthNotQuotaRateLimitOrTransient(t *testing.T) {
	for _, body := range []string{"", "Forbidden", `{"error":{"message":"access denied"}}`} {
		cls := ClassifyError(http.StatusForbidden, body, 2)
		if cls.Category != ErrAuth {
			t.Fatalf("403 body=%q: category %q, want %q", body, cls.Category, ErrAuth)
		}
		if !cls.ShouldFallback {
			t.Fatalf("403 body=%q must allow another independently configured account", body)
		}
		if cls.NewBackoffLevel != 2 || cls.CooldownMs != cooldownLong {
			t.Fatalf("403 body=%q must not use rate-limit backoff: %+v", body, cls)
		}
	}
	if got := ClassifyError(http.StatusTooManyRequests, "", 0).Category; got != ErrRateLimit {
		t.Fatalf("429 category %q, want %q", got, ErrRateLimit)
	}
	if got := ClassifyError(http.StatusServiceUnavailable, "", 0).Category; got != ErrTransient {
		t.Fatalf("503 category %q, want %q", got, ErrTransient)
	}
	if got := ClassifyError(http.StatusPaymentRequired, "", 0).Category; got != ErrQuota {
		t.Fatalf("402 category %q, want %q", got, ErrQuota)
	}
	// Text rules win over status by design: a 403 whose body says the quota is
	// exhausted (e.g. Gemini/Antigravity daily quota) is quota, not auth.
	if got := ClassifyError(http.StatusForbidden, "Quota exceeded for today", 0).Category; got != ErrQuota {
		t.Fatalf("403 with quota text: category %q, want %q", got, ErrQuota)
	}
}
