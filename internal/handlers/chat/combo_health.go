package chat

import (
	"context"
	"errors"
	"net/http"
	"time"

	"9router/proxy/internal/combohealth"
)

// comboHealthKey maps a combo entry to its provider/model health key.
func (h *ChatHandler) comboHealthKey(entry string) string {
	info := h.resolveModelEntry(entry)
	if info == nil {
		return ""
	}
	return combohealth.Key(info.Provider, info.Model)
}

// healthFailureStatus reports whether an upstream status says something about
// the provider's health. Client errors (400, 404, 413, …) do not.
func healthFailureStatus(code int) bool {
	return code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || code >= 500
}

// recordComboHealth feeds the outcome of one upstream attempt to the health
// tracker. Client cancellations are ignored.
func recordComboHealth(ctx context.Context, provider, model string, completed bool, err error, latencyMs int64) {
	key := combohealth.Key(provider, model)
	if completed {
		combohealth.Default.Success(key, time.Duration(latencyMs)*time.Millisecond)
		return
	}
	if err == nil || isClientCanceled(ctx, err) {
		return
	}
	var ue *upstreamError
	if errors.As(err, &ue) {
		if healthFailureStatus(ue.StatusCode) || isEmptyCompletionError(ue) {
			combohealth.Default.Failure(key)
		}
		return
	}
	// Transport error (connection refused, timeout, reset).
	combohealth.Default.Failure(key)
}
