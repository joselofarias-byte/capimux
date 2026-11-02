// Package deepseekprofile implements the opt-in "DeepSeek" chat profile: a
// virtual model that forwards OpenAI-compatible chat completions to a primary
// provider (DeepSeek Web, "DSH") and, only when the primary fails before the
// response starts, to a secondary provider (InferX).
//
// The profile is disabled unless CAPIMUX_DEEPSEEK_PROFILE is set. When it is
// disabled nothing in the router changes: no model is added, no request is
// intercepted. The bridge never executes tools; it forwards tools/tool_choice
// verbatim and rejects tool calls the request did not declare.
package deepseekprofile

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
)

// Environment variables that configure the profile. Secrets are read only
// from the environment or from a file the operator controls; never from the
// repository.
const (
	EnvEnabled           = "CAPIMUX_DEEPSEEK_PROFILE"
	EnvModelID           = "CAPIMUX_DEEPSEEK_PROFILE_MODEL"
	EnvDSHURL            = "CAPIMUX_DEEPSEEK_DSH_URL"
	EnvDSHModel          = "CAPIMUX_DEEPSEEK_DSH_MODEL"
	EnvDSHAPIKey         = "CAPIMUX_DEEPSEEK_DSH_API_KEY"
	EnvDSHAPIKeyFile     = "CAPIMUX_DEEPSEEK_DSH_API_KEY_FILE"
	EnvDSHTimeout        = "CAPIMUX_DEEPSEEK_DSH_TIMEOUT"
	EnvInferXURL         = "CAPIMUX_DEEPSEEK_INFERX_URL"
	EnvInferXModel       = "CAPIMUX_DEEPSEEK_INFERX_MODEL"
	EnvInferXAPIKey      = "CAPIMUX_DEEPSEEK_INFERX_API_KEY"
	EnvInferXAPIKeyFile  = "CAPIMUX_DEEPSEEK_INFERX_API_KEY_FILE"
	EnvInferXTimeout     = "CAPIMUX_DEEPSEEK_INFERX_TIMEOUT"
	EnvStreamIdleTimeout = "CAPIMUX_DEEPSEEK_STREAM_IDLE_TIMEOUT"
	EnvConnectTimeout    = "CAPIMUX_DEEPSEEK_CONNECT_TIMEOUT"
)

// Defaults. DSH answered in ~3 s on average in the owner's tests and InferX
// in ~14 s, so the time-to-first-byte budgets leave a wide margin over the
// observed means while still failing over in bounded time.
const (
	DefaultModelID           = "deepseek-profile"
	DefaultDSHModel          = "deepseek-chat"
	DefaultInferXURL         = "https://model.inferx.net/endpoints/v1"
	DefaultInferXModel       = "deepseek-v4.1-flash-1017-basic"
	DefaultDSHTimeout        = 45 * time.Second
	DefaultInferXTimeout     = 120 * time.Second
	DefaultStreamIdleTimeout = 90 * time.Second
	DefaultConnectTimeout    = 10 * time.Second
)

// Provider names used in logs and usage records.
const (
	ProviderDSH    = "deepseek-dsh"
	ProviderInferX = "deepseek-inferx"
)

// Provider is one OpenAI-compatible upstream of the profile.
type Provider struct {
	Name    string        // ProviderDSH or ProviderInferX
	BaseURL string        // e.g. http://127.0.0.1:20232/v1 (no trailing slash)
	Model   string        // upstream model id
	Timeout time.Duration // budget until the first response byte
	apiKey  *secretValue  // Bearer secret; a pointer so fmt prints an address, never the value
}

type secretValue struct{ v string }

func newSecret(v string) *secretValue {
	if v == "" {
		return nil
	}
	return &secretValue{v: v}
}

func (s *secretValue) value() string {
	if s == nil {
		return ""
	}
	return s.v
}

// HasAPIKey reports whether a Bearer secret is configured, without exposing it.
func (p Provider) HasAPIKey() bool { return p.apiKey.value() != "" }

// Config is the resolved profile configuration.
type Config struct {
	ModelID           string
	Providers         []Provider // in priority order; only usable providers
	StreamIdleTimeout time.Duration
	ConnectTimeout    time.Duration
	Warnings          []string // operator-facing notes (Spanish), never secrets
}

// ErrDisabled is returned by Load when the profile is not enabled.
var ErrDisabled = errors.New("perfil DeepSeek deshabilitado")

// Env abstracts the process environment and file reads so tests can inject
// values without touching the real environment.
type Env struct {
	Getenv   func(string) string
	ReadFile func(string) ([]byte, error)
}

// OSEnv reads the real process environment.
func OSEnv() Env { return Env{Getenv: os.Getenv, ReadFile: os.ReadFile} }

// Enabled reports whether the opt-in flag is on.
func Enabled(env Env) bool {
	switch strings.ToLower(strings.TrimSpace(env.Getenv(EnvEnabled))) {
	case "1", "true", "yes", "on", "si", "sí":
		return true
	}
	return false
}

// ModelIDFrom returns the virtual model id clients must request.
func ModelIDFrom(env Env) string {
	if v := strings.TrimSpace(env.Getenv(EnvModelID)); v != "" {
		return v
	}
	return DefaultModelID
}

// Load resolves the configuration. It returns ErrDisabled when the flag is
// off. A misconfiguration (missing DSH URL, insecure URL carrying a secret,
// bad duration) is an error: the profile then fails closed instead of
// silently sending traffic somewhere else (for example only to the paid
// secondary).
func Load(env Env) (*Config, error) {
	if !Enabled(env) {
		return nil, ErrDisabled
	}
	cfg := &Config{ModelID: ModelIDFrom(env)}
	var err error
	if cfg.StreamIdleTimeout, err = duration(env, EnvStreamIdleTimeout, DefaultStreamIdleTimeout); err != nil {
		return nil, err
	}
	if cfg.ConnectTimeout, err = duration(env, EnvConnectTimeout, DefaultConnectTimeout); err != nil {
		return nil, err
	}

	// Primary: DSH. There is deliberately no default URL: 127.0.0.1:20232 is
	// only DSH when Capimux runs on the same host as DSH; on any other host
	// the operator must point at a private tunnel.
	dshURL := strings.TrimSpace(env.Getenv(EnvDSHURL))
	if dshURL == "" {
		return nil, fmt.Errorf("perfil DeepSeek: falta %s (URL privada de DeepSeek Web alcanzable desde este proceso)", EnvDSHURL)
	}
	dshKey, err := secret(env, EnvDSHAPIKey, EnvDSHAPIKeyFile)
	if err != nil {
		return nil, err
	}
	dsh := Provider{Name: ProviderDSH, Model: valueOr(env, EnvDSHModel, DefaultDSHModel), apiKey: newSecret(dshKey)}
	if dsh.BaseURL, err = validateURL(EnvDSHURL, dshURL, false); err != nil {
		return nil, err
	}
	if dsh.Timeout, err = duration(env, EnvDSHTimeout, DefaultDSHTimeout); err != nil {
		return nil, err
	}
	cfg.Providers = append(cfg.Providers, dsh)

	// Secondary: InferX. Without a secret it is not used at all (never sent
	// an empty Bearer), and the profile runs on DSH alone.
	infKey, err := secret(env, EnvInferXAPIKey, EnvInferXAPIKeyFile)
	if err != nil {
		return nil, err
	}
	if infKey == "" {
		cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("InferX sin secreto (%s/%s): respaldo deshabilitado, solo DSH", EnvInferXAPIKey, EnvInferXAPIKeyFile))
		return cfg, nil
	}
	inf := Provider{Name: ProviderInferX, Model: valueOr(env, EnvInferXModel, DefaultInferXModel), apiKey: newSecret(infKey)}
	if inf.BaseURL, err = validateURL(EnvInferXURL, valueOr(env, EnvInferXURL, DefaultInferXURL), true); err != nil {
		return nil, err
	}
	if inf.Timeout, err = duration(env, EnvInferXTimeout, DefaultInferXTimeout); err != nil {
		return nil, err
	}
	cfg.Providers = append(cfg.Providers, inf)
	return cfg, nil
}

func valueOr(env Env, key, def string) string {
	if v := strings.TrimSpace(env.Getenv(key)); v != "" {
		return v
	}
	return def
}

func duration(env Env, key string, def time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(env.Getenv(key))
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("perfil DeepSeek: %s inválido (usar p. ej. 45s o 2m)", key)
	}
	return d, nil
}

// secret reads a Bearer value from an env var or a file. Error messages name
// the variable, never the value or the file contents.
func secret(env Env, key, fileKey string) (string, error) {
	if v := strings.TrimSpace(env.Getenv(key)); v != "" {
		return v, nil
	}
	path := strings.TrimSpace(env.Getenv(fileKey))
	if path == "" {
		return "", nil
	}
	if env.ReadFile == nil {
		return "", fmt.Errorf("perfil DeepSeek: no se puede leer %s", fileKey)
	}
	b, err := env.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("perfil DeepSeek: no se pudo leer el archivo indicado en %s", fileKey)
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", fmt.Errorf("perfil DeepSeek: el archivo indicado en %s está vacío", fileKey)
	}
	return v, nil
}

// validateURL accepts http(s) base URLs without credentials, query or
// fragment. requireTLS forbids plain http except to a loopback host, so a
// Bearer secret never travels in clear text over a network.
func validateURL(key, raw string, requireTLS bool) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("perfil DeepSeek: %s debe ser una URL http(s) válida", key)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("perfil DeepSeek: %s no debe incluir credenciales, parámetros ni fragmentos", key)
	}
	if requireTLS && u.Scheme != "https" && !isLoopback(u.Hostname()) {
		return "", fmt.Errorf("perfil DeepSeek: %s debe usar https (el secreto no puede viajar sin cifrar)", key)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
