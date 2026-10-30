# GROK-004 / OPS-01 — Cadena de confianza del updater

## Qué hace el código

- **SHA-256 obligatorio:** sin un digest válido de 64 caracteres hex no hay descarga
  ni reemplazo; el digest se compara contra el **ejecutable extraído**.
- **Manifest firmado (Ed25519, stdlib `crypto/ed25519`):** `verifySignedManifest`
  valida la firma sobre `capimux-update-manifest-v1\n` + payload (separación de
  dominio), el key ID, `schema`, `product` (`capimux`), `channel` (`stable`),
  versión semver estricta **mayor** que la instalada (anti-downgrade/replay),
  vigencia (`issuedAt`/`expiresAt`, máximo 30 días, 5 min de tolerancia de reloj),
  URL del asset solo HTTPS, tamaño y SHA-256 del asset.
- **Clave pública embebida:** `embeddedManifestPublicKeys` en
  `internal/updater/signed_manifest_trust.go` (key ID → clave pública Ed25519 de
  32 bytes en base64). Se compila en el binario; nada de la red la puede agregar,
  cambiar ni desactivar.
- **Activación sin fallback inseguro:** si el build tiene al menos una clave
  embebida, `CheckUpdate` (usado por el auto-update, el CLI y el endpoint de
  actualización del panel) consulta **solo** el manifest firmado
  (`DefaultSignedManifestURL`, o `UPDATE_SIGNED_MANIFEST_URL`, solo HTTPS, sin
  redirecciones a otro host ni a HTTP, máximo 512 KiB). Firma ausente, alterada,
  de otra clave, key ID desconocido, payload alterado, manifest vencido o una
  clave embebida malformada → error, sin consultar `version.json` sin firmar ni la
  API de GitHub Releases. Un manifest auténtico con versión igual o menor a la
  instalada se informa como "sin actualización" y nunca se ofrece para instalar.
- **Mientras no haya clave embebida** (estado actual del repo) el comportamiento es
  el anterior: `version.json`/GitHub Releases con SHA-256 obligatorio. Eso da
  *integridad*, **no autenticidad del publicador**: OPS-01 queda totalmente
  mitigado recién cuando el dueño embebe su clave pública y publica el manifest
  firmado.

## Pruebas de manipulación

`internal/updater/signed_manifest_tamper_test.go` (claves generadas dentro de cada
test; ninguna clave real en el repo) cubre: manifest alterado (payload re-codificado
y un byte cambiado), firma alterada y truncada, firma de otra clave, key ID
desconocido, firma ausente (lista vacía y campo ausente), `version.json` sin firmar
servido en la URL firmada, firma sin el contexto de dominio, manifest vencido
(replay), URL HTTP, downgrade y replay de la misma versión, binario alterado
(el digest firmado rechaza el ejecutable distinto en `PerformSelfUpdate`), clave
embebida malformada y la compatibilidad del camino sin clave. En todos los rechazos
se verifica además que no hubo fallback a las fuentes sin firmar.

## Publicación del manifest firmado (automatizada)

El workflow `.github/workflows/release.yml` genera y publica `version.signed.json`
en cada tag `v*`, junto con los binarios:

1. **Primer paso del job:** si el secreto `CAPIMUX_MANIFEST_SIGNING_KEY` está vacío o
   no existe, el job falla con un error explícito antes de compilar. Sin secreto no
   hay release.
2. `make cross` compila los binarios y `SHA256SUMS.txt`; se genera `version.json`
   (camino sin firma, para builds viejos).
3. **«Sign update manifest (Ed25519)»:** `go run ./cmd/release-manifest -signed-out
   version.signed.json` arma el payload (versión semver estable, `issuedAt`,
   `expiresAt` = emisión + 30 días, y por plataforma URL HTTPS, SHA-256 y tamaño,
   cruzados contra `SHA256SUMS.txt`), lo firma con la clave del secreto y **antes de
   escribirlo** comprueba que:
   - la clave corresponde a una clave pública embebida en ese mismo commit
     (`embeddedManifestPublicKeys`); si no hay ninguna embebida, o es otra, falla;
   - el sobre verifica con `verifySignedManifest` para cada plataforma.
   La clave nunca se imprime: se lee de la variable de entorno de ese paso y los
   mensajes de error no incluyen su contenido.
4. La release se crea como **borrador** con todos los assets (binarios,
   `SHA256SUMS.txt`, `version.json`, `version.signed.json`;
   `fail_on_unmatched_files: true`) y se publica recién después de comprobar que
   `version.signed.json` está adjunto. Así `releases/latest` nunca muestra binarios
   sin su manifest firmado.
5. La imagen Docker (`ghcr.io/joselofarias-byte/capimux`, paquete **privado**) se
   construye solo si todo lo anterior pasó (`needs: binaries`).

`TestReleaseWorkflowPublishesSignedManifestWithBinaries` verifica ese orden en el
YAML, y `TestSignedRelease*` (en `internal/updater/signed_manifest_publish_test.go`)
prueban de punta a punta el generador con claves efímeras en memoria: aceptación,
SHA-256, caducidad, payload/hash/caducidad alterados, firma alterada, clave no
embebida, binario reemplazado después de firmar y negativa a firmar sin clave
embebida.

**URL del manifest:** `DefaultSignedManifestURL` es
`https://github.com/joselofarias-byte/capimux/releases/latest/download/version.signed.json`
(el asset de la última release estable). La única redirección entre hosts que se
sigue es la de GitHub, de `github.com` a `release-assets.githubusercontent.com` u
`objects.githubusercontent.com`. Cualquier otra, o una a HTTP, se rechaza. La
autenticidad la da la firma, no el host.

**Caducidad:** el manifest vale 30 días (máximo del verificador). Pasado ese plazo,
los builds con clave informan un error y no actualizan (fail-closed) hasta que haya
una release nueva o se vuelva a firmar (ver el checklist).

## Procedimiento del dueño (no lo hace el bot)

Los pasos exactos (generar la clave fuera del repo, configurar el secreto, embeber
la pública, primer tag) están en [`docs/RELEASE-CHECKLIST.es.md`](../RELEASE-CHECKLIST.es.md).
Resumen:

1. Generar la clave en una máquina de confianza (OpenSSL 3):

   ```sh
   umask 077
   openssl genpkey -algorithm ed25519 -out capimux-release-2026.pem
   ```

   **Nunca** commitear la clave privada, pegarla en un chat ni imprimirla.

2. Cargar el PEM completo como secreto de Actions `CAPIMUX_MANIFEST_SIGNING_KEY`
   (`gh secret set CAPIMUX_MANIFEST_SIGNING_KEY < capimux-release-2026.pem`).

3. Obtener la clave pública (32 bytes, base64, 44 caracteres) y embeberla:

   ```sh
   openssl pkey -in capimux-release-2026.pem -pubout -outform DER | tail -c 32 | base64 -w0
   ```

   En `internal/updater/signed_manifest_trust.go`:

   ```go
   var embeddedManifestPublicKeys = map[string]string{
       "capimux-release-2026": "<base64 de 32 bytes>",
   }
   ```

   Key ID: 3 a 64 caracteres `[a-z0-9._-]`. `TestEmbeddedManifestPublicKeysWellFormed`
   rechaza en CI un formato incorrecto. Desde el primer build con la clave, las
   actualizaciones **solo** se instalan desde `version.signed.json`; el workflow
   garantiza que ese build sale con su manifest firmado.

4. **Firma manual (offline), solo si hace falta re-firmar** una release ya
   publicada (por ejemplo, porque el manifest venció) sin pasar el secreto por CI:

   ```sh
   gh release download vX.Y.Z -p '9router-go-*' -p SHA256SUMS.txt -D rel && cd rel
   go run ../cmd/release-manifest -version vX.Y.Z \
     -base-url https://github.com/joselofarias-byte/capimux/releases/download/vX.Y.Z \
     -sums SHA256SUMS.txt -out "" -signed-out version.signed.json \
     -signing-key-file /ruta/segura/capimux-release-2026.pem 9router-go-*
   gh release upload vX.Y.Z version.signed.json --clobber
   ```

   La herramienta tiene que correr desde el commit del tag (las claves embebidas
   deben coincidir). Alternativa sin la herramienta, solo OpenSSL (probada contra
   `verifySignedManifest` con una clave descartable):

   ```sh
   { printf 'capimux-update-manifest-v1\n'; cat payload.json; } > message.bin
   openssl pkeyutl -sign -inkey capimux-release-2026.pem -rawin -in message.bin -out sig.bin
   printf '{"payload":"%s","signatures":[{"keyId":"capimux-release-2026","sig":"%s"}]}' \
     "$(base64 -w0 payload.json)" "$(base64 -w0 sig.bin)" > version.signed.json
   ```

   Formato de `payload.json`:

   ```json
   {"schema":1,"product":"capimux","channel":"stable","version":"1.9.7",
    "issuedAt":"2026-11-01T12:00:00Z","expiresAt":"2026-11-15T12:00:00Z",
    "assets":{"linux_arm64":{"url":"https://github.com/joselofarias-byte/capimux/releases/download/v1.9.7/9router-go-linux-arm64",
                             "sha256":"<sha256 del ejecutable>","size":12345678}}}
   ```

5. Rotación: agregar la clave nueva al mapa (pueden convivir dos key IDs), publicar
   un build con ambas, cambiar el secreto a la nueva y luego quitar la vieja.
   Revocación de una clave comprometida: quitarla del mapa, cambiar el secreto y
   publicar un build nuevo; los builds viejos la siguen aceptando hasta actualizarse.

## Pendientes / no objetivos

- El bot no genera claves reales, no publica releases ni tags y no toca el
  despliegue ni la visibilidad del paquete GHCR (sigue privado).
- Anti-rollback persistente entre reinicios (más allá de "versión mayor que la
  instalada") queda para una decisión posterior.
