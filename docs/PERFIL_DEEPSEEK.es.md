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

Las duraciones usan la sintaxis de Go (`30s`, `2m`). La configuración por
variables se lee al arrancar; para cambiarla hay que reiniciar el proceso
Capimux. Las sesiones DeepSeek Web y el interruptor del respaldo InferX se
gestionan desde el panel y se aplican sin reiniciar (ver más abajo).

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

## Sesiones DeepSeek Web desde el panel

Orden del propietario (comentario en #174, 02/11): la sesión real de DeepSeek
Web se gestiona desde el panel de administración. **La clave del bridge
(`CAPIMUX_DEEPSEEK_DSH_API_KEY`) y la sesión web son cosas distintas.**

> **Pendiente — auditoría de DSH.** El contrato de sesión de
> `dsh-better-bridge` (campos de credencial, almacenamiento, expiración,
> renovación y recarga) **no se auditó** todavía: no se accedió a Oracle Lab ni
> a ningún servicio real. Por eso:
>
> - la credencial es **un único valor opaco**: Capimux no la interpreta, no
>   asume nombres de cookie ni de token y **no la envía a DSH** (todavía no hay
>   forma acordada de hacerlo);
> - la comprobación de salud real no existe: el comprobador por defecto no
>   contacta nada y devuelve **«indeterminada»**;
> - en consecuencia, ninguna sesión llega a «conectada» en un despliegue real y
>   **el respaldo InferX queda apagado** hasta completar la auditoría.
>
> TODO: definir los campos y la comprobación según la auditoría y reemplazar el
> comprobador (`deepseekweb.Checker`). Antes de habilitarlo, smoke real en un
> entorno autorizado por el propietario.

### Panel

Sección **DeepSeek Web** (menú lateral, `/dashboard/deepseek-web`):

- **Agregar**: nombre y credencial (campo enmascarado, tipo contraseña). La
  sesión empieza activa y en estado «indeterminada».
- **Comprobar**: ejecuta el comprobador; muestra conectada / expirada / error /
  indeterminada y la hora del último chequeo.
- **Actualizar**: reemplaza la credencial (rotación). Se vuelve a cifrar, el
  estado vuelve a «indeterminada» y hay que comprobar de nuevo.
- **Desactivar / Activar** y **Eliminar** (borra la credencial cifrada).
- Tarjeta aparte **Respaldo InferX (facturable)**: interruptor explícito,
  apagado por defecto, con la indicación de si está efectivo ahora.

La credencial nunca se vuelve a mostrar: las respuestas traen solo una máscara
fija (`••••••••`), sin prefijos ni sufijos.

### API (solo administración)

Detrás de `RequireAdminAuth()` (cookie de sesión del panel o token local de
CLI). Una API key de inferencia recibe **401**, aunque el login del panel esté
desactivado.

| Método | Ruta | Uso |
|---|---|---|
| GET | `/api/admin/deepseek-web/sessions` | lista + estado del respaldo |
| POST | `/api/admin/deepseek-web/sessions` | `{label, credential, expiresAt?}` |
| GET | `/api/admin/deepseek-web/sessions/{id}` | ver (enmascarada) |
| PATCH | `/api/admin/deepseek-web/sessions/{id}` | `{rev, label?, credential?, enabled?, expiresAt?, clearExpiresAt?}`; `rev` distinto → 409 |
| DELETE | `/api/admin/deepseek-web/sessions/{id}` | eliminar |
| POST | `/api/admin/deepseek-web/sessions/{id}/check` | comprobar; otra en curso → 409 |
| GET/PUT | `/api/admin/deepseek-web/fallback` | `{enabled}` → `{requested, effective, reason}` |

Cuerpos de hasta 64 KiB; credencial de hasta 16 KiB; hasta 32 sesiones.

### Almacenamiento y cifrado

- `DATA_DIR/deepseek-web/sessions.json` (0600, directorio 0700): metadatos y la
  credencial **cifrada** con AES-256-GCM (nonce aleatorio por escritura; el id
  de la sesión va como dato asociado, así un cifrado copiado a otra sesión no
  se descifra).
- Clave: `DATA_DIR/deepseek-web/secret.key` (32 bytes aleatorios en base64,
  0600), creada al primer uso. Opcionalmente, `CAPIMUX_DEEPSEEK_WEB_KEY_FILE`
  apunta a una clave guardada fuera de `DATA_DIR` (debe ser 0600; si es legible
  por grupo u otros, se rechaza).
- `main` no tiene cifrado en reposo general: el diseño de #141 no está
  mergeado y **no se activó** su migración. Este cifrado es local al módulo; la
  custodia de la clave queda como decisión pendiente ligada a #141.
- Permisos más abiertos de una instalación previa se ajustan al abrir.
- **Exportaciones**: las sesiones no están en la base de datos, así que la
  exportación del panel (descarga y Google Drive) no las incluye (hay prueba).
  Una copia de seguridad del **directorio completo** `DATA_DIR` sí incluiría el
  archivo cifrado **y** la clave: excluir `DATA_DIR/deepseek-web/` o mover la
  clave con `CAPIMUX_DEEPSEEK_WEB_KEY_FILE`.
- **Logs**: solo id de sesión, estado y si hubo rotación; nunca la credencial.
  El detalle de error de una comprobación se redacta (sin la credencial) y se
  recorta a 200 caracteres.

### Estados y expiración

- `unknown` (indeterminada): nueva, recién rotada o el comprobador no puede
  saberlo.
- `connected`: la última comprobación fue correcta y no venció.
- `expired`: pasó `expiresAt` (cargado por el administrador o informado por el
  comprobador) o el comprobador lo informó.
- `error`: la comprobación falló o la credencial cifrada no se pudo descifrar.

### Concurrencia y aislamiento

Cada sesión tiene su propio cifrado, estado y revisión. Las ediciones usan
`rev` (control optimista, 409 ante conflicto). Hay una sola comprobación en
curso por sesión; las de sesiones distintas son independientes. Si la
credencial se rota mientras una comprobación está en curso, su resultado se
descarta (no marca como conectada una credencial que no se comprobó).

### Recarga

El interruptor y los estados se leen **en cada solicitud** al perfil: cambios
del panel se aplican sin reiniciar. La entrega de la credencial a DSH no está
implementada (pendiente de la auditoría), así que no hay recarga de DSH que
coordinar: si DSH no soporta recarga, se definirá una actualización controlada
cuando se conozca el contrato.

## Reglas de respaldo (DSH → InferX)

**Habilitación (nuevo):** InferX se usa como respaldo **solo** si se cumplen
las tres condiciones: hay secreto de InferX configurado, el interruptor
«Respaldo InferX» del panel está encendido **y** existe al menos una sesión
DeepSeek Web activa en estado «conectada» (no vencida). Si falta cualquiera, el
error de DSH se devuelve al cliente (por ejemplo 503) y no se llama a InferX.
Antes de este cambio bastaba con el secreto de InferX.

Con el respaldo habilitado, se pasa a InferX **solo** si DSH falla **antes** de
que empiece la respuesta, es decir, antes de recibir el primer byte de un
cuerpo 2xx:

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

### Revertir las sesiones DeepSeek Web

1. **Cortar el respaldo pago al instante**: apagar «Respaldo InferX» en el
   panel (o `PUT /api/admin/deepseek-web/fallback` con `{"enabled": false}`).
   Efecto inmediato, sin reiniciar.
2. **Desactivar o eliminar sesiones** desde el panel. Eliminar borra la
   credencial cifrada.
3. **Borrado total**: detener Capimux y borrar `DATA_DIR/deepseek-web/`
   (estado y clave). Sin ese directorio el respaldo InferX queda apagado y el
   panel arranca vacío.
4. **Quitar el código**: revertir el merge del PR. El directorio
   `DATA_DIR/deepseek-web/` queda huérfano y se puede borrar; no hay cambios en
   la base de datos. Al revertir vuelve el comportamiento anterior: con secreto
   de InferX el respaldo se activa sin interruptor.

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
  stream con respaldo (habilitado vía API de administración con sesión de
  prueba y comprobador simulado), 401 sin respaldo, apagado igual que antes,
  API de sesiones rechaza API keys aunque el login esté apagado, sin
  interruptor no hay respaldo y la credencial no aparece en respuestas ni logs.
- `internal/deepseekweb`: cifrado en reposo y permisos, nonce distinto, cifrado
  ligado a la sesión, clave incorrecta, aislamiento multicuenta, expiración,
  rotación (recifrado, estado reiniciado, comprobación vieja descartada),
  concurrencia (una comprobación por sesión, ediciones con `rev`, `-race`),
  respaldo requiere interruptor y sesión conectada, comprobador por defecto
  nunca conecta, detalle redactado, validación, permisos heredados, clave
  externa, rotación de clave, la compuerta no crea archivos; API: 401 con API
  key/anónimo/token o cookie falsos, flujo completo sin fugas en respuestas ni
  logs, cuerpo excesivo.
- `internal/deepseekprofile` (`gate_test.go`): compuerta cerrada bloquea el
  respaldo ante 429/502/503/504 y error de conexión; abierta permite y se lee
  en vivo; no se consulta ante éxito ni errores permanentes; abierta, un stream
  iniciado sigue sin reintentarse.
- `internal/handlers/dashboard`: la exportación de la base no incluye
  credencial, cifrado, clave ni id de sesión.
- `web`: cliente sin `Authorization` (solo cookie), `rev`, errores, etiquetas
  de estado, rutas del panel.

Todas las credenciales de prueba son valores falsos.
