package chat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	json "encoding/json/v2"

	"9router/proxy/internal/deepseekprofile"
	"9router/proxy/internal/handlerutil"
	"9router/proxy/internal/log"
	"9router/proxy/internal/translator"
	"9router/proxy/internal/usagetracker"
)

// deepseekProfile is the opt-in DeepSeek profile wired into the chat handler.
// A nil value (the default) means the profile is off and the handler behaves
// exactly as before.
type deepseekProfile struct {
	modelID string
	bridge  *deepseekprofile.Bridge
	cfgErr  error // enabled but misconfigured: requests to modelID fail closed
}

// loadDeepSeekProfile reads the profile from env. It returns nil when the
// opt-in flag is off.
func loadDeepSeekProfile(env deepseekprofile.Env) *deepseekProfile {
	if !deepseekprofile.Enabled(env) {
		return nil
	}
	cfg, err := deepseekprofile.Load(env)
	if err != nil {
		log.Error("deepseek-profile", "perfil DeepSeek habilitado pero mal configurado; las solicitudes al perfil fallarán", "error", err.Error())
		return &deepseekProfile{modelID: deepseekprofile.ModelIDFrom(env), cfgErr: err}
	}
	for _, w := range cfg.Warnings {
		log.Warn("deepseek-profile", w)
	}
	names := make([]string, 0, len(cfg.Providers))
	for _, p := range cfg.Providers {
		names = append(names, p.Name)
	}
	log.Info("deepseek-profile", "perfil DeepSeek habilitado", "model", cfg.ModelID, "providers", strings.Join(names, " -> "))
	return &deepseekProfile{
		modelID: cfg.ModelID,
		bridge: deepseekprofile.NewBridge(cfg, func(level, msg string, kv ...any) {
			if level == "warn" {
				log.Warn("deepseek-profile", msg, kv...)
				return
			}
			log.Info("deepseek-profile", msg, kv...)
		}),
	}
}

// SetDeepSeekProfileEnv replaces the profile configuration (tests, embedders).
func (h *ChatHandler) SetDeepSeekProfileEnv(env deepseekprofile.Env) {
	h.deepseek = loadDeepSeekProfile(env)
}

func (p *deepseekProfile) matches(model string) bool {
	return p != nil && strings.EqualFold(strings.TrimSpace(model), p.modelID)
}

// serveDeepSeekProfile handles the request when it targets the profile model.
// It returns false (untouched) for every other model.
func (h *ChatHandler) serveDeepSeekProfile(w http.ResponseWriter, r *http.Request, body []byte, model string) bool {
	p := h.deepseek
	if !p.matches(model) {
		return false
	}
	if p.cfgErr != nil {
		handlerutil.WriteJSONError(w, http.StatusServiceUnavailable, "perfil DeepSeek mal configurado; revisar las variables CAPIMUX_DEEPSEEK_* (ver docs/PERFIL_DEEPSEEK.es.md)")
		return true
	}
	start := time.Now()
	out := p.bridge.Serve(r.Context(), w, body)
	h.recordDeepSeekOutcome(out, body, time.Since(start))
	return true
}

func (h *ChatHandler) recordDeepSeekOutcome(out deepseekprofile.Outcome, body []byte, latency time.Duration) {
	if h.Repo == nil || out.Provider == "" {
		return
	}
	info := &UsageLogInfo{Provider: out.Provider, Model: out.Model, ConnectionID: "deepseek-profile", Endpoint: "/v1/chat/completions"}
	if out.Err != nil {
		if errors.Is(out.Err, context.Canceled) {
			return
		}
		h.LogFailure(info, nil, out.Err, latency.Milliseconds(), body, nil)
		return
	}
	metrics := &streamMetrics{TTFT: out.TTFT.Milliseconds()}
	_, _ = metrics.ResponseBuf.Write([]byte(out.ResponseText))
	if out.UsageKnown() {
		h.LogUsage(info, &translator.OpenAIUsage{PromptTokens: out.Usage.PromptTokens, CompletionTokens: out.Usage.CompletionTokens}, latency.Milliseconds(), body, metrics)
		return
	}
	h.logUnknownUsage(info, out, body, latency)
}

// logUnknownUsage records a success whose provider reported no token counts
// (DSH answers with zeros). Zero is not "free": the row carries estimated
// tokens flagged as such, the cost is marked unknown, and the request is kept
// out of the daily cost/usage totals so it cannot read as 0 consumption.
func (h *ChatHandler) logUnknownUsage(info *UsageLogInfo, out deepseekprofile.Outcome, body []byte, latency time.Duration) {
	estPrompt := CountValueChars(body) / 4
	if estPrompt == 0 && len(body) > 0 {
		estPrompt = 1
	}
	estCompletion := CountValueChars(out.ResponseText) / 4
	if estCompletion == 0 && out.ResponseText != "" {
		estCompletion = 1
	}
	metaJSON := fmt.Sprintf(`{"provider":%q,"model":%q,"connectionId":%q,"usageUnknown":true,"tokensEstimated":true,"costUnknown":true}`, info.Provider, info.Model, info.ConnectionID)
	tokensJSON := fmt.Sprintf(`{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d,"estimated":true,"usage_unknown":true,"cost_unknown":true}`, estPrompt, estCompletion, estPrompt+estCompletion)
	log.Info("usage", "logged (tokens desconocidos, estimados)", "provider", info.Provider, "model", info.Model, "prompt_est", estPrompt, "completion_est", estCompletion, "latency_ms", latency.Milliseconds())
	if err := h.Repo.InsertUsageHistory(info.Provider, info.Model, info.ConnectionID, "", info.Endpoint, estPrompt, estCompletion, 0, "success", estPrompt+estCompletion, metaJSON, tokensJSON); err != nil {
		log.Error("usage", "insert failed", "error", err)
	}

	now := time.Now().UTC()
	reqID := fmt.Sprintf("%d-%s", now.UnixMilli(), info.Model)
	var reqMsgs []map[string]string
	persisted := ""
	if captureRequestDetailContent() {
		reqMsgs = extractRequestMessages(body)
		persisted = out.ResponseText
	}
	reqData, err := json.Marshal(map[string]any{
		"id": reqID, "provider": info.Provider, "model": info.Model,
		"connectionId": info.ConnectionID, "status": "success",
		"timestamp":       now.Format("2006-01-02T15:04:05.000Z"),
		"latency":         map[string]int64{"ttft": out.TTFT.Milliseconds(), "total": latency.Milliseconds()},
		"tokens":          map[string]int{"prompt_tokens": estPrompt, "completion_tokens": estCompletion},
		"tokensEstimated": true,
		"usageUnknown":    true,
		"costUnknown":     true,
		"request":         map[string]any{"messages": reqMsgs},
		"response":        map[string]any{"content": persisted},
	})
	if err == nil {
		if err := h.Repo.InsertRequestDetail(reqID, info.Provider, info.Model, info.ConnectionID, "success", string(reqData)); err != nil {
			log.Error("usage", "insert request detail failed", "error", err)
		}
	}
	usagetracker.GetTracker().PushRecent(usagetracker.RecentRequest{
		Timestamp:        now.Format(time.RFC3339),
		Model:            info.Model,
		Provider:         info.Provider,
		PromptTokens:     estPrompt,
		CompletionTokens: estCompletion,
		Status:           "ok",
	}, h.Repo)
}
