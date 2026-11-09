# Bitácora de integración

Registro de cada PR integrado a `main`: quién lo integró y con qué autorización,
qué pruebas lo respaldaron y el resultado. Horas en UTC-3 (America/Montevideo).
Método: siempre `gh pr merge N --merge --match-head-commit <head>` (commit de
merge). Sin squash, rebase, force-push ni `--admin`.

**Pruebas locales estándar** (en el head exacto, antes de cada merge):
`go build ./...`, `go vet ./...`, `go vet -tags=integration ./...`,
`go test -count=1 ./...`, `go test -race` (paquetes tocados, o `./...`),
`go test -tags=integration -race ./internal/integration/...` y build Termux
(`GOOS=android GOARCH=arm64 CGO_ENABLED=0`, binario ELF aarch64), sin
`CAPIMUX_TEST_FAKE_TAILSCALE` y con el guard de Tailscale presente. Después de
cada merge: `go build ./... && go test -count=1 ./...` sobre el `main` nuevo,
comprobando además que el árbol de `main` es idéntico al head verificado.

**CI:** `test`, `Integration tests`, `Build Docker image (verify only)` y, desde
#149, `Go race detector`, todos sobre el head exacto.

## 30-10-2026 — ciclo 10 (autorización escrita del propietario, 13:42)

Responsable: Grok Bot (ejecutor), autorizado por José Luis Farías. Batería
post-merge del ciclo 10: build, vet (normal e integración), `go test ./...`,
race en handlers/providers/controlplane, integración con race, tests 403/409 y
Termux (detalle en `CAPIMUX-GROK-BOT-ESTADO.md`, ciclo 10).

| Orden | PR | Título | Head | Merge commit | Hora | Pruebas | Resultado |
|---|---|---|---|---|---|---|---|
| 1 | #158 | seguridad: API key de cliente no sobrescribe credenciales | `1803b22a` | `bf15b250577cdb9272a4011a8e910c3747a950a9` | 13:42 | CI test, Integration, Docker, multiagent ✓; batería post-merge OK | integrado |
| 2 | #161 | catálogo: variante determinística y rechazo de respuestas vacías | `2ae0ac6c` | `3bc3929b919a7fb8bca16aceda7ce3e0c8471e73` | 14:02 | CI test, Integration, Docker ✓; post-merge: flake `TestGateAcquire_SpacesConcurrentCallers` (32.46ms vs 40ms) una vez, reintento ✓ | integrado |
| 3 | #159 | combos: sin modelos deshabilitados ni proveedores inactivos | `52b732be` | `522d683fe906e457eac63361af76a06f872f9317` | 14:08 | CI test, Integration, Docker ✓; batería post-merge OK | integrado |
| 4 | #162 | combos generados: poda y orden por versión | `0a39f129` | `c84327727711f9bb42d817637c8893afc3f24438` | 14:18 | CI test, Integration, Docker ✓; batería post-merge OK (403 y 409) | integrado |
| 5 | #160 | descubrimiento: una variante por id base, conserva `:free` | `9435776f` | `34a2fcd603ebc77e4109eeb889459c7e3af39c78` | 14:32 | CI test, Integration, Docker ✓; conflicto solo en CHANGELOG (ambas entradas); post-merge: flake `SpacesBurstAcrossConnections` con race una vez, reintento ✓ | integrado |

## 30-10-2026 — ciclo 12, consolidación (mensaje del propietario, 19:02)

Responsable: Grok Bot (ejecutor), autorizado por José Luis Farías.

| Orden | PR | Título | Head | Merge commit | Hora | Pruebas | Resultado |
|---|---|---|---|---|---|---|---|
| 1 | #163 | combos: persistir la estrategia y rechazar valores inválidos | `fa759c44` | `977f3e6809f91509f446f34f406ebbcd2e9d5f2c` | 19:03 | locales estándar; CI test, Integration, Docker ✓ (anterior al job race) | integrado |
| 2 | #149 | ci: `go test -race ./...` en un job paralelo | `db4aac86` | `f236ebb5826632b7c67541ddc1a0204d4cffe635` | 19:07 | locales estándar; CI 4/4 ✓ | integrado |
| 3 | #157 | fallback de cuentas, reintentos de combo y catálogo Gemini | `e171bfe2` | `ed2d906012f4b836594435324a07a8aba4cf2a61` | 19:10 | locales estándar; CI 4/4 ✓ | integrado |
| 4 | #148 | una API key de cliente no puede gestionar otras API keys | `35a57b00` | `1216426e168f4b8f5108ec11f4183a0d8789a15b` | 19:14 | locales estándar; CI 4/4 ✓; conflicto en `admin_guard.go` (se conservaron ambos settings); flake `TestComboRateLimitFailover` una vez, reintento ✓ | integrado |
| 5 | #140 | OPS-01: SHA-256 obligatorio y manifest firmado Ed25519 | `b25acc97` | `4bc198981e54096827993004a21e13673415e67e` | 19:18 | locales estándar; tests de manipulación y mutación; CI 4/4 ✓ | integrado |
| 6 | #150 | updater: SHA-256 con fuentes por plataforma y digest de GitHub | `87d65fa6` | `05da87215e22125001d0c7495bd978a43e3bb373` | 19:21 | locales estándar; CI 4/4 ✓; conflicto CHANGELOG (ambas) | integrado |
| 7 | #152 | updater: valores por defecto al repo `capimux` | `10a17e1a` | `5eceaf8985333f28fd573415f127bda21dc1533f` | 19:24 | locales estándar; CI 4/4 ✓; conflicto `updater.go` (bloque firmado + `canonicalUpdateURL`) y CHANGELOG | integrado |
| 8 | #153 | release: `version.json` con SHA-256 por plataforma | `c05d5852` | `4330d6daf5554bcc597553fbdfa8ade2e68b1dc0` | 19:28 | locales estándar; CI 4/4 ✓; conflicto CHANGELOG (unión) | integrado |
| 9 | #155 | release: Docker en `ghcr.io/joselofarias-byte/capimux` | `bf43bc31` | `6ee94ceb5185ce503cf73d9e796c1c7d057f209d` | 19:31 | locales estándar; test del workflow con mutación; CI 4/4 ✓; base cambiada a `main` antes de #153 | integrado (paquete GHCR privado, sin publicar) |
| 10 | #156 | estado: consolidación de la cola de PR | `66959d46` | `6efddcf0827f7221db9dc657b696649a2da359b1` | 19:36 | locales estándar; CI 4/4 + Vercel ✓ | integrado |

`main` resultante: `6efddcf0827f7221db9dc657b696649a2da359b1`.

## 30-10-2026 — ciclo 13, verificación de `main` (sin merges)

Responsable: Grok Bot (ejecutor). Decisión del propietario: no se mergea nada
nuevo sin su decisión explícita.

| Commit | Pruebas | Resultado |
|---|---|---|
| `6efddcf0` | build, vet, vet integración, `go test -count=1 ./...`, `go test -race ./...` (completo), integración con race, Termux (ELF aarch64) | todo RC=0 |

## Cómo agregar una entrada

Una fila por PR integrado: orden, número, título, head mergeado (8 caracteres),
merge commit completo, hora UTC-3, pruebas (locales + CI sobre el head) y
resultado. Los PR cerrados sin integrar van con el motivo y el enlace al PR que
los reemplaza.
