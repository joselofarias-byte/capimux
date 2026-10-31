package promptinject

import (
	"bytes"
	json "encoding/json/v2"
	"io"
	"net/http"
	"sync"

	"9router/proxy/internal/log"
)

// ComboLookup reports whether name is a configured combo.
type ComboLookup func(name string) bool

// KeyIDFunc returns the authenticated client API key id (never the secret).
type KeyIDFunc func(r *http.Request) string

var (
	storesMu sync.Mutex
	stores   = map[Backend]*Store{}
)

// For returns the process-wide store of b, so the admin API and the request
// middleware share one cache (writes invalidate it immediately).
func For(b Backend) *Store {
	storesMu.Lock()
	defer storesMu.Unlock()
	s, ok := stores[b]
	if !ok {
		s = NewStore(b)
		stores[b] = s
	}
	return s
}

// Middleware applies prompt injection and the combo public identity to
// inference requests. With both toggles off (the default) it only reads the
// settings row and calls next unchanged.
func Middleware(b Backend, isCombo ComboLookup, keyID KeyIDFunc) func(http.Handler) http.Handler {
	store := For(b)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			proto, ok := ProtocolForPath(r.URL.Path)
			if !ok || r.Method != http.MethodPost || r.Body == nil {
				next.ServeHTTP(w, r)
				return
			}
			t := ReadToggles(b)
			if !t.Injection && !t.Identity {
				next.ServeHTTP(w, r)
				return
			}
			body, err := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if err != nil {
				// Let the handler report the read failure as it always did.
				r.Body = io.NopCloser(bytes.NewReader(body))
				next.ServeHTTP(w, r)
				return
			}
			var head struct {
				Model string `json:"model"`
			}
			_ = json.Unmarshal(body, &head)
			combo := head.Model != "" && isCombo != nil && isCombo(head.Model)
			id := ""
			if keyID != nil {
				id = keyID(r)
			}

			if t.Injection && head.Model != "" {
				plan, err := store.Match(Request{Model: head.Model, IsCombo: combo, APIKeyID: id})
				if err != nil {
					log.Warn("promptinject", "no se pudieron leer las capas; se envía sin inyección", "error", err)
				} else if !plan.Empty() {
					out, aerr := Apply(proto, body, plan)
					if aerr != nil {
						// Content is never logged, only the reason.
						log.Warn("promptinject", "inyección omitida", "path", r.URL.Path, "error", aerr)
					} else {
						body = out
						r = r.WithContext(withInjected(r.Context(), plan.texts()))
						log.Info("promptinject", "capas aplicadas", "path", r.URL.Path, "model", head.Model, "layers", len(plan.Layers), "replace", plan.Replace)
						if t.Capture {
							recordCapture(Capture{Path: r.URL.Path, Model: head.Model, APIKeyID: id, Layers: plan.Layers, Body: string(body)})
						}
					}
				}
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))

			if t.Identity && combo {
				if public, ok := store.IdentityFor(head.Model); ok {
					iw := newIdentityWriter(w, public)
					defer iw.finish()
					next.ServeHTTP(iw, r)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
