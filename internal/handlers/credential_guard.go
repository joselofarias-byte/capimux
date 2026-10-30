package handlers

import (
	"net/http"

	"9router/proxy/internal/auth"
	"9router/proxy/internal/db"
	"9router/proxy/internal/handlerutil"
)

// credentialWriteKind classifies the OAuth/import routes that store provider
// credentials, for requests authenticated only by a client API key.
type credentialWriteKind int

const (
	// credentialAppendOnly routes always insert a new row with a fresh id and
	// take no destination fields. Open to a client key unless an admin set
	// allowClientCredentialImport=false.
	credentialAppendOnly credentialWriteKind = iota
	// credentialUpsert routes match the account (email, token hash, …) and
	// rewrite an existing row in place. A client key could replace the
	// owner's credentials with its own, so they are admin-only.
	credentialUpsert
)

func clientCredentialMessage(what string) string {
	return "No autorizado: " + what + " requiere una sesión de administrador del panel o el token CLI local; una API key de cliente no alcanza."
}

// guardCredentialWrite wraps a credential-storing handler with the client API
// key policy described on auth.ClientCredentialImportSetting.
func guardCredentialWrite(repo *db.Repo, kind credentialWriteKind, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo == nil || !auth.ClientKeyOnly(r, repo) {
			next(w, r)
			return
		}
		switch kind {
		case credentialAppendOnly:
			if enabled, set := auth.ClientCredentialImport(repo); enabled || !set {
				next(w, r.WithContext(auth.WithClientKeyWrite(r.Context())))
				return
			}
			handlerutil.WriteJSON(w, http.StatusForbidden, map[string]any{
				"error": clientCredentialMessage("importar credenciales de proveedor") +
					" Un administrador desactivó la importación con API key (" + auth.ClientCredentialImportSetting + "=false).",
			})
		default:
			handlerutil.WriteJSON(w, http.StatusForbidden, map[string]any{
				"error": clientCredentialMessage("guardar credenciales por este flujo (puede sobrescribir una cuenta existente)"),
			})
		}
	}
}

// adminOnly rejects requests authenticated only by a client API key. It guards
// operations that change how the router works even without touching a
// credential: deleting connections, the rotation order, provider nodes.
func adminOnly(repo *db.Repo, what string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if repo != nil && auth.ClientKeyOnly(r, repo) {
			handlerutil.WriteJSON(w, http.StatusForbidden, map[string]any{"error": clientCredentialMessage(what)})
			return
		}
		next(w, r)
	}
}
