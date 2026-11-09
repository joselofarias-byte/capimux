<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import Card from '../../lib/ui/Card.svelte'
  import Button from '../../lib/ui/Button.svelte'
  import Toggle from '../../lib/ui/Toggle.svelte'
  import Modal from '../../lib/ui/Modal.svelte'
  import { notifications } from '../../lib/notifications'
  import {
    kqApi,
    PERIOD_LABELS,
    periodTokens,
    periodRequests,
    periodLimit,
    formatNumber,
    type KeyQuotaState,
    type KeyQuotaRow,
    type KeyLimits,
    type Period,
  } from '../../api/keyQuotas'

  let st = $state<KeyQuotaState>({ enabled: false, keys: [] })
  let loading = $state(true)
  let error = $state('')
  let period = $state<Period>('24h')
  let timer: ReturnType<typeof setInterval> | null = null

  let editing = $state<KeyQuotaRow | null>(null)
  let form = $state<Record<keyof Omit<KeyLimits, 'updatedAt'>, string>>({
    rpm: '', tpm: '', concurrency: '', tokens24h: '', tokens7d: '', tokens30d: '',
  })
  let saving = $state(false)

  const totals = $derived({
    tokens: st.keys.reduce((s, k) => s + periodTokens(k.usage, period), 0),
    requests: st.keys.reduce((s, k) => s + periodRequests(k.usage, period), 0),
    inflight: st.keys.reduce((s, k) => s + (k.usage?.inflight || 0), 0),
    limited: st.keys.filter((k) => Object.entries(k.limits || {}).some(([f, v]) => f !== 'updatedAt' && Number(v) > 0)).length,
  })

  async function load() {
    try {
      st = await kqApi.state()
      error = ''
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      loading = false
    }
  }

  async function toggle(on: boolean) {
    try {
      await kqApi.setEnabled(on)
      st.enabled = on
      notifications.success(on ? 'Cuotas por API key activadas' : 'Cuotas por API key desactivadas')
    } catch (err) {
      notifications.error(err instanceof Error ? err.message : String(err))
    }
  }

  function openEdit(row: KeyQuotaRow) {
    editing = row
    const l = row.limits || {}
    form = {
      rpm: l.rpm ? String(l.rpm) : '',
      tpm: l.tpm ? String(l.tpm) : '',
      concurrency: l.concurrency ? String(l.concurrency) : '',
      tokens24h: l.tokens24h ? String(l.tokens24h) : '',
      tokens7d: l.tokens7d ? String(l.tokens7d) : '',
      tokens30d: l.tokens30d ? String(l.tokens30d) : '',
    }
  }

  function parse(v: string): number | undefined {
    const n = Number(String(v).trim() || '0')
    if (!Number.isFinite(n) || n < 0 || !Number.isInteger(n)) throw new Error('Los límites deben ser enteros mayores o iguales a 0')
    return n || undefined
  }

  async function save() {
    if (!editing) return
    saving = true
    try {
      const limits: KeyLimits = {
        rpm: parse(form.rpm), tpm: parse(form.tpm), concurrency: parse(form.concurrency),
        tokens24h: parse(form.tokens24h), tokens7d: parse(form.tokens7d), tokens30d: parse(form.tokens30d),
      }
      await kqApi.save(editing.id, limits)
      notifications.success('Límites guardados')
      editing = null
      await load()
    } catch (err) {
      notifications.error(err instanceof Error ? err.message : String(err))
    } finally {
      saving = false
    }
  }

  async function clearLimits(row: KeyQuotaRow) {
    try {
      await kqApi.remove(row.id)
      notifications.success('Límites quitados')
      await load()
    } catch (err) {
      notifications.error(err instanceof Error ? err.message : String(err))
    }
  }

  function pct(used: number, limit: number): number {
    if (!limit) return 0
    return Math.min(100, Math.round((used / limit) * 100))
  }

  onMount(() => {
    load()
    timer = setInterval(load, 10000)
  })
  onDestroy(() => {
    if (timer) clearInterval(timer)
  })

  const FIELDS: { key: keyof typeof form; label: string; hint: string }[] = [
    { key: 'rpm', label: 'Solicitudes por minuto (RPM)', hint: 'Ventana móvil de 60 s' },
    { key: 'tpm', label: 'Tokens por minuto (TPM)', hint: 'Ventana móvil de 60 s' },
    { key: 'concurrency', label: 'Solicitudes simultáneas', hint: 'En curso al mismo tiempo' },
    { key: 'tokens24h', label: 'Tokens en 24 h', hint: 'Ventana móvil, por hora' },
    { key: 'tokens7d', label: 'Tokens en 7 días', hint: 'Ventana móvil, por hora' },
    { key: 'tokens30d', label: 'Tokens en 30 días', hint: 'Ventana móvil, por hora' },
  ]
</script>

<div class="mx-auto max-w-5xl space-y-6 p-4 md:p-6" data-testid="key-quotas-view">
  <div>
    <h1 class="text-2xl font-semibold text-text-main">Cuotas por API key</h1>
    <p class="mt-1 text-sm text-text-muted">
      Límites de solicitudes, tokens y concurrencia para cada API key de cliente. Al superarlos, Capimux responde 429 con
      <span class="font-mono">Retry-After</span> sin llamar al proveedor. Solo el administrador puede verlos o cambiarlos.
    </p>
  </div>

  {#if error}<p class="text-sm text-red-500">{error}</p>{/if}

  <Card title="Interruptor">
    <div class="flex items-center justify-between gap-4">
      <div>
        <p class="text-sm font-medium text-text-main">Aplicar cuotas por API key</p>
        <p class="text-xs text-text-muted">Apagado por defecto. Con el interruptor apagado no se cuenta ni se limita nada.</p>
      </div>
      <span class="shrink-0"><Toggle label="Aplicar cuotas por API key" checked={st.enabled} onChange={toggle} /></span>
    </div>
  </Card>

  <div class="flex flex-wrap gap-2" role="tablist" aria-label="Período">
    {#each Object.entries(PERIOD_LABELS) as [p, label]}
      <button type="button" role="tab" aria-selected={period === p}
        class="rounded-full px-4 py-1.5 text-sm {period === p ? 'bg-brand-500 text-white' : 'bg-surface-2 text-text-muted'}"
        onclick={() => (period = p as Period)}>{label}</button>
    {/each}
  </div>

  <div class="grid grid-cols-2 gap-3 md:grid-cols-4">
    <div class="rounded-xl border border-border bg-surface-1 p-4">
      <p class="text-xs text-text-muted">Tokens ({PERIOD_LABELS[period]})</p>
      <p class="mt-1 text-2xl font-semibold text-text-main">{formatNumber(totals.tokens)}</p>
    </div>
    <div class="rounded-xl border border-border bg-surface-1 p-4">
      <p class="text-xs text-text-muted">Solicitudes ({PERIOD_LABELS[period]})</p>
      <p class="mt-1 text-2xl font-semibold text-text-main">{formatNumber(totals.requests)}</p>
    </div>
    <div class="rounded-xl border border-border bg-surface-1 p-4">
      <p class="text-xs text-text-muted">En curso ahora</p>
      <p class="mt-1 text-2xl font-semibold text-text-main">{totals.inflight}</p>
    </div>
    <div class="rounded-xl border border-border bg-surface-1 p-4">
      <p class="text-xs text-text-muted">Keys con límites</p>
      <p class="mt-1 text-2xl font-semibold text-text-main">{totals.limited} / {st.keys.length}</p>
    </div>
  </div>

  <Card title="API keys" subtitle="Uso contado por Capimux desde que se activó el interruptor. El costo en dinero está en la sección Usage.">
    {#if loading}
      <p class="text-sm text-text-muted">Cargando…</p>
    {:else if st.keys.length === 0}
      <p class="text-sm text-text-muted">No hay API keys de cliente.</p>
    {:else}
      <div class="space-y-3">
        {#each st.keys as row (row.id)}
          {@const used = periodTokens(row.usage, period)}
          {@const lim = periodLimit(row.limits, period)}
          <div class="rounded-lg border border-border p-3" data-testid="kq-row">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <div class="min-w-0">
                <p class="truncate text-sm font-medium text-text-main">{row.name || row.id}
                  {#if !row.active}<span class="ml-2 text-xs text-text-muted">(inactiva)</span>{/if}</p>
                <p class="text-xs text-text-muted">
                  {periodRequests(row.usage, period)} solicitudes · {row.usage?.rpm || 0} en el último minuto · {row.usage?.inflight || 0} en curso
                </p>
              </div>
              <div class="flex shrink-0 gap-2">
                <Button size="sm" variant="secondary" onclick={() => openEdit(row)}>Límites</Button>
                {#if lim || row.limits?.rpm || row.limits?.tpm || row.limits?.concurrency || row.limits?.tokens24h || row.limits?.tokens7d || row.limits?.tokens30d}
                  <Button size="sm" variant="ghost" onclick={() => clearLimits(row)}>Quitar</Button>
                {/if}
              </div>
            </div>
            <div class="mt-2">
              <div class="flex justify-between text-xs text-text-muted">
                <span>Tokens {PERIOD_LABELS[period]}: {formatNumber(used)}</span>
                <span>{lim ? `límite ${formatNumber(lim)}` : 'sin límite'}</span>
              </div>
              <div class="mt-1 h-2 w-full overflow-hidden rounded-full bg-surface-3">
                <div class="h-full rounded-full {pct(used, lim) >= 90 ? 'bg-red-500' : 'bg-brand-500'}" style="width: {lim ? pct(used, lim) : 0}%"></div>
              </div>
            </div>
            {#if row.limits?.rpm || row.limits?.tpm || row.limits?.concurrency}
              <p class="mt-2 text-xs text-text-muted">
                {#if row.limits?.rpm}RPM {row.usage?.rpm || 0}/{row.limits.rpm} · {/if}
                {#if row.limits?.tpm}TPM {formatNumber(row.usage?.tpm || 0)}/{formatNumber(row.limits.tpm)} · {/if}
                {#if row.limits?.concurrency}Simultáneas {row.usage?.inflight || 0}/{row.limits.concurrency}{/if}
              </p>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  </Card>
</div>

<Modal isOpen={editing !== null} onClose={() => (editing = null)} title={`Límites · ${editing?.name || editing?.id || ''}`} size="md">
  <div class="grid gap-3 sm:grid-cols-2">
    {#each FIELDS as f}
      <div>
        <label class="block text-xs font-medium text-text-muted" for={`kq-${f.key}`}>{f.label}</label>
        <input id={`kq-${f.key}`} type="number" min="0" step="1" inputmode="numeric" placeholder="Sin límite"
          bind:value={form[f.key]}
          class="w-full rounded-lg border border-border bg-surface-2 px-3 py-2 text-sm text-text-main" />
        <p class="mt-0.5 text-[11px] text-text-muted">{f.hint}</p>
      </div>
    {/each}
  </div>
  <p class="mt-3 text-xs text-text-muted">Dejá un campo vacío o en 0 para no limitarlo.</p>
  {#snippet footer()}
    <Button variant="ghost" onclick={() => (editing = null)}>Cancelar</Button>
    <Button onclick={save} loading={saving}>Guardar</Button>
  {/snippet}
</Modal>
