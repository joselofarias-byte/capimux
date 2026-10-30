# CAPIMUX — Estado del trabajo de Grok Bot

Última actualización: 2026-10-30, 13:40 (hora de Montevideo, UTC-3).
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

Ciclo 5 (instrucciones del dueño, 2026-10-30 06:06 UTC-3): auditar el fallback entre proveedores, el catálogo de modelos/proveedores y el recomendador, en local y desde `origin/main` actualizado (`47d6e0ce`). Ver «Auditoría (ciclo 5)» en la sección 6.

Ciclo 6 (instrucciones del dueño, 2026-10-30 09:07 UTC-3): limitar la importación y sobrescritura de credenciales de proveedores con API key de cliente, en la línea de #148, sin romper Workbuddy. Rama nueva desde `origin/main` (`47d6e0ce`), independiente de la cadena del updater. Ver «Credenciales con API key (ciclo 6)» en la sección 6.

Ciclo 7 (decisión del dueño sobre #158, 2026-10-30 10:01 UTC-3): las API keys de cliente operan con privilegios mínimos y el token administrativo controla credenciales, conexiones, provider nodes y prioridades. Pasos: migrar Workbuddy al token admin, restringir la API key, forzar que sus altas queden últimas en la rotación y activar el default cerrado. Todo dentro de #158 (misma rama, commits nuevos, sin force-push). Ver «Privilegios mínimos de API key (ciclo 7)» en la sección 6.

Ciclo 8 (2026-10-30 12:12 UTC-3): auditoría del recomendador de combos y del descubrimiento/actualización del catálogo free, desde origin/main `47d6e0ce`, sin repetir lo de #157. Dos defectos verificados, cada uno en su PR independiente: #159 y #160. Ver «Auditoría (ciclo 8)» en la sección 6.

Ciclo 9 (decisiones del dueño, 2026-10-30 12:57 UTC-3, aplicadas sin volver a consultar): cinco decisiones sobre lo documentado en el ciclo 8 y sobre #158. Variantes duplicadas del catálogo y respuestas 200 vacías de models.dev (#161), poda de combos generados y orden por versión (#162, sobre #159) y conexiones solo-admin (commit nuevo en #158). Ver «Decisiones del dueño (ciclo 9)» en la sección 6.

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

### Auditoría (ciclo 5)

PR **#157** (`fix/fallback-catalog-audit-20261030`, base `main` `47d6e0ce`, sin mergear). Contiene tres defectos reales. Cada uno tiene un test que falla en `main` y pasa con el arreglo mínimo:

1. **`internal/handlers/chat/fallback.go` (`handleAccountFallback`).**
   - Causa: cada fila se pasaba como pin a `getBestConnection`. Si la fila no era elegible (cooldown, lock de modelo, caché de cuota o asignación estricta), el pin devolvía otra cuenta. El forward usaba las credenciales de esa cuenta con el ID de la fila.
   - Efectos:
     - un éxito borraba el cooldown y el lock de la cuenta agotada, que volvía a usarse en la siguiente request;
     - el uso y el refresh OAuth se atribuían a la fila equivocada;
     - con un 500 o un error de red, la cuenta sana recibía un golpe por cada fila enfriada.
   - Arreglo: saltar la fila si `connObj.ID != c.ID`.
   - Tests: `fallback_cooldown_identity_test.go`.
2. **`internal/handlers/chat/combo.go` (`handleComboFallback` y `handleMessagesComboFallback`).**
   - Causa: un fallo no reintentable (400, 422, 500 o error de red) hacía `continue` sin excluir la cuenta.
   - Efecto: el combo reenviaba a la misma cuenta hasta 10 veces por modelo, y la cuenta sana no se usaba.
   - Arreglo: `triedThisEntry`, la lista de cuentas probadas por entrada y pasada.
   - Tests: `combo_retry_same_account_test.go`. En `main` el test daba 10 golpes.
3. **`internal/providers/registry_models.go` (`ProviderModelKinds`).**
   - Causa: `gemini-2.5-pro`, `gemini-2.5-flash` y `gemini-2.5-flash-lite` figuran como chat y como STT, pero tenían un único kind `stt`.
   - Efecto: quedaban fuera de `/v1/models`, del recomendador de combos por familia y del descubrimiento free.
   - Arreglo: quitar el kind de esos tres ids.
   - Tests: un invariante de catálogo más tests de `isLLMModelEntry` e `isUsableChatModel`, con negativos para TTS, imagen, embedding y deshabilitados.

**Revisado sin hallazgos:**
- `retry_after.go` y la clasificación 4xx/5xx/429:
  - `RetryableStatusCodes` 401/403/429/502/503/504 excluye la cuenta;
  - 500 y los errores de red son transitorios (una cuenta más);
  - el resto de los 4xx corta en el fallback de cuentas.
- `controlplane/routing` (`SelectCandidates`), `controlplane/scoring` y la lógica sticky de `applyComboStrategy`.
- `comboLockRetryable` y el segundo pase acotado del combo:
  - como máximo 2 pasadas, así que no hay bucle infinito;
  - `excludeIDs` se reinicia solo entre pasadas.
- `comboConnection`: no usa la clave por defecto si hay cuentas configuradas no seleccionables.
- Catálogo:
  - no hay ids vacíos;
  - el único duplicado era el de Gemini;
  - las claves de `ProviderModels` que no son proveedores son alias válidos;
  - `ProviderAliases` (de `catalog_sync`) apunta a ids de models.dev, así que es correcto.
- `freecoding discover` muta una URL global, pero solo se usa desde la CLI.

`-race` no detectó carreras en los paquetes tocados.

### Credenciales con API key (ciclo 6)

PR **#158** (`fix/client-credential-import-guard-20261030`, base `main` `47d6e0ce`, commits `2435942e` y `67bfd49e`, sin mergear).

- **Causa.** Con login activo, una API key de cliente pasa `RequireDashboardAuth`, y las rutas de credenciales no distinguían entre esa key y el dueño. Con la key se podía:
  - **sobrescribir** la key del dueño:
    - con `POST /api/connections` + `allowOverwrite`/`overwrite` por nombre;
    - con `PUT /api/connections|providers/{id}` + `data.apiKey` (#146 solo protegía los destinos);
    - por los flujos OAuth que buscan la cuenta y actualizan la fila (kiro ×4, cursor/import, gitlab/pat, iflow/cookie, device/poll, pkce y authcode/exchange, antigravity, cline, trae, windsurf, zed, kimchi, xiaomi-mimo, freebuff/poll, codex cli-login/start y start-proxy);
  - **crear** conexiones nuevas con `baseUrl` propio y prioridad 1, lo que captura el tráfico.
- **Workbuddy.** `scripts/workbuddy-termux-bootstrap.sh` solo usa `POST /api/oauth/codebuddy-intl/import`.
  - Lo hace con `Authorization: Bearer` (`NINEROUTER_API_KEY` o `~/.config/9router-go/admin-token`), que el router trata como API key de cliente.
  - Esa ruta es solo-alta: id nuevo, sin destino, sin overwrite.
- **Diseño.** Ajuste solo-admin `allowClientCredentialImport`, ajuste de postura como #148: la API key no puede cambiarlo.
  - Ausente (default): quedan abiertas solo las altas por `/{provider}/import` y `codex/bulk-import` (compat Workbuddy); `POST /api/connections` → 403.
  - `false`: todo cerrado.
  - `true`: también `POST /api/connections`, pero sin destino.
  - Sobrescribir queda cerrado siempre, sea cual sea el valor.
  - Admin y `requireLogin=false` no cambian.
  - Las rutas OAuth se cortan antes del handler, sin llamadas salientes.
- No depende de #148. El conflicto esperado es trivial: una línea en `authPostureSettingKeys`; hay que conservar las dos.

### Privilegios mínimos de API key (ciclo 7)

Mismo PR **#158**, commits nuevos `78ad5194`, `8270eb76` y `d216bb75`, sin mergear.

- **Migración de Workbuddy (`78ad5194`).**
  - El bootstrap importa con `x-9r-cli-token`. Lo deriva localmente de `machine-id` + `auth/cli-secret` del `DATA_DIR` del router (`NINEROUTER_DATA_DIR`, luego `DATA_DIR`, luego `~/.9router`).
  - No lo imprime ni lo pasa en argv: usa un archivo de headers temporal `0600`.
  - Rechaza archivos con permisos abiertos a grupo u otros.
  - La API key de cliente queda solo para la prueba de `/chat/completions`.
  - En Termux: actualizar el script y correrlo con el mismo usuario y dispositivo que 9router-go, exportando `DATA_DIR` si no es el estándar.
- **Restricciones (`8270eb76`), solo-admin:**
  - `DELETE` de conexiones/providers y `POST …/reorder`;
  - `PUT` que cambia `priority`;
  - CRUD de `/api/provider-nodes`.
  - Las altas con API key (`POST /api/connections`, `/{provider}/import`, `codex/bulk-import`) van siempre al final de la rotación (`LastConnectionPriority`, que cuenta las filas NULL como 999999).
- **Default cerrado (`d216bb75`).** `allowClientCredentialImport` ausente o `false`: cerrado. `true`: solo altas sin destino, al final.
- **Reversión.** Un admin pone `allowClientCredentialImport=true` (panel o `PATCH /api/settings` con el token admin), o se usa un build previo a #158. Está en `docs/WORKBUDDY.md` y el CHANGELOG.
- Sigue permitido con API key: renombrar, activar o desactivar y asignar modelo. No estaba en la lista del dueño; no se amplió el alcance.


### Auditoría (ciclo 8)

Dos PR nuevos hacia `main`, ramas nuevas desde origin/main `47d6e0ce`, sin mergear.

- **#159** `fix/combos-sin-deshabilitados-ni-inactivos-20261030` (`5e3931d5`, `3b8b070e`): https://github.com/joselofarias-byte/capimux/pull/159
  - `dashboard/combos_family.go` (`usableModelFamilies`, `POST /api/combos/auto-family`): los modelos custom se sumaban a su familia sin filtro. Un custom deshabilitado, o de un proveedor sin conexión o con su única conexión desactivada, entraba en la cadena de fallback del combo. Ahora exige alias con conexión activa y que no esté deshabilitado, igual que `/v1/models`.
  - `dashboard/combos.go` (`freeTierComboModels`, `POST /api/combos/auto-free`): usaba `GetConnectedProviders`, que cuenta conexiones desactivadas, e ignoraba los deshabilitados. Con solo una conexión inactiva devolvía 200 y armaba un combo inutilizable. Ahora usa solo conexiones activas y omite los deshabilitados.
- **#160** `fix/descubrimiento-free-variantes-modelsdev-20261030` (`74dfdfcc`): https://github.com/joselofarias-byte/capimux/pull/160
  - `controlplane/discovery/modelsdev.go`: varias entradas se reducen al mismo id base (`poolside/laguna-s-2.1:free` y `poolside/laguna-s-2.1`). Se emitían todas y ganaba la última según el orden aleatorio de mapas de Go, así que los modelos free de OpenRouter entraban y salían del pool free en cada sync.
  - Con el JSON público real de models.dev servido por httptest: en main, 489 candidatas duplicadas y 12, 9, 10 y 12 de 17 modelos free de OpenRouter como free en 4 syncs. Con el arreglo: 0 duplicadas y 17/17 estable.
  - Arreglo: una candidata por proveedor + base; gana la variante free explícita y, si no, el upstream menor.
- Revisado sin hallazgos:
  - filtros y orden de `suggestedmodels.go` (determinista para la misma entrada);
  - shuffle + sort estable del routing (intencional);
  - `builtin_free` sin duplicados;
  - guard `running` y mutexes de `SyncModelCatalog`;
  - `shouldReplacePricing` y `ownedByAdapter` del orquestador.
- Documentado, no corregido (ver sección 12): colisiones de `catalog_sync.go`, cuerpo 200 vacío, claves faltantes en Cline/unorouter, combos de familia que quedan viejos, orden alfabético de miembros.

### Decisiones del dueño (ciclo 9)

Sin merges. Un commit por decisión, con tests de regresión que fallan sin el cambio y tests negativos.

1. **Variantes duplicadas en `catalog_sync.go`** — #161 `fix/catalogo-variantes-y-respuestas-vacias-20261030`, commit `81039c25`: https://github.com/joselofarias-byte/capimux/pull/161
   - Por cada clave base se conserva la variante entera: los límites y modalidades salen de un mismo id, sin mezclar.
   - Orden de elección: disponible (no `deprecated`) → mayor contexto → capacidad (salida, modalidades de entrada, tool calling) → estabilidad (GA > beta > alpha) → menor costo (sin costo declarado al final) → id.
   - Las demás variantes quedan por separado en el campo aditivo `variants` (por id exacto), incluidas las `:free`. `GetCatalogLimits`/`GetCatalogModalities` las consultan primero.
   - Resultado de los 137 casos (feed real, 20 corridas antes y después):
     - claves que cambian entre corridas: 137/137 → 0;
     - corridas que no siguen la regla: 1411/2740 → 0;
     - catálogos distintos: 20 → 1;
     - variantes alternativas conservadas con sus propios valores: 231/231.
   - Criterio que decidió cada clave: contexto 69, salida 26, id (empate total) 15, costo 13, disponibilidad 10, modalidades 4.
   - En 9 de los 16 grupos con `:free` ganó la variante free.
2. **200 vacío o inválido de models.dev** — #161, commit `6505a2c0`.
   - `null`, `{}`, cuerpo vacío, sin modelos, sin límites ni modalidades o estructura inválida no reemplazan el catálogo.
   - Se conservan la memoria, la caché (byte a byte) y el ETag. El incidente queda en `lastError` («catálogo rechazado: …») y en un aviso de log. Se reintenta.
   - La caché se escribe en un temporal y se renombra antes de publicar en memoria.
   - En el descubrimiento: un feed vacío de models.dev y la clave `free`/`data` ausente o `null` de Cline/unorouter son error.
3. **Combos generados obsoletos** — #162 `feat/combos-generados-poda-y-orden-20261030`, commit `16563e1a`: https://github.com/joselofarias-byte/capimux/pull/162
   - Los generadores registran lo que escriben (kv `generatedCombos`).
   - Al regenerar se borran los generados sin tocar que quedaron con menos de 2 modelos válidos y disponibles (`pruned` en la respuesta).
   - Los editados, manuales o anteriores al registro no se borran: `GET /api/combos` los marca con `needsReview`/`reviewReason`/`unavailableModels`.
   - auto-free con menos de 2 modelos responde 409 en español.
4. **Orden por versión semántica** — #162, commit `8513d3b6`.
   - Comparación numérica por componente (4.10 > 4.9), con una regla explícita de qué token es versión: fechas, snapshots y sufijos no cuentan.
   - Los modelos sin versión van al final; desempate por la clave `alias/modelo`.
5. **Conexiones solo-admin** — #158, commit nuevo `1803b22a` (sin force-push).
   - Pasan a solo-admin, en el backend:
     - `PUT /api/connections/{id}` y `/api/providers/{id}` (nombre, `isActive`, `assignedModel`, todo);
     - los `…/test` de conexiones, que guardan estado;
     - el consumo de créditos de reinicio;
     - proxy pools (alta, edición, borrado y test);
     - `freebuff/session/switch`.
   - La API key sigue pudiendo usar conexiones e inferir, y `GET /api/connections`.
   - Workbuddy no se ve afectado, porque ya importa con el token admin desde el ciclo 7.

Dependencias y orden de merge:
- #159 → #162 (#162 incluye los commits de #159).
- #158, #160 y #161 son independientes.
- `git merge-tree`:
  - #162 sin conflictos con #158 ni con #161;
  - #161 sin conflictos con #158, #159 ni #160;
  - #162 con #160: solo `CHANGELOG.md`, el mismo conflicto que #159 ya tiene con #160 (conservar ambas entradas).

### Merges autorizados y bug de strategy (ciclo 10, vinculado al ciclo 9)

José Luis autorizó por escrito (30/10, 13:42 UTC-3) solo estos cinco merges, en este orden, con merge commit (sin squash, sin rebase y sin force-push). No se borraron ramas a mano y no hubo tags, releases, deploys ni imágenes.

| Orden | PR | Head mergeado | Merge commit en main | Hora (UTC-3) |
|---|---|---|---|---|
| 1 | #158 | `1803b22a` | `bf15b250577cdb9272a4011a8e910c3747a950a9` | 13:42 |
| 2 | #161 | `2ae0ac6c` | `3bc3929b919a7fb8bca16aceda7ce3e0c8471e73` | 14:02 |
| 3 | #159 | `52b732be` | `522d683fe906e457eac63361af76a06f872f9317` | 14:08 |
| 4 | #162 | `0a39f129` | `c84327727711f9bb42d817637c8893afc3f24438` | 14:18 |
| 5 | #160 | `9435776f` | `34a2fcd603ebc77e4109eeb889459c7e3af39c78` | 14:32 |

Actualizaciones de rama (todas por `git merge origin/main`, sin rebase):
- #161: merge de main (post-#158), sin conflictos.
- #159: merge de main (post-#161), sin conflictos.
- #162:
  - merge de main (post-#159), sin conflictos;
  - la base ya era `main`;
  - el diff contra main quedó exactamente con los dos commits propios: `16563e1a` y `8513d3b6`.
- #160:
  - merge de main (post-#162) con conflicto solo en `CHANGELOG.md`;
  - se resolvió conservando todas las entradas: primero las de #159 y #162 que ya estaban en main, y después la de #160;
  - el diff de `CHANGELOG.md` contra main quedó solo con la entrada de #160.

CI en verde sobre el head mergeado de cada PR:

| PR | test | Integration | Docker verify | Otros |
|---|---|---|---|---|
| #158 | 2m29s | 50s | 1m47s | multiagent 1m45s |
| #161 | 2m29s | 50s | 1m33s | — |
| #159 | 2m42s | 48s | 2m25s | — |
| #162 | 2m27s | 52s | 2m07s | — |
| #160 | 1m44s | 44s | 1m25s | — |

En #161 hubo un re-run único de `TestHandleGetConnectionUsage_SpacesBurstAcrossConnections`, el flake de tiempos conocido.

Batería sobre main local después de cada merge (`/tmp/mainverify.sh`, `HOME=/home/box`). Pasos:
- guard de tailscale verificado: la variable fake tiene 0 usos y `testing.Testing()` está en `tunnel.go`;
- `go build`;
- `go vet ./...` y `go vet -tags=integration ./...`;
- `go test -count=1 ./...`;
- `go test -race` en handlers, providers y controlplane;
- `go test -tags=integration -race ./internal/integration/...`;
- 403 de admin con API key: `TestClientKeyCannotRunAdminOperations`, `TestClientKeyCannotChangeConnectionConfigOrState`, `TestClientKeyCredentialCreationNeedsAdminOptIn` y `TestAdminTokenChangesConnectionConfigAndState`;
- desde #162, el 409 de auto-free con un solo modelo: `TestAutoFreePrunesGeneratedComboBelowTwoModels` (afirma `StatusConflict` y el mensaje en español), `TestAutoFreeComboSkipsInactiveConnectionsAndDisabledModels` y `TestAutoFreeKeepsEditedCombo`;
- build Termux (ELF aarch64, `b7`).

Resultados:
- post-#158 (`bf15b250`): todo OK.
- post-#161 (`3bc3929b`):
  - `go test ./...` falló una vez en `TestGateAcquire_SpacesConcurrentCallers` de fetchgate («32.46ms … want >= 40ms»), el flake de tiempos conocido; se re-corrió una vez el paquete y pasó;
  - lo demás OK.
- post-#159 (`522d683f`): todo OK.
- post-#162 (`c8432772`): todo OK, incluidos el 403 y el 409.
- post-#160 (`34a2fcd6`):
  - el `-race` de handlers falló una vez en `TestHandleGetConnectionUsage_SpacesBurstAcrossConnections`, el flake conocido; se re-corrió una vez el paquete y pasó;
  - lo demás OK, incluidos el 403 y el 409.

CHANGELOG: se comparó cada línea que agrega cada merge (`git diff <merge>^1 <merge> -- CHANGELOG.md`) contra el `CHANGELOG.md` de main `34a2fcd6`.

| PR | Líneas presentes |
|---|---|
| #158 | 11/11 |
| #161 | 2/2 |
| #159 | 1/1 |
| #162 | 2/2 |
| #160 | 1/1 |

No faltó ninguna y no quedan marcadores de conflicto.

Bug de strategy: PR independiente, **sin mergear**: #163 `fix/combo-strategy-persistencia-20261030`, commit `fa759c44`, desde main `34a2fcd6`: https://github.com/joselofarias-byte/capimux/pull/163
- Causa:
  - `Repo.CreateCombo` y `UpdateCombo` escribían en una columna `strategy` que el esquema real de `combos` no tiene, y descartaban el error;
  - el GET y el router leen `settings.comboStrategies[nombre]`.
- Arreglo:
  - la estrategia se persiste en `settings.comboStrategies`;
  - un PUT sin `strategy` no la toca;
  - renombrar mueve la entrada;
  - regenerar auto-free y auto-family ya no la resetea;
  - un valor inválido devuelve 400 en español. Valores válidos: `fallback`, `first-model`, `round-robin`, `roundrobin`, `sticky`, `capacity` y `fusion`.
- Tests:
  - `internal/db`, con una DB real en disco, migraciones y cierre y reapertura:
    - `TestComboStrategyPersistsAcrossReopen`;
    - `TestComboStrategyUntouchedByOtherEdits`;
    - `TestComboStrategyFallbackReadsBackWithGlobalDefault`;
    - `TestIsValidComboStrategy`.
  - HTTP:
    - `TestUpdateComboStrategyPersists`: guardar, GET, y recargar la DB con un router nuevo;
    - `TestComboStrategyRejectsInvalidValue`: 400 al crear y al editar, y la estrategia no cambia;
    - `TestUpdateComboOtherFieldsKeepStrategy`;
    - `TestRegenerateKeepsGeneratedComboStrategy`.
  - Con el código de main `34a2fcd6` fallan los 7 tests de persistencia y de rechazo; el de validación no compila sin el cambio. Con el arreglo pasan los 8.
- Batería completa en la rama: todo OK (incluidos el 403 y el 409, Termux `b7`) y `bun test` 117/117.
- CI #163 en verde a la primera: test 1m55s, Integration 1m11s, Docker verify 1m47s.

## 7. Pruebas y verificación

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

Ciclo 5 (#157, corrido localmente el 2026-10-30):
- `go build ./...` y `go vet ./...`: OK.
- `go test -count=1 ./...`: OK en los 44 paquetes.
- `go test -race` en `handlers/chat`, `handlers/dashboard`, `providers` y `controlplane/...`: OK.
- Los tests nuevos con `-count=5`: OK.
- Build Termux `CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build ./cmd/9router-go/`: OK (ELF aarch64).
- Guard de Tailscale: `testing.Testing()` está en `tunnel.go` y no se definió `CAPIMUX_TEST_FAKE_TAILSCALE`. No se usaron APIs pagas, solo `httptest`.
- CI de #157 en verde: `test` 2m28s, `Integration tests` 49s, `Build Docker image (verify only)` 1m45s.

Ciclo 6 (#158, corrido localmente el 2026-10-30):
- Test nuevo `internal/handlers/client_credential_import_test.go` con el router real:
  - **falla sobre `main`**: allowOverwrite → 200 y pisa la key del dueño; alta con `baseUrl` → 200; con `false` igual importa; la API key puede activar el ajuste;
  - **pasa con el cambio**. Cubre: admin (sesión y CLI) sí puede; API key sin el ajuste no puede; API key con el ajuste sí puede (solo-alta); sin escalada para sobrescribir en los 3 valores; `requireLogin=false` sin cambios; el request exacto de Workbuddy → 200.
- Tests unitarios del diff de credenciales y destinos: `dashboard/credential_key_test.go`.
- Los tests de handler que creaban conexiones sin credenciales ahora corren con `requireLogin=false` (helper `openDashboard`).
- Integración: `TestConnectionLifecycle` y `TestConnectionNameConflictIsRefused` ahora crean y sobrescriben como dueño, y tienen un subtest negativo con API key. Ese subtest falla sobre `origin/main` (overwrite → 200).
- Comandos corridos:
  - `go build ./...` y `go vet ./...`: OK;
  - `go test -count=1 ./...`: 44 paquetes ok;
  - `go test -race` en handlers, dashboard, oauth, auth y middleware: OK;
  - `go test -tags=integration -race` y `go vet -tags=integration`: OK;
  - Termux android/arm64: OK (ELF aarch64).
- Guard de Tailscale: `testing.Testing()` sigue activo y no se definió `CAPIMUX_TEST_FAKE_TAILSCALE`. Sin APIs pagas.
- Inestabilidad: `TestGateAcquire_SpacesConcurrentCallers` (fetchgate) volvió a fallar una vez por temporización (34 ms vs. 40 ms). Pasó en la corrida siguiente; es el flaky preexistente.
- CI de #158:
  - con `2435942e`, `Integration tests` falló (no lo había corrido localmente);
  - con `67bfd49e`: test 1m51s, Integration tests 43s, Docker verify 1m36s y multiagent 1m24s, todo en verde.

Ciclo 7 (#158, corrido localmente el 2026-10-30):
- Tests nuevos, cada uno verificado fallando sin su cambio:
  - `TestWorkbuddyBootstrapImportsWithAdminToken`: con el ajuste ausente y en `false`. Corre el script real contra el router real con un upstream stub y sin red. El script de main da 403;
  - `TestWorkbuddyBootstrapWithoutAdminTokenFailsClearly` y `TestWorkbuddyBootstrapRefusesOpenTokenFiles`;
  - `TestClientKeyCannotRunAdminOperations`: 9 operaciones con API key, en los 3 valores del ajuste, responden 403; la rotación y los nodos no cambian;
  - `TestAdminTokenRunsAdminOperations`;
  - `TestClientKeyConnectionsGoLastInRotation`: el orden previo queda intacto y el alta, el import OAuth y el bulk import quedan últimos;
  - `TestClientKeyCredentialCreationNeedsAdminOptIn`: con el ajuste ausente, el import y el bulk import con API key dan 403;
  - integración `TestConnectionLifecycle/delete_removes_it`: DELETE con API key da 403 y con el operador da 200.
- Comandos corridos:
  - `go build ./...`, `go vet ./...` y `go vet -tags=integration`: OK;
  - `go test -count=1 ./...`: todo ok;
  - `-race` en handlers, auth, db y middleware: OK;
  - `go test -tags=integration -race`: OK;
  - Termux android/arm64: OK (ELF, e_machine 0xb7 = aarch64);
  - `bash -n`: OK;
  - `shellcheck` 0.11.0: solo SC2034 (`i` sin usar en `ensure_router_running`), que ya estaba en main.
- Guard de Tailscale: `testing.Testing()` sigue activo y no se definió `CAPIMUX_TEST_FAKE_TAILSCALE`. Sin APIs pagas; no se tocó el teléfono ni el Workbuddy real.
- CI de #158 con `d216bb75`, todo en verde: test 2m29s, Integration tests 43s, Docker verify 1m50s, multiagent 1m43s y Vercel.

Ciclo 8 (#159 y #160, corrido localmente el 2026-10-30):
- Tests nuevos, verificados fallando en main:
  - #159: `TestAutoFamilySkipsDisabledAndUnconnectedCustomModels`, `TestAutoFamilySkipsInactiveConnections` y `TestAutoFreeComboSkipsInactiveConnectionsAndDisabledModels`; regresión `TestAutoFamilyCustomModelFollowsConnectionAlias` (pasa en main y con el arreglo);
  - #160: `TestModelsDevAdapter_CollapsedVariantsAreDeterministic` (main: `openrouter/inkling emitted twice`) y `TestOrchestrator_ModelsDevFreeLaneStableAcrossSyncs` (main: `round 1: free lane flipped`). Solo httptest, sin red.
- En cada rama: `go build ./...`, `go vet ./...`, `go vet -tags=integration`, `go test -count=1 ./...`, `-race` (dashboard y db en #159; `internal/controlplane/...` en #160), `go test -tags=integration -race` y build Termux android/arm64 (ELF, e_machine 0xb7): todo OK.
- Guard de Tailscale: `testing.Testing()` en `tunnel.go` (líneas 54 y 64) y sin `CAPIMUX_TEST_FAKE_TAILSCALE`. Sin APIs pagas; no se tocó el teléfono.
- CI:
  - #159: todo en verde a la primera (test 1m49s, Integration 41s, Docker verify 1m27s, Vercel).
  - #160: el primer intento falló en `TestHandleGetConnectionUsage_SpacesAntigravityBurst` (dashboard, test de tiempos: 35 ms entre lecturas, esperaba ≥ 40 ms). El PR no toca ese paquete. Se re-corrió el job y quedó todo en verde (test 2m11s, Integration 41s, Docker verify 1m42s, Vercel).

Ciclo 9 (corrido localmente el 2026-10-30, con el mismo script en cada rama):
- Tests nuevos (cada uno verificado fallando sin su cambio):
  - #161:
    - `catalog_variants_test.go`: fixture real de models.dev, determinismo, `TestBetterCatalogCandidate` e ida y vuelta por archivo;
    - `catalog_sync_reject_test.go`: 8 cuerpos rechazados, con caché, memoria y ETag intactos;
    - `discovery/empty_response_test.go`: Cline, unorouter y models.dev.
  - #162:
    - `combos_prune_test.go`, 7 tests (poda, editado, legado, manuales, auto-free 409, borrado del registro);
    - `combos_version_test.go`: 20 casos de `modelVersion`, 4.10 > 4.9, fechas, 50 permutaciones, y los dos endpoints;
    - tres tests de auto-free pasaron de bazaarlink (1 modelo free) a opencode (2).
  - #158:
    - `TestClientKeyCannotChangeConnectionConfigOrState`: 15 endpoints × 3 valores del ajuste; falla en 15 casos sobre `d216bb75`;
    - `TestAdminTokenChangesConnectionConfigAndState`;
    - se actualizaron 3 tests de handlers y la integración `TestConnectionLifecycle`;
    - los 3 tests de Workbuddy pasan.
- En cada rama, todo OK:
  - `go build ./...`, `go vet ./...` y `go vet -tags=integration`;
  - `go test -count=1 ./...`;
  - `-race`:
    - #161: providers y discovery;
    - #162: dashboard y db;
    - #158: handlers, dashboard, auth y middleware;
  - `go test -tags=integration -race`;
  - build Termux android/arm64 (ELF, e_machine 0xb7).
- Guard de Tailscale: `testing.Testing()` en `tunnel.go` (líneas 54 y 64) y sin `CAPIMUX_TEST_FAKE_TAILSCALE`.
- Solo GETs públicos gratuitos a models.dev (para el análisis de los 137 casos) y stubs httptest. No se tocó el teléfono.
- CI: ver la sección 14.

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
- Ciclo 7: el script de Workbuddy deriva el token CLI localmente en vez de guardar una copia, así no queda otro secreto en disco. Las altas por API key reciben una prioridad explícita (última) para no depender del desempate por `updatedAt` entre filas NULL.
- Guardia Tailscale: el test falla con un panic en vez de saltearse en silencio. Si una regresión intenta tocar el host, se ve.

- Ciclo 9:
  - un combo es «generado» solo mientras su fila coincide con lo que escribió el generador; ante la duda (editado o sin registro) se trata como configuración del usuario y no se borra;
  - la validez para revisión es indulgente (proveedor activo y modelo no deshabilitado, sin exigir que el modelo esté en el catálogo), para no marcar modelos custom o más nuevos;
  - el orden por versión solo reconoce tokens con una regla escrita; lo que no la cumple queda sin versión, en vez de adivinar;
  - las variantes del catálogo se guardan enteras y por id exacto, nunca mezclando campos de distintas variantes.

## 12. Riesgos y asuntos pendientes

- #157 (ciclo 5) no depende de nada: se puede mergear en cualquier momento, con merge commit.
- #158 (ciclo 6) no depende de nada; con #148 hay un conflicto trivial en `authPostureSettingKeys` (conservar ambas líneas).
- Decisión del dueño de ciclo 6, resuelta en el ciclo 7: default cerrado, Workbuddy migrado al token admin y altas por API key siempre al final.
- #158 tras el ciclo 7: con #148 sigue habiendo un solo conflicto, en `admin_guard.go` (`authPostureSettingKeys`; conservar ambas líneas). Se verificó con `git merge-tree`.
- Las copias viejas del script de Workbuddy (Bearer) reciben 403 hasta que se actualice el script o un admin active el opt-in.
- Falta probar en el Termux real del dueño el script migrado. No se tocó el teléfono.
- Orden de merge (ciclo 4): ya se mergearon #145, #146, #147, #151 y #154. Faltan, independientes, #148 y #149, y la cadena #140 → #150 → #152 → #153 → #155 (cambiando la base de #155 a `main`), siempre con merge commit.
- Importación de credenciales con API key: cerrada por defecto en #158 (ciclo 7).
- Updater:
  - No hay auto-update hasta que el dueño publique el primer tag `v*` con el `release.yml` de #153. Mientras tanto, el updater responde «al día».
  - Un hash no garantiza autenticidad: quien controle el manifest controla el hash. El manifest firmado de #140 es el paso siguiente.
- **Decisión del dueño (infra, no se tocó):** el job Docker de `release.yml` publica en `REGISTRY_IMAGE: luqmenul/9router-go` (registro upstream).
- Incidente Tailscale: el estado serve/funnel previo de la máquina de Grok Bot es desconocido y no se restauró, por indicación del dueño.
- Con `requireLogin=false` cualquiera que alcance el puerto sigue pudiendo fijar la contraseña; es la configuración elegida por el dueño, pero conviene advertirlo en la UI.
- Si `requireLogin` está activo y nunca se fijó contraseña, el login sigue aceptando `123456`/`INITIAL_PASSWORD` (paridad upstream).
- Falta validación física en Termux/Android del APK con este cambio (el APK usa el token CLI, que sigue autorizado).

- Ciclo 5 (#157): `gemini-2.0-flash` sigue clasificado solo como STT, igual que en el registro upstream. Si se quiere ofrecer como chat, es una decisión de catálogo.
- `fallback.go` tiene una línea en blanco doble preexistente que `gofmt` marca. No se tocó, para mantener el diff mínimo.

- Ciclo 8, decisiones y riesgos (documentados en el ciclo 8; los cinco primeros los resolvió el dueño en el ciclo 9 → #161, #162 y #158):
  - `providers/catalog_sync.go` indexa límites y modalidades por id base y se queda con la primera variante según el orden del mapa. Con el catálogo real hay 137 colisiones con límites o modalidades distintos (p. ej. 32768 contra 131072 de contexto). Elegir qué variante vale es una decisión de catálogo.
  - Si models.dev responde 200 con `null` o `{}`, el catálogo en memoria se reemplaza y la caché en disco se sobrescribe vacía.
  - Los adaptadores Cline y unorouter toman un 200 sin la clave `free`/`data` como catálogo vacío autoritativo y desactivan todos sus modelos. No se observó en la práctica.
  - El recomendador por familia no borra combos de familias que quedaron con menos de 2 miembros o que desaparecieron. Borrarlos es una decisión del dueño.
  - Orden de miembros alfabético: versiones viejas primero y «4.10» antes que «4.9». Es una decisión de diseño.
  - `GetConnectedProviders` quedó sin uso tras #159; no se borró.
  - `TestHandleGetConnectionUsage_SpacesAntigravityBurst` es sensible a tiempos en CI (igual que el flaky conocido de fetchgate).
  - El loop de orcarouter hace `defer resp.Body.Close()` dentro del loop (menor).

- Ciclo 9, riesgos y pendientes:
  - Casos dudosos del catálogo, que deciden las reglas de la decisión 1:
    - `thinkingmachines` `kimi-k2.6` con `:peft:N` (131072 contra 32768);
    - la variante con visión de deepseek en nearai;
    - `openrouter` `nemotron-3-ultra`: la free tiene 1M y la paga 262144, así que una búsqueda por id base devuelve 1M (la paga, por id exacto, da 262144);
    - `laguna-s-2.1`, con ambas variantes `deprecated`.
  - #162:
    - la poda ocurre solo al regenerar, no al desactivar una conexión;
    - regenerar sigue sobrescribiendo el combo con el id generado (comportamiento previo);
    - los combos generados antes de #162 no tienen registro, así que no se podan hasta la próxima regeneración;
    - `HandleUpdateCombo` no guarda `strategy` si la columna no existe en el esquema (preexistente, no se tocó).
  - #158: los clientes que renombraban, activaban o probaban conexiones con API key ahora reciben 403 (documentado en `CHANGELOG.md` y `docs/WORKBUDDY.md`).
  - Flakes de tiempos en CI del paquete dashboard (usage throttle): `TestHandleGetConnectionUsage_SpacesBurstAcrossConnections` se sumó a los ya conocidos.

## 13. Participación de otros modelos

Ninguna. Todo el análisis, la implementación y la verificación de este ciclo los hizo Grok Bot localmente; no se consultó ni se atribuye trabajo a otros modelos.

## 14. Estado final y siguiente prioridad

Corregidos y verificados: hallazgos 1 y 2 (#145, #146), la gestión de API keys del hallazgo 3 (#148), Kiro social (#147), `-race` en CI (#149) y las fuentes de SHA-256 del updater (#150, sobre #140). Pruebas aisladas de Tailscale real (#145). En el ciclo 3, además, se corrigieron y verificaron el device-code OAuth (#151), el repo renombrado (#152), la release con `sha256s` (#153) y los rate limits con `retry_after` numérico (#154). Nada mergeado.

Ciclo 5: #157 corrige el fallback de cuentas en cooldown, los reintentos repetidos de los combos sobre la misma cuenta y la clasificación de Gemini 2.5. Está sin mergear y es independiente de la cadena del updater.

Ciclo 6: #158 impide que una API key de cliente sobrescriba credenciales, y las altas quedan bajo el opt-in `allowClientCredentialImport`. Workbuddy sigue funcionando. CI en verde, sin mergear.

Ciclo 7: dentro de #158, Workbuddy importa con el token admin local, la API key ya no borra, reordena, cambia prioridades ni toca provider nodes, sus altas van al final y la importación con API key queda cerrada por defecto. CI en verde, sin mergear.

Ciclo 8: #159 (el recomendador de combos ya no sugiere modelos deshabilitados ni de proveedores sin conexión activa) y #160 (el descubrimiento de models.dev conserva de forma estable los carriles `:free`). Independientes entre sí y de los demás, CI en verde, sin mergear.

Ciclo 9: las cinco decisiones del dueño quedaron aplicadas en #161 (decisiones 1 y 2), #162 (decisiones 3 y 4, depende de #159) y #158 (decisión 5, commit `1803b22a`). CI en verde: #158 (`1803b22a`): test 2m29s, Integration 50s, Docker verify 1m47s, multiagent 1m45s; #161 (`6505a2c0`): test 2m39s tras re-correr un flake de tiempos de dashboard, Integration 42s, Docker verify 1m39s; #162 (`8513d3b6`): test 1m56s, Integration 43s, Docker verify 1m30s, a la primera. Sin mergear.

Ciclo 10: mergeados con autorización escrita #158, #161, #159, #162 y #160 (SHA en la sección 6, «ciclo 10»). Main queda en `34a2fcd6`, verificado. #163 (persistencia de `strategy`) tiene la CI en verde y queda sin mergear.

Siguiente prioridad (actualizada en el ciclo 10): #163 y #157 a decisión del dueño; #156 sin mergear. Antes era: que el dueño revise y mergee en el orden indicado (#157, #158, #160 y #161 pueden ir en cualquier momento; #159 antes que #162). Después:
- probar el script de Workbuddy migrado en Termux (#158);
- firma del manifest;
- seguir auditando el fallback, el catálogo y el recomendador.
