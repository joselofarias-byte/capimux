# CAPIMUX — Estado del trabajo de Grok Bot

Última actualización: 2026-10-29 (hora de Montevideo, UTC-3).
Repositorio: `joselofarias-byte/capimux` (público). Este informe no contiene secretos, tokens ni datos privados; todas las credenciales usadas en pruebas son sintéticas.

## 1. Objetivo del ciclo

Ciclo 1 del prompt maestro: reconocimiento del repositorio real, auditoría orientada a seguridad y fiabilidad, selección de los cinco problemas de mayor impacto y entrega de correcciones verificadas en PR independientes y trazables, sin merges, despliegues ni gastos de API.

## 2. Estado inicial

- `main` en `e3a2de8b` (2026-10-13, «feat(android): Capimux APK autónomo…», #126). Versión `1.9.6` (+ sufijo `-fabric` en tiempo de ejecución).
- Go 1.27, binario único con SPA Svelte embebida (`web/dist` vía `go:embed`), SQLite, router `chi`. Build: `make build` (necesita Bun para la SPA). Tests: `go vet ./...`, `go test ./...`, integración `go test -tags=integration -race ./internal/integration/...`, web `bun test`. CI también compila Termux arm64 (`GOOS=android GOARCH=arm64 CGO_ENABLED=0`).
- Línea base verificada localmente antes de cambiar nada: `go vet ./...` OK; `go test ./...` OK (44 paquetes); integración con `-race` OK.
- El repositorio fue renombrado de `9router-go` a `capimux`; `gh` resuelve por defecto al upstream `luqman-v1/9router-go`, por lo que hay que usar `-R joselofarias-byte/capimux` (o `gh repo set-default`).
- PR abiertos revisados para no duplicar: #144 (telemetría sombra), #143 (estadísticas por solicitud), #142 (free-best failover), #141 (DB-02 cifrado), #140 (OPS-01 updater), #139 (DATA-01 privacidad de request details), #138 (descubrimiento local), #130 (auditoría fase 1, sin CI verificada), #128 (plan Vercel), #127 (selector Android), #121 (claves `sk-infer-` de solo inferencia), #115 (identidad Grok CLI), entre otros borradores. Issues deshabilitados en el fork.
- Deuda documentada previa: `TECHNICAL_DEBT.md` (DB-02, OPS-01, VER-01, DATA-01, OAuth-01, COMP-01, DB-03, BENCH-01, DOC-01, CI-01).

## 3. Hallazgos y prioridades

| # | Severidad | Hallazgo | Impacto | Esfuerzo | Estado |
|---|-----------|----------|---------|----------|--------|
| 1 | Crítica (seguridad) | Una API key de cliente (la que se le da a Chatbox/OpenCode) atraviesa `RequireDashboardAuth` y podía: desactivar `requireLogin`, fijar la contraseña del panel cuando aún no hay hash (se acepta `currentPassword` vacío), cambiar `authMode`/OIDC/SAML, habilitar `tunnelDashboardAccess`, reescribir `tunnelUrl`/`tailscaleUrl` y ejecutar `POST /api/tunnel/tailscale-enable` (corre `tailscale up --reset` y `tailscale funnel --bg`). Escalada de clave de inferencia a administrador completo y exposición pública del host. | Toma total del panel (export de base con todas las credenciales tras fijar contraseña), bloqueo del dueño, publicación por Funnel. | Bajo | **Corregido en PR #145** |
| 2 | Alta (seguridad) | La misma API key puede cambiar el destino de credenciales existentes: `PUT /api/connections/{id}` con `providerSpecificData.baseUrl` (reproducido con binario real: el `baseUrl` de una conexión del dueño quedó apuntando a un host arbitrario), vincular proxy pools, editar `baseUrl` de nodos y URL de proxy pools. | La siguiente petición por esa conexión enviaría la clave del dueño al host elegido. | Bajo-medio | **Corregido en PR #146 (apilado)** |
| 3 | Media (seguridad) | Resto de escritura administrativa con API key: crear/borrar/alternar otras API keys, importar credenciales (`/api/oauth/codex/bulk-import` no tiene la protección que #119 dio a Grok). Se usa legítimamente (p. ej. `scripts/workbuddy-termux-bootstrap.sh` importa con API key), así que la solución correcta es la de claves con alcance (#121) y no un bloqueo global. | Integridad del pool de cuentas, DoS sobre otras claves. | Medio | Pendiente (coordinar con #121) |
| 4 | Media (fiabilidad) | OAuth-01: `HandleOAuthKiroSocialExchange` usa `http.Post` (cliente por defecto **sin timeout**) y los helpers device-code usan `http.NewRequest` sin contexto: cancelación/apagado no cortan las llamadas salientes. | Goroutines colgadas y peticiones del panel que no terminan. | Bajo-medio | **Kiro social corregido en PR #147**; resto de `device.go` pendiente |
| 5 | Media (verificación) | VER-01/CI-01: la CI no ejecuta `go test -race ./...`; varios borradores de seguridad (#130, #139, #140, #141) siguen sin CI ni validación Termux; el updater sigue *fail-open* sin SHA (OPS-01, cubierto por #140 en borrador) y `fork_defaults.go` apunta al nombre viejo `joselofarias-byte/9router-go` (hoy funciona por la redirección de GitHub). | Regresiones de concurrencia no detectadas; reemplazo del binario sin verificación de integridad. | Bajo-medio | Pendiente (#140 cubre parte) |

## 4. Trabajo implementado

**Hallazgo 1.** Nuevo guardia en `internal/handlers/dashboard/admin_guard.go`:
- `isAdminRequest`: sesión de panel válida o token CLI local. Una API key sola no es administrativa.
- `requiresAdminForPosture`: sólo aplica cuando `requireLogin` está activo (con el panel abierto por decisión del dueño no cambia nada).
- `changedPostureKey`: compara contra el valor guardado, de modo que el formulario de perfil que reenvía `requireLogin` sin cambios sigue funcionando.
- `PUT/PATCH /api/settings` y `/settings`: 403 con mensaje en español para `newPassword` y para cambios en `requireLogin`, `tunnelDashboardAccess`, `tunnelUrl`, `tailscaleUrl`, `authMode`, `ssoType`, `oidc*`, `saml*`.
- `POST /api/tunnel/tailscale-enable`: 403 antes de ejecutar cualquier comando `tailscale`.

**Hallazgo 2 (PR #146).** `internal/handlers/dashboard/destination_guard.go`: sin credencial administrativa y con `requireLogin` activo, 403 si cambia `baseUrl`, `connectionProxy*`, `strictProxy`, `vercelRelayUrl`, `proxyPoolId` o cualquier `*Url`/`*Endpoint` de una conexión (nivel superior o `providerSpecificData`), la URL base de un nodo o `urls`/`proxyUrl`/`type`/`noProxy`/`strictProxy` de un proxy pool (comparando con la fila guardada). Ediciones ordinarias siguen permitidas.

**Hallazgo 4, parcial (PR #147).** `HandleOAuthKiroSocialExchange`: `http.Post` → `http.NewRequestWithContext(r.Context())` + cliente OAuth compartido (timeout 15 s, `FallbackTransport`).

## 5. Archivos modificados

PR del hallazgo 1:
- `internal/handlers/dashboard/admin_guard.go` (nuevo)
- `internal/handlers/dashboard/settings.go`
- `internal/handlers/dashboard/tunnel.go`
- `internal/handlers/dashboard/admin_guard_test.go` (nuevo)
- `internal/handlers/inference_key_auth_posture_test.go` (nuevo, router real)
- `internal/handlers/dashboard/settings_test.go`, `internal/handlers/dashboard/tunnel_test.go` (las pruebas de lógica de contraseña/Tailscale ahora actúan como administrador, que es el contexto real del panel)
- `CHANGELOG.md`, `CAPIMUX-GROK-BOT-ESTADO.md`

PR #146: `internal/handlers/dashboard/destination_guard.go` (+ `_test.go`), `connections.go`, `provider_nodes.go`, `proxypools.go`, `internal/handlers/inference_key_credential_destination_test.go`, `connections_proxypool_test.go` y `provider_nodes_update_test.go` (actúan como administrador), `CHANGELOG.md`.

PR #147: `internal/handlers/oauth/oauth.go`, `internal/handlers/oauth/kiro_social_timeout_test.go`, `CHANGELOG.md`.

## 6. Commits, ramas y PR

| PR | Rama | Base | Contenido |
|----|------|------|-----------|
| [#145](https://github.com/joselofarias-byte/capimux/pull/145) | `fix/inference-key-auth-posture-20261029` | `main` | Hallazgo 1 + este informe |
| [#146](https://github.com/joselofarias-byte/capimux/pull/146) | `fix/inference-key-credential-destination-20261029` | rama de #145 (apilado: reutiliza su guardia) | Hallazgo 2 |
| [#147](https://github.com/joselofarias-byte/capimux/pull/147) | `fix/oauth-kiro-social-timeout-20261029` | `main` (independiente) | Hallazgo 4 (Kiro social) |

Todos en borrador de revisión; ningún merge, push a `main` ni force-push.

## 7. Pruebas ejecutadas y resultados

- Prueba nueva con el router real (`SetupServerRouter`) y SQLite temporal: **falla en `main`** (las 6 variantes devuelven 200 y el ajuste queda escrito) y **pasa con la corrección**.
- Prueba de handler con `tailscale` simulado: un llamador no administrador nunca ejecuta comandos.
- Evidencia con el binario real (no mocks), `DATA_DIR` temporal, API key creada con el token CLI:
  - `main`: `PATCH requireLogin=false` → 200; `PUT newPassword` → 200; después un `GET /api/settings` **anónimo** → 200.
  - Con la corrección: ambos → 403 con mensaje en español; ajuste ordinario (`language`) con la API key → 200; `GET` anónimo → 401; token CLI cambia `tunnelDashboardAccess` → 200.
- `go vet ./...` OK; `go test ./... -count=1` OK (44 paquetes); `go test -race` de `internal/handlers` y `internal/handlers/dashboard` OK; integración `-tags=integration -race` OK; build Linux y Termux arm64 (`GOOS=android GOARCH=arm64 CGO_ENABLED=0`) OK.
- PR #146: prueba con router real y esquema real: 8 casos fallan sin el fix y pasan con él; 5 de compatibilidad pasan siempre. Binario real: en `main` la API key cambió el `baseUrl` guardado a un host arbitrario (200); con el fix 403 y `baseUrl` intacto; renombrar con API key 200; token CLI redirige 200. Suite completa, vet, `-race` de handlers, integración `-race` y Termux arm64 OK.
- PR #147: upstream local colgado + cancelación a 200 ms: con `http.Post` la prueba falla (sigue esperando a los 5 s), con el fix termina en ~0,2 s (502). Suite completa, `-race` de `oauth` y Termux arm64 OK.
- Inestabilidad observada: `TestGateAcquire_SpacesConcurrentCallers` (`internal/fetchgate`) falló una vez por temporización bajo carga (35 ms vs. 40 ms) y pasó 3/3 al repetir, con y sin cambios: flaky preexistente, no relacionado.
- No se ejecutaron pruebas con proveedores reales ni llamadas pagas: el cambio no toca el camino de inferencia.

## 8. Errores encontrados y corregidos

- Escalada de privilegios API key → administrador del panel (hallazgo 1): corregido y verificado.
- Incidente durante la reproducción: al correr la prueba nueva contra `main` sin la corrección, el caso Tailscale llegó al handler real y ejecutó el `tailscale` instalado en la máquina de pruebas (`tailscale funnel --bg reset` y un intento de `funnel --bg <puerto>` que falló porque Funnel no está habilitado en esa tailnet). Estado posterior verificado: `tailscale serve status` → «No serve config», sin procesos `funnel` residuales. Esto en sí demuestra el riesgo real del hallazgo. Para que no se repita, la prueba vía router ya no incluye ese caso y la cobertura de Tailscale se hace con comandos simulados.

## 9. Mejoras de rendimiento medidas

Ninguna en este ciclo (no se reclaman).

## 10. Funciones nuevas

Ninguna funcionalidad nueva de producto; sólo endurecimiento de autorización.

## 11. Decisiones técnicas

- No se bloqueó toda escritura del panel con API key: rompería flujos documentados (`workbuddy-termux-bootstrap.sh`) y la paridad upstream. Se bloquearon sólo las acciones que cambian la postura de autenticación o exposición, que no tienen uso legítimo con una clave de inferencia.
- Con `requireLogin=false` el comportamiento se conserva: el panel ya está abierto por decisión del dueño.
- Se compara contra el valor almacenado para no romper guardados de formulario completos.
- Respuestas 403 con el formato plano `{error}` que la SPA muestra como texto.

## 12. Riesgos y asuntos pendientes

- Hallazgo 3 (claves con alcance, coordinar con #121), resto del 4 (contexto en `device.go`) y 5 (`-race` en CI; puede requerir permiso `workflow` del token).
- #146 está apilado sobre #145: mergear #145 primero y re-apuntar #146 a `main`.
- Con `requireLogin=false` cualquiera que alcance el puerto sigue pudiendo fijar la contraseña; es la configuración elegida por el dueño, pero conviene advertirlo en la UI.
- Si `requireLogin` está activo y nunca se fijó contraseña, el login sigue aceptando `123456`/`INITIAL_PASSWORD` (paridad upstream).
- Falta validación física en Termux/Android del APK con este cambio (el APK usa el token CLI, que sigue autorizado).

## 13. Participación de otros modelos

Ninguna. Todo el análisis, la implementación y la verificación de este ciclo los hizo Grok Bot localmente; no se consultó ni se atribuye trabajo a otros modelos.

## 14. Estado final y siguiente prioridad

Hallazgos 1 y 2 corregidos y verificados (#145, #146); hallazgo 4 corregido para Kiro social (#147). Nada mergeado. Siguiente prioridad: revisar/mergear #145 → #146; luego agregar `go test -race ./...` a la CI (VER-01) y cerrar el hallazgo 3 junto con #121.
