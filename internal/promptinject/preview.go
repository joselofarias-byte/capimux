package promptinject

import (
	json "encoding/json/v2"
	"fmt"
)

// PreviewResult is what the admin inspector returns. "Sent" is what Capimux
// hands to its router after injection (redacted); it is the only evidence of
// what reached the provider. What a model *says* it followed is a model
// output, not evidence, and the inspector never asks the model about it.
type PreviewResult struct {
	Protocol    Protocol    `json:"protocol"`
	Model       string      `json:"model"`
	IsCombo     bool        `json:"isCombo"`
	APIKeyID    string      `json:"apiKeyId,omitempty"`
	Toggles     Toggles     `json:"toggles"`
	Layers      []LayerInfo `json:"layers"`
	Replace     bool        `json:"replace"`
	Effective   string      `json:"effectiveSystem"`
	Sent        string      `json:"sentBody"`
	PublicModel string      `json:"publicModel,omitempty"`
	WouldApply  bool        `json:"wouldApply"`
	Note        string      `json:"note"`
}

// SampleBody returns a neutral request body for proto.
func SampleBody(proto Protocol, model string) []byte {
	var v any
	switch proto {
	case ProtocolAnthropic:
		v = map[string]any{"model": model, "max_tokens": 256, "messages": []any{map[string]any{"role": "user", "content": "Hola, ¿qué podés hacer?"}}}
	case ProtocolResponses:
		v = map[string]any{"model": model, "input": "Hola, ¿qué podés hacer?"}
	default:
		v = map[string]any{"model": model, "messages": []any{map[string]any{"role": "user", "content": "Hola, ¿qué podés hacer?"}}}
	}
	b, _ := json.Marshal(v)
	return b
}

// EffectiveSystem extracts the system slot text of an already injected body.
func EffectiveSystem(proto Protocol, body []byte) string {
	var obj map[string]any
	if json.Unmarshal(body, &obj) != nil {
		return ""
	}
	text := func(v any) string {
		switch t := v.(type) {
		case string:
			return t
		case []any:
			s := ""
			for _, p := range t {
				if m, ok := p.(map[string]any); ok {
					if tx, ok := m["text"].(string); ok {
						if s != "" {
							s += sep
						}
						s += tx
					}
				}
			}
			return s
		}
		return ""
	}
	switch proto {
	case ProtocolAnthropic:
		return text(obj["system"])
	case ProtocolResponses:
		return text(obj["instructions"])
	default:
		msgs, _ := obj["messages"].([]any)
		s := ""
		for _, m := range msgs {
			mm, _ := m.(map[string]any)
			if r, _ := mm["role"].(string); !isSystemRole(r) {
				break
			}
			if s != "" {
				s += sep
			}
			s += text(mm["content"])
		}
		return s
	}
}

// Preview computes, without sending anything, what the middleware would do
// for one request. It is computed whether or not the toggle is on, and says
// so in WouldApply, so an admin can check a layer before enabling it.
func (s *Store) Preview(t Toggles, proto Protocol, req Request, body []byte) (PreviewResult, error) {
	if len(body) == 0 {
		body = SampleBody(proto, req.Model)
	}
	plan, err := s.Match(req)
	if err != nil {
		return PreviewResult{}, err
	}
	out, err := Apply(proto, body, plan)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("vista previa: %w", err)
	}
	res := PreviewResult{
		Protocol: proto, Model: req.Model, IsCombo: req.IsCombo, APIKeyID: req.APIKeyID,
		Toggles: t, Layers: plan.Layers, Replace: plan.Replace,
		Effective:  Redact(EffectiveSystem(proto, out)),
		Sent:       Redact(string(out)),
		WouldApply: t.Injection && !plan.Empty(),
		Note: "Esto es la solicitud que Capimux arma tras la inyección, antes de traducirla al formato del proveedor. " +
			"Es la evidencia de lo enviado. Lo que el modelo responda sobre haber seguido o no las instrucciones es una salida del modelo, no una verificación.",
	}
	if req.IsCombo {
		if p, ok := s.IdentityFor(req.Model); ok {
			res.PublicModel = p
		}
	}
	if res.Layers == nil {
		res.Layers = []LayerInfo{}
	}
	return res, nil
}
