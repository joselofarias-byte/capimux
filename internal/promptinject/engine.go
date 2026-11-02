// Package promptinject agrega instrucciones de sistema definidas por el
// administrador a las solicitudes de inferencia de Capimux.
//
// Inspirado en el comportamiento observado del motor "Prompt Inject" de
// 9Router Pro (github.com/giienew/9router-pro, licencia MIT, basado en 9Router
// de decolua, MIT). No se copió código: es una reimplementación en Go.
//
// Capas, de menos a más específica: global → combo → modelo → API key.
// Cada capa tiene un modo:
//   - prepend: el texto va antes de las instrucciones del cliente.
//   - append:  el texto va después de las instrucciones del cliente.
//   - replace: el texto sustituye las instrucciones de sistema del cliente y
//     descarta las capas menos específicas; las capas más específicas se
//     siguen sumando. Solo actúa sobre el slot de sistema del protocolo
//     (ver Apply); nunca toca mensajes de usuario, asistente ni tool calls.
//
// El contenido de las capas es configuración sensible: nunca se registra en
// logs (solo alcance, objetivo, modo y cantidad de caracteres).
package promptinject

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Mode is how one layer combines with the client's system instructions.
type Mode string

const (
	ModePrepend Mode = "prepend"
	ModeAppend  Mode = "append"
	ModeReplace Mode = "replace"
)

// Scope is the coordinate a layer is attached to.
type Scope string

const (
	ScopeGlobal Scope = "global"
	ScopeCombo  Scope = "combo"
	ScopeModel  Scope = "model"
	ScopeKey    Scope = "key"
)

// scopeOrder lists the scopes from least to most specific.
var scopeOrder = []Scope{ScopeGlobal, ScopeCombo, ScopeModel, ScopeKey}

// MaxContentBytes bounds a single layer. Technical limit only (no content
// filtering): it keeps a misconfigured layer from ballooning every request.
const MaxContentBytes = 64 * 1024

// MaxTargetLen bounds combo/model/key identifiers.
const MaxTargetLen = 256

// Entry is one stored layer.
type Entry struct {
	Scope     Scope  `json:"scope"`
	Target    string `json:"target,omitempty"`
	Mode      Mode   `json:"mode"`
	Content   string `json:"content"`
	Enabled   bool   `json:"enabled"`
	Name      string `json:"name,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// LayerInfo describes an applied layer without its content (safe to log or
// return to callers that may not see the text).
type LayerInfo struct {
	Scope  Scope  `json:"scope"`
	Target string `json:"target,omitempty"`
	Mode   Mode   `json:"mode"`
	Chars  int    `json:"chars"`
	Name   string `json:"name,omitempty"`
}

// Info returns the content-free description of the entry.
func (e Entry) Info() LayerInfo {
	return LayerInfo{Scope: e.Scope, Target: e.Target, Mode: e.Mode, Chars: utf8.RuneCountInString(e.Content), Name: e.Name}
}

// ValidMode reports whether m is a known mode.
func ValidMode(m Mode) bool {
	return m == ModePrepend || m == ModeAppend || m == ModeReplace
}

// ValidScope reports whether s is a known scope.
func ValidScope(s Scope) bool {
	for _, k := range scopeOrder {
		if k == s {
			return true
		}
	}
	return false
}

// ErrInvalid is wrapped by every validation error.
var ErrInvalid = errors.New("configuración de inyección inválida")

// ValidateContent applies the technical limits (size, UTF-8, no NUL bytes).
// There is deliberately no content filter: the editor is general purpose.
func ValidateContent(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("%w: el contenido está vacío", ErrInvalid)
	}
	if len(s) > MaxContentBytes {
		return fmt.Errorf("%w: el contenido supera %d bytes", ErrInvalid, MaxContentBytes)
	}
	if !utf8.ValidString(s) {
		return fmt.Errorf("%w: el contenido no es UTF-8 válido", ErrInvalid)
	}
	if strings.ContainsRune(s, 0) {
		return fmt.Errorf("%w: el contenido contiene bytes NUL", ErrInvalid)
	}
	return nil
}

// Normalize validates e and fills defaults. Target is required for every
// scope except global, and must be empty for global.
func (e Entry) Normalize() (Entry, error) {
	e.Target = strings.TrimSpace(e.Target)
	e.Name = strings.TrimSpace(e.Name)
	if !ValidScope(e.Scope) {
		return e, fmt.Errorf("%w: alcance desconocido %q", ErrInvalid, e.Scope)
	}
	if e.Scope == ScopeGlobal {
		e.Target = ""
	} else if e.Target == "" || len(e.Target) > MaxTargetLen || !utf8.ValidString(e.Target) {
		return e, fmt.Errorf("%w: objetivo inválido para el alcance %s", ErrInvalid, e.Scope)
	}
	if e.Mode == "" {
		e.Mode = ModePrepend
	}
	if !ValidMode(e.Mode) {
		return e, fmt.Errorf("%w: modo desconocido %q", ErrInvalid, e.Mode)
	}
	if len(e.Name) > 120 {
		e.Name = e.Name[:120]
	}
	if err := ValidateContent(e.Content); err != nil {
		return e, err
	}
	return e, nil
}

// StorageKey is the KV key of the entry inside the injection scope.
func StorageKey(scope Scope, target string) string {
	if scope == ScopeGlobal {
		return string(ScopeGlobal)
	}
	return string(scope) + ":" + target
}

// Request identifies what a request matches.
type Request struct {
	// Model is the model string exactly as the client sent it.
	Model string
	// IsCombo is true when Model names a combo.
	IsCombo bool
	// APIKeyID is the id (never the secret) of the authenticated key.
	APIKeyID string
}

// Resolve picks the enabled layers that match req from entries, ordered least
// to most specific.
func Resolve(entries map[string]Entry, req Request) []Entry {
	var out []Entry
	for _, scope := range scopeOrder {
		var key string
		switch scope {
		case ScopeGlobal:
			key = StorageKey(ScopeGlobal, "")
		case ScopeCombo:
			if !req.IsCombo || req.Model == "" {
				continue
			}
			key = StorageKey(ScopeCombo, req.Model)
		case ScopeModel:
			if req.IsCombo || req.Model == "" {
				continue
			}
			key = StorageKey(ScopeModel, req.Model)
		case ScopeKey:
			if req.APIKeyID == "" {
				continue
			}
			key = StorageKey(ScopeKey, req.APIKeyID)
		}
		if e, ok := entries[key]; ok && e.Enabled && strings.TrimSpace(e.Content) != "" {
			out = append(out, e)
		}
	}
	return out
}

// Plan is the composed result of the layers for one request.
type Plan struct {
	Prefix  string // joined prepend texts
	Suffix  string // joined append texts
	Replace bool   // the client's system slot is discarded
	Base    string // replacement text when Replace
	Layers  []LayerInfo
}

// Empty reports whether the plan changes nothing.
func (p Plan) Empty() bool { return p.Prefix == "" && p.Suffix == "" && !p.Replace }

const sep = "\n\n"

// Compose turns resolved layers (least to most specific) into a plan. The
// most specific replace layer wins the base; anything less specific than it is
// discarded; more specific prepend/append layers still stack around it.
// Prepends are ordered least→most specific, so the most specific instruction
// is closest to the client's text; appends likewise.
func Compose(layers []Entry) Plan {
	start := 0
	for i := len(layers) - 1; i >= 0; i-- {
		if layers[i].Mode == ModeReplace {
			start = i
			break
		}
	}
	var p Plan
	var pre, suf []string
	for _, e := range layers[start:] {
		p.Layers = append(p.Layers, e.Info())
		switch e.Mode {
		case ModeReplace:
			p.Replace = true
			p.Base = e.Content
		case ModeAppend:
			suf = append(suf, e.Content)
		default:
			pre = append(pre, e.Content)
		}
	}
	p.Prefix = strings.Join(pre, sep)
	p.Suffix = strings.Join(suf, sep)
	return p
}

// wrap returns the client text surrounded by the plan.
func (p Plan) wrap(client string) string {
	base := client
	if p.Replace {
		base = p.Base
	}
	parts := make([]string, 0, 3)
	for _, s := range []string{p.Prefix, base, p.Suffix} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, sep)
}

// texts lists the injected strings (for log scrubbing).
func (p Plan) texts() []string {
	var out []string
	for _, s := range []string{p.Prefix, p.Suffix} {
		if s != "" {
			out = append(out, s)
			out = append(out, strings.Split(s, sep)...)
		}
	}
	if p.Replace && p.Base != "" {
		out = append(out, p.Base)
	}
	return out
}
