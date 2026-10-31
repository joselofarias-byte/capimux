<script lang="ts">
  // Playground de pruebas: manda solicitudes reales a /v1/chat/completions con
  // una API key de cliente, así que pasan por el mismo pipeline que cualquier
  // cliente (autenticación, inyección, cuotas, ruteo). No toca políticas ni
  // controles de los proveedores. La key se guarda solo en sessionStorage.
  import { onMount } from 'svelte'
  import Card from '../../lib/ui/Card.svelte'
  import Button from '../../lib/ui/Button.svelte'
  import Toggle from '../../lib/ui/Toggle.svelte'
  import { getStoredAPIKey, type Combo } from '../../api/client'

  let { combos = [] }: { combos?: Combo[] } = $props()

  const SESSION_KEY = 'capimux_playground_key'
  interface Msg { role: 'system' | 'user' | 'assistant'; content: string }

  let apiKey = $state('')
  let models = $state<string[]>([])
  let model = $state('')
  let system = $state('')
  let input = $state('Respondé en español: ¿qué es un combo en Capimux?')
  let stream = $state(true)
  let history = $state<Msg[]>([])
  let reply = $state('')
  let replyModel = $state('')
  let status = $state('')
  let error = $state('')
  let sending = $state(false)
  let controller: AbortController | null = null
  let started = 0
  let ttft = $state<number | null>(null)
  let total = $state<number | null>(null)
  let usage = $state<{ prompt_tokens?: number; completion_tokens?: number } | null>(null)

  const comboNames = $derived(new Set(combos.map((c) => c.name)))

  function authHeaders(): Record<string, string> {
    return { 'Content-Type': 'application/json', Authorization: `Bearer ${apiKey.trim()}` }
  }

  async function loadModels() {
    if (!apiKey.trim()) return
    sessionStorage.setItem(SESSION_KEY, apiKey.trim())
    error = ''
    try {
      const res = await fetch('/v1/models', { headers: authHeaders() })
      if (!res.ok) throw new Error(res.status === 401 ? 'API key inválida' : `Error ${res.status}`)
      const data = await res.json()
      const ids: string[] = (data?.data || []).map((m: { id: string }) => m.id).filter(Boolean)
      const extra = combos.map((c) => c.name).filter((n) => !ids.includes(n))
      models = [...extra, ...ids]
      if (!model && models.length) model = models[0]
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    }
  }

  function stop() {
    controller?.abort()
  }

  function reset() {
    history = []
    reply = ''
    replyModel = ''
    status = ''
    error = ''
    usage = null
    ttft = null
    total = null
  }

  async function send() {
    if (!apiKey.trim() || !model || !input.trim() || sending) return
    const user: Msg = { role: 'user', content: input.trim() }
    const messages: Msg[] = [...(system.trim() ? [{ role: 'system' as const, content: system.trim() }] : []), ...history, user]
    history = [...history, user]
    input = ''
    reply = ''
    replyModel = ''
    error = ''
    usage = null
    ttft = null
    total = null
    status = 'Enviando…'
    sending = true
    controller = new AbortController()
    started = performance.now()
    try {
      const res = await fetch('/v1/chat/completions', {
        method: 'POST',
        headers: authHeaders(),
        body: JSON.stringify({ model, messages, stream, ...(stream ? { stream_options: { include_usage: true } } : {}) }),
        signal: controller.signal,
      })
      if (!res.ok) {
        const text = await res.text()
        let msg = text
        try {
          const j = JSON.parse(text)
          msg = j?.error?.message || j?.error || text
        } catch {}
        throw new Error(`${res.status}: ${msg}`)
      }
      if (stream && res.body) {
        status = 'Recibiendo…'
        const reader = res.body.getReader()
        const dec = new TextDecoder()
        let buf = ''
        for (;;) {
          const { value, done } = await reader.read()
          if (done) break
          buf += dec.decode(value, { stream: true })
          let i: number
          while ((i = buf.indexOf('\n')) >= 0) {
            const line = buf.slice(0, i).trim()
            buf = buf.slice(i + 1)
            if (!line.startsWith('data:')) continue
            const payload = line.slice(5).trim()
            if (payload === '[DONE]') continue
            try {
              const chunk = JSON.parse(payload)
              if (chunk.model) replyModel = chunk.model
              if (chunk.usage) usage = chunk.usage
              const delta = chunk.choices?.[0]?.delta?.content
              if (typeof delta === 'string' && delta) {
                if (ttft === null) ttft = Math.round(performance.now() - started)
                reply += delta
              }
            } catch {}
          }
        }
      } else {
        const data = await res.json()
        replyModel = data.model || ''
        usage = data.usage || null
        reply = data.choices?.[0]?.message?.content || ''
      }
      total = Math.round(performance.now() - started)
      history = [...history, { role: 'assistant', content: reply }]
      status = 'Listo'
    } catch (err) {
      if ((err as Error)?.name === 'AbortError') {
        status = 'Detenido'
        if (reply) history = [...history, { role: 'assistant', content: reply }]
      } else {
        error = err instanceof Error ? err.message : String(err)
        status = ''
      }
    } finally {
      sending = false
      controller = null
    }
  }

  onMount(() => {
    apiKey = sessionStorage.getItem(SESSION_KEY) || getStoredAPIKey()
    if (apiKey) loadModels()
  })
</script>

<div class="mx-auto max-w-5xl space-y-6 p-4 md:p-6" data-testid="playground-view">
  <div>
    <h1 class="text-2xl font-semibold text-text-main">Playground</h1>
    <p class="mt-1 text-sm text-text-muted">
      Probá un modelo o combo con una API key de cliente. La solicitud pasa por el mismo camino que cualquier cliente
      (inyección de prompts, cuotas y permisos de esa key) y consume tokens reales del proveedor elegido.
    </p>
  </div>

  <Card title="Conexión">
    <div class="grid gap-3 md:grid-cols-3">
      <div class="md:col-span-2">
        <label class="block text-xs font-medium text-text-muted" for="pg-key">API key de cliente</label>
        <input id="pg-key" type="password" autocomplete="off" bind:value={apiKey} placeholder="sk-…"
          class="w-full rounded-lg border border-border bg-surface-2 px-3 py-2 text-sm text-text-main" />
        <p class="mt-1 text-xs text-text-muted">Se guarda solo en esta pestaña (sessionStorage).</p>
      </div>
      <div class="flex items-end">
        <Button variant="secondary" onclick={loadModels} disabled={!apiKey.trim()}>Cargar modelos</Button>
      </div>
    </div>
    <div class="mt-3 grid gap-3 md:grid-cols-3">
      <div class="md:col-span-2">
        <label class="block text-xs font-medium text-text-muted" for="pg-model">Modelo o combo</label>
        <input id="pg-model" list="pg-models" bind:value={model} placeholder="Elegí o escribí un modelo"
          class="w-full rounded-lg border border-border bg-surface-2 px-3 py-2 text-sm text-text-main" />
        <datalist id="pg-models">
          {#each models as m}<option value={m}>{comboNames.has(m) ? 'combo' : ''}</option>{/each}
        </datalist>
      </div>
      <div class="flex items-end gap-2">
        <span class="shrink-0"><Toggle label="Streaming" checked={stream} onChange={(v) => (stream = v)} /></span>
        <span class="text-sm text-text-main">Streaming</span>
      </div>
    </div>
    <div class="mt-3">
      <label class="block text-xs font-medium text-text-muted" for="pg-system">Mensaje de sistema del cliente (opcional)</label>
      <textarea id="pg-system" rows="2" bind:value={system}
        class="w-full rounded-lg border border-border bg-surface-2 px-3 py-2 text-sm text-text-main"></textarea>
    </div>
  </Card>

  <Card title="Conversación">
    <div class="space-y-3">
      {#each history as m, i (i)}
        {#if !(m.role === 'assistant' && i === history.length - 1 && sending)}
          <div class="rounded-lg p-3 text-sm {m.role === 'user' ? 'bg-brand-500/10 text-text-main' : 'bg-surface-2 text-text-main'}">
            <p class="mb-1 text-xs text-text-muted">{m.role === 'user' ? 'Vos' : 'Modelo'}</p>
            <p class="whitespace-pre-wrap">{m.content}</p>
          </div>
        {/if}
      {/each}
      {#if sending}
        <div class="rounded-lg bg-surface-2 p-3 text-sm text-text-main" data-testid="pg-streaming">
          <p class="mb-1 text-xs text-text-muted">Modelo</p>
          <p class="whitespace-pre-wrap">{reply || '…'}</p>
        </div>
      {/if}
      {#if error}<p class="text-sm text-red-500">{error}</p>{/if}

      <textarea rows="3" bind:value={input} placeholder="Escribí un mensaje"
        onkeydown={(e) => { if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) send() }}
        class="w-full rounded-lg border border-border bg-surface-2 px-3 py-2 text-sm text-text-main"></textarea>
      <div class="flex flex-wrap items-center gap-2">
        {#if sending}
          <Button variant="danger" onclick={stop}>Detener</Button>
        {:else}
          <Button onclick={send} disabled={!apiKey.trim() || !model || !input.trim()}>Enviar</Button>
        {/if}
        <Button variant="ghost" onclick={reset} disabled={sending}>Nueva conversación</Button>
        <span class="text-xs text-text-muted">{status}</span>
      </div>
      {#if replyModel || ttft !== null || total !== null || usage}
        <p class="text-xs text-text-muted" data-testid="pg-meta">
          {#if replyModel}Modelo informado en la respuesta: <span class="font-mono">{replyModel}</span>{/if}
          {#if ttft !== null} · primer token: {ttft} ms{/if}
          {#if total !== null} · total: {total} ms{/if}
          {#if usage} · tokens: {usage.prompt_tokens ?? '?'} entrada / {usage.completion_tokens ?? '?'} salida{/if}
        </p>
      {/if}
    </div>
  </Card>
</div>
