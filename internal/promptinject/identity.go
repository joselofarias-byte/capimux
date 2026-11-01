package promptinject

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"net/http"
	"strings"
)

// identityBufferMax caps how much of a non-streaming body is buffered for the
// rewrite; anything larger is passed through unchanged.
const identityBufferMax = 8 << 20

// RewriteModelField replaces the "model" the client reads in one JSON value:
// top level (OpenAI chat/completions chunks, Ollama), "message.model"
// (Anthropic message_start / non-stream message) and "response.model"
// (Responses events). Anything that is not a JSON object is returned as is.
func RewriteModelField(raw []byte, public string) []byte {
	trim := bytes.TrimSpace(raw)
	if len(trim) == 0 || trim[0] != '{' || !bytes.Contains(trim, []byte(`"model"`)) {
		return raw
	}
	var obj map[string]jsontext.Value
	if json.Unmarshal(trim, &obj) != nil {
		return raw
	}
	changed := setModel(obj, public)
	for _, nested := range []string{"message", "response"} {
		v, ok := obj[nested]
		if !ok || v.Kind() != '{' {
			continue
		}
		var inner map[string]jsontext.Value
		if json.Unmarshal(v, &inner) != nil {
			continue
		}
		if setModel(inner, public) {
			obj[nested] = mustRaw(inner)
			changed = true
		}
	}
	if !changed {
		return raw
	}
	out, err := json.Marshal(obj, json.Deterministic(true))
	if err != nil {
		return raw
	}
	return out
}

func setModel(obj map[string]jsontext.Value, public string) bool {
	v, ok := obj["model"]
	if !ok || v.Kind() != '"' {
		return false
	}
	obj["model"] = mustRaw(public)
	return true
}

// identityWriter rewrites the model name in what the client reads. It never
// touches request routing, usage rows, logs or statistics: those are written
// by the chat handlers from the real provider/model.
type identityWriter struct {
	http.ResponseWriter
	public  string
	mode    int // 0 undecided, 1 lines (SSE/NDJSON), 2 buffer JSON, 3 passthrough
	pending []byte
	status  int
	wrote   bool
}

func newIdentityWriter(w http.ResponseWriter, public string) *identityWriter {
	return &identityWriter{ResponseWriter: w, public: public}
}

func (w *identityWriter) decide() {
	if w.mode != 0 {
		return
	}
	ct := strings.ToLower(w.Header().Get("Content-Type"))
	switch {
	case strings.Contains(ct, "event-stream") || strings.Contains(ct, "ndjson"):
		w.mode = 1
	case strings.Contains(ct, "json"):
		w.mode = 2
	default:
		w.mode = 3
	}
	if w.mode != 3 {
		w.Header().Del("Content-Length")
	}
}

func (w *identityWriter) WriteHeader(code int) {
	w.decide()
	if w.mode == 2 {
		// Delay the header so a buffered body can be rewritten first.
		w.status = code
		return
	}
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *identityWriter) Write(p []byte) (int, error) {
	w.decide()
	switch w.mode {
	case 1:
		w.pending = append(w.pending, p...)
		if err := w.drainLines(false); err != nil {
			return 0, err
		}
		return len(p), nil
	case 2:
		if len(w.pending)+len(p) > identityBufferMax {
			w.mode = 3
			if err := w.flushRaw(); err != nil {
				return 0, err
			}
			return w.ResponseWriter.Write(p)
		}
		w.pending = append(w.pending, p...)
		return len(p), nil
	default:
		return w.ResponseWriter.Write(p)
	}
}

func (w *identityWriter) flushRaw() error {
	if !w.wrote && w.status != 0 {
		w.wrote = true
		w.ResponseWriter.WriteHeader(w.status)
	}
	if len(w.pending) == 0 {
		return nil
	}
	_, err := w.ResponseWriter.Write(w.pending)
	w.pending = nil
	return err
}

// drainLines writes every complete line (or all of it when final) after
// rewriting "data:" payloads and bare JSON lines.
func (w *identityWriter) drainLines(final bool) error {
	var out bytes.Buffer
	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		out.Write(w.rewriteLine(w.pending[:i]))
		out.WriteByte('\n')
		w.pending = w.pending[i+1:]
	}
	if final && len(w.pending) > 0 {
		out.Write(w.rewriteLine(w.pending))
		w.pending = nil
	}
	if out.Len() == 0 {
		return nil
	}
	w.wrote = true
	_, err := w.ResponseWriter.Write(out.Bytes())
	return err
}

func (w *identityWriter) rewriteLine(line []byte) []byte {
	cr := bytes.HasSuffix(line, []byte("\r"))
	body := bytes.TrimSuffix(line, []byte("\r"))
	var res []byte
	switch {
	case bytes.HasPrefix(body, []byte("data:")):
		payload := bytes.TrimPrefix(body, []byte("data:"))
		lead := []byte("data:")
		if bytes.HasPrefix(payload, []byte(" ")) {
			lead = []byte("data: ")
			payload = payload[1:]
		}
		res = append(append([]byte{}, lead...), RewriteModelField(payload, w.public)...)
	case bytes.HasPrefix(bytes.TrimSpace(body), []byte("{")):
		res = RewriteModelField(body, w.public)
	default:
		res = body
	}
	if cr {
		res = append(res, '\r')
	}
	return res
}

// Flush forwards complete lines and the underlying flush (streaming paths
// assert http.Flusher on the writer they are given).
func (w *identityWriter) Flush() {
	if w.mode == 1 {
		_ = w.drainLines(false)
	}
	if w.mode == 2 {
		return // buffered JSON is written on finish
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the real writer.
func (w *identityWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// finish writes whatever is still buffered.
func (w *identityWriter) finish() {
	switch w.mode {
	case 1:
		_ = w.drainLines(true)
	case 2:
		body := RewriteModelField(w.pending, w.public)
		w.pending = body
		_ = w.flushRaw()
	}
}
