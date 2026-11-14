import { afterEach, describe, expect, it, mock } from 'bun:test'
import { dswApi, statusLabel, formatCheck } from './deepseekWeb'

const FAKE = 'fake-ui-session-NOT-REAL'
const realFetch = globalThis.fetch

afterEach(() => {
  globalThis.fetch = realFetch
})

function capture(status = 200, body: unknown = {}) {
  const calls: { url: string; init: RequestInit }[] = []
  globalThis.fetch = mock(async (url: string | URL | Request, init?: RequestInit) => {
    calls.push({ url: String(url), init: init ?? {} })
    return new Response(status === 204 ? null : JSON.stringify(body), { status })
  }) as unknown as typeof fetch
  return calls
}

describe('dswApi', () => {
  it('never sends an Authorization header (cookie-only admin API)', async () => {
    const calls = capture(201, { id: 'x' })
    await dswApi.create('A', FAKE)
    const h = new Headers(calls[0].init.headers as HeadersInit)
    expect(h.has('Authorization')).toBe(false)
    expect(h.has('x-api-key')).toBe(false)
    expect(calls[0].init.credentials).toBe('same-origin')
    expect(calls[0].url).toBe('/api/admin/deepseek-web/sessions')
  })

  it('sends the rev for optimistic concurrency and encodes ids', async () => {
    const calls = capture(200, {})
    await dswApi.update('a/b', 3, { enabled: false })
    expect(calls[0].url).toBe('/api/admin/deepseek-web/sessions/a%2Fb')
    expect(JSON.parse(String(calls[0].init.body))).toEqual({ rev: 3, enabled: false })
  })

  it('surfaces the server error message', async () => {
    capture(409, { error: 'la sesión cambió' })
    await expect(dswApi.check('x')).rejects.toThrow('la sesión cambió')
  })

  it('toggles the fallback explicitly', async () => {
    const calls = capture(200, { requested: true, effective: false })
    const fs = await dswApi.setFallback(true)
    expect(calls[0].init.method).toBe('PUT')
    expect(fs.effective).toBe(false)
  })
})

describe('labels', () => {
  it('maps statuses and treats unknown values as indeterminate', () => {
    expect(statusLabel('connected')).toBe('Conectada')
    expect(statusLabel('expired')).toBe('Expirada')
    expect(statusLabel('error')).toBe('Error')
    expect(statusLabel('whatever')).toBe('Indeterminada')
    expect(formatCheck(undefined)).toBe('nunca')
  })
})
