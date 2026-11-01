<script lang="ts">
  // Editor de uso general: solo límites técnicos (tamaño y UTF-8, validados
  // también en el servidor). Sin filtros ni plantillas de contenido.
  import { piApi, byteLength, MODE_LABELS, type PIMode } from '../../api/promptInjection'
  import { notifications } from '../../lib/notifications'

  let {
    content = $bindable(''),
    mode = $bindable<PIMode>('prepend'),
    maxBytes = 65536,
    showMode = true,
    placeholder = 'Ej.: Respondé en español, conciso.',
    idPrefix = 'pi',
  }: {
    content?: string
    mode?: PIMode
    maxBytes?: number
    showMode?: boolean
    placeholder?: string
    idPrefix?: string
  } = $props()

  let fileInput = $state<HTMLInputElement | null>(null)
  let uploading = $state(false)
  const bytes = $derived(byteLength(content))
  const chars = $derived([...content].length)
  const over = $derived(bytes > maxBytes)

  async function onFile(e: Event) {
    const input = e.currentTarget as HTMLInputElement
    const file = input.files?.[0]
    input.value = ''
    if (!file) return
    uploading = true
    try {
      const res = await piApi.upload(file)
      content = res.content
      notifications.success(`Se cargó «${res.filename}» (${res.chars} caracteres). Revisalo y guardá.`)
    } catch (err) {
      notifications.error(err instanceof Error ? err.message : String(err), 'No se pudo cargar el archivo')
    } finally {
      uploading = false
    }
  }
</script>

<div class="space-y-2">
  {#if showMode}
    <label class="block text-xs font-medium text-text-muted" for="{idPrefix}-mode">Posición</label>
    <select id="{idPrefix}-mode" bind:value={mode}
      class="w-full rounded-lg border border-border bg-surface-2 px-3 py-2 text-sm text-text-main">
      {#each Object.entries(MODE_LABELS) as [value, label]}
        <option {value}>{label}</option>
      {/each}
    </select>
    {#if mode === 'replace'}
      <p class="text-xs text-amber-500">
        Reemplaza solo el bloque de instrucciones de sistema que manda el cliente (mensajes de sistema iniciales, «system» o
        «instructions»). Los mensajes y herramientas no se tocan.
      </p>
    {/if}
  {/if}
  <div class="flex items-center justify-between">
    <label class="text-xs font-medium text-text-muted" for="{idPrefix}-content">Prompt de sistema</label>
    <button type="button" class="text-xs text-brand-500 hover:underline disabled:opacity-50" disabled={uploading}
      onclick={() => fileInput?.click()}>
      {uploading ? 'Cargando…' : 'Cargar .txt'}
    </button>
    <input bind:this={fileInput} type="file" accept=".txt,text/plain" class="hidden" onchange={onFile} />
  </div>
  <textarea id="{idPrefix}-content" bind:value={content} rows="6" {placeholder}
    class="w-full rounded-lg border border-border bg-surface-2 px-3 py-2 font-mono text-sm text-text-main"></textarea>
  <div class="flex justify-between text-xs {over ? 'text-red-500' : 'text-text-muted'}">
    <span>{chars.toLocaleString('es')} caracteres</span>
    <span>{bytes.toLocaleString('es')} / {maxBytes.toLocaleString('es')} bytes</span>
  </div>
</div>
