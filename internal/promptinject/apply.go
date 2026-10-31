package promptinject

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
)

// Protocol is the client-facing request format.
type Protocol string

const (
	// ProtocolOpenAIChat covers /v1/chat/completions and Ollama /api/chat:
	// instructions live in leading system/developer messages.
	ProtocolOpenAIChat Protocol = "openai-chat"
	// ProtocolAnthropic covers /v1/messages: instructions live in "system".
	ProtocolAnthropic Protocol = "anthropic"
	// ProtocolResponses covers /v1/responses: instructions live in
	// "instructions". Replace only rewrites that field; system/developer items
	// inside "input" are left untouched (documented limitation).
	ProtocolResponses Protocol = "responses"
)

// Apply rewrites body according to plan. Only the protocol's system slot is
// touched; every other field (messages, roles, tool calls, stream, tools,
// unknown extensions) is carried over as raw JSON, byte for byte.
func Apply(proto Protocol, body []byte, plan Plan) ([]byte, error) {
	if plan.Empty() {
		return body, nil
	}
	var obj map[string]jsontext.Value
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, fmt.Errorf("promptinject: cuerpo JSON inválido: %w", err)
	}
	if obj == nil {
		return nil, fmt.Errorf("promptinject: el cuerpo no es un objeto JSON")
	}
	var err error
	switch proto {
	case ProtocolOpenAIChat:
		err = applyChat(obj, plan)
	case ProtocolAnthropic:
		err = applyStringOrBlocks(obj, "system", plan)
	case ProtocolResponses:
		err = applyString(obj, "instructions", plan)
	default:
		return nil, fmt.Errorf("promptinject: protocolo desconocido %q", proto)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(obj, json.Deterministic(true))
}

func mustRaw(v any) jsontext.Value {
	b, _ := json.Marshal(v)
	return jsontext.Value(b)
}

func isNullOrAbsent(v jsontext.Value, ok bool) bool {
	return !ok || len(v) == 0 || string(v) == "null"
}

// applyString handles a plain string slot (Responses "instructions").
func applyString(obj map[string]jsontext.Value, field string, plan Plan) error {
	raw, ok := obj[field]
	client := ""
	if !isNullOrAbsent(raw, ok) {
		if err := json.Unmarshal(raw, &client); err != nil {
			return fmt.Errorf("promptinject: %q no es texto: %w", field, err)
		}
	}
	obj[field] = mustRaw(plan.wrap(client))
	return nil
}

// applyStringOrBlocks handles Anthropic "system": a string or an array of
// content blocks. Blocks (and their cache_control) are preserved; injected
// text is added as separate text blocks at the start/end.
func applyStringOrBlocks(obj map[string]jsontext.Value, field string, plan Plan) error {
	raw, ok := obj[field]
	if isNullOrAbsent(raw, ok) || raw.Kind() == '"' {
		return applyString(obj, field, plan)
	}
	if raw.Kind() != '[' {
		return fmt.Errorf("promptinject: %q tiene un tipo no soportado", field)
	}
	if plan.Replace {
		obj[field] = mustRaw(plan.wrap(""))
		return nil
	}
	var blocks []jsontext.Value
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return fmt.Errorf("promptinject: %q inválido: %w", field, err)
	}
	out := make([]jsontext.Value, 0, len(blocks)+2)
	if plan.Prefix != "" {
		out = append(out, mustRaw(map[string]string{"type": "text", "text": plan.Prefix}))
	}
	out = append(out, blocks...)
	if plan.Suffix != "" {
		out = append(out, mustRaw(map[string]string{"type": "text", "text": plan.Suffix}))
	}
	obj[field] = mustRaw(out)
	return nil
}

type roleOnly struct {
	Role string `json:"role"`
}

func isSystemRole(r string) bool { return r == "system" || r == "developer" }

// applyChat handles OpenAI-style messages[]. The system slot is the run of
// system/developer messages at the head of the conversation.
func applyChat(obj map[string]jsontext.Value, plan Plan) error {
	raw, ok := obj["messages"]
	if isNullOrAbsent(raw, ok) {
		return fmt.Errorf("promptinject: falta messages")
	}
	var msgs []jsontext.Value
	if err := json.Unmarshal(raw, &msgs); err != nil {
		return fmt.Errorf("promptinject: messages inválido: %w", err)
	}
	head := 0
	for head < len(msgs) {
		var ro roleOnly
		if err := json.Unmarshal(msgs[head], &ro, json.RejectUnknownMembers(false)); err != nil || !isSystemRole(ro.Role) {
			break
		}
		head++
	}
	if plan.Replace {
		sys := mustRaw(map[string]string{"role": "system", "content": plan.wrap("")})
		out := append([]jsontext.Value{sys}, msgs[head:]...)
		obj["messages"] = mustRaw(out)
		return nil
	}
	if head == 0 {
		sys := mustRaw(map[string]string{"role": "system", "content": plan.wrap("")})
		obj["messages"] = mustRaw(append([]jsontext.Value{sys}, msgs...))
		return nil
	}
	// Prefix goes into the first system message, suffix into the last one of
	// the head run, so multi-message system preambles keep their order.
	first, err := wrapMessageContent(msgs[0], plan.Prefix, "")
	if err != nil {
		return err
	}
	msgs[0] = first
	last, err := wrapMessageContent(msgs[head-1], "", plan.Suffix)
	if err != nil {
		return err
	}
	msgs[head-1] = last
	obj["messages"] = mustRaw(msgs)
	return nil
}

// wrapMessageContent adds prefix/suffix to one message's content, keeping
// every other member (name, cache_control, …) intact.
func wrapMessageContent(msg jsontext.Value, prefix, suffix string) (jsontext.Value, error) {
	if prefix == "" && suffix == "" {
		return msg, nil
	}
	var m map[string]jsontext.Value
	if err := json.Unmarshal(msg, &m); err != nil {
		return nil, fmt.Errorf("promptinject: mensaje de sistema inválido: %w", err)
	}
	c, ok := m["content"]
	switch {
	case isNullOrAbsent(c, ok) || c.Kind() == '"':
		var s string
		if !isNullOrAbsent(c, ok) {
			_ = json.Unmarshal(c, &s)
		}
		m["content"] = mustRaw(Plan{Prefix: prefix, Suffix: suffix}.wrap(s))
	case c.Kind() == '[':
		var parts []jsontext.Value
		if err := json.Unmarshal(c, &parts); err != nil {
			return nil, fmt.Errorf("promptinject: content inválido: %w", err)
		}
		out := make([]jsontext.Value, 0, len(parts)+2)
		if prefix != "" {
			out = append(out, mustRaw(map[string]string{"type": "text", "text": prefix}))
		}
		out = append(out, parts...)
		if suffix != "" {
			out = append(out, mustRaw(map[string]string{"type": "text", "text": suffix}))
		}
		m["content"] = mustRaw(out)
	default:
		return nil, fmt.Errorf("promptinject: content de sistema con tipo no soportado")
	}
	return mustRaw(m), nil
}

// ProtocolForPath maps an inference path (after /v1 stripping) to its
// protocol. ok is false for paths that carry no injectable instructions.
func ProtocolForPath(path string) (Protocol, bool) {
	switch path {
	case "/chat/completions", "/api/chat", "/api/v1/chat/completions":
		return ProtocolOpenAIChat, true
	case "/messages", "/api/v1/messages":
		return ProtocolAnthropic, true
	case "/responses", "/api/v1/responses":
		return ProtocolResponses, true
	}
	return "", false
}
