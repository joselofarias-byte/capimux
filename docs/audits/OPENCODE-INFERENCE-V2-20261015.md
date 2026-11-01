# Capimux — OpenCode inference v2 compatibility audit (2026-10-15)

## Evidence
External reference: https://github.com/WhoisNeon/9router/commit/01c491dce8af58837b9f37d6e78c5c0a3239631a

The external JS adapter reports OpenCode's free endpoint moving from `/zen/v1` to `/inference/v1` on 2026-10-14. It also reports a different client version format, session headers and exact tool definitions. These are claims from an external implementation, **not verified against Capimux's Go adapter**.

## Implementation checklist (do not blindly cherry-pick JS)
- [ ] Locate the Go OpenCode provider executor and all free-tier URL builders.
- [ ] Determine whether Capimux currently calls `/zen/v1`, `/inference/v1` or a different authorized endpoint.
- [ ] Check official provider documentation and supported client behavior before changing headers or tool schemas.
- [ ] Add a versioned endpoint selection strategy; preserve backward compatibility where supported.
- [ ] Classify HTTP 403 separately from 401, 429 and 5xx. Do not interpret every 403 as a quota failure.
- [ ] Respect provider access controls; do not forge client identity or spoof tool fingerprints to bypass restrictions.
- [ ] Ensure a failed provider is isolated and other independently authorized routes can still be selected.
- [ ] Add redacted structured logs with provider, model, endpoint family, status and failure class; never log API keys.
- [ ] Add Go table-driven tests for endpoint routing, error classification, fallback eligibility, retry boundaries and SSE streaming.
- [ ] Run go test ./... and integration checks with authorized credentials only.

## Acceptance criteria
1. Existing OpenCode routes and other providers remain compatible.
2. Unsupported/forbidden upstream access yields a clear actionable error rather than a false success.
3. Fallback only uses independently configured authorized providers.
4. No changes are merged without passing tests and an actual supported endpoint verification.

## Attribution
External research and patch: WhoisNeon/9router commit 01c491d.
Capimux analysis and audit checklist: ChatGPT, 2026-10-15.
Implementation and validation: pending.

## Estado contra `main` (2026-11-01, `main` @ 9aa31343)
| Ítem | Estado en `main` | Acción en #131 |
|---|---|---|
| Ubicar el executor OpenCode y los URL builders | Están en `internal/providers/providers.go` (free: `https://opencode.ai/zen/v1/...`; Go: `.../zen/go/v1/...`) y en `internal/proxy/executor/providers.go` (`ForwardOpencode`, que rutea Responses y Messages) | Resuelto (informativo) |
| ¿`/zen/v1` o `/inference/v1`? | `main` usa **`/zen/v1`**; no hay ninguna referencia a `/inference/v1` en OpenCode | Sin verificar contra el servicio real (prohibido hacer pruebas live). **Pendiente** |
| Documentación oficial antes de cambiar headers o tools | No hecho | **Pendiente** |
| Selección versionada del endpoint con compatibilidad hacia atrás | No existe | **Pendiente:** no se implementa sin evidencia oficial del cambio |
| Clasificar 403 aparte de 401, 429 y 5xx | **Cubierto:** 403 → `ErrAuth` con cooldown largo y sin backoff; 429 → `ErrRateLimit`; 5xx → `ErrTransient`; 402 → `ErrQuota`. Un 403 que dice "quota" en el texto cuenta como cuota a propósito | Test nuevo `TestClassifyError_403IsAuthNotQuotaRateLimitOrTransient` |
| No falsificar la identidad del cliente | **En conflicto con `main`:** `ForwardOpencode` ya envía `User-Agent` `DefaultOpenCodeUA`, `x-opencode-client: desktop` y `x-opencode-session` | **Decisión del dueño** |
| Aislar el proveedor caído y seguir con otras rutas autorizadas | **Cubierto:** el 503 se propaga como `UpstreamError` y el combo cambia de cuenta o proveedor (#112); 401/403/429/5xx bloquean la cuenta | Test nuevo `TestForwardOpencode_PropagatesUpstream403`: un 403 nunca llega al cliente como éxito |
| Logs estructurados redactados (familia de endpoint) | Parcial: hay logs con proveedor, modelo y estado; no hay campo de familia de endpoint | Pendiente (menor) |
| Tests table-driven: endpoint, clasificación, fallback, SSE | Ya existen para 503, ruteo Responses, Messages y Go, y SSE; ahora también para 403 | Parcial |
| `go test ./...` e integración con credenciales autorizadas | Batería local y CI con mocks; **nunca live** | Live: pendiente, a decidir por el dueño |
