import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

// System and organization codes exist for imports, API joins and de-duplication,
// not for people reading a list. The ledger lists must therefore keep them out
// of their tables while still showing them on the detail page.
function read(relative: string) {
  return readFileSync(new URL(relative, import.meta.url), 'utf8')
}

describe('ledger code visibility', () => {
  it('keeps the business system list free of code columns', () => {
    const view = read('./BusinessSystemsView.vue')
    const tables = view.split('<el-table')
    const listTables = tables.filter((chunk) => chunk.includes(':data="filteredSystems"') || chunk.includes(':data="systems"'))
    expect(listTables.length).toBeGreaterThan(0)
    for (const chunk of listTables) {
      expect(chunk.slice(0, 4000)).not.toContain('prop="code"')
      expect(chunk.slice(0, 4000)).not.toContain('label="系统编码"')
      expect(chunk.slice(0, 4000)).not.toContain('prop="system_code"')
    }
  })

  it('keeps the organization list free of code columns', () => {
    const view = read('./OrganizationsView.vue')
    const tables = view.split('<el-table')
    const listTables = tables.filter((chunk) => chunk.includes(':data="organizations"'))
    expect(listTables.length).toBeGreaterThan(0)
    for (const chunk of listTables) {
      expect(chunk.slice(0, 4000)).not.toContain('prop="code"')
      expect(chunk.slice(0, 4000)).not.toContain('label="组织编码"')
      expect(chunk.slice(0, 4000)).not.toContain('prop="organization_code"')
    }
  })

  it('still documents that the code is a system field on the edit form', () => {
    expect(read('./BusinessSystemsView.vue')).toContain('系统字段')
    expect(read('./OrganizationsView.vue')).toContain('系统字段')
  })
})
