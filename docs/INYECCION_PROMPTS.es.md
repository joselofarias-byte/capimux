# Inyección de prompts, identidad pública de combos y Playground

Este módulo agrega instrucciones de sistema administradas por el operador a las
solicitudes de inferencia que pasan por Capimux. Está inspirado funcionalmente
en 9Router Pro (licencia MIT, © decolua y colaboradores); **no se copió código**:
el motor está reimplementado en Go dentro de `internal/promptinject`.

Todo viene **apagado por defecto**. Con los interruptores apagados, el
middleware deja pasar la solicitud sin leer el cuerpo.

## Interruptores (independientes)

| Ajuste (`settings`) | Qué hace |
|---|---|
| `promptInjectionEnabled` | Aplica las capas activas a las solicitudes de inferencia. |
| `comboPublicIdentityEnabled` | Reescribe el nombre de modelo que ve el cliente en la respuesta de un combo. |
| `promptInjectionCaptureEnabled` | Guarda en memoria (no en disco) las últimas 20 solicitudes inyectadas, con secretos redactados. Al apagarlo se vacía. |

Los tres forman parte de `authPostureSettingKeys`: una API key de cliente no los
puede cambiar (403) aunque el dashboard no exija login.

## Capas y precedencia

Orden de aplicación, de la más general a la más específica:

1. **Global**
2. **Combo** (solo si el `model` pedido es un combo)
3. **Modelo** (solo si el `model` pedido no es un combo)
4. **API key** (la key con la que se autenticó la solicitud)

Cada capa tiene un modo:

- **Al inicio (`prepend`)**: se antepone a las instrucciones de sistema del cliente.
- **Al final (`append`)**: se agrega después de las instrucciones del cliente.
- **Reemplazo controlado (`replace`)**: reemplaza las instrucciones de sistema
  del cliente. Si hay varias capas `replace`, gana la más específica y se
  descartan las capas menos específicas que ella; las más específicas siguen
  sumándose con prepend/append.

Resultado: `[prepends] + [sistema del cliente o reemplazo] + [appends]`, donde
prepends y appends se concatenan en el orden global → combo → modelo → API key,
separados por una línea en blanco.

## Formatos soportados

| Ruta | Protocolo | Dónde se inyecta |
|---|---|---|
| `/v1/chat/completions`, `/api/chat` | OpenAI Chat | Mensajes `system`/`developer` iniciales (texto o partes). Si no hay, se inserta uno `system`. |
| `/v1/messages` | Anthropic | Campo `system` (texto o bloques; se agregan bloques de texto). |
| `/v1/responses` | OpenAI Responses | Solo el campo `instructions`. Los mensajes de sistema dentro de `input` no se tocan (limitación documentada). |

Gemini no tiene endpoint de entrada nativo en Capimux; le llega la inyección a
través de la traducción desde cualquiera de los formatos anteriores. Mensajes,
roles, `tool_calls`/`tool_use` y streaming se preservan: solo se modifica el
bloque de sistema.

## Límites técnicos (sin filtros de contenido)

- Hasta 64 KiB (65 536 bytes) por capa, UTF-8 válido, sin bytes nulos.
- Destino (modelo/combo/key) hasta 256 caracteres.
- Carga desde `.txt`: solo extensión `.txt`, UTF-8, se quita BOM y se
  normaliza CRLF→LF. La carga no guarda nada: solo devuelve el texto al editor.

No hay listas de bloqueo ni presets: el editor es de uso general.

## Identidad pública por combo

Permite que el cliente vea un nombre propio (por ejemplo `asistente-equipo`)
en el campo `model` de la respuesta (JSON, SSE y NDJSON).

- **Solo cambia el nombre visible.** No altera el modelo ni el proveedor reales
  y no cambia las políticas ni los controles del proveedor.
- Logs, uso, estadísticas, `requestDetails` y auditoría registran siempre el
  **modelo y proveedor reales**. Lo verifica
  `TestComboPublicIdentityKeepsRealModelInAudit` (y su variante de streaming).

## Permisos

Todas las rutas `/api/prompt-injections*` y `/api/combo-identities*` son solo
para administrador (sesión del dashboard o token CLI). Una solicitud con API key
de cliente recibe **403**, con o sin `requireLogin`. Ninguna key puede ver ni
modificar la capa global, las de otras keys, ni la suya.

| Método | Ruta | Uso |
|---|---|---|
| GET/PUT/DELETE | `/api/prompt-injections` | Listar, guardar y borrar capas |
| PUT | `/api/prompt-injections/toggles` | Interruptores |
| POST | `/api/prompt-injections/upload` | Leer un `.txt` (campo `file`) |
| POST | `/api/prompt-injections/preview` | Inspector: solicitud final sin llamar al proveedor |
| GET/DELETE | `/api/prompt-injections/captures` | Capturas en memoria |
| GET/PUT/DELETE | `/api/combo-identities` | Identidad pública por combo |

## Privacidad y logs

- Los logs persistentes (`requestDetails`, uso) **nunca** guardan el texto
  inyectado: se reemplaza por `[instrucciones inyectadas omitidas]`.
- El log de aplicación solo registra cuántas capas se aplicaron, no su contenido.
- Las capturas son opt-in, en memoria, máximo 20, cuerpos truncados a 16 KB y
  con secretos redactados (`Bearer`, `sk-…`, `AIza…`, tokens de GitHub/Slack,
  JWT y campos JSON con nombre de secreto).

## Inspector "Probar prompt"

Muestra la solicitud que Capimux arma tras la inyección, antes de traducirla al
formato del proveedor, con secretos redactados. Es la evidencia de lo enviado:
lo que el modelo diga sobre haber seguido o no las instrucciones es una salida
del modelo, no una verificación. No llama al proveedor.

## Playground

Pestaña **Playground** del dashboard: elegir modelo o combo, enviar mensajes y
ver la respuesta en streaming. Usa una API key de cliente (guardada solo en
`sessionStorage`) y llama a `/v1/chat/completions` como cualquier cliente, así
que aplica autenticación, inyección, cuotas y permisos de esa key. Consume
tokens reales del proveedor elegido.

## Compatibilidad con OpenCode y OpenClaw

Validado con fixtures de sus formatos de solicitud (OpenAI Chat con
`tool_calls` y Anthropic Messages con bloques y `cache_control`) en
`internal/promptinject/clients_fixture_test.go`. Estado: **simulado** (no se
ejecutaron los clientes reales contra el gateway).

## Reversión

1. Apagar los interruptores en el dashboard (o `PUT /api/prompt-injections/toggles`
   con los tres en `false`): el comportamiento vuelve a ser idéntico al anterior.
2. Para borrar los datos: `DELETE FROM kv WHERE scope IN ('promptInjection','comboIdentity');`
   Los ajustes `promptInjection*` y `comboPublicIdentityEnabled` son ignorados
   por versiones anteriores. No hay cambios de esquema.
