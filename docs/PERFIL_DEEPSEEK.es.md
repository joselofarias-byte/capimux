# Perfil DeepSeek (opt-in): DSH → InferX

Perfil opcional que expone un modelo virtual (`deepseek-profile` por defecto)
servido por dos proveedores compatibles con OpenAI, en este orden:

1. **DSH (DeepSeek Web)**, primario. Modelo `deepseek-chat`.
2. **InferX**, secundario. `https://model.inferx.net/endpoints/v1`, modelo
   `deepseek-v4.1-flash-1017-basic`, con Bearer.

**Apagado por defecto.** Si no se configura nada, Capimux se comporta
exactamente igual que antes: no se agrega ningún modelo a `/v1/models`, no se
intercepta ninguna solicitud y el proveedor predeterminado no cambia. Encendido,
solo se interceptan las solicitudes a `/v1/chat/completions` cuyo `model` es el
id del perfil. El resto de los modelos, combos y proveedores sigue su camino
normal.

## Activación

| Variable | Obligatoria | Valor por defecto | Uso |
|---|---|---|---|
| `CAPIMUX_DEEPSEEK_PROFILE` | sí, para activar | apagado | `1`/`true`/`on` |
| `CAPIMUX_DEEPSEEK_PROFILE_MODEL` | no | `deepseek-profile` | id del modelo virtual |
| `CAPIMUX_DEEPSEEK_DSH_URL` | **sí** | — (sin valor por defecto) | URL base OpenAI de DSH, p. ej. `http://127.0.0.1:20232/v1` |
| `CAPIMUX_DEEPSEEK_DSH_MODEL` | no | `deepseek-chat` | |
| `CAPIMUX_DEEPSEEK_DSH_API_KEY` / `_FILE` | no | sin auth | Bearer opcional para DSH |
| `CAPIMUX_DEEPSEEK_DSH_TIMEOUT` | no | `45s` | tiempo máximo hasta el primer byte |
| `CAPIMUX_DEEPSEEK_INFERX_URL` | no | `https://model.inferx.net/endpoints/v1` | debe ser `https` (salvo loopback) |
| `CAPIMUX_DEEPSEEK_INFERX_MODEL` | no | `deepseek-v4.1-flash-1017-basic` | |
| `CAPIMUX_DEEPSEEK_INFERX_API_KEY` / `_FILE` | para usar InferX | — | secreto Bearer; sin él, InferX no se usa |
| `CAPIMUX_DEEPSEEK_INFERX_TIMEOUT` | no | `120s` | tiempo máximo hasta el primer byte |
| `CAPIMUX_DEEPSEEK_STREAM_IDLE_TIMEOUT` | no | `90s` | silencio máximo una vez iniciada la respuesta (corta, no reintenta) |
| `CAPIMUX_DEEPSEEK_CONNECT_TIMEOUT` | no | `10s` | conexión TCP/TLS |

Las duraciones usan la sintaxis de Go (`30s`, `2m`). La configuración se lee al
arrancar; para cambiarla hay que reiniciar el proceso Capimux.

Ejemplo (systemd, secreto en un archivo que no está en el repo):

```ini
Environment=CAPIMUX_DEEPSEEK_PROFILE=1
Environment=CAPIMUX_DEEPSEEK_DSH_URL=http://127.0.0.1:20232/v1
Environment=CAPIMUX_DEEPSEEK_INFERX_API_KEY_FILE=/etc/capimux/inferx.key
```

Uso desde un cliente: `"model": "deepseek-profile"` contra
`/v1/chat/completions`, con la API key de Capimux de siempre (el perfil pasa
por la misma autenticación que el resto de las rutas).

### Timeouts elegidos

Según las pruebas del propietario (reportadas por él, no repetidas aquí: DSH 6
respuestas 200 con latencia media ~2,97 s; InferX 6 respuestas 200 con ~14,03 s),
`45s` para DSH y `120s` para InferX dejan un margen amplio sobre la media y
acotan el tiempo hasta el respaldo. En solicitudes sin `stream` el primer byte
llega recién con la respuesta completa, así que el timeout cubre la generación
entera: para respuestas largas sin streaming conviene subirlo o usar `stream`.

## DSH: URL privada y alcanzable

`127.0.0.1:20232` **solo** es DSH si Capimux corre en la misma máquina que DSH
(Oracle Lab). En cualquier otra máquina (teléfono con Termux, otro servidor),
`127.0.0.1` es esa máquina y no Oracle Lab. Por eso la URL no tiene valor por
defecto: si falta, el perfil queda **cerrado** (responde 503 «mal
configurado») en lugar de mandar todo al secundario pago.

Opciones para un Capimux que no corre en Oracle Lab, todas privadas:

- **Túnel SSH local** desde la máquina de Capimux:
  `ssh -N -L 20232:127.0.0.1:20232 usuario@oracle-lab` y luego
  `CAPIMUX_DEEPSEEK_DSH_URL=http://127.0.0.1:20232/v1`.
- **IP privada de la red interna** (por ejemplo la IP de la tailnet ya
  existente) si DSH escucha en esa interfaz, con
  `CAPIMUX_DEEPSEEK_DSH_URL=http://<ip-privada>:20232/v1`.

No publicar el puerto 20232 a internet. Este cambio no toca Tailscale ni
ninguna infraestructura (nada de `serve`/`funnel`); si alguna opción requiere
cambiar la red, lo decide y lo hace el propietario.

## Reglas de respaldo (DSH → InferX)

Se pasa a InferX **solo** si DSH falla **antes** de que empiece la respuesta,
es decir, antes de recibir el primer byte de un cuerpo 2xx:

- error de conexión (conexión rechazada, DNS, corte antes del primer byte,
  cuerpo 2xx vacío);
- HTTP **429, 502, 503 o 504**;
- timeout hasta el primer byte.

**Sin respaldo**:

- HTTP **400, 401, 403** (y cualquier otro estado: 404, 422, 500…); se devuelve
  al cliente el estado y el cuerpo del proveedor (con secretos redactados);
- cualquier falla **después** de iniciada la respuesta: tokens, tool calls o
  resultados parciales ya recibidos. Un stream cortado o en silencio más allá
  de `STREAM_IDLE_TIMEOUT` termina con un evento SSE de error
  (`code: upstream_cut`); sin stream, 502. Nunca se regenera;
- cancelación del cliente.

Si InferX también falla, se devuelve su error (o 504/502 si fue timeout o
conexión). Relación con lo ya mergeado: el perfil no usa el motor de combos, así
que no altera #157 (cada cuenta una vez ante 4xx permanente). Es coherente con
#171: chat es idempotente, y aun así este perfil no reintenta nada que ya haya
empezado a responder. La privacidad de #139 se respeta: el contenido de la
solicitud y la respuesta solo se guarda con `CAPIMUX_CAPTURE_REQUEST_CONTENT=true`.

## Herramientas (tools)

- `tools`, `tool_choice` y el resto del cuerpo se reenvían **sin cambios**; solo
  se reemplaza `model`.
- El bridge **no ejecuta herramientas** ni hace turnos de seguimiento: devuelve
  la tool call al cliente, que la ejecuta si quiere.
- Se **rechaza** cualquier tool call cuyo nombre no esté declarado en `tools`
  (o `functions`) de la solicitud, o no esté autorizado por `tool_choice`
  (`"none"` no autoriza ninguna; una función nombrada autoriza solo esa). Sin
  stream: 502 con `code: tool_not_declared` y sin los argumentos. Con stream: el
  chunk con la tool call no autorizada no se reenvía, se manda un evento de
  error y se corta. Sin reintento en ambos casos.
- **Pendiente** (anotado por el propietario): verificar la implementación interna
  de ejecución de herramientas de DSH. No se investigó en el servidor real.

## Métricas y costo

DSH responde `usage` con ceros: eso significa **desconocido**, no gratis. Cuando
un proveedor del perfil no informa tokens (ceros o sin `usage`):

- `usageHistory` guarda tokens **estimados** (caracteres/4) con
  `tokens.estimated`, `tokens.usage_unknown` y `tokens.cost_unknown` en `true`, y
  `costUnknown` en el meta; el costo de la fila queda en 0 solo como marcador,
  con la bandera que lo identifica;
- **no** se suma a los totales diarios (`usageDaily`), para que no se lea como
  consumo 0;
- el detalle de la solicitud lleva `tokensEstimated`, `usageUnknown` y
  `costUnknown`.

Con tokens reales (InferX), el registro es el normal.

## Seguridad

- Secretos solo por variable de entorno o archivo (`*_FILE`); nunca en el repo.
- El secreto no aparece en logs, errores, respuestas ni en `fmt` de la
  configuración; si un proveedor lo devuelve en un cuerpo de error, se reemplaza
  por `[redactado]`.
- InferX exige `https` (salvo loopback); las URLs no pueden llevar credenciales,
  parámetros ni fragmentos; no se siguen redirecciones (el Bearer no viaja a
  otro destino).
- Sin secreto de InferX, el perfil funciona solo con DSH y nunca manda un Bearer
  vacío.

## Reversión

1. Quitar `CAPIMUX_DEEPSEEK_PROFILE` (o ponerlo en `0`) y reiniciar Capimux. Todo
   vuelve a ser como antes: el modelo virtual desaparece de `/v1/models` y
   `deepseek-profile` se resuelve como cualquier otro nombre (como antes de este
   cambio). Lo cubren pruebas (`TestDeepSeekProfile_RollbackRestoresPreviousBehaviour`,
   `TestDeepSeekProfile_OffByDefault_NothingChanges` y su versión de integración).
2. Para quitar el código: revertir el merge del PR. No hay migraciones ni
   cambios de esquema; las filas de uso ya escritas quedan como historial.

## Pruebas

Todas con servidores `httptest` en 127.0.0.1; no se llama a DSH ni a InferX
reales.

- `internal/deepseekprofile`: texto, tool calling (preserva `tools`/`tool_choice`,
  no ejecuta), streaming (texto y tool call partida en chunks), respaldo ante
  429/502/503/504, conexión, timeout antes de los headers y después de los
  headers pero antes del primer byte, 2xx vacío; sin respaldo ante
  400/401/403/404/422/500; corte a mitad de stream (texto y tool call), silencio
  a mitad de stream y corte de un cuerpo sin stream, sin reintento; cancelación
  del cliente; tool no declarada (sin tools, otra tool, `tool_choice` none o
  nombrada, stream); secreto ausente, desde archivo, nunca en logs/errores/
  respuestas; redirecciones; errores de configuración; desactivado por defecto;
  valores por defecto.
- `internal/handlers/chat`: apagado no cambia nada, reversión, otros modelos
  intactos, tokens en cero como desconocidos, uso conocido normal, mal
  configurado cierra, fallas no se facturan.
- `internal/integration` (`-tags=integration`): router completo con auth,
  stream con respaldo, 401 sin respaldo, apagado igual que antes.
