# Checklist de la primera release estable firmada (Ed25519)

Este checklist lo ejecuta **el propietario**. Ningún agente genera, guarda, pide ni
ve la clave privada. El orden importa: un build con clave pública embebida solo
acepta actualizaciones desde `version.signed.json`, así que el mecanismo de
publicación tiene que estar listo antes de embeber la clave (ya lo está: ver
`docs/security/GROK-004-OPS-01.md`, «Publicación del manifest firmado»).

## 0. Antes de empezar

- [ ] `main` con CI verde (`test`, `Go race detector`, `Integration tests`,
      `Build Docker image (verify only)`).
- [ ] Decidir el número de versión (semver estable `X.Y.Z`, sin `-rc`: el updater
      firmado no acepta pre-releases). El binario hoy dice `1.9.6` (archivo
      `VERSION`); la primera release estable tiene que ser mayor.

## 1. Generar la clave, fuera del repo

En una máquina de confianza (no en el box de los agentes, no dentro del clon):

```sh
umask 077
mkdir -p ~/capimux-keys && cd ~/capimux-keys
openssl genpkey -algorithm ed25519 -out capimux-release-2026.pem
```

- [ ] Guardar una copia de respaldo cifrada (gestor de contraseñas o medio
      offline). Si se pierde, hay que rotar (sección 6 de GROK-004-OPS-01).
- [ ] No commitear, no pegar en chats, no imprimir.

## 2. Configurar el secreto de GitHub Actions

Nombre exacto: **`CAPIMUX_MANIFEST_SIGNING_KEY`**. Valor: el PEM completo
(`-----BEGIN PRIVATE KEY-----` … `-----END PRIVATE KEY-----`).

```sh
gh secret set CAPIMUX_MANIFEST_SIGNING_KEY \
  --repo joselofarias-byte/capimux < ~/capimux-keys/capimux-release-2026.pem
```

(o *Settings → Secrets and variables → Actions → New repository secret*).

- [ ] Opcional y recomendable: guardarlo como secreto del environment `prod` en
      lugar de secreto de repositorio, con revisores requeridos. En ese caso hay
      que agregar `environment: prod` al job `binaries` de `release.yml`.

Sin este secreto el workflow de release falla en su primer paso con
«Falta el secreto CAPIMUX_MANIFEST_SIGNING_KEY» y no publica nada.

## 3. Embeber la clave pública (un PR)

```sh
openssl pkey -in ~/capimux-keys/capimux-release-2026.pem -pubout -outform DER \
  | tail -c 32 | base64 -w0; echo
```

Sale una cadena de **44 caracteres** que termina en `=`. En
`internal/updater/signed_manifest_trust.go`:

```go
var embeddedManifestPublicKeys = map[string]string{
	"capimux-release-2026": "<los 44 caracteres>",
}
```

- [ ] Key ID: 3 a 64 caracteres `[a-z0-9._-]`; el mismo que se use al rotar.
- [ ] `go test ./internal/updater/` pasa
      (`TestEmbeddedManifestPublicKeysWellFormed` rechaza base64 inválido, largo
      distinto de 32 bytes, ceros o un ID mal formado).
- [ ] En el mismo PR, subir la versión: `./scripts/bump-version.sh X.Y.Z`
      (actualiza `VERSION`, que es lo que el binario reporta). El workflow exige
      que el tag sea exactamente `v` + `VERSION`.
- [ ] CHANGELOG con una sección `## [vX.Y.Z]` (se usa como notas de la release).
- [ ] CI verde y merge (commit de merge, sin squash ni rebase).

## 4. Primer tag `v*`

Desde `main` actualizado, con el merge anterior incluido:

```sh
git switch main && git pull --ff-only
cat VERSION                     # debe decir X.Y.Z
git tag -a vX.Y.Z -m "Capimux vX.Y.Z"
git push origin vX.Y.Z
```

El workflow `Release`:

1. falla si falta el secreto o si el tag no coincide con `VERSION`;
2. compila los seis binarios y `SHA256SUMS.txt`;
3. genera `version.json` y firma `version.signed.json` (vigencia 30 días),
   verificándolo contra la clave embebida en ese commit;
4. crea la release como **borrador** con todos los assets y la publica recién
   cuando `version.signed.json` está adjunto;
5. construye y sube la imagen `ghcr.io/joselofarias-byte/capimux`.

## 5. Verificar la release

```sh
gh release view vX.Y.Z --json assets --jq '.assets[].name'
# debe listar 9router-go-*, SHA256SUMS.txt, version.json y version.signed.json
curl -sL https://github.com/joselofarias-byte/capimux/releases/latest/download/version.signed.json \
  | python3 -c 'import sys,json,base64; e=json.load(sys.stdin); print(base64.b64decode(e["payload"]).decode())'
```

- [ ] `version` = `X.Y.Z`, `expiresAt` ≈ emisión + 30 días, un asset por
      plataforma con URL `https://github.com/joselofarias-byte/capimux/releases/download/vX.Y.Z/…`.
- [ ] Un binario de esa release, al buscar actualizaciones, informa «sin
      actualización» (misma versión) con origen `signed_manifest`.

## 6. GHCR: queda privado

El paquete `ghcr.io/joselofarias-byte/capimux` se crea con el primer tag y queda
**privado**. No cambiar la visibilidad todavía (decisión del propietario del
30-10-2026). Si más adelante se publica, es un cambio irreversible en la
práctica: hacerlo desde *Package settings → Change visibility*.

## 7. Mantenimiento

- **Vencimiento:** cada `version.signed.json` vale 30 días. Antes de que venza,
  publicar una release nueva o re-firmar la actual (procedimiento offline en
  GROK-004-OPS-01, paso 4). Vencido, los builds firmados informan error y no
  actualizan (fail-closed), pero siguen funcionando.
- **Rotación / revocación:** GROK-004-OPS-01, paso 5.
