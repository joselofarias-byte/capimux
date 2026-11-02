/** Sections a partial backup can select (keys of the backup payload). */
export const BACKUP_SECTIONS = [
  { id: 'settings', label: 'Ajustes' },
  { id: 'combos', label: 'Combos' },
  { id: 'providerConnections', label: 'Conexiones (incluye credenciales)' },
  { id: 'providerNodes', label: 'Nodos de proveedor' },
  { id: 'proxyPools', label: 'Proxy pools' },
  { id: 'apiKeys', label: 'API keys (incluye las claves)' },
  { id: 'modelAliases', label: 'Alias de modelos' },
  { id: 'customModels', label: 'Modelos personalizados' },
  { id: 'mitmAlias', label: 'Alias MITM' },
  { id: 'pricing', label: 'Precios' }
] as const

const KNOWN: ReadonlySet<string> = new Set(BACKUP_SECTIONS.map((s) => s.id))

/** Export URL: no selection means a full backup. Unknown ids are dropped. */
export function buildBackupExportURL(selected: readonly string[]): string {
  const ids = [...new Set(selected.filter((s) => KNOWN.has(s)))].sort()
  if (ids.length === 0) return '/api/settings/database'
  return `/api/settings/database?sections=${encodeURIComponent(ids.join(','))}`
}
