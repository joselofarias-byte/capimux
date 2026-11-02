<script lang="ts">
  import { onMount } from 'svelte'
  import Card from '../../lib/ui/Card.svelte'
  import Button from '../../lib/ui/Button.svelte'
  import Toggle from '../../lib/ui/Toggle.svelte'
  import { notifications } from '../../lib/notifications'
  import { dswApi, statusLabel, formatCheck, type WebSessionsState, type WebSession } from '../../api/deepseekWeb'

  // TODO(auditoría DSH): un único campo opaco de credencial hasta que la
  // auditoría de dsh-better-bridge confirme los campos reales de la sesión.

  let st = $state<WebSessionsState>({ sessions: [], fallback: { requested: false, effective: false }, schemaPending: true })
  let loading = $state(true)
  let error = $state('')
  let busy = $state<Record<string, boolean>>({})

  let newLabel = $state('')
  let newCredential = $state('')
  let creating = $state(false)

  let rotating = $state<string | null>(null)
  let rotateValue = $state('')

  function msg(err: unknown): string {
    return err instanceof Error ? err.message : String(err)
  }

  async function load() {
    try {
      st = await dswApi.state()
      error = ''
    } catch (err) {
      error = msg(err)
    } finally {
      loading = false
    }
  }

  async function run(id: string, fn: () => Promise<unknown>, ok: string) {
    busy[id] = true
    try {
      await fn()
      notifications.success(ok)
    } catch (err) {
      notifications.error(msg(err))
    } finally {
      busy[id] = false
      await load()
    }
  }

  async function create() {
    if (!newLabel.trim() || !newCredential.trim()) {
      notifications.error('Nombre y credencial son obligatorios')
      return
    }
    creating = true
    try {
      await dswApi.create(newLabel.trim(), newCredential)
      notifications.success('Sesión agregada (estado indeterminado hasta comprobarla)')
      newLabel = ''
      newCredential = ''
      await load()
    } catch (err) {
      notifications.error(msg(err))
    } finally {
      newCredential = ''
      creating = false
    }
  }

  async function rotate(s: WebSession) {
    const value = rotateValue
    rotateValue = ''
    rotating = null
    if (!value.trim()) return
    await run(s.id, () => dswApi.update(s.id, s.rev, { credential: value }), 'Credencial actualizada; volver a comprobar')
  }

  function remove(s: WebSession) {
    if (!confirm(`¿Eliminar la sesión «${s.label}»? La credencial cifrada se borra.`)) return
    run(s.id, () => dswApi.remove(s.id), 'Sesión eliminada')
  }

  async function toggleFallback(on: boolean) {
    try {
      st.fallback = await dswApi.setFallback(on)
      notifications.success(on ? 'Respaldo InferX solicitado' : 'Respaldo InferX desactivado')
    } catch (err) {
      notifications.error(msg(err))
      await load()
    }
  }

  const badge: Record<string, string> = {
    connected: 'bg-green-500/15 text-green-600',
    expired: 'bg-amber-500/15 text-amber-600',
    error: 'bg-red-500/15 text-red-600',
    unknown: 'bg-surface-2 text-text-muted',
  }

  onMount(load)
</script>

<div class="mx-auto max-w-5xl space-y-6 p-4 md:p-6" data-testid="deepseek-web-view">
  <div>
    <h1 class="text-2xl font-semibold text-text-main">DeepSeek Web</h1>
    <p class="mt-1 text-sm text-text-muted">
      Sesiones web de DeepSeek para el perfil DSH → InferX (apagado por defecto). La credencial se guarda cifrada y
      nunca se vuelve a mostrar. La clave del bridge y la sesión web son cosas distintas. Solo el administrador
      accede a esta sección.
    </p>
    {#if st.schemaPending}
      <p class="mt-2 rounded-md bg-amber-500/10 p-3 text-xs text-amber-700" data-testid="schema-pending">
        Pendiente: los campos reales de la sesión dependen de la auditoría de DSH. Por ahora la credencial es un único
        valor opaco y la comprobación devuelve «Indeterminada».
      </p>
    {/if}
  </div>

  {#if error}<p class="text-sm text-red-500">{error}</p>{/if}

  <Card title="Agregar sesión">
    <form class="grid gap-3 md:grid-cols-[1fr_2fr_auto]" onsubmit={(e) => { e.preventDefault(); create() }}>
      <input class="rounded-md border border-border bg-surface px-3 py-2 text-sm" placeholder="Nombre (p. ej. cuenta principal)"
        maxlength="80" bind:value={newLabel} aria-label="Nombre de la sesión" />
      <input class="rounded-md border border-border bg-surface px-3 py-2 font-mono text-sm" type="password"
        autocomplete="off" spellcheck="false" placeholder="Credencial de sesión (se guarda cifrada)"
        bind:value={newCredential} aria-label="Credencial de sesión" data-testid="credential-input" />
      <Button type="submit" loading={creating} icon="add">Agregar</Button>
    </form>
  </Card>

  <Card title="Sesiones">
    {#if loading}
      <p class="text-sm text-text-muted">Cargando…</p>
    {:else if st.sessions.length === 0}
      <p class="text-sm text-text-muted">No hay sesiones. Sin una sesión conectada el respaldo InferX no se usa.</p>
    {:else}
      <ul class="divide-y divide-border">
        {#each st.sessions as s (s.id)}
          <li class="flex flex-col gap-2 py-3 md:flex-row md:items-center md:justify-between">
            <div class="min-w-0">
              <p class="text-sm font-medium text-text-main">
                {s.label}
                <span class="ml-2 rounded-full px-2 py-0.5 text-xs {badge[s.status] ?? badge.unknown}">{statusLabel(s.status)}</span>
                {#if !s.enabled}<span class="ml-1 text-xs text-text-muted">(desactivada)</span>{/if}
              </p>
              <p class="text-xs text-text-muted">
                Credencial: <span class="font-mono">{s.hasCredential ? s.credential : '—'}</span> ·
                Último chequeo: {formatCheck(s.lastCheck)}
                {#if s.expiresAt} · Vence: {formatCheck(s.expiresAt)}{/if}
              </p>
              {#if s.lastError}<p class="text-xs text-red-500">{s.lastError}</p>{/if}
              {#if rotating === s.id}
                <form class="mt-2 flex gap-2" onsubmit={(e) => { e.preventDefault(); rotate(s) }}>
                  <input class="flex-1 rounded-md border border-border bg-surface px-3 py-1.5 font-mono text-sm" type="password"
                    autocomplete="off" spellcheck="false" placeholder="Nueva credencial" bind:value={rotateValue}
                    aria-label="Nueva credencial" />
                  <Button size="sm" type="submit">Guardar</Button>
                  <Button size="sm" variant="ghost" onclick={() => { rotating = null; rotateValue = '' }}>Cancelar</Button>
                </form>
              {/if}
            </div>
            <div class="flex shrink-0 flex-wrap gap-2">
              <Button size="sm" variant="outline" icon="sync" loading={busy[s.id]} disabled={!s.enabled}
                onclick={() => run(s.id, () => dswApi.check(s.id), 'Comprobación terminada')}>Comprobar</Button>
              <Button size="sm" variant="outline" icon="key" onclick={() => { rotating = s.id; rotateValue = '' }}>Actualizar</Button>
              <Button size="sm" variant="outline" icon={s.enabled ? 'pause' : 'play_arrow'}
                onclick={() => run(s.id, () => dswApi.update(s.id, s.rev, { enabled: !s.enabled }), s.enabled ? 'Sesión desactivada' : 'Sesión activada')}>
                {s.enabled ? 'Desactivar' : 'Activar'}
              </Button>
              <Button size="sm" variant="danger" icon="delete" onclick={() => remove(s)}>Eliminar</Button>
            </div>
          </li>
        {/each}
      </ul>
    {/if}
  </Card>

  <Card title="Respaldo InferX (facturable)">
    <div class="flex items-center justify-between gap-4">
      <div>
        <p class="text-sm font-medium text-text-main">Permitir respaldo en InferX</p>
        <p class="text-xs text-text-muted">
          Apagado por defecto. Solo se usa si este interruptor está encendido <strong>y</strong> hay al menos una sesión
          activa en estado «Conectada». Tener la clave de InferX configurada no alcanza.
        </p>
        <p class="mt-1 text-xs {st.fallback.effective ? 'text-green-600' : 'text-text-muted'}" data-testid="fallback-effective">
          {st.fallback.effective ? 'Efectivo ahora' : (st.fallback.reason ?? 'No efectivo')}
        </p>
      </div>
      <span class="shrink-0"><Toggle label="Permitir respaldo InferX" checked={st.fallback.requested} onChange={toggleFallback} /></span>
    </div>
  </Card>
</div>
