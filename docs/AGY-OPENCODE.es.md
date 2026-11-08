# Capimux JolufaSoft: AGY y OpenCode

Esta guía configura AGY como proveedor **personalizado OpenAI-compatible**, sin
confundirlo con Antigravity (`ag/`) ni con el proveedor interno `opencode` (`oc/`).
OpenCode aquí es el cliente que consume Capimux, no una cuenta de otro proveedor.

## Evidencia y límites

El **5 de noviembre de 2026 a las 22:31 UTC**, desde el entorno de desarrollo,
se hizo solamente `GET https://agy.jolufasoft.com/v1/models`, por HTTPS, sin
credenciales ni redirecciones. Resultado: **HTTP 401**, JSON:

```json
{"error":{"code":"invalid_api_key","message":"API key validation failed","param":null,"type":"invalid_request_error"}}
```

Esto comprueba que el endpoint respondió y rechazó una solicitud anónima; **no**
prueba un catálogo autenticado ni inferencia. No se dispone de una clave AGY
para esta comprobación. Chat Completions, Responses, herramientas, SSE,
cancelación remota, cuotas, precios y modelos gratuitos de AGY quedan **sin
verificar**. Un catálogo tampoco certifica esas capacidades por modelo.

El código existente de Capimux ofrece:

- Nodos personalizados con `baseUrl`, prefijo independiente y `apiType` `chat`
  o `responses` (`internal/handlers/dashboard/provider_nodes.go`).
- Descubrimiento `GET /api/providers/{connectionId}/models`: consulta
  `{baseUrl}/models` con `Authorization: Bearer <clave de la conexión>`, admite
  `data` o `models` y devuelve los IDs sin una lista AGY codificada
  (`internal/handlers/dashboard/connections.go`). Este endpoint administrativo
  no es el catálogo público OpenAI `GET /v1/models`.
- Traducciones y streaming genéricos en `internal/proxy/executor/`; que existan
  en Capimux **no** implica que AGY acepte esos protocolos.

Las pruebas HTTP locales de `custom_models_http_test.go` usan un proveedor falso,
no AGY: verifican Bearer, IDs arbitrarios intactos, nueva consulta por solicitud,
catálogo vacío, errores 401/429/503, JSON inválido sin exponer el cuerpo remoto
y cancelación de una consulta de modelos. Una prueba adicional del ejecutor
genérico comprueba SSE incremental con un fragmento de herramienta intacto,
Bearer y cancelación sin repetir la solicitud. No certifica un ciclo completo
de herramientas ni las capacidades SSE de AGY.

## Clave autorizada y descubrimiento

Usar solamente una API key emitida para una cuenta autorizada. No incrustarla en
JSON, repositorios, capturas, argumentos de procesos ni mensajes de diagnóstico.
En Bash (también en Termux), cargarla sin historial ni eco:

```bash
set +x
read -r -s -p 'API key autorizada de AGY: ' AGY_API_KEY; printf '\n'
export AGY_API_KEY
```

Consultar el catálogo con este script; el secreto no aparece en argumentos ni
se imprime el cuerpo de errores. No usar `curl -v`, trazas de shell ni volcados
de cabeceras autenticadas. El resultado contiene únicamente IDs de modelos:

```bash
python3 - <<'PY'
import json, os, urllib.error, urllib.request
request = urllib.request.Request(
    'https://agy.jolufasoft.com/v1/models',
    headers={'Authorization': 'Bearer ' + os.environ['AGY_API_KEY']})
try:
    with urllib.request.urlopen(request, timeout=20) as response:
        catalog = json.load(response)
except urllib.error.HTTPError as error:
    raise SystemExit('Consulta rechazada: HTTP ' + str(error.code))
except (urllib.error.URLError, TimeoutError):
    raise SystemExit('No se pudo consultar el catálogo; revisar conectividad/TLS')
for model in catalog.get('data', catalog.get('models', [])):
    if isinstance(model, dict) and isinstance(model.get('id'), str):
        print(model['id'])
PY
```

No se desactiva TLS ni se prueban modelos supuestos. Una respuesta 401 requiere
revisar la clave con su emisor, no rotar cuentas para eludir restricciones.

## Incorporar AGY a Capimux

1. Ejecutar Capimux con `HOST=127.0.0.1` y un puerto elegido (por ejemplo
   `PORT=20130`). Habilitar autenticación administrativa y una API key de cliente;
   no publicar el dashboard ni `/api/*` en Internet.
2. En el dashboard crear un proveedor personalizado **OpenAI-compatible**,
   nombre `AGY JolufaSoft`, prefijo propio `agy` y URL base
   `https://agy.jolufasoft.com/v1` (sin añadir `/chat/completions`). Seleccionar
   `chat` solamente como protocolo a validar inicialmente, no como capacidad
   ya demostrada. No usar el prefijo reservado `ag`.
3. Agregar una conexión API key con la clave autorizada. Es autenticación
   **Bearer API key**, no OAuth; no inventar URLs OAuth, refresh ni revocación.
   Rotar/revocar la clave en el sistema del emisor cuando corresponda.
4. Consultar los modelos de esa conexión e importar/agregar solamente los IDs
   devueltos. El descubrimiento no significa que todos los modelos ya estén
   publicados o habilitados: comprobar después el catálogo de Capimux
   `GET http://127.0.0.1:20130/v1/models` usando la **clave de cliente Capimux**.
   Copiar exactamente los IDs publicados allí, incluidos sus prefijos/aliases.
5. Validar una solicitud mínima de inferencia con consentimiento de consumo.
   La validación del dashboard puede hacer una llamada a `/chat/completions`
   además del catálogo: no asumir que es una operación sin costo.

## OpenCode local: catálogo explícito, sin IDs inventados

La documentación oficial de OpenCode indica `@ai-sdk/openai-compatible` para
Chat Completions y `@ai-sdk/openai` para Responses. Empezar con Chat Completions;
no cambiar a Responses sin pruebas end-to-end con el modelo real.

Configurar `~/.config/opencode/opencode.json` o `opencode.json` del proyecto.
La sintaxis `{env:VARIABLE}` evita guardar la clave literal. Ejemplo de
**plantilla**, que requiere sustituir el marcador por un ID obtenido del
`/v1/models` de Capimux; no es un ID de modelo real:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "capimux": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Capimux JolufaSoft",
      "options": {
        "baseURL": "http://127.0.0.1:20130/v1",
        "apiKey": "{env:CAPIMUX_API_KEY}"
      },
      "models": {
        "<ID_EXACTO_PUBLICADO_POR_CAPIMUX>": {
          "name": "Modelo autorizado descubierto"
        }
      }
    }
  }
}
```

Cargar `CAPIMUX_API_KEY` de forma interactiva como arriba. Esta clave autoriza
al cliente contra Capimux y **no es la clave AGY**. Seleccionar en OpenCode el
proveedor `capimux` y el ID configurado. El mapa `models` debe actualizarse
cuando cambie el catálogo; no se presupone que OpenCode descubra automáticamente
los modelos personalizados. No declarar contextos, precios, visión ni soporte
de herramientas por inferencia del nombre.

## VPS, Android ARM64 y Termux

- **VPS:** mantener Capimux en loopback. Desde el equipo del cliente abrir
  `ssh -N -L 20130:127.0.0.1:20130 usuario@servidor` y conservar el mismo
  `baseURL` local. Autenticar igualmente las solicitudes. Si OpenCode corre en
  el VPS, usar directamente el loopback del VPS. Un despliegue público requiere
  TLS y reglas que excluyan administración; el túnel no exige abrir ese puerto.
- **Termux:** instalar OpenCode siguiendo sus instrucciones vigentes para el
  entorno disponible. Un binario `linux-arm64` no equivale a `android-arm64`;
  no se verificó ejecución Android en esta tarea. La opción de menor dependencia
  es OpenCode en el VPS y sesión SSH desde Termux. Si OpenCode funciona
  nativamente en Termux, `pkg install openssh python` permite el túnel anterior
  y el descubrimiento; mantenerlo activo mientras se usa el cliente.
- Comprobar que el puerto local esté libre. No usar `0.0.0.0` como sustituto
  del túnel. Probar reconexión, suspensión de Android y cancelación antes de
  considerar estable el flujo móvil. No se promete trabajo en segundo plano.

## Privacidad y almacenamiento

La sustitución por entorno evita secretos en el JSON de OpenCode, pero no cifra
el entorno ni la base de datos de Capimux. Las conexiones conservan credenciales
en sus datos SQLite: tratarlos, los WAL y los respaldos como secretos; no se
ha certificado cifrado en reposo. Usar usuario dedicado, directorio de datos
privado (`0700`), archivos de credenciales/respaldos `0600`, volumen cifrado y
acceso administrativo restringido. No aplicar permisos recursivos a ciegas.
OpenCode guarda credenciales de `/connect` en
`~/.local/share/opencode/auth.json`: protegerlo también; esta guía usa entorno.

Mantener desactivado `CAPIMUX_CAPTURE_REQUEST_CONTENT` (no establecerlo en
`true`). `CAPIMUX_REQUEST_DETAILS_RETENTION_DAYS` acepta 1–365, por defecto 7;
se refiere a detalles de solicitudes, no a toda la telemetría ni a respaldos.
El borrado SQLite es lógico, no borrado seguro. Revisar la política en
[privacidad de diagnósticos](security/GROK-004-DATA-01.md). No adjuntar cuerpos,
claves, cookies o tokens a issues. Las estimaciones de consumo no equivalen a
facturación oficial del proveedor.

## Validación pendiente con autorización

Probar cada modelo elegido y registrar únicamente resultado, estado, latencia
y contadores no sensibles:

- Chat no streaming, luego SSE incremental y terminación `[DONE]`.
- Cancelación del cliente y timeout; comprobar que el proveedor deja de generar
  cuando su contrato lo permita, sin prometer reversión de cargos.
- Ciclo de herramienta: `tools`, argumentos, `tool_call_id`, resultado y cierre;
  usar una herramienta inocua, sin ejecutar comandos del modelo automáticamente.
- `/responses` solamente si AGY documenta/acepta ese contrato; verificar eventos
  y traducciones con OpenCode, no sólo un HTTP 200.
- 401, 429 y 5xx sin filtrar cuerpos sensibles. No provocar cuotas reales ni
  repetir solicitudes ya iniciadas para probar failover; usar upstreams falsos.

Prueba local reproducible de catálogo (sin credenciales ni acceso a AGY):

```bash
go test ./internal/handlers/dashboard -run 'TestCustomModelsHTTP|TestCustomProviderSSEHTTP' -count=1
go test -race ./internal/handlers/dashboard -run 'TestCustomModelsHTTP|TestCustomProviderSSEHTTP' -count=1
```

Referencias oficiales consultadas el 2026-11-05:
[proveedores OpenCode](https://opencode.ai/docs/providers/) y
[configuración OpenCode](https://opencode.ai/docs/config/).
