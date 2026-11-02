package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"9router/proxy/internal/auth"
	"9router/proxy/internal/combohealth"
	"9router/proxy/internal/db"
	"9router/proxy/internal/handlerutil"
	"9router/proxy/internal/middleware"
)

// setupComboHealthRoutes exposes the health tracker behind the "health" combo
// strategy. Admin only: a client API key gets 403 even with login off.
func setupComboHealthRoutes(r chi.Router, repo *db.Repo) {
	const what = "consultar la salud de los combos"
	guard := func(next http.HandlerFunc) http.HandlerFunc {
		guarded := adminOnly(repo, what, next)
		return func(w http.ResponseWriter, r *http.Request) {
			if auth.SessionValid(r) || auth.ValidCLIToken(r.Header.Get(auth.CLITokenHeader)) {
				next(w, r)
				return
			}
			if middleware.ExtractApiKey(r) != "" {
				handlerutil.WriteJSON(w, http.StatusForbidden, map[string]any{"error": clientCredentialMessage(what)})
				return
			}
			guarded(w, r)
		}
	}
	r.Get("/api/combo-health", guard(func(w http.ResponseWriter, _ *http.Request) {
		handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"members": combohealth.Default.Snapshot()})
	}))
	r.Delete("/api/combo-health", guard(func(w http.ResponseWriter, _ *http.Request) {
		combohealth.Default.Reset()
		w.WriteHeader(http.StatusNoContent)
	}))
}
