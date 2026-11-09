import type { MultiagentStrategy, MultiagentTask } from '../api/client'

export const MULTIAGENT_TASKS: { value: MultiagentTask; label: string }[] = [
  { value: 'programming', label: 'Programación' },
  { value: 'review', label: 'Revisión' },
  { value: 'analysis', label: 'Análisis' },
  { value: 'conversation', label: 'Conversación' },
  { value: 'translation', label: 'Traducción' },
  { value: 'reasoning', label: 'Razonamiento' },
]
export const MULTIAGENT_STRATEGIES: { value: MultiagentStrategy; label: string; description: string }[] = [
  { value: 'parallel', label: 'En paralelo', description: 'Compará las respuestas de todos los modelos seleccionados.' },
  { value: 'priority', label: 'Prioridad', description: 'Consultá los modelos en orden hasta obtener la primera respuesta válida, con respaldo seguro.' },
  { value: 'fallback', label: 'Respaldo (fallback)', description: 'Igual que prioridad: consultá en orden hasta la primera respuesta válida, con respaldo seguro.' },
  { value: 'round-robin', label: 'Rotación (round-robin)', description: 'Consultá un solo modelo por turno del servidor, sin respaldo automático. La secuencia se reinicia al reiniciar el proceso.' },
  { value: 'manual', label: 'Manual', description: 'Usá únicamente el primer modelo de la lista ordenada.' },
]
export const PROFILE_STORAGE_KEY = 'capimux.multiagent.profiles.v1'
const MAX_MODELS = 10
const MAX_STORAGE_LENGTH = 40000
export interface MultiagentProfile {
  task: MultiagentTask
  strategy: MultiagentStrategy
  models: string[]
}
type ProfileStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>

export function modelLimit(limit: number): number {
  return Number.isInteger(limit) && limit > 0 ? Math.min(limit, MAX_MODELS) : 0
}

export function normalizeProfiles(value: unknown): MultiagentProfile[] {
  if (!Array.isArray(value)) return []
  const profiles: MultiagentProfile[] = []
  for (const entry of value.slice(0, MULTIAGENT_TASKS.length)) {
    if (!entry || typeof entry !== 'object') continue
    const { task, strategy, models } = entry
    if (!MULTIAGENT_TASKS.some(option => option.value === task) ||
      !MULTIAGENT_STRATEGIES.some(option => option.value === strategy) ||
      profiles.some(profile => profile.task === task) || !Array.isArray(models) ||
      models.length < 1 || models.length > MAX_MODELS ||
      !models.every(model => typeof model === 'string' && model.length > 0 &&
        model.length <= 512 && !Array.from(model as string).some(character =>
          character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127))) continue
    profiles.push({ task, strategy, models: [...new Set<string>(models)] })
  }
  return profiles
}

export function readProfiles(storage: ProfileStorage | null): MultiagentProfile[] {
  try {
    const raw = storage?.getItem(PROFILE_STORAGE_KEY)
    if (!raw || raw.length > MAX_STORAGE_LENGTH) return []
    const parsed = JSON.parse(raw)
    return parsed?.version === 1 ? normalizeProfiles(parsed.profiles) : []
  } catch { return [] }
}

export function writeProfiles(storage: ProfileStorage | null, profiles: MultiagentProfile[]): boolean {
  if (!storage) return false
  try {
    const safe = normalizeProfiles(profiles)
    if (safe.length === 0) storage.removeItem(PROFILE_STORAGE_KEY)
    else storage.setItem(PROFILE_STORAGE_KEY, JSON.stringify({ version: 1, profiles: safe }))
    return true
  } catch { return false }
}
