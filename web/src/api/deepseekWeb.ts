// Admin API for DeepSeek Web sessions (DSH → InferX profile). Cookie-only on
// purpose: never attach a client API key; the server only accepts the admin
// session or the local CLI token here.
//
// TODO(auditoría DSH): the credential is ONE opaque field until the audit of
// dsh-better-bridge confirms the real session fields. Do not add cookie/token
// names here before that.

export type SessionStatus = 'connected' | 'expired' | 'error' | 'unknown'

export interface WebSession {
  id: string
  label: string
  enabled: boolean
  hasCredential: boolean
  credential: string // always a fixed mask from the server
  status: SessionStatus
  lastCheck?: string
  lastError?: string
  expiresAt?: string
  createdAt: string
  updatedAt: string
  rev: number
}

export interface FallbackState {
  requested: boolean
  effective: boolean
  reason?: string
}

export interface WebSessionsState {
  sessions: WebSession[]
  fallback: FallbackState
  schemaPending: boolean
}

const BASE = '/api/admin/deepseek-web'

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(BASE + path, {
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

export const dswApi = {
  state: () => call<WebSessionsState>('GET', '/sessions'),
  create: (label: string, credential: string, expiresAt?: string) =>
    call<WebSession>('POST', '/sessions', { label, credential, ...(expiresAt ? { expiresAt } : {}) }),
  update: (id: string, rev: number, changes: { label?: string; credential?: string; enabled?: boolean }) =>
    call<WebSession>('PATCH', `/sessions/${encodeURIComponent(id)}`, { rev, ...changes }),
  check: (id: string) => call<WebSession>('POST', `/sessions/${encodeURIComponent(id)}/check`),
  remove: (id: string) => call<void>('DELETE', `/sessions/${encodeURIComponent(id)}`),
  setFallback: (enabled: boolean) => call<FallbackState>('PUT', '/fallback', { enabled }),
}

export const STATUS_LABELS: Record<SessionStatus, string> = {
  connected: 'Conectada',
  expired: 'Expirada',
  error: 'Error',
  unknown: 'Indeterminada',
}

export function statusLabel(s: string): string {
  return STATUS_LABELS[s as SessionStatus] ?? STATUS_LABELS.unknown
}

export function formatCheck(iso?: string): string {
  if (!iso) return 'nunca'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? 'nunca' : d.toLocaleString()
}
