package dashboard

import (
	"fmt"
	"net/http"
	"reflect"

	"9router/proxy/internal/auth"
	"9router/proxy/internal/promptinject"
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
	// Opt-in that lets client API keys manage other keys; a key must not be
	// able to grant itself that.
	clientKeyManagementSetting,
	// Opt-in that lets client API keys add provider credentials; a key must
	// not be able to grant itself that.
	auth.ClientCredentialImportSetting,
	// Prompt injection / combo identity / capture switches: sensitive
	// configuration that only an admin turns on or off.
	promptinject.SettingInjectionEnabled,
	promptinject.SettingIdentityEnabled,
	promptinject.SettingCaptureEnabled,
}

// strictAdminSettingKeys are refused to any request carrying a client API key
// without an admin credential, even when the dashboard login is off.
var strictAdminSettingKeys = promptinject.SettingKeys

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

// clientKeyManagementSetting is the explicit opt-in (default off) that lets a
// client API key create, delete or toggle API keys while login is enforced.
// It can only be turned on by an admin (it is an auth-posture key).
const clientKeyManagementSetting = "allowClientKeyManagement"

// denyClientKeyManagement writes 403 and returns true when a caller holding
// only a client API key tries to manage API keys without the opt-in. Without
// this, one leaked key could mint a replacement for itself (surviving its own
// revocation) or delete/disable every other key.
func (h *DashboardHandler) denyClientKeyManagement(w http.ResponseWriter, r *http.Request, what string) bool {
	if !h.requiresAdminForPosture(r) {
		return false
	}
	if raw, err := h.Repo.GetSettingsRaw(); err == nil {
		if allowed, _ := raw[clientKeyManagementSetting].(bool); allowed {
			return false
		}
	}
	writePlainError(w, http.StatusForbidden, adminRequiredMessage(what)+" Para permitirlo de forma explícita, un administrador puede activar el ajuste "+clientKeyManagementSetting+".")
	return true
}
