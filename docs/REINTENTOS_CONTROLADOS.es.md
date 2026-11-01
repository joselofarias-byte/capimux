# Reintentos controlados sin duplicar operaciones no idempotentes

## Qué ya existía (sin cambios)

- **Chat** (`/v1/chat/completions`, `/v1/messages`, `/v1/responses`): rotación
  de cuentas ante estados reintentables (`providers.RetryableStatusCodes`),
  bloqueo con backoff de la cuenta, failover inmediato del combo en
  502/503/504, segunda pasada acotada por `Retry-After` (`passRetry`) y
  refresco reactivo de token ante 401. Un error no reintentable (400/422…)
  prueba cada cuenta una sola vez (#157). La generación de texto no tiene
  efectos secundarios en el proveedor más allá de la llamada, así que seguir
  haciendo failover tras un corte es correcto; una vez que se enviaron bytes al
  cliente no se reintenta (`committedResponseWriter`).
- **Video**: la creación ya no rotaba ante 5xx.

## Qué se corrigió

Se encontraron **duplicaciones reales** en `main` (pruebas que fallan sin este
cambio):

| Caso | Antes | Ahora |
|---|---|---|
| Imagen por combo, 1.er miembro corta la conexión tras recibir | 2.º miembro genera otra imagen | se devuelve el error |
| Imagen por combo, 1.er miembro 500/503 | 2.º miembro genera otra imagen | se devuelve el error |
| Video por combo, corte tras enviar | 2.º miembro crea otro trabajo | se devuelve el error |
| Imagen Antigravity, cuenta 1 con 500/503/corte/200 ilegible | cuenta 2 genera otra imagen | se devuelve el error |

## Regla (`internal/retrypolicy`)

Para operaciones **no idempotentes** (`/images/generations|edits|variations`,
`/videos/generations|edits|extensions`) solo se rota a otra cuenta o miembro
del combo cuando la falla **prueba** que el proveedor no creó nada:

- **Rota:** 4xx (401, 403, 429, 400…: solicitud rechazada) y fallas de
  transporte **antes de enviar** (DNS, conexión rechazada, `dial`,
  `proxyconnect`, handshake/certificado TLS).
- **No rota (ambigua):** cualquier 5xx, timeout o corte **después** de enviar,
  cuerpo de respuesta ilegible y respuesta 200 que no se pudo formatear (la
  imagen ya se generó).

Las operaciones idempotentes conservan su comportamiento.

## Pruebas

- `internal/retrypolicy` (-race): clasificación con errores reales de red
  (conexión rechazada vs. corte tras enviar vs. timeout), DNS envuelto,
  `MayRotate`, marca `Stop`.
- `internal/handlers/media`: Antigravity con dos cuentas — 503, 500, corte y
  200 ilegible → 1 intento y 0 en la 2.ª; 429 y 401 → rota.
- Integración (-race): combos de imagen y video — corte/500/503 → 1/0 intentos;
  429/401/conexión rechazada → rota; **regresión** de chat: el combo sigue
  haciendo failover tras un corte.

## Reversión

Revertir el commit; no hay migraciones ni configuración.

## Atribución

Inspirado en la recuperación/reintentos de 9Router Pro (licencia MIT,
© decolua y colaboradores). Reimplementado en Go sin copiar código.
