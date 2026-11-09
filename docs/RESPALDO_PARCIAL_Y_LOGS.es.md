# Respaldo parcial y redacción de logs

## Exportación / importación parcial

El respaldo cifrado `.9rbak` existente (contraseña del dashboard + frase de
cifrado, Argon2id) ahora acepta una **selección de secciones**.

- **Exportar:** `GET /api/settings/database?sections=combos,pricing`. Sin
  `sections` se descarga el respaldo completo, como siempre. Secciones válidas:
  `settings`, `providerConnections`, `providerNodes`, `proxyPools`, `apiKeys`,
  `combos`, `modelAliases`, `customModels`, `mitmAlias`, `pricing`. Una sección
  desconocida devuelve 400. El archivo se llama `9router-backup-parcial-…`.
- **Importar:** el mismo `POST /api/settings/database`. Si el contenido trae
  `"sections": [...]`, **solo** se borran y restauran esas secciones; todo lo
  demás queda intacto. Claves fuera de la lista se ignoran (un archivo
  manipulado no puede escribir una sección que no declara). `sections` vacío,
  no lista o con nombres desconocidos → 400 sin tocar datos. Sin `sections`
  sigue siendo una restauración completa.
- Útil para compartir combos o precios **sin credenciales**: una exportación
  sin `providerConnections` ni `apiKeys` no incluye claves.
- UI: Perfil → Base de datos → "Exportación parcial (opcional)", con casillas
  en español.
- Permisos sin cambios: exige contraseña del dashboard o token CLI. Una API key
  de cliente recibe 401 al exportar o importar (con login activado o no) y los
  datos no cambian.

## Redacción central de logs

Antes, el enmascarado dependía de que cada llamada usara `log.MaskSecret`.
Ahora `internal/log` aplica una red de seguridad a **todas** las líneas (texto,
JSON y el buffer de consola del dashboard):

- Valores con nombres sensibles (`authorization`, `apiKey`, `token`,
  `refresh_token`, `password`, `secret`, `client_secret`, `cookie`,
  `passphrase`, …) se enmascaran siempre.
- En mensajes, errores y textos: `Bearer …`/`Basic …` → `Bearer ***`; formas de
  claves de proveedor (`sk-…`, `gsk_…`, `xai-…`, `AIza…`, `ghp_…`) →
  `sk-p...XXXX`.
- No modifica los argumentos del llamador. Valores no sensibles quedan igual.
- No registra contenido de prompts (ver `docs/INYECCION_PROMPTS.es.md`).

## Proxies por proveedor / conexión (P1)

**Ya existía**, no se reimplementó: proxy pools (`/api/proxy-pools`, admin),
`proxyPoolId` por conexión y por proveedor (`settings.providerStrategies`).

## Pruebas

- `dashboard`: exportación solo de secciones listadas (sin credenciales), 400
  por sección desconocida, importación parcial que solo toca sus secciones,
  claves no listadas ignoradas, `sections` mal formado → 400 sin cambios,
  regresión de restauración completa, exportación parcial sin contraseña
  rechazada.
- Integración: API key → 401 en exportar/importar parcial con login on/off y
  datos intactos.
- `log` (-race): redacción de texto, JSON, errores, consola y no mutación.
- Web: `bun test` de `backupSections`.

## Reversión

Revertir el commit; sin migraciones. Los respaldos parciales solo se pueden
importar en versiones que entiendan `sections` (en versiones previas se
tratarían como completos: **no** importarlos allí).

## Atribución

Inspirado en la exportación selectiva y la redacción de logs de 9Router Pro
(licencia MIT, © decolua y colaboradores). Reimplementado en Go sin copiar
código.
