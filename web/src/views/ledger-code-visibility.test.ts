import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

// System and organization codes exist for imports, API joins and de-duplication,
// not for people reading a list. The ledger lists must therefore keep them out
// of their tables while still showing them on the detail page.
function read(relative: string) {
  return readFileSync(new URL(relative, import.meta.url), 'utf8')
}

function listTables(view: string, bindings: string[]) {
  // Match complete table blocks, not the el-table-column tags or the first
  // 4000 characters only. Otherwise code columns can escape the assertion.
  return Array.from(view.matchAll(/<el-table(?=\s|>)[^>]*>[\s\S]*?<\/el-table>/g), match => match[0])
    .filter(table => bindings.some(binding => table.includes(`:data="${binding}"`)))
}

describe('ledger code visibility', () => {
  it('inspects the whole list table including columns beyond the old slice limit', () => {
    const table = `<el-table :data="pageRows">${' '.repeat(4001)}<el-table-column prop="code" /></el-table>`
    expect(listTables(table, ['pageRows'])).toEqual([table])
    expect(listTables(table, ['systems'])).toEqual([])
    expect(listTables(table, ['pageRows'])[0]).toContain('prop="code"')
  })
  it('keeps the business system list free of code columns', () => {
    const view = read('./BusinessSystemsView.vue')
    const tables = listTables(view, ['filteredSystems', 'systems'])
    expect(tables.length).toBeGreaterThan(0)
    for (const chunk of tables) {
      expect(chunk).not.toContain('prop="code"')
      expect(chunk).not.toContain('label="系统编码"')
      expect(chunk).not.toContain('prop="system_code"')
    }
  })

  it('keeps the organization list free of code columns', () => {
    const view = read('./OrganizationsView.vue')
    const tables = listTables(view, ['pageRows'])
    expect(tables.length).toBeGreaterThan(0)
    for (const chunk of tables) {
      expect(chunk).not.toContain('prop="code"')
      expect(chunk).not.toContain('label="组织编码"')
      expect(chunk).not.toContain('prop="organization_code"')
    }
  })

  it('still documents that the code is a system field on the edit form', () => {
    expect(read('./BusinessSystemsView.vue')).toContain('系统字段')
    expect(read('./OrganizationsView.vue')).toContain('系统字段')
  })
})
