package log

import (
	"fmt"
	"regexp"
	"strings"
)

// Central redaction safety net: every log line (text, JSON and the dashboard
// console buffer) passes through it, so a call site that forgets
// MaskSecret still does not leak a credential. Inspired by the log redaction
// of 9Router Pro (MIT, © decolua and contributors); reimplemented.

// secretKeys are kv names whose value is always masked.
var secretKeys = map[string]bool{
	"authorization": true, "proxy-authorization": true,
	"apikey": true, "api_key": true, "api-key": true, "x-api-key": true, "x-goog-api-key": true,
	"token": true, "access_token": true, "accesstoken": true, "refresh_token": true, "refreshtoken": true, "id_token": true,
	"password": true, "passwd": true, "secret": true, "client_secret": true, "clientsecret": true,
	"cookie": true, "set-cookie": true, "passphrase": true,
}

var (
	bearerRe = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	// Provider key shapes: OpenAI/Anthropic/Groq/xAI style "sk-…", "gsk_…",
	// "xai-…", Google "AIza…", GitHub tokens.
	keyRe = regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{16,}|gsk_[A-Za-z0-9]{16,}|xai-[A-Za-z0-9]{16,}|AIza[0-9A-Za-z_-]{30,}|gh[pousr]_[A-Za-z0-9]{20,})`)
)

// RedactString masks bearer credentials and provider key shapes in s.
func RedactString(s string) string {
	if s == "" {
		return s
	}
	if strings.Contains(strings.ToLower(s), "bearer ") || strings.Contains(strings.ToLower(s), "basic ") {
		s = bearerRe.ReplaceAllString(s, "$1 ***")
	}
	return keyRe.ReplaceAllStringFunc(s, MaskSecret)
}

// redactValue returns the value to log for kv name key.
func redactValue(key string, v any) any {
	if v == nil {
		return v
	}
	if secretKeys[strings.ToLower(key)] {
		s := fmt.Sprint(v)
		if s == "" {
			return s
		}
		return MaskSecret(s)
	}
	switch x := v.(type) {
	case string:
		return RedactString(x)
	case error:
		s := x.Error()
		if r := RedactString(s); r != s {
			return r
		}
		return v
	case fmt.Stringer:
		s := x.String()
		if r := RedactString(s); r != s {
			return r
		}
		return v
	}
	return v
}
