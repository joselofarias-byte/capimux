package handlers

import (
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"

	json "encoding/json/v2"

	"github.com/go-chi/chi/v5"

	"9router/proxy/internal/auth"
	"9router/proxy/internal/db"
	"9router/proxy/internal/handlerutil"
	"9router/proxy/internal/middleware"
	"9router/proxy/internal/promptinject"
)

// strictAdmin guards sensitive configuration (prompt injection, public
// identities, captures). It is stricter than adminOnly: a request that carries
// a client API key and no admin credential gets 403 even when the dashboard
// login is off, because those policies must never be readable or writable by
// an inference key (its own included).
func strictAdmin(repo *db.Repo, what string, next http.HandlerFunc) http.HandlerFunc {
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

// setupPromptInjectionRoutes mounts the admin API of the prompt injection
// module. Every route is admin-only.
func setupPromptInjectionRoutes(r chi.Router, repo *db.Repo) {
	h := &promptInjectHandler{repo: repo, store: promptinject.For(repo)}
	const what = "gestionar la inyección de prompts"
	r.Get("/api/prompt-injections", strictAdmin(repo, what, h.list))
	r.Put("/api/prompt-injections", strictAdmin(repo, what, h.put))
	r.Delete("/api/prompt-injections", strictAdmin(repo, what, h.del))
	r.Put("/api/prompt-injections/toggles", strictAdmin(repo, what, h.toggles))
	r.Post("/api/prompt-injections/upload", strictAdmin(repo, what, h.upload))
	r.Post("/api/prompt-injections/preview", strictAdmin(repo, what, h.preview))
	r.Get("/api/prompt-injections/captures", strictAdmin(repo, what, h.captures))
	r.Delete("/api/prompt-injections/captures", strictAdmin(repo, what, h.clearCaptures))
	const whatID = "gestionar la identidad pública de combos"
	r.Get("/api/combo-identities", strictAdmin(repo, whatID, h.identities))
	r.Put("/api/combo-identities", strictAdmin(repo, whatID, h.putIdentity))
	r.Delete("/api/combo-identities", strictAdmin(repo, whatID, h.delIdentity))
}

type promptInjectHandler struct {
	repo  *db.Repo
	store *promptinject.Store
}

func writePIError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, promptinject.ErrInvalid) {
		status = http.StatusBadRequest
	}
	handlerutil.WriteJSON(w, status, map[string]any{"error": err.Error()})
}

func (h *promptInjectHandler) list(w http.ResponseWriter, r *http.Request) {
	entries, err := h.store.Entries()
	if err != nil {
		writePIError(w, err)
		return
	}
	ids, err := h.store.Identities()
	if err != nil {
		writePIError(w, err)
		return
	}
	if entries == nil {
		entries = []promptinject.Entry{}
	}
	if ids == nil {
		ids = []promptinject.Identity{}
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"toggles":    promptinject.ReadToggles(h.repo),
		"entries":    entries,
		"identities": ids,
		"limits":     map[string]any{"maxContentBytes": promptinject.MaxContentBytes, "maxTargetLen": promptinject.MaxTargetLen},
	})
}

func decodeBody(r *http.Request, v any, max int64) error {
	b, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	if err != nil {
		return err
	}
	if int64(len(b)) > max {
		return errors.New("cuerpo demasiado grande")
	}
	return json.Unmarshal(b, v)
}

func (h *promptInjectHandler) put(w http.ResponseWriter, r *http.Request) {
	var e promptinject.Entry
	if err := decodeBody(r, &e, promptinject.MaxContentBytes*2); err != nil {
		handlerutil.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "JSON inválido: " + err.Error()})
		return
	}
	saved, err := h.store.Put(e)
	if err != nil {
		writePIError(w, err)
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, saved)
}

func (h *promptInjectHandler) del(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if err := h.store.Delete(promptinject.Scope(q.Get("scope")), q.Get("target")); err != nil {
		writePIError(w, err)
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *promptInjectHandler) toggles(w http.ResponseWriter, r *http.Request) {
	var in map[string]any
	if err := decodeBody(r, &in, 4096); err != nil {
		handlerutil.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "JSON inválido"})
		return
	}
	updates := map[string]any{}
	for _, k := range promptinject.SettingKeys {
		if v, ok := in[k]; ok {
			b, isBool := v.(bool)
			if !isBool {
				handlerutil.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": k + " debe ser booleano"})
				return
			}
			updates[k] = b
		}
	}
	if len(updates) == 0 {
		handlerutil.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "sin interruptores para actualizar"})
		return
	}
	if err := h.repo.UpdateSettingsRaw(updates); err != nil {
		writePIError(w, err)
		return
	}
	if v, ok := updates[promptinject.SettingCaptureEnabled]; ok && v == false {
		promptinject.ClearCaptures()
	}
	handlerutil.WriteJSON(w, http.StatusOK, promptinject.ReadToggles(h.repo))
}

// upload reads one .txt file and returns its text after the technical checks
// (size, UTF-8, no NUL). It stores nothing: the admin reviews the text in the
// editor and saves it with PUT.
func (h *promptInjectHandler) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, promptinject.MaxContentBytes+64*1024)
	if err := r.ParseMultipartForm(promptinject.MaxContentBytes + 64*1024); err != nil {
		handlerutil.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "formulario inválido o archivo demasiado grande"})
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		handlerutil.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "falta el archivo (campo «file»)"})
		return
	}
	defer f.Close()
	if !strings.EqualFold(filepath.Ext(hdr.Filename), ".txt") {
		handlerutil.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "solo se aceptan archivos .txt"})
		return
	}
	b, err := io.ReadAll(io.LimitReader(f, promptinject.MaxContentBytes+4))
	if err != nil {
		writePIError(w, err)
		return
	}
	b = []byte(strings.TrimPrefix(string(b), "\ufeff")) // BOM UTF-8
	if len(b) > promptinject.MaxContentBytes {
		handlerutil.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "el archivo supera el máximo de 64 KiB"})
		return
	}
	if !utf8.Valid(b) {
		handlerutil.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "el archivo no está en UTF-8"})
		return
	}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	if err := promptinject.ValidateContent(text); err != nil {
		writePIError(w, err)
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"content": text, "bytes": len(text), "chars": utf8.RuneCountInString(text), "filename": filepath.Base(hdr.Filename),
	})
}

type previewRequest struct {
	Path     string         `json:"path"`
	Model    string         `json:"model"`
	APIKeyID string         `json:"apiKeyId"`
	Body     map[string]any `json:"body"`
}

func (h *promptInjectHandler) preview(w http.ResponseWriter, r *http.Request) {
	var in previewRequest
	if err := decodeBody(r, &in, 1<<20); err != nil {
		handlerutil.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "JSON inválido"})
		return
	}
	if in.Path == "" {
		in.Path = "/chat/completions"
	}
	proto, ok := promptinject.ProtocolForPath(strings.TrimPrefix(in.Path, "/v1"))
	if !ok {
		handlerutil.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "ruta sin instrucciones inyectables: " + in.Path})
		return
	}
	var body []byte
	if in.Body != nil {
		if in.Model != "" {
			in.Body["model"] = in.Model
		} else if m, _ := in.Body["model"].(string); m != "" {
			in.Model = m
		}
		body, _ = json.Marshal(in.Body)
	}
	if strings.TrimSpace(in.Model) == "" {
		handlerutil.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "falta el modelo o combo"})
		return
	}
	combo, _ := h.repo.GetComboByName(in.Model)
	res, err := h.store.Preview(promptinject.ReadToggles(h.repo), proto,
		promptinject.Request{Model: in.Model, IsCombo: combo != nil, APIKeyID: in.APIKeyID}, body)
	if err != nil {
		handlerutil.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, res)
}

func (h *promptInjectHandler) captures(w http.ResponseWriter, r *http.Request) {
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"enabled":  promptinject.ReadToggles(h.repo).Capture,
		"limit":    promptinject.CaptureLimit,
		"captures": promptinject.Captures(),
	})
}

func (h *promptInjectHandler) clearCaptures(w http.ResponseWriter, r *http.Request) {
	promptinject.ClearCaptures()
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *promptInjectHandler) identities(w http.ResponseWriter, r *http.Request) {
	ids, err := h.store.Identities()
	if err != nil {
		writePIError(w, err)
		return
	}
	if ids == nil {
		ids = []promptinject.Identity{}
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"enabled":    promptinject.ReadToggles(h.repo).Identity,
		"identities": ids,
		"note":       "La identidad pública solo cambia el nombre de modelo que ve el cliente. No altera el modelo ni el proveedor reales ni sus políticas; uso, logs y estadísticas registran siempre el modelo y proveedor reales.",
	})
}

func (h *promptInjectHandler) putIdentity(w http.ResponseWriter, r *http.Request) {
	var id promptinject.Identity
	if err := decodeBody(r, &id, 4096); err != nil {
		handlerutil.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "JSON inválido"})
		return
	}
	if c, err := h.repo.GetComboByName(strings.TrimSpace(id.Combo)); err != nil || c == nil {
		handlerutil.WriteJSON(w, http.StatusNotFound, map[string]any{"error": "el combo no existe"})
		return
	}
	saved, err := h.store.PutIdentity(id)
	if err != nil {
		writePIError(w, err)
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, saved)
}

func (h *promptInjectHandler) delIdentity(w http.ResponseWriter, r *http.Request) {
	if err := h.store.DeleteIdentity(r.URL.Query().Get("combo")); err != nil {
		writePIError(w, err)
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}
