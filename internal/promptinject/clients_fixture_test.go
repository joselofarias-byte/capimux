package promptinject

import (
	"strings"
	"testing"
)

// Fixtures with the request shapes OpenCode (AI SDK, openai-compatible) and
// OpenClaw (openai-completions / anthropic-messages / openai-responses) send.
// They are SIMULATED: no real OpenCode/OpenClaw process runs here. They pin
// that injection keeps every field those clients rely on.
var clientFixtures = []struct {
	name  string
	proto Protocol
	body  string
	keep  []string
}{
	{"opencode-chat-stream", ProtocolOpenAIChat,
		`{"model":"equipo","stream":true,"stream_options":{"include_usage":true},"max_tokens":32000,"temperature":0,"tool_choice":"auto","tools":[{"type":"function","function":{"name":"bash","description":"Ejecuta un comando","parameters":{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}}}],"messages":[{"role":"system","content":"Sos OpenCode."},{"role":"system","content":"Contexto del proyecto."},{"role":"user","content":[{"type":"text","text":"listá archivos"}]},{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]},{"role":"tool","tool_call_id":"call_1","content":"go.mod"}]}`,
		[]string{`"stream_options":{"include_usage":true}`, `"tool_call_id":"call_1"`, `"arguments":"{\"command\":\"ls\"}"`, `"tool_choice":"auto"`, `Contexto del proyecto.`}},
	{"openclaw-anthropic", ProtocolAnthropic,
		`{"model":"equipo","max_tokens":4096,"stream":true,"system":[{"type":"text","text":"Sos OpenClaw.","cache_control":{"type":"ephemeral"}}],"tools":[{"name":"read","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"hola"},{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"read","input":{"path":"a"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"ok"}]}]}`,
		[]string{`"cache_control":{"type":"ephemeral"}`, `"tool_use_id":"tu_1"`, `"input_schema"`, `"stream":true`}},
	{"openclaw-responses", ProtocolResponses,
		`{"model":"equipo","stream":true,"instructions":"Sos OpenClaw.","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hola"}]},{"type":"function_call","call_id":"fc_1","name":"read","arguments":"{}"},{"type":"function_call_output","call_id":"fc_1","output":"ok"}],"tools":[{"type":"function","name":"read","parameters":{"type":"object"}}],"store":false}`,
		[]string{`"call_id":"fc_1"`, `"function_call_output"`, `"store":false`, `"stream":true`}},
}

func TestClientFixturesSimulatedKeepClientFields(t *testing.T) {
	for _, f := range clientFixtures {
		for _, plan := range []Plan{{Prefix: "Respondé en español, conciso."}, {Suffix: "Usá viñetas."}, {Replace: true, Base: "Sé breve."}} {
			out, err := Apply(f.proto, []byte(f.body), plan)
			if err != nil {
				t.Fatalf("%s: %v", f.name, err)
			}
			s := string(out)
			for _, k := range f.keep {
				if plan.Replace && (k == "Contexto del proyecto." || strings.Contains(k, "cache_control")) {
					continue // reemplazo controlado: el bloque de sistema inicial se sustituye
				}
				if !strings.Contains(s, k) {
					t.Errorf("%s (%+v): falta %s en %s", f.name, plan, k, s)
				}
			}
			if !strings.Contains(EffectiveSystem(f.proto, out), firstNonEmpty(plan.Prefix, plan.Suffix, plan.Base)) {
				t.Errorf("%s: la instrucción no quedó en el slot de sistema", f.name)
			}
		}
	}
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}
