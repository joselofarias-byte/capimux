package auth

import (
	"context"
	"net/http"

	"9router/proxy/internal/db"
)

// ClientCredentialImportSetting is the admin-only setting that decides whether
// a request authenticated only by a client (inference) API key may add new
// provider credentials. Overwriting existing credentials is never allowed for
// such a request, whatever its value.
//
//   - absent or false (default): no credential import or creation with a
//     client key. scripts/workbuddy-termux-bootstrap.sh imports with the local
//     admin (CLI) token instead.
//   - true: new credentials may be added through the append-only imports
//     (POST /api/oauth/{provider}/import, POST /api/oauth/codex/bulk-import)
//     and POST /api/connections, without destination fields, without
//     replacing existing rows and always at the end of the rotation.
const ClientCredentialImportSetting = "allowClientCredentialImport"

// ClientKeyOnly reports whether a dashboard request was let through only by a
// client API key: login is enforced and the caller holds neither a dashboard
// session nor the local CLI token. With requireLogin=false the dashboard is
// open by the owner's explicit choice and this reports false.
func ClientKeyOnly(r *http.Request, repo *db.Repo) bool {
	if SessionValid(r) || ValidCLIToken(r.Header.Get(CLITokenHeader)) {
		return false
	}
	return RequireLogin(repo)
}

// ClientCredentialImport returns the stored value of
// ClientCredentialImportSetting and whether it was set at all. A non-boolean
// value counts as set to false (closed).
func ClientCredentialImport(repo *db.Repo) (enabled bool, set bool) {
	raw, err := repo.GetSettingsRaw()
	if err != nil || raw == nil {
		return false, true // unreadable settings: fail closed
	}
	v, ok := raw[ClientCredentialImportSetting]
	if !ok || v == nil {
		return false, false
	}
	b, _ := v.(bool)
	return b, true
}

type clientKeyWriteKey struct{}

// WithClientKeyWrite marks a credential write that a client API key was
// allowed to make. Handlers that insert connections put such rows at the end
// of the provider's rotation (see db.Repo.LastConnectionPriority).
func WithClientKeyWrite(ctx context.Context) context.Context {
	return context.WithValue(ctx, clientKeyWriteKey{}, true)
}

// IsClientKeyWrite reports whether WithClientKeyWrite marked ctx.
func IsClientKeyWrite(ctx context.Context) bool {
	v, _ := ctx.Value(clientKeyWriteKey{}).(bool)
	return v
}
