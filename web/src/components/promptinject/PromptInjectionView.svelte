<script lang="ts">
  // Inyección de prompts por capas (global, combo, modelo, API key), identidad
  // pública de combos, inspector de la solicitud final y capturas opt-in.
  // Comportamiento inspirado en 9Router Pro (MIT); implementación propia.
  import { onMount } from 'svelte'
  import Card from '../../lib/ui/Card.svelte'
  import Button from '../../lib/ui/Button.svelte'
  import Toggle from '../../lib/ui/Toggle.svelte'
  import Modal from '../../lib/ui/Modal.svelte'
  import PromptEditor from './PromptEditor.svelte'
  import { notifications } from '../../lib/notifications'
  import type { APIKey, Combo } from '../../api/client'
  import {
    piApi, MODE_LABELS, SCOPE_LABELS,
    type PICapture, type PIEntry, type PIIdentity, type PIMode, type PIPreview, type PIScope, type PIToggles,
  } from '../../api/promptInjection'

  let { combos = [], apiKeys = [] }: { combos?: Combo[]; apiKeys?: APIKey[] } = $props()

  let loading = $state(true)
  let loadError = $state('')
  let toggles = $state<PIToggles>({ promptInjectionEnabled: false, comboPublicIdentityEnabled: false, promptInjectionCaptureEnabled: false })
  let entries = $state<PIEntry[]>([])
  let identities = $state<PIIdentity[]>([])
  let maxBytes = $state(65536)

  // Global
  let gEnabled = $state(true)
  let gMode = $state<PIMode>('prepend')
  let gContent = $state('')
  let gExists = $state(false)
  let saving = $state(false)

  // Capa específica
  let lScope = $state<PIScope>('model')
  let lTarget = $state('')
  let lMode = $state<PIMode>('prepend')
  let lContent = $state('')
  let lEnabled = $state(true)

  // Modal de combo
  let comboOpen = $state(false)
  let cName = $state('')
  let cEnabled = $state(false)
  let cMode = $state<PIMode>('prepend')
  let cContent = $state('')
  let cPublic = $state('')
  let cPublicEnabled = $state(false)

  // Inspector
  let tPath = $state('/v1/chat/completions')
  let tModel = $state('')
  let tKey = $state('')
  let tMessage = $state('Hola, ¿qué podés hacer?')
  let tSystem = $state('')
  let preview = $state<PIPreview | null>(null)
  let previewing = $state(false)

  let captures = $state<PICapture[]>([])

  const specific = $derived(entries.filter((e) => e.scope !== 'global'))
  const keyName = (id?: string) => apiKeys.find((k) => k.id === id)?.name || id || ''

  function targetLabel(e: PIEntry): string {
    if (e.scope === 'key') return keyName(e.target)
    return e.target || ''
  }

  async function load() {
    loading = true
    loadError = ''
    try {
      const s = await piApi.state()
      toggles = s.toggles
      entries = s.entries
      identities = s.identities
      maxBytes = s.limits?.maxContentBytes || 65536
      const g = s.entries.find((e) => e.scope === 'global')
      gExists = !!g
      if (g) {
        gEnabled = g.enabled
        gMode = g.mode
        gContent = g.content
      }
      if (toggles.promptInjectionCaptureEnabled) await loadCaptures()
    } catch (err) {
      loadError = err instanceof Error ? err.message : String(err)
    } finally {
      loading = false
    }
  }

  async function setToggle(key: keyof PIToggles, value: boolean) {
    try {
      toggles = await piApi.setToggles({ [key]: value })
      if (key === 'promptInjectionCaptureEnabled') {
        if (value) await loadCaptures()
        else captures = []
      }
    } catch (err) {
      notifications.error(err instanceof Error ? err.message : String(err), 'No se pudo cambiar el interruptor')
    }
  }

  async function saveEntry(e: PIEntry, okMsg: string) {
    saving = true
    try {
      await piApi.save(e)
      notifications.success(okMsg)
      await load()
      return true
    } catch (err) {
      notifications.error(err instanceof Error ? err.message : String(err), 'No se pudo guardar')
      return false
    } finally {
      saving = false
    }
  }

  async function removeEntry(scope: PIScope, target = '') {
    try {
      await piApi.remove(scope, target)
      notifications.success('Capa eliminada')
      if (scope === 'global') {
        gContent = ''
        gEnabled = true
        gMode = 'prepend'
      }
      await load()
    } catch (err) {
      notifications.error(err instanceof Error ? err.message : String(err), 'No se pudo eliminar')
    }
  }

  function saveGlobal() {
    return saveEntry({ scope: 'global', mode: gMode, content: gContent, enabled: gEnabled }, 'Prompt global guardado')
  }

  async function saveLayer() {
    const ok = await saveEntry(
      { scope: lScope, target: lTarget.trim(), mode: lMode, content: lContent, enabled: lEnabled },
      'Capa guardada',
    )
    if (ok) {
      lContent = ''
      lTarget = ''
    }
  }

  function editLayer(e: PIEntry) {
    if (e.scope === 'combo') {
      openCombo(e.target || '')
      return
    }
    lScope = e.scope
    lTarget = e.target || ''
    lMode = e.mode
    lContent = e.content
    lEnabled = e.enabled
  }

  function openCombo(name: string) {
    cName = name
    const e = entries.find((x) => x.scope === 'combo' && x.target === name)
    cEnabled = e ? e.enabled : false
    cMode = e?.mode || 'prepend'
    cContent = e?.content || ''
    const id = identities.find((i) => i.combo === name)
    cPublic = id?.publicModel || ''
    cPublicEnabled = id ? id.enabled : false
    comboOpen = true
  }

  async function saveCombo() {
    saving = true
    try {
      if (cContent.trim()) {
        await piApi.save({ scope: 'combo', target: cName, mode: cMode, content: cContent, enabled: cEnabled })
      } else if (entries.some((x) => x.scope === 'combo' && x.target === cName)) {
        await piApi.remove('combo', cName)
      }
      if (cPublic.trim()) {
        await piApi.saveIdentity({ combo: cName, publicModel: cPublic.trim(), enabled: cPublicEnabled })
      } else if (identities.some((i) => i.combo === cName)) {
        await piApi.removeIdentity(cName)
      }
      notifications.success(`Ajustes de «${cName}» guardados`)
      comboOpen = false
      await load()
    } catch (err) {
      notifications.error(err instanceof Error ? err.message : String(err), 'No se pudo guardar el combo')
    } finally {
      saving = false
    }
  }

  async function runPreview() {
    if (!tModel.trim()) {
      notifications.warning('Elegí un modelo o combo')
      return
    }
    previewing = true
    try {
      let body: Record<string, unknown>
      if (tPath === '/v1/messages') {
        body = { max_tokens: 256, messages: [{ role: 'user', content: tMessage }], ...(tSystem ? { system: tSystem } : {}) }
      } else if (tPath === '/v1/responses') {
        body = { input: tMessage, ...(tSystem ? { instructions: tSystem } : {}) }
      } else {
        body = { messages: [...(tSystem ? [{ role: 'system', content: tSystem }] : []), { role: 'user', content: tMessage }] }
      }
      preview = await piApi.preview({ path: tPath, model: tModel.trim(), apiKeyId: tKey || undefined, body })
    } catch (err) {
      notifications.error(err instanceof Error ? err.message : String(err), 'No se pudo calcular la vista previa')
    } finally {
      previewing = false
    }
  }

  async function loadCaptures() {
    try {
      captures = (await piApi.captures()).captures
    } catch (err) {
      notifications.error(err instanceof Error ? err.message : String(err))
    }
  }

  async function clearCaptures() {
    await piApi.clearCaptures()
    captures = []
  }

  function prettyJSON(s: string): string {
    try {
      return JSON.stringify(JSON.parse(s), null, 2)
    } catch {
      return s
    }
  }

  onMount(load)
</script>

<div class="mx-auto max-w-5xl space-y-6 p-4 md:p-6" data-testid="prompt-injection-view">
  <div>
    <h1 class="text-2xl font-semibold text-text-main">Inyección de prompts</h1>
    <p class="mt-1 text-sm text-text-muted">
      Agregá instrucciones de sistema a las solicitudes que pasan por Capimux, por capas: global → combo → modelo → API key
      (se suman en ese orden; un reemplazo más específico anula a los menos específicos). Solo el administrador puede ver o cambiar esta configuración.
    </p>
  </div>

  {#if loading}
    <p class="text-sm text-text-muted">Cargando…</p>
  {:else if loadError}
    <Card><p class="text-sm text-red-500">{loadError}</p></Card>
  {:else}
    <Card title="Interruptores" subtitle="Cada función es independiente y viene apagada por defecto.">
      <div class="space-y-4">
        <div class="flex items-start justify-between gap-4">
          <div>
            <p class="text-sm font-medium text-text-main">Inyección de prompts</p>
            <p class="text-xs text-text-muted">Aplica las capas activas a chat/completions, messages, responses y /api/chat.</p>
          </div>
          <span class="shrink-0"><Toggle label="Inyección de prompts" checked={toggles.promptInjectionEnabled}
            onChange={(v) => setToggle('promptInjectionEnabled', v)} /></span>
        </div>
        <div class="flex items-start justify-between gap-4">
          <div>
            <p class="text-sm font-medium text-text-main">Identidad pública de combos</p>
            <p class="text-xs text-text-muted">
              Solo cambia el nombre de modelo que ve el cliente en la respuesta. No altera el modelo ni el proveedor reales ni
              sus políticas; uso, logs y estadísticas registran siempre el modelo y proveedor reales.
            </p>
          </div>
          <span class="shrink-0"><Toggle label="Identidad pública de combos" checked={toggles.comboPublicIdentityEnabled}
            onChange={(v) => setToggle('comboPublicIdentityEnabled', v)} /></span>
        </div>
        <div class="flex items-start justify-between gap-4">
          <div>
            <p class="text-sm font-medium text-text-main">Captura de solicitudes (diagnóstico)</p>
            <p class="text-xs text-text-muted">
              Guarda en memoria, con secretos redactados, las últimas 20 solicitudes con inyección. Se borra al apagarla o al
              reiniciar. Los logs persistentes nunca guardan el texto inyectado.
            </p>
          </div>
          <span class="shrink-0"><Toggle label="Captura de solicitudes" checked={toggles.promptInjectionCaptureEnabled}
            onChange={(v) => setToggle('promptInjectionCaptureEnabled', v)} /></span>
        </div>
      </div>
    </Card>

    <Card title="Prompt global" subtitle="Se aplica a todas las solicitudes, salvo que una capa más específica lo reemplace.">
      <div class="space-y-3">
        <div class="flex items-center justify-between">
          <span class="text-sm text-text-main">Activo</span>
          <span class="shrink-0"><Toggle label="Prompt global activo" checked={gEnabled} onChange={(v) => (gEnabled = v)} /></span>
        </div>
        <PromptEditor idPrefix="global" bind:content={gContent} bind:mode={gMode} {maxBytes} />
        <div class="flex gap-2">
          <Button onclick={saveGlobal} loading={saving} disabled={!gContent.trim()}>Guardar</Button>
          {#if gExists}
            <Button variant="ghost" onclick={() => removeEntry('global')}>Borrar</Button>
          {/if}
        </div>
      </div>
    </Card>

    <Card title="Capas por modelo o API key" subtitle="Un modelo concreto (sin combo) o una API key de cliente.">
      <div class="space-y-4">
        {#if specific.length}
          <div class="overflow-x-auto">
            <table class="w-full text-left text-sm">
              <thead class="text-xs text-text-muted">
                <tr><th class="py-1">Alcance</th><th>Destino</th><th>Posición</th><th>Tamaño</th><th>Estado</th><th></th></tr>
              </thead>
              <tbody>
                {#each specific as e (e.scope + ':' + e.target)}
                  <tr class="border-t border-border-subtle">
                    <td class="py-2">{SCOPE_LABELS[e.scope]}</td>
                    <td class="font-mono text-xs">{targetLabel(e)}</td>
                    <td>{MODE_LABELS[e.mode]}</td>
                    <td>{[...e.content].length} car.</td>
                    <td>{e.enabled ? 'Activa' : 'Pausada'}</td>
                    <td class="text-right">
                      <button class="text-xs text-brand-500 hover:underline" onclick={() => editLayer(e)}>Editar</button>
                      <button class="ml-2 text-xs text-red-500 hover:underline" onclick={() => removeEntry(e.scope, e.target)}>Borrar</button>
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {:else}
          <p class="text-sm text-text-muted">Todavía no hay capas específicas.</p>
        {/if}

        <div class="grid gap-3 md:grid-cols-2">
          <div>
            <label class="block text-xs font-medium text-text-muted" for="layer-scope">Alcance</label>
            <select id="layer-scope" bind:value={lScope}
              class="w-full rounded-lg border border-border bg-surface-2 px-3 py-2 text-sm text-text-main">
              <option value="model">Modelo</option>
              <option value="key">API key</option>
            </select>
          </div>
          <div>
            <label class="block text-xs font-medium text-text-muted" for="layer-target">{lScope === 'key' ? 'API key' : 'Modelo (proveedor/modelo)'}</label>
            {#if lScope === 'key'}
              <select id="layer-target" bind:value={lTarget}
                class="w-full rounded-lg border border-border bg-surface-2 px-3 py-2 text-sm text-text-main">
                <option value="">Elegí una API key</option>
                {#each apiKeys as k}
                  <option value={k.id}>{k.name || k.id}</option>
                {/each}
              </select>
            {:else}
              <input id="layer-target" bind:value={lTarget} placeholder="ej.: openai/gpt-4o-mini"
                class="w-full rounded-lg border border-border bg-surface-2 px-3 py-2 text-sm text-text-main" />
            {/if}
          </div>
        </div>
        <PromptEditor idPrefix="layer" bind:content={lContent} bind:mode={lMode} {maxBytes} />
        <div class="flex items-center gap-3">
          <span class="shrink-0"><Toggle label="Capa activa" checked={lEnabled} onChange={(v) => (lEnabled = v)} /></span>
          <span class="text-sm text-text-main">Activa</span>
          <Button onclick={saveLayer} loading={saving} disabled={!lContent.trim() || !lTarget.trim()}>Guardar capa</Button>
        </div>
      </div>
    </Card>

    <Card title="Combos" subtitle="Prompt e identidad pública por combo.">
      {#if combos.length}
        <ul class="divide-y divide-border-subtle">
          {#each combos as c (c.id)}
            {@const e = entries.find((x) => x.scope === 'combo' && x.target === c.name)}
            {@const id = identities.find((i) => i.combo === c.name)}
            <li class="flex items-center justify-between py-2">
              <div>
                <p class="font-mono text-sm text-text-main">{c.name}</p>
                <p class="text-xs text-text-muted">
                  {e ? `Prompt: ${MODE_LABELS[e.mode]}${e.enabled ? '' : ' (pausado)'}` : 'Sin prompt'}
                  · {id ? `Nombre público: ${id.publicModel}${id.enabled ? '' : ' (pausado)'}` : 'Sin nombre público'}
                </p>
              </div>
              <Button size="sm" variant="secondary" onclick={() => openCombo(c.name)}>Ajustes</Button>
            </li>
          {/each}
        </ul>
      {:else}
        <p class="text-sm text-text-muted">No hay combos creados.</p>
      {/if}
    </Card>

    <Card title="Probar prompt" subtitle="Vista previa de la solicitud final, sin enviarla al proveedor.">
      <div class="space-y-3">
        <div class="grid gap-3 md:grid-cols-3">
          <div>
            <label class="block text-xs font-medium text-text-muted" for="t-path">Formato</label>
            <select id="t-path" bind:value={tPath}
              class="w-full rounded-lg border border-border bg-surface-2 px-3 py-2 text-sm text-text-main">
              <option value="/v1/chat/completions">OpenAI · /v1/chat/completions</option>
              <option value="/v1/messages">Anthropic · /v1/messages</option>
              <option value="/v1/responses">Responses · /v1/responses</option>
            </select>
          </div>
          <div>
            <label class="block text-xs font-medium text-text-muted" for="t-model">Modelo o combo</label>
            <input id="t-model" list="pi-combos" bind:value={tModel} placeholder="ej.: mi-combo"
              class="w-full rounded-lg border border-border bg-surface-2 px-3 py-2 text-sm text-text-main" />
            <datalist id="pi-combos">
              {#each combos as c}<option value={c.name}></option>{/each}
            </datalist>
          </div>
          <div>
            <label class="block text-xs font-medium text-text-muted" for="t-key">API key (opcional)</label>
            <select id="t-key" bind:value={tKey}
              class="w-full rounded-lg border border-border bg-surface-2 px-3 py-2 text-sm text-text-main">
              <option value="">Ninguna</option>
              {#each apiKeys as k}<option value={k.id}>{k.name || k.id}</option>{/each}
            </select>
          </div>
        </div>
        <div class="grid gap-3 md:grid-cols-2">
          <div>
            <label class="block text-xs font-medium text-text-muted" for="t-system">Sistema del cliente (opcional)</label>
            <textarea id="t-system" rows="2" bind:value={tSystem}
              class="w-full rounded-lg border border-border bg-surface-2 px-3 py-2 text-sm text-text-main"></textarea>
          </div>
          <div>
            <label class="block text-xs font-medium text-text-muted" for="t-msg">Mensaje de prueba</label>
            <textarea id="t-msg" rows="2" bind:value={tMessage}
              class="w-full rounded-lg border border-border bg-surface-2 px-3 py-2 text-sm text-text-main"></textarea>
          </div>
        </div>
        <Button onclick={runPreview} loading={previewing}>Ver solicitud final</Button>

        {#if preview}
          <div class="space-y-3 rounded-lg border border-border-subtle bg-surface-2 p-3" data-testid="pi-preview">
            <p class="text-xs text-text-muted">{preview.note}</p>
            {#if !preview.wouldApply}
              <p class="text-xs text-amber-500">
                {toggles.promptInjectionEnabled ? 'Ninguna capa activa coincide con esta solicitud.' : 'La inyección está apagada: hoy esta solicitud saldría sin cambios.'}
              </p>
            {/if}
            <div class="text-sm text-text-main">
              <strong>Capas aplicadas:</strong>
              {#if preview.layers.length}
                {preview.layers.map((l) => `${SCOPE_LABELS[l.scope]}${l.target ? ' ' + (l.scope === 'key' ? keyName(l.target) : l.target) : ''} (${MODE_LABELS[l.mode]}, ${l.chars} car.)`).join(' → ')}
              {:else}ninguna{/if}
              {#if preview.publicModel}<br /><strong>Nombre público:</strong> {preview.publicModel}{/if}
            </div>
            <div>
              <p class="text-xs font-medium text-text-muted">Instrucciones de sistema resultantes</p>
              <pre class="max-h-48 overflow-auto whitespace-pre-wrap rounded bg-surface p-2 text-xs text-text-main">{preview.effectiveSystem || '(vacío)'}</pre>
            </div>
            <div>
              <p class="text-xs font-medium text-text-muted">Cuerpo enviado (secretos redactados)</p>
              <pre class="max-h-72 overflow-auto rounded bg-surface p-2 text-xs text-text-main">{prettyJSON(preview.sentBody)}</pre>
            </div>
          </div>
        {/if}
      </div>
    </Card>

    {#if toggles.promptInjectionCaptureEnabled}
      <Card title="Capturas recientes" subtitle="Solicitudes reales con inyección, tal como Capimux las entregó al ruteo (redactadas).">
        <div class="mb-3 flex gap-2">
          <Button size="sm" variant="secondary" onclick={loadCaptures}>Actualizar</Button>
          <Button size="sm" variant="ghost" onclick={clearCaptures}>Vaciar</Button>
        </div>
        {#if captures.length}
          <div class="space-y-2">
            {#each captures as c}
              <details class="rounded border border-border-subtle p-2">
                <summary class="cursor-pointer text-sm text-text-main">
                  {new Date(c.at).toLocaleString('es-UY')} · {c.path} · {c.model} · {c.layers.length} capa(s)
                </summary>
                <pre class="mt-2 max-h-72 overflow-auto text-xs text-text-main">{prettyJSON(c.body)}</pre>
                {#if c.truncated}<p class="text-xs text-text-muted">(recortado)</p>{/if}
              </details>
            {/each}
          </div>
        {:else}
          <p class="text-sm text-text-muted">Sin capturas todavía.</p>
        {/if}
      </Card>
    {/if}
  {/if}
</div>

<Modal isOpen={comboOpen} onClose={() => (comboOpen = false)} title={`Ajustes del combo · ${cName}`} size="lg">
  <div class="space-y-4">
    <div class="flex items-center justify-between">
      <div>
        <p class="text-sm font-medium text-text-main">Activar inyección de prompts</p>
        <p class="text-xs text-text-muted">Se suma a la capa global y a la de la API key.</p>
      </div>
      <span class="shrink-0"><Toggle label="Activar inyección en el combo" checked={cEnabled} onChange={(v) => (cEnabled = v)} /></span>
    </div>
    <PromptEditor idPrefix="combo" bind:content={cContent} bind:mode={cMode} {maxBytes} />
    <div class="space-y-1 border-t border-border-subtle pt-3">
      <div class="flex items-center justify-between">
        <label class="text-sm font-medium text-text-main" for="combo-public">Identidad pública</label>
        <span class="shrink-0"><Toggle label="Identidad pública activa" checked={cPublicEnabled} onChange={(v) => (cPublicEnabled = v)} /></span>
      </div>
      <input id="combo-public" bind:value={cPublic} placeholder="ej.: asistente-equipo" maxlength="200"
        class="w-full rounded-lg border border-border bg-surface-2 px-3 py-2 text-sm text-text-main" />
      <p class="text-xs text-text-muted">
        Solo cambia el nombre de modelo que ve el cliente. No cambia el modelo ni el proveedor reales ni sus políticas; uso,
        logs y estadísticas siguen registrando el modelo y proveedor reales.
      </p>
    </div>
  </div>
  {#snippet footer()}
    <div class="flex justify-end gap-2">
      <Button variant="ghost" onclick={() => (comboOpen = false)}>Cancelar</Button>
      <Button onclick={saveCombo} loading={saving}>Guardar</Button>
    </div>
  {/snippet}
</Modal>
