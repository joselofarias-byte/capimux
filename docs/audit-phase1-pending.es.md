# Auditoría fase 1 (#130): alcance de las cinco partes pendientes

Rama: `fix/audit-phase1-main`, creada desde `origin/main`. El PR #130 sigue abierto y con su base sin cambios (`feat/free-best-dynamic-routing`). Esta rama trae a `main` todos los fixes de seguridad de #130 que faltaban, sin el linaje free-best.

## Fixes portados de #130 (activos, sin flag)

Estos fixes son la diferencia entre `origin/main` y `5c988f86` en código y tests:

- **OAuth.** El refresh de Claude, xAI y el flujo estándar corta en 1 MiB (`internal/proxy/oauth/{claude,xai,standard}.go`). Test: `response_limit_test.go`.
- **Endpoint de uso.** `/v1/messages` de modelo único registra el endpoint `/v1/messages` y ya no `/v1/v1/messages` (`chat.go`). Test: `messages_endpoint_test.go`. El comentario viejo de `fallback.go` quedó corregido. Se sigue aceptando el valor duplicado por compatibilidad.
- **`strictProxy` fail-closed** (`connections_proxy.go`). Cubre pool inexistente, inactivo o sin URL, URL inválida y tipo de proxy desconocido. Sin `strictProxy`, el comportamiento no cambia. Test: `proxy_strict_test.go`.
- **Caché de pools.** El caché se refresca si la fila cambió y el round-robin arranca por la primera URL (`internal/db/proxyPools.go`). Tests: `proxyPools_refresh_test.go`, `repos_test.go`.
- **Updater** (commit `6f016fe9` de #130). Ya estaba cubierto en `main` por `fork_defaults.go` (#152) y el manifiesto firmado (#164). No hizo falta portarlo.

## Las cinco partes

| Parte | Qué hace | Archivos (#130) | Cambio de comportamiento | Riesgo | Solapamiento | ¿Seguridad? | Decisión en esta rama |
|---|---|---|---|---|---|---|---|
| (a) #99 A4: routing adaptativo con resultados reales | Registra en `globalTrustManager` latencia, TTFT y éxito de cada request, y `SelectCandidates` los usa como factores de scoring | `routing/policy.go`, `trust/state.go`, `chat/fallback.go` | Sí: cambia el orden de candidatos en producción | Medio: el orden se mueve según el tráfico, y puede castigar cuentas por errores transitorios | #144 (telemetría shadow en `internal/adaptive`) mide sin decidir. Las dos partes tocan `fallback.go` | No (calidad de routing) | Solo documentada. Conviene decidirla después de #144, con datos shadow |
| (b) #100: `provider-live.yml` | Corre un test vivo de OpenCode Muse Spark cada 6 h (`RUN_LIVE_OPENCODE=1`) | `.github/workflows/provider-live.yml`, `muse_spark_e2e_test.go` | Agrega llamadas vivas a un proveedor desde CI | Alto: tráfico real y cron, con posibles costos o bloqueos | Ninguno | No | **No portada**, solo documentada. Si se quiere, sería manual (`workflow_dispatch`), sin cron y sin secretos |
| (c) #103: registry al arranque y explain-route | Al iniciar sincroniza las cuentas desde la base y falla si el registry o las cuentas no cargan. Además, explain-route rechaza políticas inventadas | `cmd/9router-go/main.go` (en main, `internal/app/server.go`), `chat/admin_api.go` | Sí en los dos casos (arranque y respuesta 400), por eso los dos van con opt-in | Bajo con los opt-in apagados | #142 (ya mergeado en `main`) monta explain-route detrás de `RequireAdminAuth` pero no valida la política | Sí (estado de cuentas viejo visible y validación de entrada) | **Portada.** `CAPIMUX_STRICT_CONTROLPLANE_INIT=1` activa el arranque fail-closed y `CAPIMUX_EXPLAIN_ROUTE_STRICT_POLICY=1` activa la validación de política. Los dos están apagados por defecto |
| (d) #99 A7: rutas admin de Fabric | Monta `/api/admin/registry` y `/api/admin/explain-route` dentro de `RequireApiKey` | `handlers/router.go`, `admin_api.go` | Sí: suma endpoints | Medio: con una API key de motor se leería el registry | #142 (mergeado en `main` como `f42af206`) monta explain-route y free-best detrás de `RequireAdminAuth`, que es más estricto, y no monta `/api/admin/registry` | Sí (superficie admin) | **No portada a propósito**: #142 la cubre con auth de admin. Si después se quiere `/api/admin/registry`, tiene que ir detrás de `RequireAdminAuth` |
| (e) #98: `ActivateSnapshotPreservingAccounts` y refresh de ofertas | Publica el snapshot de discovery conservando las cuentas en memoria. Refresca precio, capacidades y estado de cada oferta y retira las no vistas. Hace autoritativo el costo positivo en OrcaRouter | `registry/snapshot.go`, `discovery/orchestrator.go`, `discovery/orcarouter.go` | Sí: por eso va con opt-in | Bajo con los flags apagados | `main` ya tiene `applyDiscoveredModel`, `deactivateUnseenModels` y `RefreshAccountsAfterSnapshot` (cuentas re-sincronizadas después de activar) | Sí (una cuenta deshabilitada visible entre la activación y el refresh, y una oferta paga clasificada como gratis) | **Portada.** `CAPIMUX_DISCOVERY_PRESERVE_ACCOUNTS=1` usa `ActivateSnapshotPreservingAccounts`. `CAPIMUX_DISCOVERY_STRICT_PRICING=1` hace que un costo positivo sea siempre `paid`. Las dos están apagadas por defecto. El refresh de ofertas ya estaba en `main` |

## Flags nuevos (todos apagados por defecto)

Valores que los activan: `1`, `true`, `yes`, `on`. Cualquier otro valor, o la variable sin definir, los deja apagados.

- `CAPIMUX_EXPLAIN_ROUTE_STRICT_POLICY`: `/api/admin/explain-route` responde 400 si la política no es `balanced`, `free-only`, `free-first` ni `trusted-only`.
- `CAPIMUX_STRICT_CONTROLPLANE_INIT`: si no carga el registry o la sincronización de cuentas, el arranque falla.
- `CAPIMUX_DISCOVERY_PRESERVE_ACCOUNTS`: el snapshot de discovery no pisa las cuentas en memoria.
- `CAPIMUX_DISCOVERY_STRICT_PRICING`: un costo positivo en el catálogo de OrcaRouter se clasifica como `paid` aunque el modo o una heurística digan que es gratis.

## Tests nuevos

- `internal/app/controlplane_init_test.go`: modo por defecto (solo warning y sin sync extra), y modo estricto (falla por registry, falla sin cuentas, y caso OK).
- `internal/controlplane/registry/preserve_accounts_test.go`: una cuenta deshabilitada se conserva. Regresión: `ActivateSnapshot` sin cambios. Se verifica que el mapa de cuentas no se comparte.
- `internal/controlplane/discovery/strict_pricing_test.go`: con el flag, un costo positivo da `paid` y lo gratis sigue gratis. Regresión sin el flag. Parseo de flags.
- `internal/handlers/chat/admin_api_test.go`: con el flag, rechazo de políticas inventadas (negativo) y aceptación de las válidas y la vacía. Regresión sin el flag: sigue respondiendo 200.
