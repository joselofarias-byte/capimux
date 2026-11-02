package deepseekprofile

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Usage mirrors the OpenAI usage object.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Known reports whether the provider reported real token counts. DSH answers
// with zeros: that means "unknown", never "free".
func (u *Usage) Known() bool {
	return u != nil && (u.PromptTokens > 0 || u.CompletionTokens > 0)
}

// FailureKind classifies an attempt that did not start a response.
type FailureKind string

const (
	FailNone        FailureKind = ""
	FailConnection  FailureKind = "connection"   // dial/reset/EOF before the first byte
	FailTimeout     FailureKind = "timeout"      // no first byte within the provider budget
	FailHTTP        FailureKind = "http"         // non-2xx status
	FailClientGone  FailureKind = "client_gone"  // caller cancelled
	FailToolBlocked FailureKind = "tool_blocked" // undeclared tool call (after start)
	FailCut         FailureKind = "cut"          // response cut after it started
	FailInvalid     FailureKind = "invalid"      // unreadable 2xx body (after start)
)

// Attempt records one provider attempt for logs and tests.
type Attempt struct {
	Provider string
	Kind     FailureKind
	Status   int
}

// Outcome describes how a request ended.
type Outcome struct {
	Provider     string // provider that produced the response (or the last one tried)
	Model        string
	Status       int // status sent to the client
	Stream       bool
	Usage        *Usage
	ResponseText string // assistant text, capped, for token estimation only
	TTFT         time.Duration
	Attempts     []Attempt
	Err          error // non-nil when the client did not get a complete success
}

// UsageKnown reports whether the outcome carries real token counts.
func (o Outcome) UsageKnown() bool { return o.Usage.Known() }

const (
	maxErrorBody    = 64 << 10
	maxResponseBody = 32 << 20
	maxCapturedText = 100_000
)

// Bridge forwards chat completions to the profile providers.
type Bridge struct {
	cfg    *Config
	client *http.Client
	logf   func(level, msg string, kv ...any)

	// gate, when set, is consulted before falling back to the paid InferX
	// provider. It is read on every request (panel changes apply live). nil
	// keeps the env-only behaviour (used by tests of the bridge itself).
	gate func() bool
}

// SetFallbackGate installs the InferX fallback gate. Production wiring always
// installs one: InferX is used only if the panel toggle is on and a DeepSeek
// Web session is connected.
func (b *Bridge) SetFallbackGate(g func() bool) { b.gate = g }

// fallbackAllowedTo reports whether the bridge may move on to p.
func (b *Bridge) fallbackAllowedTo(p Provider) bool {
	if p.Name != ProviderInferX || b.gate == nil {
		return true
	}
	return b.gate()
}

// NewBridge builds a bridge with its own HTTP client: no global response
// header timeout (the per-provider first-byte budget applies instead) and no
// redirects, so a Bearer secret is never replayed to another URL.
func NewBridge(cfg *Config, logf func(level, msg string, kv ...any)) *Bridge {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: cfg.ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext
	tr.TLSHandshakeTimeout = cfg.ConnectTimeout
	tr.ResponseHeaderTimeout = 0
	if logf == nil {
		logf = func(string, string, ...any) {}
	}
	return &Bridge{
		cfg: cfg,
		client: &http.Client{
			Transport: tr,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		logf: logf,
	}
}

// ModelID is the virtual model clients request.
func (b *Bridge) ModelID() string { return b.cfg.ModelID }

// Config exposes the resolved configuration (secrets stay unexported).
func (b *Bridge) Config() *Config { return b.cfg }

// Fallback reports whether a failure before the response started may move
// on to the next provider: connection errors, timeouts and HTTP 429, 502,
// 503 or 504. Everything else (400, 401, 403, 404, 422, 500…) is final.
func Fallback(kind FailureKind, status int) bool {
	switch kind {
	case FailConnection, FailTimeout:
		return true
	case FailHTTP:
		switch status {
		case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		}
	}
	return false
}

// redact removes every configured secret from s.
func (b *Bridge) redact(s []byte) []byte {
	for _, p := range b.cfg.Providers {
		if k := p.apiKey.value(); k != "" {
			s = bytes.ReplaceAll(s, []byte(k), []byte("[redactado]"))
		}
	}
	return s
}

func (b *Bridge) redactString(s string) string { return string(b.redact([]byte(s))) }

// errorClass describes a transport error without its URL.
func (b *Bridge) errorClass(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return b.redactString(err.Error())
}

// Serve handles one OpenAI chat completion body for the profile model.
func (b *Bridge) Serve(ctx context.Context, w http.ResponseWriter, body []byte) Outcome {
	var req map[string]json.RawMessage
	if err := json.Unmarshal(body, &req); err != nil || req == nil {
		writeJSONError(w, http.StatusBadRequest, "cuerpo JSON inválido", "invalid_request")
		return Outcome{Status: http.StatusBadRequest, Err: errors.New("cuerpo JSON inválido")}
	}
	stream := false
	if raw, ok := req["stream"]; ok {
		_ = json.Unmarshal(raw, &stream)
	}
	policy := newToolPolicy(req)

	out := Outcome{Stream: stream}
	var last attemptResult
	for i, p := range b.cfg.Providers {
		upstreamBody, err := withModel(req, p.Model)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "no se pudo preparar la solicitud", "internal")
			out.Status, out.Err = http.StatusInternalServerError, err
			return out
		}
		start := time.Now()
		res := b.attempt(ctx, p, upstreamBody, stream)
		out.Provider, out.Model = p.Name, p.Model
		if res.kind == FailNone {
			out.Attempts = append(out.Attempts, Attempt{Provider: p.Name, Status: res.resp.StatusCode})
			out.TTFT = time.Since(start)
			b.deliver(ctx, w, p, res, policy, stream, &out)
			return out
		}
		out.Attempts = append(out.Attempts, Attempt{Provider: p.Name, Kind: res.kind, Status: res.status})
		last = res
		if res.kind == FailClientGone {
			out.Err = context.Canceled
			return out
		}
		hasNext := i+1 < len(b.cfg.Providers)
		if Fallback(res.kind, res.status) && hasNext && !b.fallbackAllowedTo(b.cfg.Providers[i+1]) {
			b.logf("warn", "perfil DeepSeek: respaldo InferX no habilitado (requiere toggle explícito y sesión DeepSeek Web conectada)", "provider", p.Name, "kind", string(res.kind), "status", res.status)
			hasNext = false
		}
		if Fallback(res.kind, res.status) && hasNext {
			b.logf("warn", "perfil DeepSeek: respaldo al siguiente proveedor", "provider", p.Name, "next", b.cfg.Providers[i+1].Name, "kind", string(res.kind), "status", res.status, "detail", res.detail)
			continue
		}
		b.logf("warn", "perfil DeepSeek: proveedor falló sin respaldo", "provider", p.Name, "kind", string(res.kind), "status", res.status, "detail", res.detail)
		break
	}
	b.writeFailure(w, last, &out)
	return out
}

func withModel(req map[string]json.RawMessage, model string) ([]byte, error) {
	m := make(map[string]json.RawMessage, len(req))
	for k, v := range req {
		m[k] = v
	}
	raw, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	m["model"] = raw
	return json.Marshal(m)
}

type attemptResult struct {
	kind   FailureKind
	status int
	body   []byte // error body (non-2xx), redacted
	detail string // sanitized error class for logs
	resp   *http.Response
	reader *bufio.Reader
	cancel context.CancelFunc
	ctx    context.Context
}

// attempt sends the request and waits for the first byte of a 2xx body
// within the provider budget. Nothing is written to the client here, so any
// failure reported is "before the response started".
func (b *Bridge) attempt(parent context.Context, p Provider, body []byte, stream bool) attemptResult {
	ctx, cancel := context.WithCancel(parent)
	var timedOut atomic.Bool
	timer := time.AfterFunc(p.Timeout, func() { timedOut.Store(true); cancel() })
	fail := func(kind FailureKind, status int, errBody []byte, detail string) attemptResult {
		timer.Stop()
		cancel()
		return attemptResult{kind: kind, status: status, body: errBody, detail: detail}
	}
	classify := func(err error) attemptResult {
		switch {
		case timedOut.Load():
			return fail(FailTimeout, 0, nil, "sin primer byte dentro de "+p.Timeout.String())
		case parent.Err() != nil:
			return fail(FailClientGone, 0, nil, "cliente canceló")
		default:
			return fail(FailConnection, 0, nil, b.errorClass(err))
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return fail(FailConnection, 0, nil, "solicitud inválida")
	}
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	if k := p.apiKey.value(); k != "" {
		req.Header.Set("Authorization", "Bearer "+k)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return classify(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		resp.Body.Close()
		if timedOut.Load() {
			return fail(FailTimeout, 0, nil, "sin respuesta completa dentro de "+p.Timeout.String())
		}
		return fail(FailHTTP, resp.StatusCode, b.redact(errBody), fmt.Sprintf("HTTP %d", resp.StatusCode))
	}
	br := bufio.NewReaderSize(resp.Body, 64<<10)
	if _, err := br.Peek(1); err != nil {
		resp.Body.Close()
		if errors.Is(err, io.EOF) && !timedOut.Load() && parent.Err() == nil {
			return fail(FailConnection, 0, nil, "respuesta 2xx vacía")
		}
		return classify(err)
	}
	if !timer.Stop() {
		// The budget expired right as the first byte arrived. Nothing was
		// forwarded yet, so this is still a timeout before the start.
		resp.Body.Close()
		return fail(FailTimeout, 0, nil, "sin primer byte dentro de "+p.Timeout.String())
	}
	return attemptResult{resp: resp, reader: br, cancel: cancel, ctx: ctx}
}

// idleReader cancels the attempt when no byte arrives for idle.
type idleReader struct {
	r      io.Reader
	timer  *time.Timer
	idle   time.Duration
	fired  atomic.Bool
	once   sync.Once
	cancel context.CancelFunc
}

func newIdleReader(r io.Reader, idle time.Duration, cancel context.CancelFunc) *idleReader {
	ir := &idleReader{r: r, idle: idle, cancel: cancel}
	ir.timer = time.AfterFunc(idle, func() { ir.fired.Store(true); cancel() })
	return ir
}

func (ir *idleReader) Read(p []byte) (int, error) {
	n, err := ir.r.Read(p)
	if n > 0 {
		ir.timer.Reset(ir.idle)
	}
	return n, err
}

func (ir *idleReader) stop() { ir.once.Do(func() { ir.timer.Stop() }) }

// deliver forwards a started response. From here on nothing is retried:
// tokens, tool calls or partial output may already have been produced.
func (b *Bridge) deliver(ctx context.Context, w http.ResponseWriter, p Provider, res attemptResult, policy toolPolicy, stream bool, out *Outcome) {
	defer res.cancel()
	defer res.resp.Body.Close()
	ir := newIdleReader(res.reader, b.cfg.StreamIdleTimeout, res.cancel)
	defer ir.stop()

	ct := res.resp.Header.Get("Content-Type")
	if stream && strings.Contains(strings.ToLower(ct), "text/event-stream") {
		b.deliverStream(ctx, w, p, ir, policy, out)
		return
	}
	b.deliverJSON(w, p, ir, policy, out)
}

func (b *Bridge) deliverJSON(w http.ResponseWriter, p Provider, r *idleReader, policy toolPolicy, out *Outcome) {
	data, err := io.ReadAll(io.LimitReader(r, maxResponseBody))
	if err != nil {
		reason := "conexión cortada"
		if r.fired.Load() {
			reason = "sin datos durante " + b.cfg.StreamIdleTimeout.String()
		}
		b.logf("warn", "perfil DeepSeek: respuesta interrumpida, sin reintento", "provider", p.Name, "detail", reason)
		writeJSONError(w, http.StatusBadGateway, "DeepSeek ("+p.Name+"): respuesta interrumpida; no se reintenta para no duplicar resultados", "upstream_cut")
		out.Status, out.Err = http.StatusBadGateway, fmt.Errorf("%s: %w", p.Name, errCut)
		out.Attempts[len(out.Attempts)-1].Kind = FailCut
		return
	}
	var msg completionMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		writeJSONError(w, http.StatusBadGateway, "DeepSeek ("+p.Name+"): respuesta ilegible; no se reintenta", "upstream_invalid")
		out.Status, out.Err = http.StatusBadGateway, fmt.Errorf("%s: respuesta ilegible", p.Name)
		out.Attempts[len(out.Attempts)-1].Kind = FailInvalid
		return
	}
	if name, bad := policy.firstUndeclared(&msg); bad {
		b.logf("warn", "perfil DeepSeek: tool call no declarada rechazada", "provider", p.Name, "tool", safeName(name))
		writeJSONError(w, http.StatusBadGateway, fmt.Sprintf("DeepSeek (%s) pidió la herramienta no declarada o no autorizada %q; rechazada, no se ejecuta ni se reintenta", p.Name, safeName(name)), "tool_not_declared")
		out.Status, out.Err = http.StatusBadGateway, fmt.Errorf("%s: %w", p.Name, errToolBlocked)
		out.Attempts[len(out.Attempts)-1].Kind = FailToolBlocked
		return
	}
	out.Usage = msg.Usage
	var text strings.Builder
	for _, ch := range msg.Choices {
		if s, ok := ch.Message.Content.(string); ok {
			text.WriteString(s)
		}
	}
	out.ResponseText = capText(text.String())
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Capimux-Provider", p.Name)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
	out.Status = http.StatusOK
}

var (
	errCut         = errors.New("respuesta interrumpida tras iniciar")
	errToolBlocked = errors.New("tool call no declarada")
)

// IsToolBlocked reports whether err is an undeclared-tool rejection.
func IsToolBlocked(err error) bool { return errors.Is(err, errToolBlocked) }

// IsCut reports whether err is a response cut after it started.
func IsCut(err error) bool { return errors.Is(err, errCut) }

func (b *Bridge) deliverStream(ctx context.Context, w http.ResponseWriter, p Provider, r *idleReader, policy toolPolicy, out *Outcome) {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("X-Capimux-Provider", p.Name)
	w.WriteHeader(http.StatusOK)
	out.Status = http.StatusOK
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}
	tracker := newStreamToolTracker(policy)
	var text strings.Builder
	br := bufio.NewReaderSize(r, 64<<10)
	var event bytes.Buffer
	done, finished := false, false

	emitError := func(code, msg string) {
		payload, _ := json.Marshal(map[string]any{"error": map[string]any{"message": msg, "type": "capimux_deepseek_profile", "code": code}})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
		flush()
	}

	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := bytes.TrimRight(line, "\r\n")
			if len(trimmed) == 0 {
				// End of an SSE event: inspect it, then forward it whole.
				if event.Len() > 0 {
					if blocked, name := b.inspectEvent(event.Bytes(), tracker, &text, out, &done, &finished); blocked {
						b.logf("warn", "perfil DeepSeek: tool call no declarada rechazada (stream)", "provider", p.Name, "tool", safeName(name))
						emitError("tool_not_declared", fmt.Sprintf("DeepSeek (%s) pidió la herramienta no declarada o no autorizada %q; rechazada, no se ejecuta ni se reintenta", p.Name, safeName(name)))
						out.Err = fmt.Errorf("%s: %w", p.Name, errToolBlocked)
						out.Attempts[len(out.Attempts)-1].Kind = FailToolBlocked
						return
					}
					event.WriteString("\n")
					_, _ = w.Write(event.Bytes())
					flush()
					event.Reset()
					if done {
						out.ResponseText = capText(text.String())
						return
					}
				}
			} else {
				event.Write(trimmed)
				event.WriteString("\n")
			}
		}
		if err != nil {
			// A trailing event without its blank line is still inspected.
			if event.Len() > 0 && errors.Is(err, io.EOF) {
				if blocked, name := b.inspectEvent(event.Bytes(), tracker, &text, out, &done, &finished); blocked {
					emitError("tool_not_declared", fmt.Sprintf("DeepSeek (%s) pidió la herramienta no declarada o no autorizada %q; rechazada, no se ejecuta ni se reintenta", p.Name, safeName(name)))
					out.Err = fmt.Errorf("%s: %w", p.Name, errToolBlocked)
					out.Attempts[len(out.Attempts)-1].Kind = FailToolBlocked
					return
				}
				event.WriteString("\n")
				_, _ = w.Write(event.Bytes())
				flush()
			}
			out.ResponseText = capText(text.String())
			if done || (finished && errors.Is(err, io.EOF)) {
				return
			}
			reason := "conexión cortada"
			if r.fired.Load() {
				reason = "sin datos durante " + b.cfg.StreamIdleTimeout.String()
			} else if ctx.Err() != nil {
				out.Err = context.Canceled
				return
			}
			b.logf("warn", "perfil DeepSeek: stream interrumpido, sin reintento", "provider", p.Name, "detail", reason)
			emitError("upstream_cut", "DeepSeek ("+p.Name+"): respuesta interrumpida ("+reason+"); no se reintenta para no duplicar resultados")
			out.Err = fmt.Errorf("%s: %w", p.Name, errCut)
			out.Attempts[len(out.Attempts)-1].Kind = FailCut
			return
		}
	}
}

// inspectEvent parses the data lines of one SSE event. It reports whether the
// event carries an unauthorised tool call (the event is then not forwarded).
func (b *Bridge) inspectEvent(ev []byte, tracker *streamToolTracker, text *strings.Builder, out *Outcome, done, finished *bool) (bool, string) {
	for _, l := range bytes.Split(ev, []byte("\n")) {
		data, ok := bytes.CutPrefix(l, []byte("data:"))
		if !ok {
			continue
		}
		data = bytes.TrimSpace(data)
		if bytes.Equal(data, []byte("[DONE]")) {
			if name, bad := tracker.finishAll(); bad {
				return true, name
			}
			*done = true
			continue
		}
		var c streamChunk
		if json.Unmarshal(data, &c) != nil {
			continue
		}
		if name, bad := tracker.observe(&c); bad {
			return true, name
		}
		for _, ch := range c.Choices {
			if text.Len() < maxCapturedText {
				text.WriteString(ch.Delta.Content)
			}
			if ch.FinishReason != nil && *ch.FinishReason != "" {
				*finished = true
			}
		}
		if c.Usage != nil {
			out.Usage = c.Usage
		}
	}
	return false, ""
}

func (b *Bridge) writeFailure(w http.ResponseWriter, last attemptResult, out *Outcome) {
	tried := make([]string, 0, len(out.Attempts))
	for _, a := range out.Attempts {
		tried = append(tried, a.Provider)
	}
	who := strings.Join(tried, ", ")
	switch last.kind {
	case FailHTTP:
		out.Status = last.status
		out.Err = fmt.Errorf("DeepSeek (%s): HTTP %d", who, last.status)
		body := last.body
		if len(bytes.TrimSpace(body)) == 0 || !json.Valid(body) {
			writeJSONError(w, last.status, fmt.Sprintf("DeepSeek (%s): el proveedor respondió HTTP %d", who, last.status), "upstream_http")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(last.status)
		_, _ = w.Write(body)
	case FailTimeout:
		out.Status = http.StatusGatewayTimeout
		out.Err = fmt.Errorf("DeepSeek (%s): tiempo de espera agotado antes de la respuesta", who)
		writeJSONError(w, out.Status, out.Err.Error(), "upstream_timeout")
	default:
		out.Status = http.StatusBadGateway
		out.Err = fmt.Errorf("DeepSeek (%s): no se pudo conectar con el proveedor", who)
		writeJSONError(w, out.Status, out.Err.Error(), "upstream_unreachable")
	}
}

func writeJSONError(w http.ResponseWriter, status int, msg, code string) {
	payload, _ := json.Marshal(map[string]any{"error": map[string]any{"message": msg, "type": "capimux_deepseek_profile", "code": code}})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

func capText(s string) string {
	if len(s) > maxCapturedText {
		return s[:maxCapturedText]
	}
	return s
}

// safeName bounds a model-supplied tool name before it reaches a log line or
// an error message.
func safeName(n string) string {
	n = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, n)
	if len(n) > 64 {
		n = n[:64] + "…"
	}
	return n
}
