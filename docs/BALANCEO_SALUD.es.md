# Balanceo por salud de combos (estrategia `health`)

Estrategia opcional de combo que prueba primero los miembros (proveedor/modelo)
más sanos. **Está apagada por defecto**: solo actúa en los combos a los que el
admin les asigna la estrategia `health`; las estrategias existentes
(`fallback`, `round-robin`, `sticky`, `capacity`, `fusion`) no cambian.

## Cómo funciona

- Cada intento upstream (de cualquier ruta, combo o no) alimenta un registro en
  memoria por `proveedor/modelo`: tasa de éxito (EWMA, α = 0,3), latencia de
  los éxitos (EWMA), fallas consecutivas y última falla/éxito.
- **Cuentan como falla de salud:** 408, 429, 5xx, respuestas vacías y errores
  de transporte (conexión rechazada/cortada, timeout).
- **No cuentan:** errores del cliente (400, 404, 413, 422…) y solicitudes
  canceladas por el cliente. Un request mal formado no castiga al proveedor.
- Con 3 fallas consecutivas se abre un "circuito" con enfriamiento de 30 s que
  se duplica con cada falla extra (tope 5 min).
- Orden de prueba: primero circuitos cerrados; después mayor tasa de éxito (en
  bandas de 0,1); a igual banda, menor latencia; a igual todo, el orden
  configurado del combo (orden estable). Un miembro sin datos se considera
  sano (para que se pruebe y gane historial).
- **Nunca se descarta un miembro**: uno caído pasa al final, así el combo
  conserva toda su disponibilidad. Siguen vigentes los mecanismos existentes:
  bloqueo de cuentas por 429/5xx, `IsProviderAvailable`, failover inmediato
  en 502/503/504 y la segunda pasada acotada por `Retry-After`.
- El estado vive en memoria: al reiniciar el proceso, todos vuelven a "sano".

## Uso

En **Combos**, elegí la estrategia **"Salud — prioriza modelos sanos"**, o vía
API de dashboard (`strategy: "health"`). Vale para `/v1/chat/completions`,
`/v1/messages` (Anthropic) y streaming.

## Observabilidad (solo admin)

| Método | Ruta | Descripción |
|---|---|---|
| GET | `/api/combo-health` | Salud por proveedor/modelo (éxito, latencia, fallas, circuito). |
| DELETE | `/api/combo-health` | Reinicia el registro. |

Una API key de cliente recibe **403** en ambas, con login activado o no; la
sesión del dashboard y el token CLI tienen acceso.

## Pruebas

- `internal/combohealth`: orden estable sin datos, el miembro con fallas pasa
  al final sin descartarse, enfriamiento y recuperación, backoff del
  enfriamiento, uso concurrente (5.000 muestras exactas con `-race`).
- `internal/handlers/chat`: clasificación (400/404/cancelada no cuentan;
  429/503/transporte sí).
- Integración (`-tags=integration`): con `health` el miembro caído se prueba
  1 vez de 6 y el sano atiende las 6; regresión: con `fallback` el orden no
  cambia (4 intentos al caído); `/v1/messages` y streaming; 400 no degrada;
  403 para API key en GET/DELETE.

## Reversión

Cambiar la estrategia del combo a otra. No hay migraciones ni datos
persistidos.

## Atribución

Inspirado en el balanceo por salud de 9Router Pro (licencia MIT, © decolua y
colaboradores). Reimplementado en Go sin copiar código.
