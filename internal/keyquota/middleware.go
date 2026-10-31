package keyquota

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"

	json "encoding/json/v2"
)

type ctxKey struct{}

// TicketFrom returns the request's admission ticket, if any.
func TicketFrom(ctx context.Context) *Ticket {
	t, _ := ctx.Value(ctxKey{}).(*Ticket)
	return t
}

// AddTokens records tokens consumed by the request in ctx (no-op when the
// feature is off or the request carries no ticket).
func AddTokens(ctx context.Context, n int64) {
	TicketFrom(ctx).AddTokens(n)
}

// Middleware enforces the limits of the authenticated key on POST requests
// (inference and generation). keyID returns the authenticated key's id.
func Middleware(b Backend, keyID func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				next.ServeHTTP(w, r)
				return
			}
			id := keyID(r)
			if id == "" {
				next.ServeHTTP(w, r)
				return
			}
			t, d := For(b).Acquire(id)
			if d != nil {
				writeDenial(w, r, d)
				return
			}
			if t == nil {
				next.ServeHTTP(w, r)
				return
			}
			defer t.Release()
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, t)))
		})
	}
}

func writeDenial(w http.ResponseWriter, r *http.Request, d *Denial) {
	secs := int(math.Ceil(d.RetryAfter.Seconds()))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	errType := "rate_limit_error"
	if strings.HasPrefix(d.Limit, "tokens") {
		errType = "insufficient_quota"
	}
	var body any
	if strings.HasSuffix(r.URL.Path, "/messages") {
		// Anthropic clients parse {"type":"error","error":{...}}.
		body = map[string]any{"type": "error", "error": map[string]any{"type": "rate_limit_error", "message": d.Message}}
	} else {
		body = map[string]any{"error": map[string]any{"message": d.Message, "type": errType, "code": "api_key_" + d.Limit + "_exceeded"}}
	}
	_ = json.MarshalWrite(w, body)
}
