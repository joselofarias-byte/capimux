# Licencia Beta Pro: comportamiento y operación

Este documento describe el cliente de licencias que consolida la cadena de PRs #70→#92. La firma de releases (`docs/security/GROK-004-OPS-01.md`) es **otra** clave, del propietario, y no tiene relación con esto.

## Sin configuración (estado por defecto)

- **No hay servidor de licencias por defecto.** El binario no trae ninguna URL embebida; la del prototipo `workers.dev` se quitó.
- `NINEROUTER_LICENSE_URL` es la única forma de apuntar a un servidor. Tiene que ser `https://`; `http://` solo se acepta en loopback, para pruebas. La URL no puede llevar credenciales, query ni fragmento.
- Sin `NINEROUTER_LICENSE_URL`:
  - `license activate`, `license renew` y `license release` fallan con *"license server not configured"* **antes** de crear o modificar archivos locales, y no abren ninguna conexión.
  - `license status` funciona offline y muestra `Community`.
  - El servidor arranca y opera en Community (`pkg/edition.CommunityProvider`), sin consultar nada externo.
- **Pro nunca se habilita porque falte configuración.** Solo lo habilita un lease firmado por una clave del keyring del canal, ligado a esta instalación y a este build, dentro de su vigencia y con tiempo confiable.

## Servidor caído, lento o con error

| Situación | Resultado |
|---|---|
| Sin lease y servidor caído o lento | Community; la activación falla con error y no se guarda nada |
| Lease válido en caché y servidor caído, con timeout (15 s por defecto) o con 5xx | Se mantiene el lease en caché y Pro sigue activo hasta `expiresAt`, y luego durante la gracia hasta `graceUntil`; la renovación falla sin tocar la caché |
| Lease vencido (pasada la gracia) | Community, sin bloquear la instalación |
| Lease alterado, de otra instalación, de otro build o sin tiempo confiable | Community más un error de diagnóstico (falla cerrado) |
| Reloj atrasado (rollback) | Community |
| Respuesta con redirección (3xx) | Se rechaza: el cliente no sigue redirecciones, para no reenviar el código ni las pruebas a otro host |
| Respuesta mayor a 1 MiB | Se rechaza |

Los errores HTTP del servidor nunca incluyen el cuerpo de la respuesta, que podría repetir el código de activación. El cliente no loguea códigos, tokens ni claves.

## Clave de instalación (prueba de posesión)

- Es una clave Ed25519 **generada localmente** en el primer uso de activar o `status`. Se guarda en `DATA_DIR/license/installation-key`: el archivo tiene permisos 0600 y el directorio 0700. En instalaciones previas con permisos más abiertos, se reajustan al cargar.
- El `installation_id` (`ik1_…`) es la huella SHA-256 de la clave pública. El servidor recibe solo la clave pública y firmas de prueba con separación de dominio (activar, renovar y desvincular). **La clave privada nunca se envía.**
- Si la clave no coincide con el `installation_id`, se rechaza. Si se copian el `installation_id` y el lease sin la clave, no se puede renovar ni desvincular, y renovar o desvincular nunca crean una clave de reemplazo.
- Los IDs legados (UUID, de instalaciones previas a la prueba de posesión) siguen leyéndose. Para ligarse a la clave, hay que reactivarlos.
- **Backups:** el export de la base (descarga y Google Drive) solo contiene tablas de la base de datos y **no** incluye `DATA_DIR/license`. Un backup del sistema de archivos que copie `DATA_DIR` completo sí incluiría la clave; protegelo como cualquier otro secreto o excluí `license/`.

## Android / Termux

Usa solo la biblioteca estándar de Go (`crypto/ed25519`, archivos y bloqueos `O_EXCL`), sin CGO. CI compila `CGO_ENABLED=0 GOOS=android GOARCH=arm64`.

## Reversión

Eliminar `DATA_DIR/license/` deja la instalación en Community. Sin `NINEROUTER_LICENSE_URL`, no se contacta ningún servicio.
