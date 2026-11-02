package chat

import (
	"net/http"
	"testing"
)

// A permanent 4xx inside a combo stays on the same entry (next account) and
// leaves no exclusion behind, so later entries can still use the account.
// This is main's behavior from #157; upstreamHopPlan still reports
// next_model for the single-provider path, which stops there as on main.
func TestFinishComboAttemptPermanentRequestTriesNextAccountWithoutExclusion(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity} {
		var exclude []string
		h := &ChatHandler{}
		res := h.finishComboAttempt(comboAttempt{
			Err:        &upstreamError{StatusCode: status, Body: []byte(`{"error":{"message":"nope"}}`)},
			ConnID:     "conn-1",
			Provider:   "deepseek",
			Model:      "deepseek-chat",
			ExcludeIDs: &exclude,
		})
		if res.NextModel || res.StopRequest {
			t.Errorf("status %d: result %+v, want the next account of the same entry", status, res)
		}
		if res.Upstream == nil || res.Upstream.StatusCode != status {
			t.Errorf("status %d: upstream %+v, want the original error kept", status, res.Upstream)
		}
		if len(exclude) != 0 {
			t.Errorf("status %d: exclusions %v, want none", status, exclude)
		}
	}
}
