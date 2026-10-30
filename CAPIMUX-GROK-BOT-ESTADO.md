# CAPIMUX — Estado del trabajo de Grok Bot

Última actualización: 2026-10-30, 03:35 (hora de Montevideo, UTC-3).
Repositorio: `joselofarias-byte/capimux` (público). Este informe no contiene secretos, tokens ni datos privados; todas las credenciales usadas en pruebas son sintéticas.

## 1. Objetivo del ciclo

Ciclo 1 del prompt maestro: reconocimiento del repositorio real, auditoría orientada a seguridad y fiabilidad, selección de los cinco problemas de mayor impacto y entrega de correcciones verificadas en PR independientes y trazables, sin merges, despliegues ni gastos de API.

Ciclo 2 (instrucciones del dueño, 2026-10-29 22:46 UTC-3): documentar el incidente Tailscale y aislar las pruebas del binario real; preparar #145 y re-apuntar #146 a `main`; PR separados para la gestión de API keys por claves secundarias (#121), `go test -race` en CI y SHA-256 obligatorio en el updater (sobre #140).

Ciclo 3 (instrucciones del dueño, 2026-10-29 23:05 UTC-3), con desarrollo continuo y un PR por ítem:
- cancelación y timeouts acotados en los flujos device-code restantes;
- `fork_defaults.go` y otras referencias al repositorio viejo;
- publicar `sha256s` por plataforma en `release.yml`/`version.json`;
- seguir auditando fallback, reintentos y rate limits, y catálogo/recomendador.

Ciclo 4, de consolidación (instrucciones del dueño, 2026-10-30 03:04 UTC-3). Se pidió:
- verificar qué se mergeó;
- revisar #155;
- actualizar contra `main` las ramas abiertas propias, solo con merge (sin rebase ni force-push);
- correr build, vet, tests, `-race`, integración y Termux;
- analizar los solapamientos del updater.

Ver «Consolidación (ciclo 4)» en la sección 6.

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
| 3 | Media (seguridad) | Resto de escritura administrativa con API key: crear/borrar/alternar otras API keys, importar credenciales (`/api/oauth/codex/bulk-import` no tiene la protección que #119 dio a Grok). La importación se usa legítimamente (`scripts/workbuddy-termux-bootstrap.sh` importa con API key), así que no se bloquea globalmente. | Integridad del pool de cuentas, DoS sobre otras claves. | Medio | **Gestión de API keys corregida en PR #148**; importación pendiente |
| 4 | Media (fiabilidad) | OAuth-01: `HandleOAuthKiroSocialExchange` usa `http.Post` (cliente por defecto **sin timeout**) y los helpers device-code usan `http.NewRequest` sin contexto: cancelación/apagado no cortan las llamadas salientes. | Goroutines colgadas y peticiones del panel que no terminan. | Bajo-medio | **Kiro social: PR #147; device-code: PR #151** |
| 5 | Media (verificación) | VER-01/CI-01: la CI no ejecuta `go test -race ./...`; varios borradores de seguridad (#130, #139, #140, #141) siguen sin CI ni validación Termux; el updater sigue *fail-open* sin SHA (OPS-01, cubierto por #140 en borrador) y `fork_defaults.go` apunta al nombre viejo `joselofarias-byte/9router-go` (hoy funciona por la redirección de GitHub). | Regresiones de concurrencia no detectadas; reemplazo del binario sin verificación de integridad. | Bajo-medio | **`-race` en CI: PR #149**; **SHA-256 obligatorio: #140 + PR #150**; **repo renombrado: PR #152**; **release con `sha256s`: PR #153** |
| 6 | Media (fiabilidad) | Rate limits: un `retry_after` numérico en el cuerpo (incluido el par `error.retry_after`/`reset_at` que el propio router envía) hacía fallar el decode y la espera se perdía. Un `Retry-After` enorme desbordaba a «sin espera». | La cuenta agotada vuelve a los 2 s y absorbe más requests fallidas. | Bajo | **Corregido en PR #154** |

## 4. Trabajo implementado

**Hallazgo 1.** Nuevo guardia en `internal/handlers/dashboard/admin_guard.go`:
- `isAdminRequest`: sesión de panel válida o token CLI local. Una API key sola no es administrativa.
- `requiresAdminForPosture`: sólo aplica cuando `requireLogin` está activo (con el panel abierto por decisión del dueño no cambia nada).
- `changedPostureKey`: compara contra el valor guardado, de modo que el formulario de perfil que reenvía `requireLogin` sin cambios sigue funcionando.
- `PUT/PATCH /api/settings` y `/settings`: 403 con mensaje en español para `newPassword` y para cambios en `requireLogin`, `tunnelDashboardAccess`, `tunnelUrl`, `tailscaleUrl`, `authMode`, `ssoType`, `oidc*`, `saml*`.
- `POST /api/tunnel/tailscale-enable`: 403 antes de ejecutar cualquier comando `tailscale`.

**Hallazgo 2 (PR #146).** `internal/handlers/dashboard/destination_guard.go`: sin credencial administrativa y con `requireLogin` activo, 403 si cambia `baseUrl`, `connectionProxy*`, `strictProxy`, `vercelRelayUrl`, `proxyPoolId` o cualquier `*Url`/`*Endpoint` de una conexión (nivel superior o `providerSpecificData`), la URL base de un nodo o `urls`/`proxyUrl`/`type`/`noProxy`/`strictProxy` de un proxy pool (comparando con la fila guardada). Ediciones ordinarias siguen permitidas.

**Hallazgo 3 (PR #148, pedido #121).** `admin_guard.go` agrega `denyClientKeyManagement`: `POST /api/keys`, `DELETE /api/keys/{id}` y `PUT /api/keys/{id}/toggle` devuelven 403 con mensaje en español si quien llama sólo tiene una API key de cliente. Autorización explícita: sesión de administrador, token CLI local o el ajuste `allowClientKeyManagement` (que sólo un administrador puede activar, porque se agregó a las claves de postura). `GET /api/keys` sigue funcionando y enmascarado. `workbuddy-termux-bootstrap.sh` no usa `/api/keys` (usa `/api/oauth/codebuddy-intl/import` y `/chat/completions`), así que sigue funcionando.

**`go test -race` en CI (PR #149).** Nuevo job `race` («Go race detector») en `.github/workflows/ci.yml`: build de la SPA con Bun, Go 1.27, `go test -race -count=1 -timeout 20m ./...`. El push con el cambio de workflow fue **aceptado** (el token tiene scope `workflow`): no hubo bloqueo. Se estabilizó `TestGateAcquire_SpacesConcurrentCallers` (flaky por temporización: 35,49 ms contra 36 ms) con una tolerancia por intervalo y otra para el total.

**Updater con SHA-256 obligatorio (PR #150, sobre el draft #140).** #140 ya rechaza hash ausente o malformado antes de descargar y compara en tiempo constante; no se duplicó. #150 sale de la cabeza de #140 y cierra los huecos que harían fallar actualizaciones legítimas o elegirían el hash equivocado:
- `sha256s` por plataforma en el manifest (y `default`); el `sha256` único sigue funcionando.
- El fallback de GitHub Releases usa el `digest: "sha256:…"` del asset elegido; antes nunca tenía hash.
- `verifyDownloadedUpdate` acepta el digest del archivo descargado o el del ejecutable extraído.

**Hallazgo 4, parcial (PR #147).** `HandleOAuthKiroSocialExchange`: `http.Post` → `http.NewRequestWithContext(r.Context())` + cliente OAuth compartido (timeout 15 s, `FallbackTransport`).

**Ciclo 3**

- **Device-code OAuth (PR #151).** Todos los `*Start`/`*Poll` y helpers de `device.go` y `muse_device.go` reciben `ctx` y usan `http.NewRequestWithContext`. `HandleDeviceStart`/`HandleDevicePoll` acotan cada request a `deviceFlowTimeout` (60 s) sobre `r.Context()`. El backoff de Muse respeta `ctx.Done()`.
- **Repositorio renombrado (PR #152).**
  - `forkUpdateGitHubRepo` pasa a `joselofarias-byte/capimux` y el manifest a `capimux`.
  - `canonicalUpdateRepo`/`canonicalUpdateURL` mapean overrides con el nombre viejo, sin depender de la redirección de GitHub.
  - También se actualizaron `scripts/bootstrap-codex-audit.sh` y `docs/SELF_UPDATE_POLICY.md`.
  - Se dejaron a propósito: el nombre del binario `9router-go`, los enlaces al upstream `luqman-v1` y los briefs históricos.
- **Release con hashes (PR #153, sobre #150 + #152).**
  - Nuevo `cmd/release-manifest` (con `internal/updater/release_manifest.go`). Genera `version.json` con `downloadUrls`/`sha256s` por `GOOS_GOARCH`, contrastado con `SHA256SUMS.txt`. Falla ante un tag inválido, una URL que no es https, un archivo desconocido, una plataforma duplicada o un hash discordante.
  - `release.yml` corre `sha256sum -c`, genera el manifest y sube `version.json` y `9router-go-android-arm64`.
  - `make cross` incluye `android-arm64`.
  - El manifest por defecto es `releases/latest/download/version.json`.
  - Un 404 de releases/latest significa «al día».
  - No se tocó el job Docker.
- **Rate limits (PR #154).** `extractRetryAfter` lee los campos de espera como string o número, incluidos `error.retry_after`/`reset_at`/`resets_at`. `secondsWait` acota los delta-seconds enormes a `maxResetCooldown`.
- **Flaky de `fetchgate` (PR #149).** `TestGateAcquire_JitterOnlyWidensTheGap` mide desde un origen fijo. Pasa con `-count=20` y con `-race -count=5`, y falla si se muta `gate.go`.

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

#145, ciclo 2: `internal/handlers/dashboard/tunnel.go` (seams protegidos y guardia en `tailscale-disable`), `internal/handlers/dashboard/tailscale_guard_test.go` (nuevo), `internal/handlers/tailscale_router_fake_test.go` (nuevo), `tunnel_test.go`.

PR #148: `admin_guard.go`, `apikeys.go`, `internal/handlers/client_key_management_test.go` (nuevo), `apikeys_reveal_test.go`, `dashboard_test.go`, `internal/integration/keys_test.go` (el ciclo de vida usa sesión de administrador, más un caso negativo con API key), `CHANGELOG.md`.

PR #149: `.github/workflows/ci.yml`, `internal/fetchgate/gate_test.go`, `TECHNICAL_DEBT.md`, `CHANGELOG.md`.

PR #150: `internal/updater/updater.go`, `internal/updater/sha256_sources_test.go` (nuevo), `CHANGELOG.md`, más los archivos de #140.

PR #151: `internal/handlers/oauth/device.go`, `muse_device.go`, `device_cancel_test.go` (nuevo), `CHANGELOG.md`.

PR #152: `internal/updater/fork_defaults.go`, `updater.go`, `fork_repo_rename_test.go` (nuevo), `scripts/bootstrap-codex-audit.sh`, `docs/SELF_UPDATE_POLICY.md`, `CHANGELOG.md`.

PR #153: `.github/workflows/release.yml`, `Makefile`, `cmd/release-manifest/main.go` (nuevo), `internal/updater/release_manifest.go` y `_test.go` (nuevos), `fork_defaults.go`, `updater.go`, `docs/SELF_UPDATE_POLICY.md`, `CHANGELOG.md`, más los de #140, #150 y #152.

PR #154: `internal/handlers/chat/fallback.go`, `retry_after.go`, `numeric_retry_after_test.go` (nuevo), `CHANGELOG.md`.

## 6. Commits, ramas y PR

| PR | Rama | Base | Contenido |
|----|------|------|-----------|
| [#145](https://github.com/joselofarias-byte/capimux/pull/145) | `fix/inference-key-auth-posture-20261029` | `main` | Hallazgo 1 + este informe |
| [#146](https://github.com/joselofarias-byte/capimux/pull/146) | `fix/inference-key-credential-destination-20261029` | `main` (antes apilado sobre #145) | Hallazgo 2 |
| [#147](https://github.com/joselofarias-byte/capimux/pull/147) | `fix/oauth-kiro-social-timeout-20261029` | `main` (independiente) | Hallazgo 4 (Kiro social) |
| [#148](https://github.com/joselofarias-byte/capimux/pull/148) | `fix/client-key-management-guard-20261029` | `main` (incluye commits de #145) | Hallazgo 3: gestión de API keys (#121) |
| [#149](https://github.com/joselofarias-byte/capimux/pull/149) | `ci/go-race-detector-20261029` | `main` (independiente) | `go test -race` en CI |
| [#150](https://github.com/joselofarias-byte/capimux/pull/150) | `fix/updater-sha256-sources-20261029` | `main` (incluye los 10 commits de #140) | Fuentes de SHA-256 del updater |
| [#151](https://github.com/joselofarias-byte/capimux/pull/151) | `fix/oauth-device-context-20261029` | `main` (independiente) | Device-code OAuth con contexto y timeout |
| [#152](https://github.com/joselofarias-byte/capimux/pull/152) | `fix/updater-fork-repo-capimux-20261029` | `main` (independiente) | Referencias al repo renombrado |
| [#153](https://github.com/joselofarias-byte/capimux/pull/153) | `feat/release-manifest-sha256-20261029` | `main` (incluye #140, #150 y #152) | `version.json` con `sha256s` en releases |
| [#154](https://github.com/joselofarias-byte/capimux/pull/154) | `fix/rate-limit-numeric-retry-after-20261029` | `main` (independiente) | `retry_after` numérico y Retry-After enorme |

**Re-apuntado de #146:** mientras su base era la rama de #145, la CI no corría (el workflow sólo dispara para PR contra `main`). Se mergeó la rama de #145 dentro de #146 con un commit de merge (`0199717d`), **sin force-push**, y se cambió la base a `main`. Se verificó que siguen siendo ancestros los commits de #145 (`aacb2e27`, `017108af`, `9276b414`) y el propio de #146 (`17b84843`): no se perdió nada. **Consecuencia:** el diff de #146 contra `main` incluye los cambios de #145. Hay que mergear #145 primero; después #146 muestra sólo lo suyo. Lo mismo pasa con #148 (sale de #145) y con #150 (sale de #140). Commits de #145 hechos después del merge (por ejemplo, este informe) no están en #146 ni en #148; no hace falta, porque son sólo documentación.

Ningún merge, push a `main`, force-push ni borrado de ramas.

### Consolidación (ciclo 4)

**Mergeados en `main`** (según `gh pr list --state all`, horas en UTC-3 del 29/10):

| PR | Hora del merge |
|----|----------------|
| #145 | 23:40 |
| #146 | 23:40 |
| #147 | 23:44 |
| #151 | 23:44 |
| #154 | 23:44 |

`main` = `47d6e0ce`. Este informe ya estaba en `main` vía #145. Esta actualización va en la rama `docs/estado-consolidacion-20261030`.

**Ramas propias actualizadas** con `git merge origin/main`: sin conflictos (CHANGELOG automerge, sin entradas duplicadas), sin rebase ni force-push. Antes del merge estaban 20–23 commits detrás de `main`.

| PR | Nuevo head | Notas |
|----|------------|-------|
| #148 | `10ded974` | |
| #149 | `d46fe0d5` | Incluye un test nuevo (ver más abajo) |
| #150 | `da49618b` | |
| #152 | `c2032c28` | |
| #153 | `13db3bf3` | |

**No se pushearon:**
- **#140** (draft): por la instrucción previa de no tocar su rama. Se probó el merge con `main` en local.
- **#155**: rama `fix/capimux-ghcr-release` del mismo usuario, abierta por otra sesión y no por este ciclo. Solo se analizó y se probó en local.

**Verificación local** (worktree por PR, sobre el merge con `main`, con el guard Tailscale presente en todos y `CAPIMUX_TEST_FAKE_TAILSCALE` sin definir):
- Comandos: `go build ./...`, `go vet ./...`, `go test -count=1 ./...`, `go test -race -count=1 ./...`, `-tags integration ./internal/integration/` y `CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build ./...`.
- Todo OK en #148, #149, #150, #152, #153 y #155.
- **#140**: la primera corrida falló en dos tests de temporización fuera de su diff:
  - `fetchgate` `JitterOnlyWidensTheGap` (24 ms contra 27 ms), ya corregido en #149 y todavía no en `main`;
  - con `-race`, `dashboard` `SpacesAntigravityBurst` (35,9 ms contra 40 ms).
  - Al repetirlas, 2/2 corridas de test y 2/2 de race pasaron.

**Nuevo en #149:** `assertSpaced` (`usage_throttle_test.go`) mide desde la primera lectura y tolera medio piso de ruido de entrega.
- `-race -count=20` OK; dos corridas de `-race ./...` completas OK.
- Con `fetchgate.New(0,0)`, los dos tests de espaciado fallan: siguen detectando ráfagas.

**CI real tras el push** (03:24 UTC-3): #148, #149 (incluido el race), #150, #152 y #153 en verde (test, integración y Docker). #155 no tiene CI porque su base no es `main`; solo corre Vercel.

**Solapamientos del updater:**
- Cadena de ancestros: `#140 ⊂ #150 ⊂ #153`, `#152 ⊂ #153` y `#153 ⊂ #155`. #155 = #153 + 1 commit en `release.yml`.
- Archivos compartidos:
  - `internal/updater/updater.go`: #140, #150, #152 y #153;
  - `checksum_guard*`, `signed_manifest*` y `docs/security/GROK-004-OPS-01.md`: #140, #150 y #153;
  - `fork_defaults.go`, `fork_repo_rename_test.go`, `docs/SELF_UPDATE_POLICY.md` y `scripts/bootstrap-codex-audit.sh`: #152 y #153;
  - `release.yml`: #153 y #155.
- Orden recomendado, siempre con merge commit (no squash, porque rompería la cadena y duplicaría cambios):
  1. #140 (sacarlo de draft);
  2. #150;
  3. #152;
  4. #153;
  5. cambiar la base de #155 a `main`, esperar su CI y mergearlo.
- Alternativa: mergear solo #153 (contiene #140, #150 y #152) y cerrar esas tres como incluidas. #140 aparecería como mergeado automáticamente, porque su head es ancestro.

**#155** (`release.yml`): cambia `REGISTRY_IMAGE` a `ghcr.io/joselofarias-byte/capimux`, usa `GITHUB_TOKEN` y agrega `needs: binaries`. actionlint OK. Observaciones:
- El tag `latest` (`enable={{is_default_branch}}`) no se publica en un push de tag. Es comportamiento previo.
- El primer paquete GHCR queda privado hasta que el dueño lo haga público.
- `README.md:138` sigue diciendo `luqmenul/9router-go:latest`.

**Independientes:** #148 (lo de #145 que incluía ya está en `main`) y #149.

**Candidatos a cerrar:** ninguno está vacío. Solo serían redundantes #140, #150 y #152 si se elige mergear únicamente #153.

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
- PR #148: 4 casos negativos con el router real (API key crea, borra o alterna claves → 403) fallan sin el fix y pasan con él. Casos de regresión: el GET enmascarado, el token CLI, la autorización explícita del administrador y el borrado por el administrador. Binario real: en `main` una API key creó una clave elegida por el atacante (200, 2 claves); con el fix 403 (1 clave). La primera CI falló en `Integration tests`: `TestApiKeyLifecycle` (`-tags integration`, que no había corrido localmente) creaba claves con API key. Se corrigió para que use sesión de administrador y se agregó un subtest negativo. Ese subtest falla con el `apikeys.go` de `main` y pasa con el fix. Ahora la integración pasa localmente en las 6 ramas.
- PR #149: `go test -race ./...` local completo en ~57 s; `fetchgate` con `-race -count=10` OK. En CI, el job nuevo «Go race detector» **pasó**.
- PR #150: 3 tests nuevos (17 subtests), sólo `httptest`, sin reemplazar el binario de test. Revirtiendo el fix fallan 7 subtests. E2E con el binario real (`capimux update` sobre una copia en `/tmp`, manifest y asset en 127.0.0.1): sin sha, sha incorrecto y sha de otra plataforma → rechazado y binario intacto; `sha256s` del ejecutable o del `.tar.gz` → actualiza. El binario de `main` **instala sin verificar** cuando el manifest no trae sha.
- PR #151: 18 casos de cancelación (8 proveedores × start/poll, más el timeout global y el backoff de Muse). Sin el fix, cada uno espera los 15 s del cliente y falla. Con el fix termina en ~2 s. El caso de regresión con un upstream que responde sigue funcionando.
- PR #152: los tests fallan con los valores viejos. Con el binario real, `version` y `update` responden «al día».
- PR #153:
  - Cobertura: 7 tests (nombres de assets, 7 rechazos del generador, contraste con `SHA256SUMS.txt`, ida y vuelta por TLS local con descarga legítima y adulterada, 404/500 de la API de releases, URL por defecto y mapeos, y release.yml ⇄ `CROSS_TARGETS`). Revertir el 404 o quitar `android-arm64` los hace fallar.
  - actionlint: OK.
  - Cross-build local de los 6 targets: `sha256sum -c` OK y manifest con 6 hashes.
  - E2E con el binario real sobre HTTPS local:
    - el binario legítimo actualiza a 99.0.0;
    - el adulterado da `checksum mismatch` y el binario queda intacto;
    - un manifest sin `sha256s` (lo que se publica hoy) se rechaza y el binario queda intacto.
- PR #154: 3 tests (14 formatos, clamp y un test end-to-end por `handleAccountFallback` con HTTP y SQLite reales, donde la cuenta queda en cooldown ~45 s y no 2 s). Los 3 fallan sin el fix.
- Cada rama del ciclo 3 pasó la suite completa, vet, `-race` de los paquetes tocados, integración y el build de Termux arm64.
- CI en GitHub (al cierre): #145–#153 en verde (test, integración y Docker; en #149 también el race). #154 recién abierto, con la CI en curso.

## 8. Errores encontrados y corregidos

- Escalada de privilegios API key → administrador del panel (hallazgo 1): corregido y verificado.
- Test flaky de `fetchgate` por temporización: estabilizado en #149.
- `TestApiKeyLifecycle` (integración) dependía de que una API key pudiera crear claves: actualizado en #148.
- Segundo flaky de `fetchgate` (`JitterOnlyWidensTheGap`, 22 ms vs. 27 ms): corregido en #149.
- Updater: un 404 de GitHub (repo sin releases) se reportaba como fallo del chequeo: corregido en #153.
- Rate limits: `retry_after` numérico ignorado y desborde de Retry-After: corregidos en #154.
- Incidente Tailscale del 2026-10-29: ver el apartado «Incidente Tailscale» al final de esta sección.

### Incidente Tailscale (2026-10-29)

**Máquina:** la máquina de trabajo de Grok Bot (Linux Debian 13 x86_64, kernel 6.12), donde Grok Bot clona y prueba el repositorio. **No** fue el teléfono del dueño ni ningún otro dispositivo suyo; nunca se tocó ni se pidió activar Tailscale en el teléfono.

**Cómo pasó:** para demostrar que la prueba nueva fallaba sin la corrección, se ejecutó `go test` de `internal/handlers` sobre el código de `main`. Uno de los casos llamaba `POST /api/tunnel/tailscale-enable` a través del router real con una API key. Sin el guardia, el handler resolvió el binario real `/usr/local/bin/tailscale` de esa máquina (el seam `tailscaleBinFn`/`tailscaleExec` sólo se puede sustituir desde el paquete `dashboard`, no desde `handlers`).

**Comandos que se ejecutaron, en orden** (reconstruidos del código del handler y confirmados con el CLI falso, que registra exactamente esta secuencia):
1. `tailscale status --json` (sólo lectura). Respondió `BackendState: Running`, así que **no** se ejecutó `tailscale up --reset`.
2. `tailscale funnel --bg reset`: **modificador**. Borra la configuración serve/funnel de ese nodo.
3. `tailscale funnel --bg 20130`: falló con «Funnel is not enabled» (el handler devolvió `funnelNotEnabled`). **No se abrió ningún Funnel** ni se expuso ningún puerto.

Después, para verificar el estado, se ejecutaron sólo lecturas: `tailscale funnel status` y `tailscale serve status`, que respondieron «No serve config».

**Qué pudo cambiar:** sólo la configuración serve/funnel (ServeConfig) del nodo Tailscale de la máquina de Grok Bot. Si antes de ese momento había reglas `tailscale serve`/`funnel` en esa máquina, quedaron borradas. No se sabe si las había. No se tocaron el login, las preferencias de `up`, las ACL, los demás dispositivos de la tailnet ni el teléfono. Siguiendo la instrucción del dueño, **no se intentó restaurar** ninguna configuración desconocida.

**Medidas para que no se repita** (en #145):
- Los seams por defecto (`defaultTailscaleBin` y `defaultTailscaleExec` en `internal/handlers/dashboard/tunnel.go`) usan `testing.Testing()`. Dentro de un binario de test nunca resuelven el `tailscale` del host: sólo el falso indicado en `CAPIMUX_TEST_FAKE_TAILSCALE`. Ejecutar cualquier otro binario provoca un `panic` con el mensaje «tailscale real bloqueado…», así que una regresión hace fallar la prueba en vez de tocar la tailnet. En producción `testing.Testing()` es falso y no cambia nada (+0,9 KB de binario).
- `TestDefaultTailscaleSeams_NeverTouchHostInTests` verifica que en tests no se resuelve el binario del host y que ejecutar `/usr/local/bin/tailscale`, `/usr/bin/tailscale` o `tailscale` provoca el panic del guardia.
- `TestSetupServerRouter_TailscaleEnableDisable_FakeCLIOnly`: cobertura con el router real usando sólo un script falso que registra sus argumentos. Con la API key, enable y disable devuelven 403 y el falso no recibe ninguna llamada. Con sesión de administrador, la secuencia corre contra el falso. Quitando el guardia, la prueba falla mostrando exactamente la secuencia del incidente.
- También `POST /api/tunnel/tailscale-disable` (ejecuta `funnel --bg reset`) pasa a exigir administrador.
- Regla operativa: Grok Bot nunca vuelve a ejecutar `tailscale serve`, `funnel`, `up`, `reset` ni equivalentes reales durante pruebas.

## 9. Mejoras de rendimiento medidas

Ninguna en este ciclo (no se reclaman).

## 10. Funciones nuevas

Funciones nuevas: ajuste opt-in `allowClientKeyManagement` (#148), `sha256s` por plataforma en el manifest del updater y el uso del digest de assets de GitHub (#150). También el generador `cmd/release-manifest` y el binario `android-arm64` en las releases (#153). El resto es endurecimiento.

## 11. Decisiones técnicas

- No se bloqueó toda escritura del panel con API key: rompería flujos documentados (`workbuddy-termux-bootstrap.sh`) y la paridad upstream. Se bloquearon sólo las acciones que cambian la postura de autenticación o exposición, que no tienen uso legítimo con una clave de inferencia.
- Con `requireLogin=false` el comportamiento se conserva: el panel ya está abierto por decisión del dueño.
- Se compara contra el valor almacenado para no romper guardados de formulario completos.
- Respuestas 403 con el formato plano `{error}` que la SPA muestra como texto.
- Updater: se construyó sobre #140 en lugar de duplicarlo. Su rama no se modificó.
- Releases: `version.json` se publica como asset de la release y se genera con Go (sin dependencias nuevas). El manifest raw de `main` queda mapeado para binarios viejos.
- Repo renombrado: los overrides con el nombre viejo se mapean, en lugar de romperlos.
- Guardia Tailscale: el test falla con un panic en vez de saltearse en silencio. Si una regresión intenta tocar el host, se ve.

## 12. Riesgos y asuntos pendientes

- Orden de merge (ciclo 4): ya se mergearon #145, #146, #147, #151 y #154. Faltan, independientes, #148 y #149, y la cadena #140 → #150 → #152 → #153 → #155 (cambiando la base de #155 a `main`), siempre con merge commit.
- Pendiente: importación de credenciales con API key (`/api/oauth/codex/bulk-import`, usada por flujos tipo workbuddy). Requiere claves con alcance como #121.
- Updater:
  - No hay auto-update hasta que el dueño publique el primer tag `v*` con el `release.yml` de #153. Mientras tanto, el updater responde «al día».
  - Un hash no garantiza autenticidad: quien controle el manifest controla el hash. El manifest firmado de #140 es el paso siguiente.
- **Decisión del dueño (infra, no se tocó):** el job Docker de `release.yml` publica en `REGISTRY_IMAGE: luqmenul/9router-go` (registro upstream).
- Incidente Tailscale: el estado serve/funnel previo de la máquina de Grok Bot es desconocido y no se restauró, por indicación del dueño.
- Con `requireLogin=false` cualquiera que alcance el puerto sigue pudiendo fijar la contraseña; es la configuración elegida por el dueño, pero conviene advertirlo en la UI.
- Si `requireLogin` está activo y nunca se fijó contraseña, el login sigue aceptando `123456`/`INITIAL_PASSWORD` (paridad upstream).
- Falta validación física en Termux/Android del APK con este cambio (el APK usa el token CLI, que sigue autorizado).

## 13. Participación de otros modelos

Ninguna. Todo el análisis, la implementación y la verificación de este ciclo los hizo Grok Bot localmente; no se consultó ni se atribuye trabajo a otros modelos.

## 14. Estado final y siguiente prioridad

Corregidos y verificados: hallazgos 1 y 2 (#145, #146), la gestión de API keys del hallazgo 3 (#148), Kiro social (#147), `-race` en CI (#149) y las fuentes de SHA-256 del updater (#150, sobre #140). Pruebas aisladas de Tailscale real (#145). En el ciclo 3, además, se corrigieron y verificaron el device-code OAuth (#151), el repo renombrado (#152), la release con `sha256s` (#153) y los rate limits con `retry_after` numérico (#154). Nada mergeado.

Siguiente prioridad: que el dueño revise y mergee en el orden indicado. Después:
- claves con alcance para la importación (#121);
- firma del manifest;
- seguir auditando el fallback, el catálogo y el recomendador.
