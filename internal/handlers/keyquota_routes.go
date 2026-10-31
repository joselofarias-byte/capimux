package handlers

import (
	"errors"
	"io"
	"net/http"
	"sort"

	json "encoding/json/v2"

	"github.com/go-chi/chi/v5"

	"9router/proxy/internal/auth"
	"9router/proxy/internal/db"
	"9router/proxy/internal/handlerutil"
	"9router/proxy/internal/keyquota"
	"9router/proxy/internal/middleware"
)

// quotaAdminOnly guards the per-key quota API. Like the other sensitive admin
// APIs it is stricter than adminOnly: a request carrying a client API key and
// no admin credential gets 403 even with the dashboard login off, so a key can
// neither read other keys' limits nor raise its own.
func quotaAdminOnly(repo *db.Repo, next http.HandlerFunc) http.HandlerFunc {
	const what = "gestionar las cuotas de API keys"
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

func setupKeyQuotaRoutes(r chi.Router, repo *db.Repo) {
	h := &keyQuotaHandler{repo: repo, lim: keyquota.For(repo)}
	r.Get("/api/api-key-limits", quotaAdminOnly(repo, h.list))
	r.Put("/api/api-key-limits/toggle", quotaAdminOnly(repo, h.toggle))
	r.Put("/api/api-key-limits/{id}", quotaAdminOnly(repo, h.put))
	r.Delete("/api/api-key-limits/{id}", quotaAdminOnly(repo, h.del))
}

type keyQuotaHandler struct {
	repo *db.Repo
	lim  *keyquota.Limiter
}

type keyQuotaRow struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Active bool            `json:"active"`
	Limits keyquota.Limits `json:"limits"`
	Usage  keyquota.Usage  `json:"usage"`
}

func (h *keyQuotaHandler) list(w http.ResponseWriter, r *http.Request) {
	keys, err := h.repo.GetApiKeys()
	if err != nil {
		handlerutil.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "no se pudieron leer las API keys"})
		return
	}
	enabled, limits, usage := h.lim.Snapshot()
	rows := make([]keyQuotaRow, 0, len(keys))
	for _, k := range keys {
		name := ""
		if k.Name != nil {
			name = *k.Name
		}
		// The key secret itself is never returned.
		rows = append(rows, keyQuotaRow{ID: k.ID, Name: name, Active: k.IsActive == 1, Limits: limits[k.ID], Usage: usage[k.ID]})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "keys": rows})
}

func (h *keyQuotaHandler) toggle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.UnmarshalRead(io.LimitReader(r.Body, 4096), &body); err != nil {
		handlerutil.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "JSON inválido"})
		return
	}
	if err := h.lim.SetEnabled(body.Enabled); err != nil {
		handlerutil.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "no se pudo guardar"})
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"enabled": body.Enabled})
}

func (h *keyQuotaHandler) keyExists(id string) bool {
	keys, err := h.repo.GetApiKeys()
	if err != nil {
		return false
	}
	for _, k := range keys {
		if k.ID == id {
			return true
		}
	}
	return false
}

func (h *keyQuotaHandler) put(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !h.keyExists(id) {
		handlerutil.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "la API key no existe"})
		return
	}
	var lim keyquota.Limits
	if err := json.UnmarshalRead(io.LimitReader(r.Body, 16<<10), &lim); err != nil {
		handlerutil.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "JSON inválido"})
		return
	}
	saved, err := h.lim.SetLimits(id, lim)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, keyquota.ErrInvalid) {
			status = http.StatusBadRequest
		}
		handlerutil.WriteJSON(w, status, map[string]any{"error": err.Error()})
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, saved)
}

func (h *keyQuotaHandler) del(w http.ResponseWriter, r *http.Request) {
	if err := h.lim.DeleteLimits(chi.URLParam(r, "id")); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, keyquota.ErrInvalid) {
			status = http.StatusBadRequest
		}
		handlerutil.WriteJSON(w, status, map[string]any{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
