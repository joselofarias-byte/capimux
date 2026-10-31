// Admin API for per-key quotas. Calls are cookie-only on purpose: the
// dashboard client attaches a stored client API key as Authorization, and this
// API refuses any request that carries one (403) to keep keys out of it.

export interface KeyLimits {
  rpm?: number
  tpm?: number
  concurrency?: number
  tokens24h?: number
  tokens7d?: number
  tokens30d?: number
  updatedAt?: string
}

export interface KeyUsage {
  inflight: number
  rpm: number
  tpm: number
  tokens24h: number
  tokens7d: number
  tokens30d: number
  requests24h: number
  requests7d: number
  requests30d: number
}

export interface KeyQuotaRow {
  id: string
  name: string
  active: boolean
  limits: KeyLimits
  usage: KeyUsage
}

export interface KeyQuotaState {
  enabled: boolean
  keys: KeyQuotaRow[]
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: 'same-origin',
    headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 204) return undefined as T
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error(data?.error || `Error ${res.status}`)
  return data as T
}

export const kqApi = {
  state: () => call<KeyQuotaState>('GET', '/api/api-key-limits'),
  setEnabled: (enabled: boolean) => call<{ enabled: boolean }>('PUT', '/api/api-key-limits/toggle', { enabled }),
  save: (id: string, limits: KeyLimits) => call<KeyLimits>('PUT', `/api/api-key-limits/${encodeURIComponent(id)}`, limits),
  remove: (id: string) => call<void>('DELETE', `/api/api-key-limits/${encodeURIComponent(id)}`),
}

export type Period = '24h' | '7d' | '30d'

export const PERIOD_LABELS: Record<Period, string> = { '24h': '24 h', '7d': '7 días', '30d': '30 días' }

export function periodTokens(u: KeyUsage | undefined, p: Period): number {
  if (!u) return 0
  return p === '24h' ? u.tokens24h : p === '7d' ? u.tokens7d : u.tokens30d
}

export function periodRequests(u: KeyUsage | undefined, p: Period): number {
  if (!u) return 0
  return p === '24h' ? u.requests24h : p === '7d' ? u.requests7d : u.requests30d
}

export function periodLimit(l: KeyLimits | undefined, p: Period): number {
  if (!l) return 0
  return (p === '24h' ? l.tokens24h : p === '7d' ? l.tokens7d : l.tokens30d) || 0
}

export function formatNumber(n: number): string {
  if (n >= 1e9) return (n / 1e9).toFixed(1).replace('.', ',') + ' B'
  if (n >= 1e6) return (n / 1e6).toFixed(1).replace('.', ',') + ' M'
  if (n >= 1e3) return (n / 1e3).toFixed(1).replace('.', ',') + ' k'
  return String(n)
}
