package deepseekweb

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"9router/proxy/internal/log"
)

const maxBody = 64 << 10

// Routes mounts the admin API. The caller MUST mount it behind
// middleware.RequireAdminAuth(): inference API keys never reach it.
func Routes(r chi.Router, get func() (*Manager, error)) {
	h := &handler{get: get}
	r.Get("/api/admin/deepseek-web/sessions", h.list)
	r.Post("/api/admin/deepseek-web/sessions", h.create)
	r.Get("/api/admin/deepseek-web/sessions/{id}", h.view)
	r.Patch("/api/admin/deepseek-web/sessions/{id}", h.update)
	r.Delete("/api/admin/deepseek-web/sessions/{id}", h.del)
	r.Post("/api/admin/deepseek-web/sessions/{id}/check", h.check)
	r.Get("/api/admin/deepseek-web/fallback", h.getFallback)
	r.Put("/api/admin/deepseek-web/fallback", h.setFallback)
}

type handler struct{ get func() (*Manager, error) }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	msg := "error interno de sesiones DeepSeek Web"
	switch {
	case errors.Is(err, ErrNotFound):
		status, msg = http.StatusNotFound, err.Error()
	case errors.Is(err, ErrConflict), errors.Is(err, ErrCheckInProgress), errors.Is(err, ErrSessionDisabled):
		status, msg = http.StatusConflict, err.Error()
	case errors.Is(err, ErrInvalidLabel), errors.Is(err, ErrEmptyCredential), errors.Is(err, ErrCredentialTooBig), errors.Is(err, ErrTooManySessions), errors.Is(err, errBadBody):
		status, msg = http.StatusBadRequest, err.Error()
	}
	writeJSON(w, status, map[string]string{"error": msg})
}

var errBadBody = errors.New("cuerpo JSON inválido")

func decode(r *http.Request, v any) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil || len(b) > maxBody || json.Unmarshal(b, v) != nil {
		return errBadBody
	}
	return nil
}

func (h *handler) mgr(w http.ResponseWriter) *Manager {
	m, err := h.get()
	if err != nil {
		log.Error("deepseek-web", "no se pudo abrir el almacén de sesiones", "error", err.Error())
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "almacén de sesiones DeepSeek Web no disponible"})
		return nil
	}
	return m
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	if m := h.mgr(w); m != nil {
		writeJSON(w, http.StatusOK, map[string]any{"sessions": m.List(), "fallback": m.Fallback(), "schemaPending": true})
	}
}

func (h *handler) view(w http.ResponseWriter, r *http.Request) {
	m := h.mgr(w)
	if m == nil {
		return
	}
	s, err := m.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s)
}

type createBody struct {
	Label      string     `json:"label"`
	Credential string     `json:"credential"`
	ExpiresAt  *time.Time `json:"expiresAt"`
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	m := h.mgr(w)
	if m == nil {
		return
	}
	var b createBody
	if err := decode(r, &b); err != nil {
		writeErr(w, err)
		return
	}
	s, err := m.Create(CreateInput{Label: b.Label, Credential: []byte(b.Credential), ExpiresAt: b.ExpiresAt})
	if err != nil {
		writeErr(w, err)
		return
	}
	log.Info("deepseek-web", "sesión creada", "id", s.ID)
	writeJSON(w, http.StatusCreated, s)
}

type updateBody struct {
	Rev            int64      `json:"rev"`
	Label          *string    `json:"label"`
	Credential     *string    `json:"credential"`
	Enabled        *bool      `json:"enabled"`
	ExpiresAt      *time.Time `json:"expiresAt"`
	ClearExpiresAt bool       `json:"clearExpiresAt"`
}

func (h *handler) update(w http.ResponseWriter, r *http.Request) {
	m := h.mgr(w)
	if m == nil {
		return
	}
	var b updateBody
	if err := decode(r, &b); err != nil {
		writeErr(w, err)
		return
	}
	in := UpdateInput{Rev: b.Rev, Label: b.Label, Enabled: b.Enabled, ExpiresAt: b.ExpiresAt, ClearExpires: b.ClearExpiresAt}
	if b.Credential != nil {
		in.Credential = []byte(*b.Credential)
	}
	id := chi.URLParam(r, "id")
	s, err := m.Update(id, in)
	if err != nil {
		writeErr(w, err)
		return
	}
	log.Info("deepseek-web", "sesión actualizada", "id", id, "credencial_rotada", b.Credential != nil)
	writeJSON(w, http.StatusOK, s)
}

func (h *handler) del(w http.ResponseWriter, r *http.Request) {
	m := h.mgr(w)
	if m == nil {
		return
	}
	id := chi.URLParam(r, "id")
	if err := m.Delete(id); err != nil {
		writeErr(w, err)
		return
	}
	log.Info("deepseek-web", "sesión eliminada", "id", id)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) check(w http.ResponseWriter, r *http.Request) {
	m := h.mgr(w)
	if m == nil {
		return
	}
	id := chi.URLParam(r, "id")
	s, err := m.Check(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	log.Info("deepseek-web", "sesión comprobada", "id", id, "status", string(s.Status))
	writeJSON(w, http.StatusOK, s)
}

func (h *handler) getFallback(w http.ResponseWriter, r *http.Request) {
	if m := h.mgr(w); m != nil {
		writeJSON(w, http.StatusOK, m.Fallback())
	}
}

func (h *handler) setFallback(w http.ResponseWriter, r *http.Request) {
	m := h.mgr(w)
	if m == nil {
		return
	}
	var b struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decode(r, &b); err != nil || b.Enabled == nil {
		writeErr(w, errBadBody)
		return
	}
	fs, err := m.SetFallback(*b.Enabled)
	if err != nil {
		writeErr(w, err)
		return
	}
	log.Info("deepseek-web", "respaldo InferX cambiado", "solicitado", fs.Requested, "efectivo", fs.Effective)
	writeJSON(w, http.StatusOK, fs)
}
