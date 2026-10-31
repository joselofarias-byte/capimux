// Package keyquota enforces per-client-API-key limits: requests per minute,
// tokens per minute, concurrent requests, and token quotas over rolling 24h,
// 7d and 30d windows. It is off by default and admin-configured.
//
// Inspired by the per-key quotas of 9Router Pro (MIT, © decolua and
// contributors); reimplemented in Go, no code copied.
package keyquota

import (
	"errors"
	"fmt"
	"time"

	json "encoding/json/v2"
)

const (
	// ScopeLimits is the kv scope holding one JSON Limits per API key id.
	ScopeLimits = "apiKeyLimits"
	// toggleKey is the kv key (inside ScopeLimits) of the feature switch. It
	// lives in kv rather than in settings so only the admin-only quota API can
	// change it.
	toggleKey = "_enabled"

	maxRPM         = 1_000_000
	maxConcurrency = 10_000
	maxTokens      = int64(1_000_000_000_000)
)

// ErrInvalid wraps every validation error.
var ErrInvalid = errors.New("límite inválido")

// Limits are the limits of one API key. Zero means "no limit".
type Limits struct {
	RPM         int    `json:"rpm,omitempty"`
	TPM         int64  `json:"tpm,omitempty"`
	Concurrency int    `json:"concurrency,omitempty"`
	Tokens24h   int64  `json:"tokens24h,omitempty"`
	Tokens7d    int64  `json:"tokens7d,omitempty"`
	Tokens30d   int64  `json:"tokens30d,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
}

// Zero reports whether no limit is set.
func (l Limits) Zero() bool {
	return l.RPM == 0 && l.TPM == 0 && l.Concurrency == 0 && l.Tokens24h == 0 && l.Tokens7d == 0 && l.Tokens30d == 0
}

// Validate checks bounds. Limits are technical knobs, so the only rules are
// non-negative values below generous caps.
func (l Limits) Validate() error {
	switch {
	case l.RPM < 0 || l.RPM > maxRPM:
		return fmt.Errorf("%w: rpm debe estar entre 0 y %d", ErrInvalid, maxRPM)
	case l.Concurrency < 0 || l.Concurrency > maxConcurrency:
		return fmt.Errorf("%w: concurrencia debe estar entre 0 y %d", ErrInvalid, maxConcurrency)
	}
	for name, v := range map[string]int64{"tpm": l.TPM, "tokens24h": l.Tokens24h, "tokens7d": l.Tokens7d, "tokens30d": l.Tokens30d} {
		if v < 0 || v > maxTokens {
			return fmt.Errorf("%w: %s debe estar entre 0 y %d", ErrInvalid, name, maxTokens)
		}
	}
	return nil
}

func decodeLimits(raw string) (Limits, bool) {
	var l Limits
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		return Limits{}, false
	}
	return l, l.Validate() == nil
}

func encodeLimits(l Limits) (string, error) {
	l.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	b, err := json.Marshal(l)
	return string(b), err
}
