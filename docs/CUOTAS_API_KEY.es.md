# Cuotas por API key

Límites por API key de cliente, configurables solo por el administrador.
Inspirado funcionalmente en 9Router Pro (licencia MIT, © decolua y
colaboradores); **no se copió código**: reimplementado en Go en
`internal/keyquota`.

**Apagado por defecto.** Con el interruptor apagado no se cuenta ni se limita
nada y el middleware deja pasar la solicitud sin tocarla.

## Límites

| Campo | Significado | Ventana |
|---|---|---|
| `rpm` | Solicitudes por minuto | móvil, 60 s |
| `tpm` | Tokens por minuto | móvil, 60 s |
| `concurrency` | Solicitudes en curso al mismo tiempo | instantánea |
| `tokens24h` | Tokens en 24 horas | móvil, granularidad horaria |
| `tokens7d` | Tokens en 7 días | móvil, granularidad horaria |
| `tokens30d` | Tokens en 30 días | móvil, granularidad horaria |

`0` o ausente = sin límite. Valores negativos → 400.

Se aplican a las solicitudes `POST` autenticadas con API key (inferencia y
generación). `GET /v1/models` no cuenta.

## Comportamiento

- **Admisión atómica**: el chequeo y el incremento ocurren bajo un mismo mutex,
  así que solicitudes concurrentes no pueden superar un límite entre el chequeo
  y la cuenta (test con `-race` y 400 goroutines).
- Al superar un límite: **429** con `Retry-After` (segundos) y **sin llamar al
  proveedor**. Cuerpo OpenAI
  `{"error":{"type":"rate_limit_error"|"insufficient_quota","code":"api_key_<límite>_exceeded"}}`;
  en `/v1/messages`, formato Anthropic `{"type":"error","error":{"type":"rate_limit_error"}}`.
- Los tokens se cuentan al terminar la solicitud con el uso informado por el
  proveedor (o la estimación que ya usa Capimux si no informa), incluido
  streaming y streams abortados. Los límites de tokens son de admisión: una
  solicitud admitida termina aunque supere la cuota; la siguiente se rechaza.
- Uso persistido por hora en la tabla `apiKeyUsageHourly` (UPSERT atómico); se
  conserva tras reiniciar y se poda a 30 días.

## API (solo administrador)

| Método | Ruta | Uso |
|---|---|---|
| GET | `/api/api-key-limits` | Interruptor, límites y uso por key (nunca devuelve el secreto) |
| PUT | `/api/api-key-limits/toggle` | `{"enabled": true\|false}` |
| PUT | `/api/api-key-limits/{id}` | Guardar límites (404 si la key no existe) |
| DELETE | `/api/api-key-limits/{id}` | Quitar límites |

Una solicitud con API key de cliente recibe **403** en todas, con o sin
`requireLogin`: una key no puede ver límites ajenos ni subir los propios. El
interruptor vive en `kv` (no en `settings`), así que tampoco se puede cambiar por
`PATCH /api/settings`.

## Dashboard

Pestaña **Cuotas por API key** (`/dashboard/key-limits`): interruptor, períodos
24 h / 7 días / 30 días, totales (tokens, solicitudes, en curso, keys con
límites), uso por key con barra contra su límite y modal de edición. El costo en
dinero sigue en la sección Usage.

## Reversión

1. Apagar el interruptor: el comportamiento vuelve a ser el anterior.
2. Borrar datos: `DELETE FROM kv WHERE scope = 'apiKeyLimits'; DROP TABLE IF EXISTS apiKeyUsageHourly;`
