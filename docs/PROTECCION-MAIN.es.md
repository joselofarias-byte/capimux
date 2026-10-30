# Propuesta: protección de la rama `main`

**Estado:** propuesta, **no aplicada**. Aplicarla es un cambio real de
configuración del repositorio y lo decide el propietario. Hoy `main` no tiene
protección (`GET /repos/joselofarias-byte/capimux/branches/main/protection` →
404 «Branch not protected») ni rulesets.

## Qué exige

| Regla | Valor | Motivo |
| --- | --- | --- |
| PR obligatorio | sí | Nada llega a `main` sin PR. |
| Revisiones aprobadas | 1 | «Revisión obligatoria» (decisión del 30-10-2026). |
| Descartar aprobaciones con commits nuevos | sí | La aprobación vale para el head revisado. |
| Checks requeridos | `test`, `Go race detector`, `Integration tests`, `Build Docker image (verify only)` | Los cuatro jobs de `.github/workflows/ci.yml`, emitidos por GitHub Actions (app id 15368). |
| Rama al día con `main` (`strict`) | sí | El CI tiene que haber corrido sobre la combinación real. |
| Conversaciones resueltas | sí | |
| Historial lineal | **no** | La política del proyecto es commit de merge (`--merge`); un historial lineal lo prohibiría. |
| Force-push | prohibido | |
| Borrar la rama | prohibido | |
| Aplicar también a administradores (`enforce_admins`) | sí | Sin excepciones silenciosas (ver «Bloqueo» abajo). |

Vercel no se incluye como check requerido: es una vista previa del panel y
depende de un servicio externo.

## Comando exacto (no ejecutado)

```sh
gh api --method PUT \
  -H "Accept: application/vnd.github+json" \
  repos/joselofarias-byte/capimux/branches/main/protection \
  --input - <<'JSON'
{
  "required_status_checks": {
    "strict": true,
    "checks": [
      { "context": "test", "app_id": 15368 },
      { "context": "Go race detector", "app_id": 15368 },
      { "context": "Integration tests", "app_id": 15368 },
      { "context": "Build Docker image (verify only)", "app_id": 15368 }
    ]
  },
  "enforce_admins": true,
  "required_pull_request_reviews": {
    "dismiss_stale_reviews": true,
    "require_code_owner_reviews": false,
    "required_approving_review_count": 1,
    "require_last_push_approval": false
  },
  "restrictions": null,
  "required_linear_history": false,
  "allow_force_pushes": false,
  "allow_deletions": false,
  "required_conversation_resolution": true,
  "block_creations": false,
  "lock_branch": false,
  "allow_fork_syncing": false
}
JSON
```

Verificación posterior (lectura):

```sh
gh api repos/joselofarias-byte/capimux/branches/main/protection \
  --jq '{checks: [.required_status_checks.checks[].context], strict: .required_status_checks.strict, reviews: .required_pull_request_reviews.required_approving_review_count, admins: .enforce_admins.enabled}'
```

Revertir (si hiciera falta): `gh api --method DELETE repos/joselofarias-byte/capimux/branches/main/protection`.

## Bloqueo concreto que hay que decidir antes de aplicarla

Los PR de los agentes se abren con la cuenta `joselofarias-byte`, la misma del
propietario. **GitHub no permite aprobar un PR propio**, así que con
`required_approving_review_count: 1` y `enforce_admins: true` ningún PR de los
agentes se podría mergear. Opciones:

1. **Recomendada:** que los agentes usen una cuenta propia (colaborador con
   permiso *Write*, o una GitHub App) para abrir los PR. El propietario aprueba
   con su cuenta y la regla queda tal cual.
2. Dejar `required_approving_review_count: 0` (PR y checks obligatorios, sin
   aprobación formal). La revisión sigue existiendo, pero no la hace cumplir
   GitHub.
3. `enforce_admins: false`: el propietario puede saltear la aprobación como
   administrador. Es un bypass manual, contrario a «no forzar merges».

Los nombres de los checks tienen que coincidir exactamente con los `name:` de
los jobs de `ci.yml`. Si se renombra un job, hay que actualizar esta regla o
todos los PR quedan esperando un check que ya no existe.
