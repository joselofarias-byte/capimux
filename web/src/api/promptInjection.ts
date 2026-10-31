// Admin API of the prompt injection module. Requests go with the dashboard
// session cookie only: the server rejects (403) any request that carries a
// client API key, so the stored engine key is deliberately not attached.

export type PIScope = 'global' | 'combo' | 'model' | 'key'
export type PIMode = 'prepend' | 'append' | 'replace'

export interface PIEntry {
  scope: PIScope
  target?: string
  mode: PIMode
  content: string
  enabled: boolean
  name?: string
  updatedAt?: string
}

export interface PIIdentity {
  combo: string
  publicModel: string
  enabled: boolean
  updatedAt?: string
}

export interface PIToggles {
  promptInjectionEnabled: boolean
  comboPublicIdentityEnabled: boolean
  promptInjectionCaptureEnabled: boolean
}

export interface PILayerInfo {
  scope: PIScope
  target?: string
  mode: PIMode
  name?: string
  chars: number
}

export interface PIState {
  toggles: PIToggles
  entries: PIEntry[]
  identities: PIIdentity[]
  limits: { maxContentBytes: number; maxTargetLen: number }
}

export interface PIPreview {
  protocol: string
  model: string
  isCombo: boolean
  layers: PILayerInfo[]
  replace: boolean
  effectiveSystem: string
  sentBody: string
  publicModel?: string
  wouldApply: boolean
  note: string
}

export interface PICapture {
  at: string
  path: string
  model: string
  apiKeyId?: string
  layers: PILayerInfo[]
  body: string
  truncated: boolean
}

async function call<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = { ...(init.headers as Record<string, string> | undefined) }
  if (init.body && !(init.body instanceof FormData)) headers['Content-Type'] = 'application/json'
  const res = await fetch(path, { ...init, headers, credentials: 'same-origin' })
  const text = await res.text()
  let data: unknown = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    data = text
  }
  if (!res.ok) {
    const msg =
      (data && typeof data === 'object' && 'error' in data && typeof (data as { error: unknown }).error === 'string'
        ? (data as { error: string }).error
        : typeof data === 'string' && data
          ? data
          : `Error ${res.status}`)
    throw new Error(msg)
  }
  return data as T
}

export const piApi = {
  state: () => call<PIState>('/api/prompt-injections'),
  save: (e: PIEntry) => call<PIEntry>('/api/prompt-injections', { method: 'PUT', body: JSON.stringify(e) }),
  remove: (scope: PIScope, target = '') =>
    call<{ ok: boolean }>(`/api/prompt-injections?scope=${encodeURIComponent(scope)}&target=${encodeURIComponent(target)}`, {
      method: 'DELETE',
    }),
  setToggles: (t: Partial<PIToggles>) =>
    call<PIToggles>('/api/prompt-injections/toggles', { method: 'PUT', body: JSON.stringify(t) }),
  upload: (file: File) => {
    const fd = new FormData()
    fd.append('file', file)
    return call<{ content: string; bytes: number; chars: number; filename: string }>('/api/prompt-injections/upload', {
      method: 'POST',
      body: fd,
    })
  },
  preview: (req: { path: string; model: string; apiKeyId?: string; body?: Record<string, unknown> }) =>
    call<PIPreview>('/api/prompt-injections/preview', { method: 'POST', body: JSON.stringify(req) }),
  captures: () => call<{ enabled: boolean; limit: number; captures: PICapture[] }>('/api/prompt-injections/captures'),
  clearCaptures: () => call<{ ok: boolean }>('/api/prompt-injections/captures', { method: 'DELETE' }),
  saveIdentity: (id: PIIdentity) =>
    call<PIIdentity>('/api/combo-identities', { method: 'PUT', body: JSON.stringify(id) }),
  removeIdentity: (combo: string) =>
    call<{ ok: boolean }>(`/api/combo-identities?combo=${encodeURIComponent(combo)}`, { method: 'DELETE' }),
}

export const MODE_LABELS: Record<PIMode, string> = {
  prepend: 'Al inicio (recomendado)',
  append: 'Al final',
  replace: 'Reemplazar (controlado)',
}

export const SCOPE_LABELS: Record<PIScope, string> = {
  global: 'Global',
  combo: 'Combo',
  model: 'Modelo',
  key: 'API key',
}

export function byteLength(s: string): number {
  return new TextEncoder().encode(s).length
}
