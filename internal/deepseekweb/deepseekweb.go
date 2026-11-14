// Package deepseekweb manages DeepSeek Web ("DSH") sessions for the opt-in
// DeepSeek profile from the admin panel.
//
// IMPORTANT (TODO, pending the DSH audit): the real session contract of
// dsh-better-bridge (credential field names, storage, expiry, renewal and
// reload) has NOT been audited yet. This package therefore treats the session
// credential as ONE opaque secret: it never parses it, never assumes cookie or
// token names and never sends it anywhere. The real health check is not
// implemented either: the default Checker answers "unknown" until the field
// schema and the check endpoint are confirmed by the audit. Tests use mocks.
//
// Guarantees implemented here:
//   - the credential is encrypted at rest (AES-256-GCM, AAD bound to the
//     session id) and is never returned, logged or exported;
//   - every session is isolated (own ciphertext, own lock, own status);
//   - edits use optimistic concurrency (rev) and a stale health check never
//     overwrites the status of a rotated credential;
//   - the paid InferX fallback is only allowed when the separate explicit
//     toggle is on AND at least one enabled session is connected and unexpired.
package deepseekweb

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Status of one web session as last observed.
type Status string

const (
	StatusUnknown   Status = "unknown"   // never checked, or the check cannot tell
	StatusConnected Status = "connected" // last check succeeded and not expired
	StatusExpired   Status = "expired"   // past ExpiresAt or reported expired
	StatusError     Status = "error"     // last check failed
)

func (s Status) valid() bool {
	switch s {
	case StatusUnknown, StatusConnected, StatusExpired, StatusError:
		return true
	}
	return false
}

// Limits.
const (
	MaxCredentialBytes = 16 << 10
	MaxLabelRunes      = 80
	MaxSessions        = 32
	maxDetailRunes     = 200
	DefaultCheckBudget = 15 * time.Second
)

// Errors returned by the Manager. Messages are Spanish, operator-facing and
// never contain secrets.
var (
	ErrNotFound         = errors.New("sesión no encontrada")
	ErrConflict         = errors.New("la sesión cambió mientras se editaba; recargar y reintentar")
	ErrCheckInProgress  = errors.New("ya hay una comprobación en curso para esta sesión")
	ErrInvalidLabel     = errors.New("nombre inválido (1 a 80 caracteres)")
	ErrEmptyCredential  = errors.New("la credencial de sesión es obligatoria")
	ErrCredentialTooBig = errors.New("la credencial de sesión es demasiado grande")
	ErrTooManySessions  = errors.New("se alcanzó el máximo de sesiones")
	ErrSessionDisabled  = errors.New("la sesión está desactivada")
)

// SessionInfo is the public, secret-free view of a session.
type SessionInfo struct {
	ID            string     `json:"id"`
	Label         string     `json:"label"`
	Enabled       bool       `json:"enabled"`
	HasCredential bool       `json:"hasCredential"`
	Credential    string     `json:"credential"` // always the fixed mask, never the value
	Status        Status     `json:"status"`
	LastCheck     *time.Time `json:"lastCheck,omitempty"`
	LastError     string     `json:"lastError,omitempty"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	Rev           int64      `json:"rev"`
}

// CredentialMask is what responses show instead of the credential. It is
// fixed-length on purpose: no prefix/suffix of the secret is revealed.
const CredentialMask = "••••••••"

// CheckResult is what a Checker reports.
type CheckResult struct {
	Status    Status
	ExpiresAt *time.Time // optional, when the check learns it
	Detail    string     // short, operator-facing; redacted and capped by the Manager
}

// Checker verifies one session credential. Implementations must not log the
// credential. The default (pendingAuditChecker) never contacts anything.
type Checker interface {
	Check(ctx context.Context, sessionID string, credential []byte) CheckResult
}

type pendingAuditChecker struct{}

// PendingAuditDetail explains why the default check returns unknown.
const PendingAuditDetail = "comprobación real pendiente de la auditoría de DSH (esquema de sesión sin confirmar)"

func (pendingAuditChecker) Check(context.Context, string, []byte) CheckResult {
	return CheckResult{Status: StatusUnknown, Detail: PendingAuditDetail}
}

// PendingAuditChecker returns the default checker: it contacts nothing and
// always reports unknown, so it can never enable the paid fallback.
func PendingAuditChecker() Checker { return pendingAuditChecker{} }

func sanitizeDetail(detail string, secret []byte) string {
	if len(secret) > 0 {
		detail = strings.ReplaceAll(detail, string(secret), "[redactado]")
	}
	detail = strings.Join(strings.Fields(detail), " ")
	if r := []rune(detail); len(r) > maxDetailRunes {
		detail = string(r[:maxDetailRunes]) + "…"
	}
	return detail
}
