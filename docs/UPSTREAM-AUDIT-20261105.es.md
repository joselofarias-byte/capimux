# Auditoría de integración — Capimux / JolufaSoft

Fecha: 2026-11-05. Esta entrega es **una integración selectiva en borrador**, no la certificación de todas las funciones ni de todos los cambios upstream. No se modifica `main` ni se fusionan PR existentes.

## Estado comprobado

- Repositorio exacto: [joselofarias-byte/capimux](https://github.com/joselofarias-byte/capimux), público, rama predeterminada `main`.
- GitHub declara como padre [luqman-v1/9router-go](https://github.com/luqman-v1/9router-go). [decolua/9router](https://github.com/decolua/9router) es la referencia funcional JS indicada por el proyecto, no el padre Git Go.
- `main`: `adfdbcbe929db41ff62a76d8bbe6173f969e51a7`.
- Upstream examinado: `6919a289cfd214d7e587635a9bde58c8ac0fd39b`.
- Merge-base tras profundizar historia: `dc702150ad70c3bf7b02fde9149ce81159cf9736` (v1.9.6).
- `git rev-list --left-right --count origin/main...upstream/main`: **418 / 611**. No equivalen a 611 parches aplicables; incluyen merges y cambios incompatibles.
- Hay 122 archivos modificados por ambos lados desde la base. No se hace merge global.
- Issues deshabilitados en el fork: no existe cola consultable mediante `gh issue list`. Cero releases publicadas en la API del fork en esta auditoría; tags heredados de upstream no son releases de Capimux.
- Se consultaron 155 ramas y 48 PR abiertos, workflows y últimas ejecuciones con GitHub CLI. Los últimos cinco runs observados fueron `Upstream provider sync`, exitosos (el más reciente: [42073927310](https://github.com/joselofarias-byte/capimux/actions/runs/42073927310)). Eso **no prueba CI de esta entrega**.
- Workflows presentes: CI, Android APK, Android ARM64 failover, validación multiagente, provider sync y release. La lista remota también conserva workflows históricos sin archivo actual; no se eliminan ni ejecutan.

## PR solicitados y trabajo concurrente

| PR | Estado real | Decisión |
|---|---|---|
| [#173](https://github.com/joselofarias-byte/capimux/pull/173) | Fusionado 2026-11-01 | Corrección de fecha de pruebas ya presente; no duplicar. |
| [#174](https://github.com/joselofarias-byte/capimux/pull/174) | Fusionado 2026-11-01 | Perfil DeepSeek opt-in ya presente; preservar. |
| [#142](https://github.com/joselofarias-byte/capimux/pull/142) | Fusionado 2026-11-01 | Restricción free-best ya presente; no ampliar rutas elegibles. |
| [#143](https://github.com/joselofarias-byte/capimux/pull/143) | Fusionado 2026-11-01 | Estadísticas por solicitud ya presentes; reutilizar. |
| [#141](https://github.com/joselofarias-byte/capimux/pull/141) | Abierto, borrador | Diseño AES-GCM sin integración de lectura/escritura ni migración. Pendiente; no declarar DB cifrada. |
| [#130](https://github.com/joselofarias-byte/capimux/pull/130) | Abierto, borrador; base `feat/free-best-dynamic-routing` | Diff histórico enorme; fixes de auditoría ampliamente continuados por #175 ya fusionado. No cerrar ni incorporar íntegro; pendiente cotejo residual. |

Se respeta el trabajo abierto #178 (strictProxy probes/relays), #179 (README/identidad), #177 (sesiones DeepSeek) y #176 (licencias): no se toman sus ramas ni se fusionan. Los experimentos #144/#136/#133 y la auditoría OpenCode #131 no se reintroducen por inferencia de compatibilidad. El resto de PR históricos, incluidos los dirigidos a ramas antiguas, no se incorpora automáticamente.

## Clasificación preliminar de upstream

Ejemplos concretos examinados en la historia; **no es revisión exhaustiva de 611 commits**:

| Categoría | Commit / superficie | Decisión |
|---|---|---|
| Seguridad | `dcd3d342020521304c1a7319efa1fd43ee25a19a` (#132), servidor HTTP | **Incorporado selectivamente**: ReadHeaderTimeout 10s, IdleTimeout 120s, MaxHeaderBytes 1 MiB. Este último explicita el default Go, no aumenta protección por sí solo. WriteTimeout sigue cero para SSE. Hooks Fabric intactos. |
| Seguridad | `0338ddea845f`, transporte SSRF; `9b553e7583ca`, vault/políticas por clave | Pendientes de adaptación. No romper transporte loopback autorizado del fork ni rutas administrativas ya protegidas. |
| Errores/compatibilidad | `05ae8ff39ef0`, normalización de campos de tokens | Pendiente de port y regresión cruzada con traducciones del fork. |
| Rendimiento | `cf22cf0f272d`, fast-path normalización | Pendiente; no importar sin la corrección correspondiente. |
| Proveedores | `48c11157bfdb`, catálogo dinámico | Pendiente de comparación detallada. El fork ya tiene nodos personalizados y descubrimiento; pruebas nuevas fijan su contrato sin duplicarlo. |
| OAuth | `e674b052aa06`, deduplicación/caché de renovación | Pendiente; conservar límites existentes. No declarar refresh/revocación de todos los proveedores certificados. |
| UI | `05abc5a4ebde`, cuotas móvil | Pendiente; no reemplazar UI propia ni introducir React. |
| Documentación | `75decf9f3b6f`, plan keirouter | No incorporado en esta entrega: ajeno a prioridades verificadas; revisar antes de portar. |
| Pruebas | `bc37e44aae01`, políticas por clave | Pendiente junto a la funcionalidad correspondiente; no copiar tests de contratos ausentes. |

No se añade ningún proveedor eliminado, alias entre proveedores, bypass de cuota ni credencial por defecto. La corrección upstream `63839a7745cc` de auth no se copia: la autorización administrativa del fork ya exige sesión/CLI en las rutas sensibles examinadas.

## Funciones y deuda

Implementado: comparador hasta diez modelos, seis tareas, perfiles por tarea/proveedor/modelo locales al navegador, prioridades/orden, fallback conservador, round-robin y selección manual; límites y compatibilidad anterior. Ver [MULTIMODELO.md](MULTIMODELO.md).

Ya existían y se preservan: cuentas/proveedores autorizados, combos, rutas free/free-best, límites, OAuth específico por proveedor, estadísticas y retención de detalles. No se certifican globalmente por existir. Los perfiles no se sincronizan con servidor; la tarea no altera prompts. La telemetría de detalles usa retención 1–365 días (default siete), no borrado universal de históricos/backups. Las credenciales SQLite **no se declaran cifradas**; #141 sigue pendiente.

AGY se documenta como nodo independiente con Bearer y catálogo dinámico. Solo se comprobó respuesta pública 401; catálogo autenticado, inferencia, Responses, herramientas y SSE reales pendientes por falta de credencial autorizada. Pruebas HTTP locales usan mocks, no prueban AGY. Ver [AGY-OPENCODE.es.md](AGY-OPENCODE.es.md).

## Evidencia de verificación

Ejecutado localmente en esta entrega: suite Go, vet, integración con router/base reales y proveedores simulados, pruebas frontend/build con Bun 1.4.2, carreras en paquetes afectados, SSE prolongado, catálogo personalizado/Bearer/cancelación/errores y UI real (guardar/cargar perfil tras recarga y simulación sin envío). Los logs finales y Actions del PR determinan el estado publicado; no atribuir al head final un run de otro SHA.

Binarios: script compila dos veces cada arquitectura Linux AMD64/ARM64 con misma fuente/toolchain/assets y compara bytes antes de publicar SHA-256. Esto **no prueba** ejecución ARM64/Termux ni reproducibilidad entre toolchains/hosts diferentes. Los artefactos de PR duran siete días y no constituyen release firmada. No se publican claves ni bases reales.

Pendientes: revisión exhaustiva/restante de upstream, cifrado/migración con custodia de clave, OAuth/revocación end-to-end por proveedor, OpenCode real local/VPS, Android/Termux nativo, AGY autenticado y capacidades por modelo. Esta entrega no satisface por sí sola la totalidad del programa solicitado.
