import { describe, expect, it } from 'bun:test'
import { BACKUP_SECTIONS, buildBackupExportURL } from './backupSections'

describe('buildBackupExportURL', () => {
  it('sin selección pide el respaldo completo', () => {
    expect(buildBackupExportURL([])).toBe('/api/settings/database')
  })
  it('ordena, deduplica y descarta ids desconocidos', () => {
    expect(buildBackupExportURL(['pricing', 'combos', 'combos', 'otra'])).toBe(
      '/api/settings/database?sections=combos%2Cpricing'
    )
    expect(buildBackupExportURL(['otra'])).toBe('/api/settings/database')
  })
  it('todas las secciones tienen etiqueta en español', () => {
    for (const s of BACKUP_SECTIONS) expect(s.label.length).toBeGreaterThan(0)
    expect(BACKUP_SECTIONS.length).toBe(10)
  })
})
