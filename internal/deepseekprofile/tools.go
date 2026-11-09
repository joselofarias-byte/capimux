package deepseekprofile

import (
	"encoding/json"
	"strings"
)

// toolPolicy is the set of tool names the client authorised in its request.
// The bridge only checks names; it never runs a tool.
type toolPolicy struct {
	allowed map[string]bool
}

// newToolPolicy derives the authorised names from `tools` (and the legacy
// `functions`), narrowed by `tool_choice`: "none" authorises nothing and a
// named function authorises only that one.
func newToolPolicy(req map[string]json.RawMessage) toolPolicy {
	p := toolPolicy{allowed: map[string]bool{}}
	var tools []struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if raw, ok := req["tools"]; ok {
		_ = json.Unmarshal(raw, &tools)
	}
	for _, t := range tools {
		if t.Function.Name != "" && (t.Type == "" || t.Type == "function") {
			p.allowed[t.Function.Name] = true
		}
	}
	var functions []struct {
		Name string `json:"name"`
	}
	if raw, ok := req["functions"]; ok {
		_ = json.Unmarshal(raw, &functions)
	}
	for _, f := range functions {
		if f.Name != "" {
			p.allowed[f.Name] = true
		}
	}

	if raw, ok := req["tool_choice"]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			if s == "none" {
				p.allowed = map[string]bool{}
			}
		} else {
			var named struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			}
			if json.Unmarshal(raw, &named) == nil && named.Function.Name != "" {
				if p.allowed[named.Function.Name] {
					p.allowed = map[string]bool{named.Function.Name: true}
				} else {
					p.allowed = map[string]bool{}
				}
			}
		}
	}
	return p
}

func (p toolPolicy) allows(name string) bool { return p.allowed[name] }

// allowsPrefix reports whether a streamed, possibly partial, name can still
// become an authorised one.
func (p toolPolicy) allowsPrefix(prefix string) bool {
	for n := range p.allowed {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
}

// completionMessage is the part of a non-streamed completion the bridge
// inspects.
type completionMessage struct {
	Choices []struct {
		Message struct {
			Content   any `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tool_calls"`
			FunctionCall *struct {
				Name string `json:"name"`
			} `json:"function_call"`
		} `json:"message"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
}

// firstUndeclared returns the first tool name in a completion that the
// policy does not authorise, or "" when every call is authorised.
func (p toolPolicy) firstUndeclared(c *completionMessage) (string, bool) {
	for _, ch := range c.Choices {
		for _, tc := range ch.Message.ToolCalls {
			if !p.allows(tc.Function.Name) {
				return tc.Function.Name, true
			}
		}
		if fc := ch.Message.FunctionCall; fc != nil && !p.allows(fc.Name) {
			return fc.Name, true
		}
	}
	return "", false
}

// streamChunk is the part of an SSE chat.completion.chunk the bridge inspects.
type streamChunk struct {
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int `json:"index"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tool_calls"`
			FunctionCall *struct {
				Name string `json:"name"`
			} `json:"function_call"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
}

// streamToolTracker accumulates streamed tool-call names per choice and tool
// index, so a name split across chunks is checked as a whole.
type streamToolTracker struct {
	policy toolPolicy
	names  map[[2]int]string
}

func newStreamToolTracker(p toolPolicy) *streamToolTracker {
	return &streamToolTracker{policy: p, names: map[[2]int]string{}}
}

// observe folds a chunk in. It returns the offending name when the chunk
// carries a tool call that can no longer become authorised, or when a
// finished choice holds a call whose full name is not authorised.
func (t *streamToolTracker) observe(c *streamChunk) (string, bool) {
	for _, ch := range c.Choices {
		for _, tc := range ch.Delta.ToolCalls {
			k := [2]int{ch.Index, tc.Index}
			if tc.Function.Name != "" {
				t.names[k] += tc.Function.Name
			}
			if _, seen := t.names[k]; !seen {
				// A call without a name yet: track it so finish checks it.
				t.names[k] = ""
			}
			if !t.policy.allowsPrefix(t.names[k]) || (t.names[k] == "" && len(t.policy.allowed) == 0) {
				return t.names[k], true
			}
		}
		if fc := ch.Delta.FunctionCall; fc != nil {
			k := [2]int{ch.Index, -1}
			t.names[k] += fc.Name
			if !t.policy.allowsPrefix(t.names[k]) || len(t.policy.allowed) == 0 {
				return t.names[k], true
			}
		}
		if ch.FinishReason != nil && *ch.FinishReason != "" {
			if n, bad := t.finishChoice(ch.Index); bad {
				return n, true
			}
		}
	}
	return "", false
}

func (t *streamToolTracker) finishChoice(idx int) (string, bool) {
	for k, n := range t.names {
		if k[0] == idx && !t.policy.allows(n) {
			return n, true
		}
	}
	return "", false
}

// finishAll checks every tracked call, for streams that end with [DONE]
// without a finish_reason.
func (t *streamToolTracker) finishAll() (string, bool) {
	for _, n := range t.names {
		if !t.policy.allows(n) {
			return n, true
		}
	}
	return "", false
}
