# Triage de pull requests abiertos — 2026-11-08

Auditoría de solo lectura. Recomienda. No cierra, no mergea, no retargetea, no convierte a draft o ready, no comenta y no edita ningún otro PR. No es una aprobación de José Luis Farías. La palabra de un agente no sustituye la del dueño.

Fecha del triage: **2026-11-08**.

Referencia de `origin/main`: **`6e4f0c2e68400a7314b233a628286e761953ee19`**
(merge de [#180](https://github.com/joselofarias-byte/capimux/pull/180), 2026-11-05 23:20 UTC, «Capimux: integración selectiva, multimodelo 2–10 y límites HTTP seguros»). Ese SHA es ancestro del checkout usado para mirar código. Incluye el merge de [#175](https://github.com/joselofarias-byte/capimux/pull/175) en `adfdbcbe`.

## Cómo se juntaron los datos

1. `git fetch origin main` y `git rev-parse origin/main` → el SHA de arriba.
2. `gh pr list --repo joselofarias-byte/capimux --state open --limit 200` → **48** PRs abiertos (el tope por defecto de 30 no alcanza; con límite 200 no hubo segunda página).
3. Para cada PR, `GET /repos/joselofarias-byte/capimux/pulls/{n}`: `draft`, `mergeable`, `mergeable_state`, `commits`, cuerpo y `base.sha` guardado.
4. Compare real de tres puntos, no el `base.sha` del PR (queda viejo):
   `GET /repos/joselofarias-byte/capimux/compare/6e4f0c2e68400a7314b233a628286e761953ee19...{head.sha}`
   → `behind_by` (atrás de main) y `ahead_by` (adelante de main).
5. El mismo compare contra la **rama base actual** del PR (`{base.ref}...{head.sha}`), para no leer una pila como si todos los commits fueran de ese PR.
6. Lista de archivos del diff que GitHub publica en el PR (`pulls/{n}/files`) y, en #130, también los archivos del compare contra main.
7. Cuerpos, comentarios de issue y el árbol de `origin/main` para juzgar si algo ya está cubierto. No se reejecutó la batería de Go ni de Bun de ningún PR. Si un comentario cita un exit code, es de ese comentario, no de este triage.

`mergeable` vino calculado en los 48 (ninguno quedó `null` / `unknown`).

**Cómo leer las columnas.** `mergeable_state=clean` significa que GitHub no vio conflicto de archivos. No significa «al día con main». Varios PR `clean` están cientos de commits atrás: GitHub igual los marca mergeable porque un merge commit no pisaría las mismas líneas. La columna atrás/adelante es el compare contra `6e4f0c2e`. En un PR apilado, «adelante vs main» incluye commits de los padres que tampoco están en main; «vs su base» es el tramo propio.

## Resumen ejecutivo

| | Cantidad |
|---|---:|
| PRs abiertos triados | **48** |
| Borrador | 41 |
| Listo para revisión | 7 (#76, #104, #105, #107, #127, #128, #179) |
| `mergeable=true` y `mergeable_state=clean` | 33 |
| `mergeable=false` y `mergeable_state=dirty` | 14 |
| `mergeable=true` y `mergeable_state=unstable` | 1 (#96; el check `validate` figura FAILURE; no reejecuté ese workflow) |
| `mergeable` sin calcular | 0 |
| Al día con `origin/main` (0 atrás) | 5 (#131, #176, #177, #178, #179) |
| Atrás de `origin/main` | 43 |

| Recomendación | Cantidad | PRs |
|---|---:|---|
| **Cerrar por obsoleto** | 1 | #107 |
| **Actualizar vs main** | 10 | #65, #67, #69, #76, #105, #106, #114, #115, #121, #127 |
| **Esperar decisión de José Luis** | 37 | el resto, listado abajo |

Ninguna de esas recomendaciones se ejecutó en el corte del 2026-11-08. «Actualizar vs main» en una pila cuya base no es `main` quiere decir **portar el delta a una rama nueva desde main**, no apretar «Update branch» sobre `feat/free-best-dynamic-routing` ni sobre una cadena de octubre. Lo medido después de esas actualizaciones está en [Estado al 2026-11-09 tras las actualizaciones](#estado-al-2026-11-09-tras-las-actualizaciones). Las cifras de las tablas de arriba siguen siendo las del 2026-11-08.

### Los 48, sin omisiones

#50, #51, #54, #57, #62, #63, #65, #67, #68, #69, #70, #71, #73, #76, #78, #82, #84, #86, #88, #90, #92, #93, #94, #95, #96, #97, #104, #105, #106, #107, #114, #115, #116, #121, #122, #127, #128, #129, #130, #131, #133, #136, #141, #144, #176, #177, #178, #179.

## Decisiones del dueño ya tomadas (verificadas, no se contradicen)

José Luis Farías las tomó. Este triage las comprueba contra el repo y no recomienda lo contrario.

1. **2026-11-01, merge secuencial.** Autorizó mergear #174, después #142, después #143.
   - [#174](https://github.com/joselofarias-byte/capimux/pull/174) MERGED 2026-11-01T12:23:27Z.
   - [#142](https://github.com/joselofarias-byte/capimux/pull/142) MERGED 2026-11-01T12:30:59Z.
   - [#143](https://github.com/joselofarias-byte/capimux/pull/143) MERGED 2026-11-01T12:37:07Z.
   - El orden de `mergedAt` coincide con esa secuencia. Los tres están en la historia de `6e4f0c2e`.
   - **#130 no se cierra.** Sigue abierto, draft, base `feat/free-best-dynamic-routing`. Las cinco partes pendientes están escritas en `docs/audit-phase1-pending.es.md` (llegó con #175). Más abajo se resumen.
   - **#141 y #144 siguen en draft.**
   - **#131 se sigue actualizando y no se mergea.** Head actual `693297b974b9`, 0 atrás / 5 adelante, CLEAN. El comentario del 2026-11-08 en el PR documenta ese merge de main. Sigue draft.

2. **2026-11-02, licencias.** Consolidar los borradores #70–#92 en un solo PR contra main, sin mergearlo hasta la validación final. Los intermedios no se cierran hasta que el consolidado esté creado, validado **y** mergeado; después se cierran con una referencia y se conservan las ramas.
   - El consolidado existe: [#176](https://github.com/joselofarias-byte/capimux/pull/176), rama `feat/license-consolidated-20261102`, head `1dc7532e9be8`, draft, base `main`, 0 atrás / 96 adelante, `mergeable=true`, `mergeable_state=clean`.
   - Sigue sin mergear. El cuerpo describe URL solo por `NINEROUTER_LICENSE_URL` (sin default), Community sin servidor, Pro solo con licencia verificable, y sin lockout si el servidor falta o está caído.
   - Los 10 borradores de origen siguen abiertos: #70, #71, #73, #78, #82, #84, #86, #88, #90, #92. No se recomienda cerrarlos ahora.
   - En el rango numérico #70–#92 hay otros números que **no** son esa cadena: #72, #74, #79, #80, #81, #83, #85, #87, #89 y #91 están CLOSED (validadores de CI, no mergeados) y no entran en estos 48. #75, #76 y #77 tampoco son esa cadena: #75 y #77 ya están mergeados a main por otros temas; **#76 es OAuth móvil** y se trata aparte.

3. **2026-11-03.** #175 pasó a ready y José Luis lo mergeó: MERGED 2026-11-03T12:47:05Z, merge commit `adfdbcbe` en main. #176 sigue en draft hasta la clave pública del emisor de producción y el límite Community/Pro. La URL sin default y la clave Ed25519 local de instalación ya están en el diseño que el propio #176 describe; este triage no las vuelve a poner en duda. #177 sigue en draft hasta la integración real con DSH.

4. **2026-11-03, después.** #178 es el PR aparte de los dos huecos de `strictProxy` vistos después de #175 (pruebas de conexión y relays Vercel/Cloudflare/Deno). Sigue draft, head `5282470e9a7e`, 0 atrás / 2 adelante, CLEAN. Las tres preguntas de abajo siguen sin respuesta del dueño en los comentarios del PR.

## Lo que necesita a José Luis

Ningún ítem de esta lista está decidido por este triage. No hay aprobación de merge.

1. **#176 (seguir en draft).** Falta la clave pública del emisor de producción y el límite Community/Pro. Recién con eso validado y, después, mergeado, se cierran #70, #71, #73, #78, #82, #84, #86, #88, #90 y #92 citando a #176 y **conservando las ramas**.
2. **#177 (seguir en draft).** Falta la integración real con DSH: esquema de campos de la sesión (cookies/tokens, expiración, renovación, recarga), autorización de un smoke en un entorno real, y custodia de la clave (archivo local, clave externa, o unificar con #141). Hoy el comprobador por defecto no contacta a DSH.
3. **#178 (seguir en draft).** Tres preguntas abiertas:
   - (a) Los media handlers `xquik_search.go`, `antigravity_search.go` y `media.go` también reintentan en directo sin mirar `strictProxy`. ¿PR chico aparte?
   - (b) Con `strictProxy` y proxy HTTP, un upstream loopback que sale por `ClientForUpstream` va directo en la misma máquina, sin el proxy. ¿También fail-closed?
   - (c) Cambio de comportamiento ya escrito en el borrador: una conexión relay estricta deja de alcanzar hosts que no son el relay (un refresh de token que antes iba directo ahora falla). La prueba de conexión de un pool relay estricto falla siempre, con un mensaje claro, porque el test no puede usar el relay. Falta aceptar o rechazar ese cambio.
4. **#131 (seguir en draft, no mergear).** Falta decidir los headers que imitan al cliente OpenCode (`User-Agent`, `x-opencode-client`, `x-opencode-session`) y el path público `/inference/v1`. En main el executor usa `/zen/v1` (y `/zen/go/v1` en el camino Go). El comentario del dueño del 2026-11-01 ya descartó, por estar en main, el executor, la separación 403/401/429/5xx y el fallback del 403. El PR sigue siendo doc y tests.
5. **#141 (seguir en draft).** Falta el diseño de custodia de la clave antes de cablear lectura/escritura y la migración. El cifrado AES-GCM del PR no está integrado al SQLite de producción. Además está 7 commits atrás de main (justo #175 y #180).
6. **#144 (seguir en draft).** Falta la aprobación explícita para integrar la telemetría shadow. También es el insumo que #175 dejó para la parte (a) de #130. Está 7 commits atrás (los mismos #175 y #180).
7. **#130 (no cerrar).** El delta del head contra main son 12 archivos y 22 commits, y esos archivos son los que #175 ya llevó a main (OAuth 1 MiB, `/v1/messages`, `strictProxy` fail-closed, refresh de pools, changelog). El diff de 1049 archivos que muestra GitHub es contra `feat/free-best-dynamic-routing`, no contra main. No hace falta «actualizar» ese PR para no perder los fixes: ya están en main vía #175, con conflictos si se intentara mergear #130 así. Lo que falta decidir son las cinco partes (detalle en la sección de #130): si se encienden los cuatro opt-in y en qué entorno; si la parte (a) espera a #144; si algún día se monta `/api/admin/registry` detrás de `RequireAdminAuth`; si la parte (b) existe como workflow manual, sin cron y sin secretos.
8. **#179 (listo para revisión, no es draft).** Está al día (0 atrás / 3 adelante, CLEAN) y solo toca `README.md`. El merge a main espera el OK explícito. Este documento no lo da.
9. **#63.** Main sigue teniendo `opencode` y `opencode-zen` con `NoAuth` y `DefaultAPIKey: "public"` (`internal/providers/providers.go`). #63 quiere quitar esa ruta anónima y está en conflicto, 251 atrás. Hay que decidir si esa ruta se elimina o se mantiene mientras siguen abiertos los headers de #131. No se recomienda cerrarlo: el cambio no está en main.
10. **Línea free-best vieja y auditorías Codex (#50, #51, #93, #94, #95, #96, #97, #104).** La base `feat/free-best-dynamic-routing` no es main (los PR están ~609 commits atrás, salvo #50/#51 que salen de otra punta). #98 y #99 ya se mergearon **en esa rama**, no en main, y absorben A1–A7. En main, #124, #142 y #175 cubren pedazos distintos. Falta decidir, caso por caso, si se cierra el duplicado o si se porta un resto. El detalle está en ese grupo. No se recomienda un cierre masivo.
11. **#94 y #96, en particular.** El comportamiento que describen se ve en el `orchestrator.go` y en `RefreshAccountsAfterSnapshot` de main, y #98 ya metió A3/A5 en la rama feature. No se recomienda cerrarlos en este informe: no hay un PR mergeado a main que los nombre, y el test de carrera `orchestrator_account_race_test.go` de #96 no está en main. La decisión es si se cierran como redundantes o si se porta solo ese test.
12. **#104.** El scoring del control plane descarta `TrustQuarantined`, pero `internal/handlers/chat` no menciona ese estado. No reproduje el bypass de `getBestConnection`. ¿Se porta el cierre de #104 a main, o el scoring actual ya alcanza?
Evidencia local 2026-11-10, sin decisión: el bypass se reprodujo en `origin/main` `6e4f0c2e68400a7314b233a628286e761953ee19`; detalle en https://github.com/joselofarias-byte/capimux/pull/104#issuecomment-6767137995.
13. **#116 y #122.** Los dos dicen en el cuerpo que no se mergean: son builds temporales (#114+#115, y #115 sobre un main posterior). El protocolo Grok 1.0.45 de #115 **no** está en main (sigue `0.2.99` / `0.2.93`). ¿Se cierran estas dos builds y se conservan #114 y #115?
14. **#54, #57, #62, #68.** #54 es el baseline Fabric congelado en `bfa0e954`, 420 atrás y en conflicto. #180 fue una integración selectiva y dejó deuda upstream explícita en `docs/UPSTREAM-AUDIT-20261105.es.md`. El paquete `internal/controlplane/accountstrategy` de #57/#62/#68 no existe en main y el propio #57 dice que no se cablea hasta estabilizar #54. ¿Se retoma esa línea o se deja?
15. **#50 y #51.** #50 revalida cupo por cuenta y está en conflicto, 273 atrás. #51 (perfiles de prompt opt-in) está apilado encima y el paquete `internal/promptprofile` no existe en main. #168, ya mergeado, es otra función (inyección por capas), no estos perfiles. ¿Se rehacen sobre main o se abandonan?
16. **#128 y #129.** Docs de un día (plan Vercel y review ARGON-001), 210 atrás, sin conflicto de archivos. ¿Siguen vigentes después de #175 y #180, o quedan como acta?
17. **#133 y #136 (seguir en draft).** Falta la validación con la API real (URL, auth, modelos, streaming) y el OK antes de cualquier rollout. Están 7 commits atrás (#175 y #180).
18. **#107 es la única recomendación de cierre**, y también la ejecuta José Luis si la acepta: no se cierra desde este PR. Ver la evidencia en su fila.

## Licencias

Cadena de borradores, más el consolidado #176. #88 y #90 son hermanos: los dos salen de #86. #92 es un commit encima de #90. #176 dice haber mergeado la punta de #92 y, aparte, #88, porque #88 no estaba en esa punta. Todos los intermedios están `clean` contra su base inmediata y **251 commits atrás de main**. No se actualizan uno por uno: el vehículo contra main es #176, y #176 ya está al día.

| PR | Título | Estado | mergeable / estado | Atrás / adelante vs main | Vs su base (atrás / adelante) | Cobertura | Recomendación |
|---|---|---|---|---|---|---|---|
| [#70](https://github.com/joselofarias-byte/capimux/pull/70) | feat: harden multisystem entitlement lease contract | Borrador | true / clean | 251 / 5 | base `main`: 251 / 5 | Incluido en #176. No cerrar todavía. | Esperar decisión de José Luis |
| [#71](https://github.com/joselofarias-byte/capimux/pull/71) | feat: persist entitlement runtime state | Borrador | true / clean | 251 / 19 | #70: 0 / 14 | Incluido en #176. | Esperar decisión de José Luis |
| [#73](https://github.com/joselofarias-byte/capimux/pull/73) | feat: add public license activation and renewal client | Borrador | true / clean | 251 / 36 | #71: 0 / 17 | Incluido en #176. El cuerpo de #176 dice que el cliente se revisó (1 MiB, timeout, sin seguir redirects). | Esperar decisión de José Luis |
| [#78](https://github.com/joselofarias-byte/capimux/pull/78) | test: pin private Worker cross-language lease vector | Borrador | true / clean | 251 / 42 | #73: 0 / 6 | Incluido en #176. Test, sin clave de producción. | Esperar decisión de José Luis |
| [#82](https://github.com/joselofarias-byte/capimux/pull/82) | feat: expose Beta Pro license CLI and simple menu | Borrador | true / clean | 251 / 47 | #78: 0 / 5 | Incluido en #176. | Esperar decisión de José Luis |
| [#84](https://github.com/joselofarias-byte/capimux/pull/84) | feat: bind licenses to installation proof keys | Borrador | true / clean | 251 / 57 | #82: 0 / 10 | Incluido en #176. | Esperar decisión de José Luis |
| [#86](https://github.com/joselofarias-byte/capimux/pull/86) | refactor: establish public commercial edition boundary | Borrador | true / clean | 251 / 64 | #84: 0 / 7 | Incluido en #176. | Esperar decisión de José Luis |
| [#88](https://github.com/joselofarias-byte/capimux/pull/88) | feat: expose importable runtime for private product assembly | Borrador | true / clean | 251 / 68 | #86: 0 / 4 | Incluido en #176 como hermano de #90, no como ancestro de #92. | Esperar decisión de José Luis |
| [#90](https://github.com/joselofarias-byte/capimux/pull/90) | fix: harden licensed runtime trust and beta artifact gate | Borrador | true / clean | 251 / 86 | #86: 0 / 22 | Incluido en #176 (ancestro de #92, según el cuerpo de #176). | Esperar decisión de José Luis |
| [#92](https://github.com/joselofarias-byte/capimux/pull/92) | fix: fail closed on license proof-of-possession gaps | Borrador | true / clean | 251 / 87 | #90: 0 / 1 | Punta de la cadena. Incluido en #176. El «87 adelante de main» es la pila entera; el tramo propio es 1 commit. | Esperar decisión de José Luis |
| [#176](https://github.com/joselofarias-byte/capimux/pull/176) | feat(licencias): consolidar la cadena #70→#92 sobre main, sin URL por defecto (borrador) | Borrador | true / clean | 0 / 96 | base `main`: 0 / 96 | Es el consolidado. Head `1dc7532e9be8`. No está validado para merge: faltan la clave pública del emisor de producción y el límite Community/Pro. | Esperar decisión de José Luis |

## strictProxy, auditoría fase 1 y cifrado

| PR | Título | Estado | mergeable / estado | Atrás / adelante vs main | Vs su base | Cobertura | Recomendación |
|---|---|---|---|---|---|---|---|
| [#130](https://github.com/joselofarias-byte/capimux/pull/130) | fix(security): Capimux audit phase 1 safe fixes | Borrador | false / dirty | 38 / 22 | feature: 3 / 583 | No cerrar. Ver las cinco partes. El compare contra main son 12 archivos, solapados con #175. | Esperar decisión de José Luis |
| [#178](https://github.com/joselofarias-byte/capimux/pull/178) | fix(proxy): strictProxy en pruebas de conexión y relays edge | Borrador | true / clean | 0 / 2 | base `main`: 0 / 2 | Follow-up real de huecos que #175 no cubrió (probes y relays). Head `5282470e9a7e`. No está listo para merge: faltan (a), (b) y (c). | Esperar decisión de José Luis |
| [#141](https://github.com/joselofarias-byte/capimux/pull/141) | security(DB-02): staged AES-GCM field encryption and safe SQLite migration design | Borrador | true / clean | 7 / 16 | base `main`: 7 / 16 | Diseño y tests aislados. No integra el SQLite de producción. `docs/UPSTREAM-AUDIT-20261105.es.md` lo deja pendiente. Los 7 commits de atrás son #175 y #180. | Esperar decisión de José Luis |

### Las cinco partes pendientes de #130

Fuente en main: `docs/audit-phase1-pending.es.md` y el cuerpo de #175. Resumen, no un sustituto de ese doc.

| Parte | Qué es | Dónde quedó |
|---|---|---|
| (a) | #99 A4: el routing usa latencia, TTFT y éxito reales y eso cambia el orden de candidatos | Solo documentada. #175 pide decidirla con los datos shadow de #144. No es un fix de seguridad. |
| (b) | #100: test vivo de OpenCode cada 6 h | No portada. Sería tráfico real. Si se quiere, workflow manual, sin cron y sin secretos. |
| (c) | #103: arranque que falla si no cargan registry o cuentas, y explain-route que rechaza políticas inventadas | Portada detrás de `CAPIMUX_STRICT_CONTROLPLANE_INIT` y `CAPIMUX_EXPLAIN_ROUTE_STRICT_POLICY`, apagados por defecto. |
| (d) | #99 A7: montar `/api/admin/registry` y `/api/admin/explain-route` | explain-route está en main detrás de `RequireAdminAuth` (#142). `/api/admin/registry` no está montado, a propósito. |
| (e) | #98: snapshot que no pisa cuentas, y costo positivo de OrcaRouter siempre `paid` | Portada detrás de `CAPIMUX_DISCOVERY_PRESERVE_ACCOUNTS` y `CAPIMUX_DISCOVERY_STRICT_PRICING`, apagados por defecto. El refresh de ofertas ya estaba en main. |

Esos cuatro opt-in siguen apagados en main. #178 declara no haberlos tocado.

El PR #130 en GitHub cuenta 583 commits y 1049 archivos porque su base es la rama feature, de la que además diverge (3 atrás / 583 adelante de la punta `6a8b2d90`, 2026-10-16). Contra main el mismo head está 38 atrás / 22 adelante y el compare lista solo: `CHANGELOG.md`, `internal/db/proxyPools.go`, `proxyPools_refresh_test.go`, `repos_test.go`, `chat.go`, `connections_proxy.go`, `messages_endpoint_test.go`, `proxy_strict_test.go`, `oauth/claude.go`, `response_limit_test.go`, `oauth/standard.go`, `oauth/xai.go`. Esos caminos ya tienen versión en main por #175, y por eso el PR está `dirty`.

## OpenCode e inference

| PR | Título | Estado | mergeable / estado | Atrás / adelante vs main | Vs su base | Cobertura | Recomendación |
|---|---|---|---|---|---|---|---|
| [#131](https://github.com/joselofarias-byte/capimux/pull/131) | audit: OpenCode inference v2 compatibility and 403 handling | Borrador | true / clean | 0 / 5 | base `main`: 0 / 5 | Doc y tests. Head `693297b974b9`. El 403 ya está cubierto en main según el comentario del 2026-11-01. Siguen abiertos headers y `/inference/v1`. | Esperar decisión de José Luis |
| [#63](https://github.com/joselofarias-byte/capimux/pull/63) | fix: require authenticated OpenCode Zen API key | Borrador | false / dirty | 251 / 16 | base `main`: 251 / 16 | No está cubierto: main conserva `NoAuth` y `DefaultAPIKey: "public"` en `opencode` y `opencode-zen`, con `User-Agent` y `x-opencode-client` ya puestos. | Esperar decisión de José Luis |

## DeepSeek Web

#174 ya está en main. #177 es el seguimiento de sesiones Web y sigue en draft a propósito.

| PR | Título | Estado | mergeable / estado | Atrás / adelante vs main | Vs su base | Cobertura | Recomendación |
|---|---|---|---|---|---|---|---|
| [#177](https://github.com/joselofarias-byte/capimux/pull/177) | feat(deepseek): sesiones DeepSeek Web desde el panel (perfil apagado por defecto) | Borrador | true / clean | 0 / 3 | base `main`: 0 / 3 | No lo cubre #174: #174 es el perfil DSH→InferX; este agrega sesiones cifradas en el panel. Head `0d4465cc1681`. Sin integración DSH real el respaldo queda apagado en la práctica. | Esperar decisión de José Luis |

## Rutas free, auditoría Codex y la rama `feat/free-best-dynamic-routing`

#98 (A1–A3/A5) y #99 (A4/A6/A7) se mergearon el 2026-10-07 **en la rama feature**, que hoy está en `6a8b2d90` y 10 commits por delante de la base `183ea5ec` de #93–#97. Eso vuelve a esos cinco PR viejos respecto de su propia base (10 atrás) y no los mete en main. #142 y #175 llevaron a main otro recorte, no esta rama.

| PR | Título | Estado | mergeable / estado | Atrás / adelante vs main | Vs su base | Cobertura | Recomendación |
|---|---|---|---|---|---|---|---|
| [#50](https://github.com/joselofarias-byte/capimux/pull/50) | feat: revalidate free-route quota and availability per account | Borrador | false / dirty | 273 / 1 | base `main`: 273 / 1 | Incertidumbre. Main tiene free-best de #142 y flags de #175. No demostré que el delta entero de este PR ya esté. | Esperar decisión de José Luis |
| [#51](https://github.com/joselofarias-byte/capimux/pull/51) | feat: add opt-in OpenAI and Codex prompt profiles | Borrador | true / clean | 273 / 2 | #50: 0 / 1 | El paquete `internal/promptprofile` no está en main. #168 mergeado es inyección por capas, otro contrato. | Esperar decisión de José Luis |
| [#93](https://github.com/joselofarias-byte/capimux/pull/93) | Fix Codex audit A1/A2/A6: free pricing, cooldown fallback, strict proxy | Borrador | false / dirty | 609 / 9 | feature: 10 / 6 | Parcial. A6 (strict proxy inválido) está en main por #175, como fail-closed. A1 en main es opt-in, no siempre activo. A2: main ya salta cuentas en cooldown en `getBestConnection`; no comparé el patch. #98/#99 lo absorbieron en la rama feature. | Esperar decisión de José Luis |
| [#94](https://github.com/joselofarias-byte/capimux/pull/94) | Fix Codex audit A3: reconcile stale discovery offers | Borrador | false / dirty | 609 / 5 | feature: 10 / 2 | Parece cubierto, con incertidumbre. En main, un adapter que falla conserva la clasificación y un catálogo exitoso desactiva lo no visto. #98 metió A3 en la rama feature. No hay diff byte a byte. | Esperar decisión de José Luis |
| [#95](https://github.com/joselofarias-byte/capimux/pull/95) | Fix Codex audit A4: feed real outcomes into trust and routing | Borrador | false / dirty | 609 / 8 | feature: 10 / 5 | No está en main. #175 dejó (a) sin portar y la ató a #144. #99 lo mergeó solo en la rama feature. | Esperar decisión de José Luis |
| [#96](https://github.com/joselofarias-byte/capimux/pull/96) | Fix Codex audit A5: preserve newer account state across discovery activation | Borrador | true / unstable | 609 / 6 | feature: 10 / 3 | Parece cubierto en el mecanismo (`RefreshAccountsAfterSnapshot` desde `ffc0b49`, 2026-09-25, y el opt-in de #175). El test de carrera de este PR no está en main. CI `validate` en FAILURE. #98 metió A5 en la rama feature. | Esperar decisión de José Luis |
| [#97](https://github.com/joselofarias-byte/capimux/pull/97) | Fix Codex audit A7: mount Fabric admin endpoints | Borrador | false / dirty | 609 / 6 | feature: 10 / 3 | Parcial a propósito. explain-route está detrás de `RequireAdminAuth`. `/api/admin/registry` no se montó. Mergear #97 tal cual lo pondría bajo API key de motor, que #175 evitó. | Esperar decisión de José Luis |
| [#104](https://github.com/joselofarias-byte/capimux/pull/104) | Post-merge audit: prevent trust-quarantine bypass at connection selection | Listo para revisión | true / clean | 609 / 16 | feature: 5 / 8 | Incertidumbre. El scoring descarta cuentas en cuarentena; el selector de chat no nombra `TrustQuarantined`. No reproduje el bypass. | Esperar decisión de José Luis |
| [#105](https://github.com/joselofarias-byte/capimux/pull/105) | Post-merge audit: preserve real-account trust and cooldown in fusion panels | Listo para revisión | true / clean | 609 / 11 | feature: 4 / 2 | No está cubierto. En main, `makePanelCall` sigue usando `DefaultAPIKey` / `NoAuth` antes de buscar la cuenta configurada (`combo_fusion.go`). | Actualizar vs main |
| [#106](https://github.com/joselofarias-byte/capimux/pull/106) | Validar destino configurado y bloqueos 401/403/429 en fusion | Borrador | true / clean | 609 / 12 | #105: 0 / 1 | Solo un test encima de #105. No cambia producción. Viene con el port de #105. | Actualizar vs main |
| [#107](https://github.com/joselofarias-byte/capimux/pull/107) | Continuar fallback de cuentas ante errores de transporte y HTTP 500 | Listo para revisión | true / clean | 609 / 11 | feature: 4 / 2 | Cubierto en main por [#124](https://github.com/joselofarias-byte/capimux/pull/124), mergeado el 2026-10-09. El cuerpo de #124 dice que adapta el alcance y las pruebas de #107. El commit `0f54dedcd2c67fc6cbeeb395e20f317d465d457e` es ancestro de `6e4f0c2e`. `failure_policy.go` rota en HTTP 500 y error de transporte sin marcarlos como cupo, y existe `account_transient_fallback_test.go`. José Luis no pidió mantener #107 abierto. | Cerrar por obsoleto |

## Estrategias de cuenta y Antigravity

| PR | Título | Estado | mergeable / estado | Atrás / adelante vs main | Vs su base | Cobertura | Recomendación |
|---|---|---|---|---|---|---|---|
| [#57](https://github.com/joselofarias-byte/capimux/pull/57) | feat: add provider-agnostic account selection strategies | Borrador | true / clean | 258 / 6 | base `main`: 258 / 6 | El paquete no está en main. El PR dice que no cablea tráfico hasta estabilizar #54. | Esperar decisión de José Luis |
| [#62](https://github.com/joselofarias-byte/capimux/pull/62) | fix: mix weighted account hashes independently | Borrador | true / clean | 258 / 8 | #57: 0 / 2 | Hijo de #57. Mismo paquete, ausente en main. | Esperar decisión de José Luis |
| [#68](https://github.com/joselofarias-byte/capimux/pull/68) | feat: add expiry-pressure selection strategy | Borrador | true / clean | 258 / 12 | #62: 0 / 4 | Hijo de #62. Sin cableado de runtime, según su cuerpo. Trae un workflow `tmp_expiry_pressure_selector.yml`. | Esperar decisión de José Luis |
| [#65](https://github.com/joselofarias-byte/capimux/pull/65) | feat: auto-rotate Antigravity accounts | Borrador | true / clean | 251 / 5 | base `main`: 251 / 5 | No está el default. En main hay round-robin si la estrategia es explícita o si el fallback global no es `fill-first`. No hay auto-rotación solo por ser Antigravity con varias cuentas. | Actualizar vs main |
| [#67](https://github.com/joselofarias-byte/capimux/pull/67) | fix: keep Antigravity auto-rotation with provider metadata | Borrador | true / clean | 251 / 7 | #65: 0 / 2 | Hijo de #65, al día con esa base. El arreglo (metadata sin `rotateStrategy` no apaga la rotación) no aplica hasta que #65 exista en main. | Actualizar vs main |
| [#69](https://github.com/joselofarias-byte/capimux/pull/69) | feat: prepare Antigravity expiry-pressure quota signals | Borrador | true / clean | 251 / 11 | #67: 0 / 4 | Hijo de #67. Helpers sin cambiar el routing por defecto. Workflow `tmp_antigravity_expiry_runtime.yml`. Va con la pila de #65. | Actualizar vs main |

## Móvil, Android, OAuth y Grok CLI

| PR | Título | Estado | mergeable / estado | Atrás / adelante vs main | Vs su base | Cobertura | Recomendación |
|---|---|---|---|---|---|---|---|
| [#76](https://github.com/joselofarias-byte/capimux/pull/76) | fix: make mobile OAuth Add Connection responsive | Listo para revisión | false / dirty | 248 / 7 | base `main`: 248 / 7 | El fallo sigue en main: `openPKCEOAuth` en `ProviderDetailView.svelte` hace `await` de la API y del loopback de Codex y solo después pone `showOAuthModal` y llama a `window.open`. No es un PR de licencias. | Actualizar vs main |
| [#114](https://github.com/joselofarias-byte/capimux/pull/114) | feat(ui): mobile dashboard and encrypted Drive backups | Borrador | false / dirty | 218 / 61 | base `main`: 218 / 61 | No está cubierto entero. `TopBar.svelte` en main todavía tiene «Install App». #48 ya había traído backup cifrado a Drive; este PR reconecta UI móvil y comparte el `.9rbak`. Incertidumbre sobre cuánto de Drive ya está en las rutas actuales. | Actualizar vs main |
| [#115](https://github.com/joselofarias-byte/capimux/pull/115) | fix(grok): update Grok CLI protocol identity | Borrador | false / dirty | 218 / 10 | base `main`: 218 / 10 | No está en main. `internal/proxy/grokcli.go` sigue en `x-grok-client-version: 0.2.99` y el probe del dashboard en `0.2.93`. El PR pide seguir en draft hasta una prueba física. Actualizar no autoriza el merge. | Actualizar vs main |
| [#116](https://github.com/joselofarias-byte/capimux/pull/116) | test(android): PR114 + PR115 integration build | Borrador | false / dirty | 218 / 35 | base `main`: 218 / 35 | No es el PR definitivo: el cuerpo dice no mergearlo y que lo permanente sigue en #114 y #115. El artefacto de CI que cita ya se generó en su momento. Esas dos ramas siguen abiertas y en conflicto. | Esperar decisión de José Luis |
| [#122](https://github.com/joselofarias-byte/capimux/pull/122) | test(grok): combine PR115 protocol with merged import fixes for isolated Termux validation | Borrador | false / dirty | 214 / 7 | base `main`: 214 / 7 | Build temporal. El cuerpo dice no mergearlo y revisar #115 para el protocolo. Mismos 7 archivos de protocolo que #115. El protocolo no está en main. | Esperar decisión de José Luis |
| [#127](https://github.com/joselofarias-byte/capimux/pull/127) | Android: elegir modelos con buscador y casillas, sin reiniciar | Listo para revisión | false / dirty | 210 / 6 | base `main`: 210 / 6 | No está en main: no hay `ModelPicker` / `ModelSelection` bajo `android/`. | Actualizar vs main |

## Dashboard

| PR | Título | Estado | mergeable / estado | Atrás / adelante vs main | Vs su base | Cobertura | Recomendación |
|---|---|---|---|---|---|---|---|
| [#121](https://github.com/joselofarias-byte/capimux/pull/121) | feat(dashboard): panel Mis APIs con credenciales rápidas | Borrador | true / clean | 216 / 10 | base `main`: 216 / 10 | No está en main: no hay `QuickApiConnect` ni `sk-infer-` en `web/src`. `clean` con 216 atrás quiere decir «no hay conflicto», no «está al día». El cuerpo deja pendiente la prueba en Termux. | Actualizar vs main |

## Experimentos de proveedores

| PR | Título | Estado | mergeable / estado | Atrás / adelante vs main | Vs su base | Cobertura | Recomendación |
|---|---|---|---|---|---|---|---|
| [#133](https://github.com/joselofarias-byte/capimux/pull/133) | [Experimental] Evaluate Magnitude local inference integration | Borrador | true / clean | 7 / 13 | base `main`: 7 / 13 | Sigue siendo un spike. El cuerpo pide no mergear hasta URL real, tests que no se salteen y OK de despliegue. Los 7 de atrás son #175 y #180. | Esperar decisión de José Luis |
| [#136](https://github.com/joselofarias-byte/capimux/pull/136) | feat: Paper Instruments Ultramarine provider (WIP) | Borrador | true / clean | 7 / 12 | base `main`: 7 / 12 | Spike opt-in. El cuerpo lo deja en draft hasta confirmar endpoint, modelo y una conexión real autorizada. | Esperar decisión de José Luis |
| [#144](https://github.com/joselofarias-byte/capimux/pull/144) | experiment: opt-in adaptive shadow telemetry (DeepInfer-inspired) | Borrador | true / clean | 7 / 19 | base `main`: 7 / 19 | Sigue en draft por decisión del dueño. Toca `fallback.go`. No activa selección adaptativa. Relacionado con la parte (a) de #130. | Esperar decisión de José Luis |

## Integración upstream

| PR | Título | Estado | mergeable / estado | Atrás / adelante vs main | Vs su base | Cobertura | Recomendación |
|---|---|---|---|---|---|---|---|
| [#54](https://github.com/joselofarias-byte/capimux/pull/54) | sync(upstream): integrate Fabric onto frozen bfa0e954 baseline | Borrador | false / dirty | 420 / 45 | base `main`: 420 / 45 | Incertidumbre. No es el mismo trabajo que #180: aquel es selectivo y el doc de auditoría del 2026-11-05 deja pendientes de upstream (SSRF, catálogo, OAuth, UI). Este PR es un baseline congelado de octubre, 280 archivos en el diff del PR, en conflicto. No está demostrado que su delta Fabric entero ya esté en main, ni que convenga rehacerlo. | Esperar decisión de José Luis |

## Documentación

| PR | Título | Estado | mergeable / estado | Atrás / adelante vs main | Vs su base | Cobertura | Recomendación |
|---|---|---|---|---|---|---|---|
| [#179](https://github.com/joselofarias-byte/capimux/pull/179) | docs: actualizar README para identificar el fork Capimux de JolufaSoft | Listo para revisión | true / clean | 0 / 3 | base `main`: 0 / 3 | Vigente. Solo `README.md`. Head `578961eafe0d`. El merge espera el OK de José Luis. | Esperar decisión de José Luis |
| [#128](https://github.com/joselofarias-byte/capimux/pull/128) | Plan secure Capimux backend for Vercel frontend | Listo para revisión | true / clean | 210 / 1 | base `main`: 210 / 1 | Un markdown de arquitectura del 2026-10-14. `clean` y 210 atrás. No comprobé si el plan sigue siendo el de producción después de #180. | Esperar decisión de José Luis |
| [#129](https://github.com/joselofarias-byte/capimux/pull/129) | docs: ARGON-001 integration review and acceptance gates | Borrador | true / clean | 210 / 1 | base `main`: 210 / 1 | Acta de review, sin implementación. Misma base vieja que #128 (`e3a2de8b`). | Esperar decisión de José Luis |

## Hipótesis que se revalidaron

- `origin/main` es `6e4f0c2e68400a7314b233a628286e761953ee19`. Confirmado.
- #175 mergeado el 2026-11-03. El delta de #130 contra ese main cabe en 12 archivos que #175 ya tocó. #130 sigue abierto y en conflicto. Confirmado.
- #176 consolida la cadena y está 0 atrás / 96 adelante, CLEAN, draft, head `1dc7532e`. Confirmado.
- #178 está 0 / 2, CLEAN, draft, head `5282470e`. Confirmado.
- #177 está 0 / 3, CLEAN, draft, head `0d4465cc`. Confirmado.
- #179 no es draft, 0 / 3, CLEAN, head `578961ea`. Confirmado. El merge sigue esperando a José Luis.
- #131 está 0 / 5, CLEAN, draft, head `693297b`. Confirmado. Siguen los headers y `/inference/v1`.
- #141 y #144 siguen en draft, pero **no** están 0 atrás: ambos están 7 atrás (los commits de #175 y #180) y CLEAN.
- #133 y #136 también están 7 atrás y en draft.
- De los cierres que circulaban como hipótesis (#96, #107, #116, #122, #94, #97, #93, #130): solo #107 tiene un sucesor mergeado a main que lo nombra (#124). #130 no se cierra. El resto queda en «esperar decisión», con la evidencia de cada fila.

## Qué no hace este documento

No cierra #107 ni ningún otro PR. No marca ready #176, #177, #178, #131, #141 ni #144. No aprueba el merge de #179. No reejecuta CI. Los `mergeable_state` y los ahead/behind de las tablas de arriba son los que devolvió la API de GitHub el 2026-11-08 contra el SHA de main citado al principio; la repetición para la lista «Actualizar vs main» está en la sección siguiente.

## Estado al 2026-11-09 tras las actualizaciones

Remedición de la lista «Actualizar vs main», ya recorrida. No cierra, no mergea, no retargetea y no cambia draft ni ready. No es una aprobación de José Luis Farías.

Reloj de la medición: **2026-11-10T00:16:00Z** (2026-11-09 21:16 en America/Montevideo). Tip de `origin/main` en ese momento, otra vez tras `git fetch origin main`: **`6e4f0c2e68400a7314b233a628286e761953ee19`**. No se movió respecto del corte del 2026-11-08. Este PR (#181) está 0 atrás y 1 adelante de ese tip; no hizo falta mergear `main` en esta rama.

Cómo se midió, por PR:

1. `git fetch` de la rama y `git rev-list --left-right --count origin/main...<head>`.
2. `gh pr view`: `isDraft`, `mergeable`, `mergeStateStatus`, `baseRefName`, `headRefOid`. `MERGEABLE` / `CLEAN` se anotan como `mergeable=true` y `mergeable_state=clean`. `CONFLICTING` / `DIRTY` como `false` / `dirty`.
3. Check-runs del SHA del head (`GET /commits/{sha}/check-runs`) y, para saber el evento, `GET /actions/runs?head_sha=`. Solo figuran checks que existen. Un check que no apareció no está verde.
4. `ci.yml` dispara `pull_request` solo hacia `main`, `sync/upstream-2026-10-01` y `agent/criptouy/upstream-reconcile-20261001`. Una pila cuya base no es `main` no corre ese CI.
5. «Qué falta» sale de comentarios y de este triage. No se reejecutó `go test` ni `bun test` de esos PR en esta pasada. La corrida local posterior de #67 y #69 está en el párrafo que sigue a la tabla.

`linux-binaries` queda `skipped` cuando el workflow sí arranca y la rama no empieza por `integration/capimux-`. Eso es un check-run real, no un success. `multiagent` y `apk` solo entran si el diff toca sus paths. Si no aparecieron, se dice.

En un PR apilado, «adelante vs main» incluye los commits de los padres. «Vs su base» es el tramo propio. #105 y #106 no se cerraron ni se retargetearon. El port contra `main` es #183.

| PR | Draft | Head | Merge que trae `6e4f0c2` | Atrás / adelante vs main | Vs su base | mergeable / estado | CI del head (check-runs) | Qué falta para mergear |
|---|---|---|---|---|---|---|---|---|
| [#65](https://github.com/joselofarias-byte/capimux/pull/65) | true | `d43cbb56c3bf` | el mismo SHA (padres `697fa86c7020` y `6e4f0c2e6840`) | 0 / 6 | base `main`: 0 / 6 | true / clean. Sin conflictos | `pull_request` contra `main`, run 42539502781, success: `test`, `Go race detector`, `Integration tests`, `Build Docker image (verify only)`. `linux-binaries` completed+skipped (run 42539502807). `Vercel Preview Comments` completed+success. `multiagent` y `apk` no aparecieron | Sigue en draft. El comentario del dueño del 2026-10-05 deja HOLD detrás de #54 y #50 (los dos siguen OPEN) y pide separar o justificar el fix de `tool_use.id` en `gemini.go`, que sigue en el diff contra main (cuatro archivos). No hay un comentario posterior que levante ese HOLD |
| [#67](https://github.com/joselofarias-byte/capimux/pull/67) | true | `7f18a368b2e2` | no hay merge directo de `main`. El tip mergea el padre #65 (`d43cbb56c3bf`, padres `2125202579ec` y `d43cbb56c3bf`). `main` entra por ese ancestro | 0 / 9 | base `feature/antigravity-auto-rotation-20261004`: 0 / 3 | true / clean contra esa base. Un `git merge-tree --write-tree origin/main` local, sin commit, sale exit 0. El campo de GitHub no habla de un PR contra `main` | No corrió `pull_request` contra `main`: `actions/runs?head_sha=` devuelve 0 runs. El único check-run es `Vercel Preview Comments` completed+success. `test`, `Go race detector`, `Integration tests` y `Build Docker image` no aparecieron. No están verdes | Sigue en draft, hijo de #65, base distinta de `main`. Mismo HOLD del 2026-10-05. El CI de Go de este SHA no existe en Actions |
| [#69](https://github.com/joselofarias-byte/capimux/pull/69) | true | `85c3c70fbc8d` | no hay merge directo de `main`. El tip mergea #67 (padres `64fa77856419` y `7f18a368b2e2`). `main` entra por el ancestro `d43cbb56c3bf` | 0 / 14 | base `fix/antigravity-provider-strategy-gaps-20261005`: 0 / 5 | true / clean contra esa base. `git merge-tree --write-tree origin/main` local sale exit 0 | No corrió `pull_request` contra `main`. Check-runs: `runtime` completed+success (workflow «Temporary Antigravity expiry runtime tests», evento `push`, run 42571767924) y `Vercel Preview Comments` completed+success. `test`, `Go race detector`, `Integration tests` y `Build Docker image` no aparecieron | Sigue en draft. El comentario del update (2026-11-09) dice que la presión de caducidad no está cableada al routing y que el merge necesita el OK del dueño. El delta contra #67 sigue siendo solo `antigravity_expiry_pressure.go` y `tmp_antigravity_expiry_runtime.yml`: el pedido del 2026-10-05 de tests deterministas de los helpers no está en ese diff. Mismo HOLD detrás de #54 y #50 |
| [#76](https://github.com/joselofarias-byte/capimux/pull/76) | false | `3992372984f7` | el mismo SHA (padres `0a5b3170ce8d` y `6e4f0c2e6840`) | 0 / 8 | base `main`: 0 / 8 | true / clean. Sin conflictos | `pull_request` contra `main`: `test`, `Go race detector`, `Integration tests`, `Build Docker image (verify only)` y `multiagent` completed+success. `linux-binaries` completed+skipped. `Vercel Preview Comments` completed+success. `apk` no apareció | Listo para revisión. El comentario del 2026-10-05 pide un retest en el teléfono (la pestaña quedó en `about:blank`). El update del 2026-11-09 deja el merge al OK de José Luis después de ese retest. No hay comentario posterior que lo dé |
| [#105](https://github.com/joselofarias-byte/capimux/pull/105) | false | `2f619402b509` | ninguno contra este `main` | 609 / 11 | base `feat/free-best-dynamic-routing`: 4 / 2 | true / clean contra esa base, que no es `main`. `git merge-tree --write-tree origin/main` local sale exit 1 (hay conflictos). No se mergeó | El único check-run es `validate` completed+success (2026-10-07, run 37613098271). El evento es `pull_request`, pero el PR asociado tiene base `feat/free-best-dynamic-routing`, no `main`. Los jobs actuales de `ci.yml` no aparecieron | Sigue abierto y no se tocó. El port contra `main` es #183. El comentario del 2026-11-01 pedía esa versión y no autorizaba cerrarlo |
| [#106](https://github.com/joselofarias-byte/capimux/pull/106) | true | `821eb2bd6031` | ninguno contra este `main` | 609 / 12 | base #105 (`fix/postmerge-audit-fusion-account-feedback-20261007`): 0 / 1 | true / clean contra #105. `git merge-tree --write-tree origin/main` local sale exit 1. No se mergeó | Igual que #105: solo `validate` completed+success (2026-10-07, run 37627548593), `pull_request` contra la rama de #105, no contra `main`. Los jobs actuales de `ci.yml` no aparecieron | Sigue abierto, draft, apilado sobre #105. No se tocó. El test viaja en #183 |
| [#183](https://github.com/joselofarias-byte/capimux/pull/183) | true | `fae5f0d761a2` | no hay merge commit: un commit encima de `6e4f0c2e6840` (`fix(fusion): bind panel cooldown to the configured account`) | 0 / 1 | base `main`: 0 / 1 | true / clean. Sin conflictos | `pull_request` contra `main`, run 42524178195, success: `test`, `Go race detector`, `Integration tests`, `Build Docker image (verify only)`. `linux-binaries` completed+skipped (run 42524178181). `Vercel Preview Comments` completed+success. `multiagent` y `apk` no aparecieron | Sigue en draft. El cuerpo no porta `RecordRequestOutcome` / `TrustQuarantined` porque no están en `main`, y lo marca como comportamiento que no se inventa. Falta el OK del dueño para salir de draft. #105 y #106 siguen abiertos |
| [#114](https://github.com/joselofarias-byte/capimux/pull/114) | true | `471240e7bb51` | el mismo SHA (padres `43ad964e3560` y `6e4f0c2e6840`) | 0 / 62 | base `main`: 0 / 62 | true / clean. Sin conflictos en el merge (el comentario del 2026-11-09 resolvió dos archivos y los nombra) | `pull_request` contra `main`: `test`, `Go race detector`, `Integration tests`, `Build Docker image (verify only)` y `multiagent` completed+success. `linux-binaries` completed+skipped. `Vercel Preview Comments` completed+success. `apk` no apareció | Sigue en draft. El comentario del 2026-11-09 deja pendiente la prueba física en Android del `.9rbak`, la decisión sobre el retiro de la PWA, los textos de free-tier de octubre de 2026, y el OK de merge. El OAuth directo de Drive solo si el dueño quiere configurar el client id |
| [#115](https://github.com/joselofarias-byte/capimux/pull/115) | true | `5057edba1581` | el mismo SHA (padres `17339561e073` y `6e4f0c2e6840`) | 0 / 11 | base `main`: 0 / 11 | true / clean. Sin conflictos | `pull_request` contra `main`, run 42643713930, success: `test`, `Go race detector`, `Integration tests`, `Build Docker image (verify only)`. `linux-binaries` completed+skipped. `Vercel Preview Comments` completed+success. `multiagent` y `apk` no aparecieron (el diff contra main son 7 archivos de identidad Grok; no entran en esos paths) | Sigue en draft. El comentario del 2026-11-09 pide prueba física con una cuenta Grok autorizada, confirmar si `1.0.45` sigue vigente, y el OK de merge. Además, `internal/handlers/chat/live_catalog.go` en este head sigue en `grokCLIVersion = "0.2.99"` y no está en el diff de tres puntos contra main: la unificación pedida el 2026-11-02 no está en este head |
| [#121](https://github.com/joselofarias-byte/capimux/pull/121) | true | `67b85aa2bdfa` | `74dfdd2a24bd` (padres `2596bd117fd3` y `6e4f0c2e6840`). El tip es un commit posterior: `fix(test): present an admin session when minting inference keys` | 0 / 12 | base `main`: 0 / 12 | true / clean. Sin conflictos | `pull_request` contra `main`: `test`, `Go race detector`, `Integration tests`, `Build Docker image (verify only)`, `multiagent` y `Vercel Preview Comments` completed+success. `linux-binaries` no apareció como check-run. El workflow run 42672568184 terminó `skipped` con 0 jobs (la rama no empieza por `integration/capimux-`). No es un success. `apk` no apareció | Sigue en draft. El comentario del 2026-11-02 pregunta si las claves legacy `sk-` pierden el acceso al panel; no hay respuesta. El del 2026-10-08 deja la prueba física en Termux fuera de este CI. El del 2026-11-09 pide mirar el auto-merge de `apikeys.go` y `dashboard_auth.go` y no aprueba el merge |
| [#127](https://github.com/joselofarias-byte/capimux/pull/127) | false | `7cf2579ef12a` | el mismo SHA (padres `233df81ca06a` y `6e4f0c2e6840`) | 0 / 7 | base `main`: 0 / 7 | true / clean. Sin conflictos | `pull_request` contra `main`: `test`, `Go race detector`, `Integration tests`, `Build Docker image (verify only)`, `multiagent` y `apk` completed+success. `linux-binaries` completed+skipped. `Vercel Preview Comments` completed+success | Listo para revisión. El comentario del 2026-11-09 deja tres preguntas sin respuesta: si el tope Android pasa de 8 a 10, si `versionCode` 2 / `0.1.1` es el salto correcto, y si la web queda como en `main` (sin la autoselección de la primera carga). El OK de merge no está |

El 2026-11-10 se corrieron en local, en checkouts detached y sin rebase, las cabezas de [#67](https://github.com/joselofarias-byte/capimux/pull/67) `7f18a368b2e2fad305cfa4c0ad4307062c9fc47f` y [#69](https://github.com/joselofarias-byte/capimux/pull/69) `85c3c70fbc8d4f5e676bad4be63ff28d08d59a22`: con `go1.27.3` y `GOTOOLCHAIN=local`, `go test -count=1 ./...`, `go test -count=1 -race ./...` y `go test -count=1 -tags=integration ./internal/integration/...` salieron exit 0 en las dos. El listado `-v` deja `TestOpenCodeBinaryEmptyFallback` en SKIP (sin `NINE_ROUTER_TEST_OPENCODE_BIN`), no se corrió `live_e2e`, y `TestTimeoutReportsTimeout` no falló en estas pasadas, así que no se reprodujo en `origin/main` `6e4f0c2e6840`. Detalle en los comentarios de [#67](https://github.com/joselofarias-byte/capimux/pull/67#issuecomment-6757910038) y [#69](https://github.com/joselofarias-byte/capimux/pull/69#issuecomment-6757910945). No es un OK de merge.

### Decisiones que siguen en José Luis

Ninguna está tomada por esta medición. El orden es el de lo que ya se puede contestar sin más ingeniería, y después lo que sigue atado a una prueba o a un diseño.

1. **#179, OK de merge.** Sigue abierto, `draft=false`, base `main`, head `578961eafe0d`, 0 atrás / 3 adelante, `mergeable=true` / `clean`. El merge de `main` en la rama es `578961eafe0d` (padres `ed8d58e4cd66` y `6e4f0c2e6840`). Check-runs de ese head: `test`, `Go race detector`, `Integration tests`, `Build Docker image (verify only)` y `Vercel Preview Comments`, todos completed+success. `linux-binaries` no apareció. El comentario del 2026-11-08 dice que no quedó una decisión de producto y que el merge espera el OK explícito. Este documento no lo da.
2. **#107, cierre.** Sigue abierto, `draft=false`, base `feat/free-best-dynamic-routing`, head `c1d2845a7886` (sin merge de este `main`), 609 atrás / 11 adelante de `origin/main`, 4 atrás / 2 adelante de su base, `mergeable=true` / `clean` contra esa base. El único check-run del head es `validate` completed+success (2026-10-07); no es CI de `pull_request` contra `main`. #124 está MERGED (2026-10-09). El comentario del dueño del 2026-11-01 sugiere cerrar #107 como cubierto y dice que no lo toca hasta que el propietario decida. No hay un comentario posterior. No se cierra desde aquí.
3. **#127, tres preguntas y el OK de merge.** Siguen sin respuesta en los comentarios. Ver la fila. El PR ya está listo para revisión y el CI del head, incluido `apk`, está en success.
4. **#76, retest en el teléfono y el OK de merge.** El fallo de `about:blank` está en el comentario del 2026-10-05. El update del 2026-11-09 no lo sustituye. El PR ya no es draft.
5. **#183, salir de draft.** El port está 0 atrás, clean, con el CI de `pull_request` contra `main` en success. Falta el OK. El cuerpo deja afuera la cuarentena en memoria. #105 y #106 siguen abiertos, con los heads de la tabla, y no se cierran desde aquí.
6. **#115, prueba física y versión.** Cuenta Grok autorizada, si `1.0.45` sigue vigente, y `live_catalog.go` sigue en `0.2.99` fuera del diff. Sigue en draft.
7. **#114, prueba física y la PWA.** Compartir y restaurar el `.9rbak` en Android, si el retiro de la PWA se queda, y los textos de free-tier de octubre de 2026. Sigue en draft. El OK de merge no está.
8. **#121, claves legacy y Termux.** Si las claves `sk-` que no son `sk-infer-` pierden el acceso al panel (pregunta del 2026-11-02, sin respuesta) y la prueba física en Termux (comentario del 2026-10-08). Sigue en draft. El CI del head está en success; eso no es la prueba en el teléfono.
9. **#65, #67 y #69, el HOLD del 2026-10-05.** #54 y #50 siguen OPEN. No hay un comentario posterior que levante el HOLD ni que separe `gemini.go`. #69 no cablea la presión de caducidad. #67 y #69 no tienen el CI de `pull_request` contra `main`. Los tres siguen en draft.
10. **#130, no cerrar.** Sigue abierto, `draft=true`, base `feat/free-best-dynamic-routing`, head `5c988f867eac`, 38 atrás / 22 adelante de `origin/main`, 3 atrás / 583 adelante de su base, `mergeable=false` / `dirty`. `docs/audit-phase1-pending.es.md` sigue en `origin/main`. Las cinco partes de esa nota, y de la sección de #130 de este triage, siguen sin una decisión del dueño: encender o no los cuatro opt-in, si (a) espera a #144, si algún día se monta `/api/admin/registry` detrás de `RequireAdminAuth`, y si (b) existe como workflow manual. #175 está MERGED (2026-11-03) y no cierra este PR. El comentario del 2026-11-01 lo deja abierto a propósito.
11. **#63, la ruta anónima.** Sigue abierto, `draft=true`, base `main`, head `5f83c3975790` (sin cambios, sin merge de este `main`), 251 atrás / 16 adelante, `mergeable=false` / `dirty`. En `origin/main`, `internal/providers/providers.go` sigue con `opencode` y `opencode-zen` en `NoAuth: true` y `DefaultAPIKey: "public"`. Hay que decidir si esa ruta se quita o se mantiene mientras #131 no cierra headers y `/inference/v1`. El comentario del 2026-10-05 además deja el merge detrás de una inferencia autenticada con cuenta propia. No se actualizó la rama en esta pasada. Un `git merge-tree --write-tree origin/main` local sale exit 1, con conflicto de contenido en `internal/proxy/executor/opencode_test.go`.
12. **#176, licencias.** Sigue `draft=true`, head `1dc7532e9be8`, 0 atrás / 96 adelante, `mergeable=true` / `clean`. El comentario del 2026-11-08 deja abiertas la clave pública del emisor de producción, la frontera Community/Pro, la URL de producción, si la activación exige `--stdin`, y los caveats de keyring, cableado Pro y backup de `DATA_DIR`. No se recontaron los check-runs de este head en esta pasada.
13. **#177, DeepSeek Web.** Sigue `draft=true`, head `0d4465cc1681`, 0 atrás / 3 adelante, `mergeable=true` / `clean`. El cuerpo sigue pidiendo el esquema de campos de la sesión DSH, la autorización de un smoke en un entorno real, y la custodia de la clave (archivo local, clave externa, o unificar con #141). El comprobador por defecto no contacta a DSH. No se recontaron los check-runs de este head en esta pasada.
14. **#178, strictProxy.** Sigue `draft=true`, head `5282470e9a7e`, 0 atrás / 2 adelante, `mergeable=true` / `clean`. El cuerpo deja fuera de alcance, para decidir aparte, los media handlers (`xquik_search.go`, `antigravity_search.go`, `media.go`) que reintentan en directo sin mirar `strictProxy`, y `ClientForUpstream` en loopback, que no pasa por el proxy HTTP. El cliente de relay estricto que solo acepta los hosts del relay ya está escrito en el borrador. El cuerpo dice que el merge necesita el OK. No se recontaron los check-runs de este head en esta pasada.
15. **#131, headers y `/inference/v1`.** Sigue `draft=true`, head `693297b974b9`, 0 atrás / 5 adelante, `mergeable=true` / `clean`. El comentario del dueño del 2026-11-01 descarta el executor (ya está en `main`, `/zen/v1` y `/zen/go/v1`) y deja pendiente la migración a `/inference/v1`, los docs oficiales, el endpoint versionado, y la decisión sobre los headers que imitan al cliente OpenCode. No se recontaron los check-runs de este head en esta pasada.
16. **#141, custodia de la clave.** Sigue `draft=true`, head `484096eb2ea4`, 7 atrás / 16 adelante de `6e4f0c2e6840` (`main` no es ancestro: el último merge de `main` en la rama es contra `ec94e1ee`). `mergeable=true` / `clean` contra su base `main`, con esos 7 commits de atraso. Falta el diseño de custodia antes de cablear lectura, escritura y la migración. El cifrado del PR no está integrado al SQLite de producción. No se recontaron los check-runs de este head en esta pasada.
17. **#144, telemetría shadow.** Sigue `draft=true`, head `1f63ce51b597`, 7 atrás / 19 adelante, `main` tampoco es ancestro. `mergeable=true` / `clean`. El cuerpo sigue con las cuatro condiciones sin marcar: CI y race, streaming y retry de autenticación, observabilidad y cardinalidad, y la aprobación explícita para integrar. No hay merge automático. También es el insumo que la parte (a) de #130 dejó para decidir. No se recontaron los check-runs de este head en esta pasada.

### Qué no se midió en esta pasada

- No se reejecutó `go test`, `go vet`, `bun test` ni `bun run build` del resto de la tabla. La excepción es la corrida local de #67 y #69 del párrafo que sigue a la tabla (`go test`, `-race`, `-tags=integration`, y `bun run build` solo para generar `web/dist`). Los demás exit codes citados arriba son de comentarios ajenos, no de esta medición.
- No se repitieron los check-runs de #176, #177, #178, #131, #141 ni #144. De esos seis solo se recontaron head, draft, ahead/behind y `mergeable`.
- No se hizo una prueba física en teléfono, Termux ni contra xAI, OpenCode, Drive o DSH. Este entorno no instala APKs ni toca el teléfono.
- El `git merge-tree` local no crea commit y no es el `mergeable` de GitHub. Se usó solo para no leer un `clean` de #105, #106, #67 y #69 como si ya fuera un merge contra `main`.

