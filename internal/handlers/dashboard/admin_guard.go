package dashboard

import (
	"fmt"
	"net/http"
	"reflect"

	"9router/proxy/internal/auth"
)

// authPostureSettingKeys are settings whose change can weaken how the
// dashboard is authenticated or exposed: turning the login off, letting tunnel
// hosts reach the dashboard, switching to SSO-only login, repointing the
// identity provider, or rewriting the tunnel hostnames the page guard compares
// against. A client (inference) API key may still pass RequireDashboardAuth for
// ordinary configuration, but it must never be able to change these.
var authPostureSettingKeys = []string{
	"requireLogin",
	"tunnelDashboardAccess",
	"tunnelUrl",
	"tailscaleUrl",
	"authMode",
	"ssoType",
	"oidcIssuerUrl",
	"oidcClientId",
	"oidcClientSecret",
	"oidcScopes",
	"samlEntryPoint",
	"samlIssuer",
	"samlCert",
}

// isAdminRequest reports whether the caller holds an administrative credential:
// a valid dashboard session cookie or the on-device CLI token. A client API key
// alone is not administrative.
func isAdminRequest(r *http.Request) bool {
	return auth.SessionValid(r) || trustedRequest(r)
}

// requiresAdminForPosture reports whether the request reached the dashboard
// without an administrative credential while login is enforced, i.e. it was let
// through by a client API key. With requireLogin=false the dashboard is open by
// the owner's explicit choice, so behaviour is unchanged there.
func (h *DashboardHandler) requiresAdminForPosture(r *http.Request) bool {
	if isAdminRequest(r) {
		return false
	}
	return auth.RequireLogin(h.Repo)
}

// changedPostureKey returns the first auth-posture key whose requested value
// differs from the stored one. Re-sending an unchanged value (the profile page
// saves every field at once) is not a change.
func changedPostureKey(current, updates map[string]any) (string, bool) {
	for _, key := range authPostureSettingKeys {
		next, ok := updates[key]
		if !ok {
			continue
		}
		prev, had := current[key]
		if key == "requireLogin" && !had {
			// auth.RequireLogin treats a missing key as true.
			prev = true
		}
		if !reflect.DeepEqual(prev, next) {
			return key, true
		}
	}
	return "", false
}

func adminRequiredMessage(what string) string {
	return fmt.Sprintf("No autorizado: %s requiere una sesión de administrador del panel o el token CLI local; una API key de cliente no alcanza.", what)
}
