package promptinject

import (
	"context"
	json "encoding/json/v2"
	"strings"
)

// ScrubMarker replaces injected instructions in persisted request logs.
const ScrubMarker = "[instrucciones inyectadas omitidas]"

type injectedKey struct{}

// withInjected records, for this request only, the texts that were injected,
// so persistent logs can leave them out.
func withInjected(ctx context.Context, texts []string) context.Context {
	return context.WithValue(ctx, injectedKey{}, texts)
}

// Injected reports whether injection rewrote the request carried by ctx.
func Injected(ctx context.Context) bool {
	t, _ := ctx.Value(injectedKey{}).([]string)
	return len(t) > 0
}

// ScrubForLog returns body with every injected text replaced by ScrubMarker.
// The request details table persists message text; injected instructions are
// sensitive configuration and are not persisted there (the opt-in, in-memory
// capture is the only place an admin sees them in a real request). Bodies of
// requests without injection are returned unchanged.
func ScrubForLog(ctx context.Context, body []byte) []byte {
	if ctx == nil {
		return body
	}
	texts, _ := ctx.Value(injectedKey{}).([]string)
	if len(texts) == 0 || len(body) == 0 {
		return body
	}
	var v any
	if json.Unmarshal(body, &v) != nil {
		return nil // unknown shape: persist nothing rather than leak
	}
	v = scrubValue(v, texts)
	out, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return out
}

func scrubValue(v any, texts []string) any {
	switch t := v.(type) {
	case string:
		for _, s := range texts {
			if s != "" && strings.Contains(t, s) {
				t = strings.ReplaceAll(t, s, ScrubMarker)
			}
		}
		return t
	case []any:
		for i := range t {
			t[i] = scrubValue(t[i], texts)
		}
		return t
	case map[string]any:
		for k, x := range t {
			t[k] = scrubValue(x, texts)
		}
		return t
	}
	return v
}
