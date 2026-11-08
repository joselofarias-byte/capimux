import { describe, expect, it } from 'bun:test'
import { modelLimit, normalizeProfiles, readProfiles, writeProfiles, PROFILE_STORAGE_KEY, MULTIAGENT_TASKS } from './multiagentProfiles'

const profile = { task: 'programming' as const, strategy: 'parallel' as const, models: ['provider/model', 'other/model'] }
function memoryStorage() {
  const data = new Map<string, string>()
  return {
    getItem: (key: string) => data.get(key) ?? null,
    setItem: (key: string, value: string) => { data.set(key, value) },
    removeItem: (key: string) => { data.delete(key) },
  }
}

describe('multiagent profiles', () => {
  it('respects policy limits while allowing legacy single-model use and up to ten models', () => {
    expect([1, 8, 10, 15, 0, -1, NaN, 2.5].map(modelLimit)).toEqual([1, 8, 10, 10, 0, 0, 0, 0])
    expect(normalizeProfiles([{ ...profile, models: ['one/model'] }])).toHaveLength(1)
    expect(normalizeProfiles([{ ...profile, models: Array.from({ length: 10 }, (_, i) => `p/${i}`) }])[0]?.models).toHaveLength(10)
  })
  it('preserves exact provider/model IDs and priority order without copying private fields', () => {
    const storage = memoryStorage()
    expect(writeProfiles(storage, [{ ...profile, prompt: 'private', apiKey: 'secret', output: 'sensitive' } as typeof profile])).toBe(true)
    expect(readProfiles(storage)).toEqual([profile])
    const raw = storage.getItem(PROFILE_STORAGE_KEY)!
    for (const secret of ['private', 'secret', 'sensitive', 'apiKey', 'prompt', 'output']) expect(raw).not.toContain(secret)
    expect(normalizeProfiles([{ ...profile, models: ['a/x', 'b/x', 'a/x'] }])[0]?.models).toEqual(['a/x', 'b/x'])
  })
  it('rejects malformed profiles and unknown enums', () => {
    for (const invalid of [null, {}, 'text', [null], [{ ...profile, task: 'unknown' }],
      [{ ...profile, strategy: 'unknown' }], [{ ...profile, models: [] }],
      [{ ...profile, models: [null] }], [{ ...profile, models: ['\n'] }],
      [{ ...profile, models: ['a'.repeat(513)] }], [{ ...profile, models: Array(11).fill('p/m') }]]) {
      expect(normalizeProfiles(invalid)).toEqual([])
    }
  })
  it('bounds profiles to one per task', () => {
    expect(normalizeProfiles([profile, profile])).toEqual([profile])
    const all = MULTIAGENT_TASKS.map(({ value }) => ({ ...profile, task: value }))
    expect(normalizeProfiles([...all, ...all])).toHaveLength(6)
  })
  it('handles malformed, oversized, and incompatible storage without throwing', () => {
    const storage = memoryStorage()
    for (const raw of ['{', 'null', '[]', '{"version":2,"profiles":[]}', ' '.repeat(40001)]) {
      storage.setItem(PROFILE_STORAGE_KEY, raw)
      expect(readProfiles(storage)).toEqual([])
    }
    expect(readProfiles(null)).toEqual([])
    expect(writeProfiles(null, [profile])).toBe(false)
  })
  it('handles blocked storage or quota exhaustion and removes deleted profiles', () => {
    const blocked = { getItem() { throw Error('blocked') }, setItem() { throw Error('quota') }, removeItem() { throw Error('blocked') } }
    expect(readProfiles(blocked)).toEqual([])
    expect(writeProfiles(blocked, [profile])).toBe(false)
    expect(writeProfiles(blocked, [])).toBe(false)
    const storage = memoryStorage()
    writeProfiles(storage, [profile])
    expect(writeProfiles(storage, [])).toBe(true)
    expect(storage.getItem(PROFILE_STORAGE_KEY)).toBeNull()
  })
})
