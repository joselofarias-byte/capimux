<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import { api, type MultiagentPolicy, type MultiagentResult, type MultiagentTask, type MultiagentStrategy } from '../api/client'

  import { MULTIAGENT_TASKS, MULTIAGENT_STRATEGIES, modelLimit, readProfiles, writeProfiles, type MultiagentProfile } from '../lib/multiagentProfiles'

  let task = $state<MultiagentTask>('programming')
  let strategy = $state<MultiagentStrategy>('parallel')
  let profiles = $state<MultiagentProfile[]>([])
  let profileStatus = $state('')
  let storage: Storage | null = null
  const savedProfile = $derived(profiles.find(profile => profile.task === task))
  const maxModels = $derived(modelLimit(policy?.max_models ?? 0))
  const strategyDescription = $derived(MULTIAGENT_STRATEGIES.find(option => option.value === strategy)?.description)

  let prompt = $state('')
  let models = $state<string[]>([])
  let policy = $state<MultiagentPolicy | null>(null)
  let policyLoading = $state(true)
  let allowExternal = $state(false)
  let dryRun = $state(true)
  let preview = $state(false)
  let loading = $state(false)
  let error = $state('')
  let results = $state<MultiagentResult[]>([])
  let controller: AbortController | null = null
  const policyController = new AbortController()
  const canRun = $derived(!loading && !!policy && !!prompt.trim() && models.length > 0 &&
    models.length <= maxModels && (dryRun || (allowExternal && policy.external_enabled)))

  async function loadPolicy() {
    policyLoading = true
    error = ''
    try {
      const loaded = await api.getMultiagentPolicy(policyController.signal)
      policy = loaded
      models = models.filter(model => loaded.models.includes(model))
    } catch (e) {
      if (!(e instanceof Error && e.name === 'AbortError')) {
        error = e instanceof Error ? e.message : 'No se pudieron cargar los modelos.'
      }
    } finally { policyLoading = false }
  }
  onMount(() => {
    try { storage = window.localStorage } catch { storage = null }
    profiles = readProfiles(storage)
    void loadPolicy()
  })
  onDestroy(() => { controller?.abort(); policyController.abort() })

  function toggleModel(model: string, checked: boolean) {
    models = checked ? [...models, model] : models.filter(m => m !== model)
  }

  function saveProfile() {
    const next = [...profiles.filter(profile => profile.task !== task), { task, strategy, models: [...models] }]
    if (!writeProfiles(storage, next)) {
      profileStatus = 'No se pudo guardar el perfil en este navegador.'
      return
    }
    profiles = next
    profileStatus = 'Perfil guardado para esta tarea en este navegador.'
  }

  function loadProfile() {
    if (!savedProfile || !policy) return
    strategy = savedProfile.strategy
    models = savedProfile.models.filter(model => policy.models.includes(model)).slice(0, maxModels)
    profileStatus = models.length === savedProfile.models.length
      ? 'Perfil cargado.' : 'Perfil cargado parcialmente: algunos modelos ya no están habilitados o exceden el límite actual.'
  }

  function deleteProfile() {
    const next = profiles.filter(profile => profile.task !== task)
    if (!writeProfiles(storage, next)) {
      profileStatus = 'No se pudo eliminar el perfil de este navegador.'
      return
    }
    profiles = next
    profileStatus = 'Perfil eliminado.'
  }

  function moveModel(index: number, direction: number) {
    const next = [...models]
    const target = index + direction
    if (target < 0 || target >= next.length) return
    ;[next[index], next[target]] = [next[target], next[index]]
    models = next
  }

  async function run() {
    if (!canRun) return
    const simulated = dryRun
    error = ''
    results = []
    preview = false
    loading = true
    controller = new AbortController()
    try {
      const data = await api.runMultiagent({
        prompt, task, strategy, models: [...models], concurrency: 3, timeout_ms: 60000,
        allow_external: !simulated && allowExternal, dry_run: simulated
      }, controller.signal)
      results = data.results
      preview = data.dry_run === true
    } catch (e) {
      if (e instanceof Error && e.name === 'AbortError') error = 'Solicitud cancelada.'
      else error = e instanceof Error ? e.message : 'No se pudo completar la solicitud.'
    } finally {
      loading = false
      controller = null
    }
  }

  function resultError(message: string) {
    const labels: Record<string, string> = {
      'cancelled': 'Cancelado',
      'timed out': 'El modelo superó el tiempo de espera.',
      'response too large': 'La respuesta superó el tamaño permitido.',
      'empty model response': 'El modelo devolvió una respuesta vacía.',
      'model request failed': 'No se pudo obtener la respuesta de este modelo.'
    }
    return labels[message] || message
  }
</script>

<section class="space-y-6">
  <div class="space-y-2">
    <h2 class="text-2xl font-semibold text-text-main">Comparador multimodelo · Capimux/JolufaSoft</h2>
    <p class="text-sm text-text-muted">Compará entre 2 y 10 modelos según la política del servidor. También podés consultar un solo modelo.</p>
  </div>
  <form onsubmit={(event) => { event.preventDefault(); void run() }} class="space-y-4 rounded-xl border border-border-subtle bg-surface p-5">
    <div class="grid gap-4 sm:grid-cols-2">
      <label class="space-y-2 text-sm font-medium text-text-main">
        <span>Tarea</span>
        <select bind:value={task} onchange={() => { profileStatus = '' }} disabled={loading} class="w-full rounded-lg border border-border-subtle bg-surface-2 p-3">
          {#each MULTIAGENT_TASKS as option (option.value)}<option value={option.value}>{option.label}</option>{/each}
        </select>
      </label>
      <label class="space-y-2 text-sm font-medium text-text-main">
        <span>Estrategia de enrutamiento</span>
        <select bind:value={strategy} disabled={loading} class="w-full rounded-lg border border-border-subtle bg-surface-2 p-3">
          {#each MULTIAGENT_STRATEGIES as option (option.value)}<option value={option.value}>{option.label}</option>{/each}
        </select>
      </label>
    </div>
    <p class="text-sm text-text-muted">{strategyDescription}</p>
    <p class="text-xs text-text-muted">La tarea clasifica la solicitud; no modifica el prompt.</p>
    {#if strategy === 'priority' || strategy === 'fallback'}
      <p class="text-xs text-text-muted">Respaldo seguro: solo se avanza ante un rechazo HTTP 429 o 5xx explícito. No se reintenta ante timeout, cancelación, errores de red ambiguos ni respuestas vacías o inválidas, para evitar solicitudes duplicadas.</p>
    {/if}
    <div class="flex flex-wrap gap-2">
      <button type="button" onclick={saveProfile} disabled={loading || models.length === 0 || models.length > maxModels} class="rounded-lg border border-border-subtle px-3 py-2 text-sm text-text-main disabled:opacity-40">Guardar perfil de tarea</button>
      <button type="button" onclick={loadProfile} disabled={loading || !savedProfile || !policy} class="rounded-lg border border-border-subtle px-3 py-2 text-sm text-text-main disabled:opacity-40">Cargar perfil</button>
      <button type="button" onclick={deleteProfile} disabled={loading || !savedProfile} class="rounded-lg border border-border-subtle px-3 py-2 text-sm text-text-main disabled:opacity-40">Eliminar perfil</button>
    </div>
    <p class="text-xs text-text-muted">Un perfil por tarea, solo en este navegador: guarda la estrategia y los identificadores exactos de proveedor/modelo, nunca prompts, respuestas ni credenciales.</p>
    {#if profileStatus}<p role="status" class="text-sm text-text-muted">{profileStatus}</p>{/if}
    <label class="block text-sm font-medium text-text-main" for="multiagent-prompt">Prompt</label>
    <textarea id="multiagent-prompt" bind:value={prompt} disabled={loading} rows="6" maxlength="20000" placeholder="Escribí la tarea para todos los modelos…" class="w-full rounded-lg border border-border-subtle bg-surface-2 p-3 text-text-main" required></textarea>
    <fieldset disabled={loading || policyLoading} class="space-y-3">
      <legend class="text-sm font-medium text-text-main">Modelos</legend>
      {#if policyLoading}
        <p role="status" class="text-sm text-text-muted">Cargando modelos…</p>
      {:else if policy && policy.models.length > 0}
        <div class="grid gap-2 sm:grid-cols-2">
          {#each policy.models as model (model)}
            <label class="flex items-center gap-3 rounded-lg border border-border-subtle p-3 text-sm text-text-main">
              <input type="checkbox" checked={models.includes(model)}
                disabled={!models.includes(model) && models.length >= maxModels}
                onchange={(event) => toggleModel(model, event.currentTarget.checked)} />
              <span class="break-all">{model}</span>
            </label>
          {/each}
        </div>
        <p class="text-xs text-text-muted">{models.length} de {maxModels} modelos seleccionados.</p>
      {:else if policy}
        <p class="text-sm text-text-muted">Todavía no hay modelos habilitados para el comparador.</p>
      {/if}
    </fieldset>
    {#if models.length > 0}
      <fieldset disabled={loading} class="space-y-2">
        <legend class="text-sm font-medium text-text-main">Orden de selección / prioridad</legend>
        <ol class="space-y-2">
          {#each models as model, index (model)}
            <li class="flex flex-wrap items-center gap-2 rounded-lg border border-border-subtle p-2 text-sm text-text-main">
              <span class="min-w-0 flex-1 break-all">{index + 1}. {model}</span>
              <button type="button" onclick={() => moveModel(index, -1)} disabled={index === 0} aria-label={`Subir ${model}`} class="rounded border border-border-subtle px-2 py-1 disabled:opacity-40">↑</button>
              <button type="button" onclick={() => moveModel(index, 1)} disabled={index === models.length - 1} aria-label={`Bajar ${model}`} class="rounded border border-border-subtle px-2 py-1 disabled:opacity-40">↓</button>
            </li>
          {/each}
        </ol>
        {#if strategy === 'manual'}<p class="text-xs text-text-muted">Solo se consultará {models[0]}; los demás permanecerán seleccionados para otras estrategias.</p>{/if}
      </fieldset>
    {/if}
    {#if !policyLoading && !policy}
      <button type="button" onclick={() => void loadPolicy()} class="rounded-lg border border-border-subtle px-3 py-2 text-sm text-text-main">Reintentar carga</button>
    {/if}
    <label class="flex items-center gap-3 text-sm text-text-main">
      <input type="checkbox" bind:checked={dryRun} disabled={loading} />
      <span>Validar selección sin enviar el prompt</span>
    </label>
    {#if !dryRun}
      {#if policy && !policy.external_enabled}
        <p role="status" class="text-sm text-text-muted">El servidor tiene habilitada únicamente la simulación.</p>
      {:else}
        <label class="flex items-center gap-3 text-sm text-text-main">
          <input type="checkbox" bind:checked={allowExternal} disabled={loading} />
          <span>Enviar a los proveedores seleccionados usando sus cuotas o créditos</span>
        </label>
      {/if}
    {/if}
    <div class="flex gap-3">
      <button type="submit" disabled={!canRun} class="rounded-lg bg-primary px-5 py-2 text-white disabled:opacity-40">{dryRun ? 'Validar selección' : 'Ejecutar selección'}</button>
      {#if loading}<button type="button" onclick={() => controller?.abort()} class="rounded-lg border border-border-subtle px-4 py-2 text-text-main">Cancelar</button>{/if}
    </div>
  </form>
  {#if error}<p role="alert" class="text-sm text-red-500">{error}</p>{/if}
  {#if loading}<p role="status" class="text-sm text-text-muted">Consultando modelos…</p>{/if}
  {#if preview}<p role="status" class="text-sm text-text-muted">Selección validada. No se enviaron solicitudes a proveedores.</p>{/if}
  {#if results.length > 0}
    <div class="grid gap-4 md:grid-cols-2">
      {#each results as result (result.model)}
        <article class="space-y-3 rounded-xl border border-border-subtle bg-surface p-5">
          <div class="flex flex-wrap justify-between gap-2">
            <h3 class="break-all font-semibold text-text-main">{result.model}</h3>
            {#if !preview}<span class="text-xs text-text-muted">{result.duration_ms} ms</span>{/if}
          </div>
          {#if preview}<p class="text-sm text-text-muted">Modelo habilitado.</p>
          {:else if result.error}<p class="text-sm text-red-500">{resultError(result.error)}</p>
          {:else}<pre class="whitespace-pre-wrap break-words font-sans text-sm text-text-main">{result.output}</pre>{/if}
        </article>
      {/each}
    </div>
  {/if}
</section>
