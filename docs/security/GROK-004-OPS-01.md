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

## Procedimiento del dueño (no lo hace el bot)

Hacerlo en una máquina de confianza, fuera del repo. **Nunca** commitear, subir a
GitHub Secrets sin decidirlo, pegar en un chat ni imprimir la clave privada.

1. Generar la clave (OpenSSL 3):

   ```sh
   umask 077
   openssl genpkey -algorithm ed25519 -out capimux-release-2026.pem
   ```

2. Obtener la clave pública (32 bytes, base64) y embeberla:

   ```sh
   openssl pkey -in capimux-release-2026.pem -pubout -outform DER | tail -c 32 | base64 -w0
   ```

   En `internal/updater/signed_manifest_trust.go`:

   ```go
   var embeddedManifestPublicKeys = map[string]string{
       "capimux-release-2026": "<base64 de 32 bytes>",
   }
   ```

   Ojo: desde el primer build con la clave, las actualizaciones **solo** se
   instalan desde `version.signed.json`. Publicar el manifest firmado antes (o junto
   con) ese build.

3. Por cada release, armar `payload.json`. Se firman exactamente los bytes del
   archivo, y esos mismos bytes van en `payload`:

   ```json
   {"schema":1,"product":"capimux","channel":"stable","version":"1.9.7",
    "issuedAt":"2026-11-01T12:00:00Z","expiresAt":"2026-11-15T12:00:00Z",
    "assets":{"linux_arm64":{"url":"https://github.com/joselofarias-byte/capimux/releases/download/v1.9.7/9router-go-linux-arm64",
                             "sha256":"<sha256 del ejecutable>","size":12345678}}}
   ```

   Claves de plataforma `GOOS_GOARCH` (`linux_arm64`, `android_arm64`, …).
   `expiresAt - issuedAt` ≤ 30 días: hay que volver a firmar antes de que venza.

4. Firmar y armar el sobre:

   ```sh
   { printf 'capimux-update-manifest-v1\n'; cat payload.json; } > message.bin
   openssl pkeyutl -sign -inkey capimux-release-2026.pem -rawin -in message.bin -out sig.bin
   printf '{"payload":"%s","signatures":[{"keyId":"capimux-release-2026","sig":"%s"}]}' \
     "$(base64 -w0 payload.json)" "$(base64 -w0 sig.bin)" > version.signed.json
   ```

   (Procedimiento probado contra `verifySignedManifest` con una clave descartable.)

5. Publicar `version.signed.json` en `main` (URL por defecto
   `https://raw.githubusercontent.com/joselofarias-byte/capimux/main/version.signed.json`)
   junto con los binarios del release.

6. Rotación: agregar la clave nueva al mapa (pueden convivir dos key IDs), publicar
   un build con ambas, firmar con la nueva y luego quitar la vieja. Revocación de una
   clave comprometida: quitarla del mapa y publicar un build nuevo; los builds viejos
   la siguen aceptando hasta actualizarse.

## Pendientes / no objetivos

- No genera claves reales, no publica releases, no toca el despliegue.
- Anti-rollback persistente entre reinicios (más allá de "versión mayor que la
  instalada") y la firma automatizada en CI quedan para una decisión posterior.
