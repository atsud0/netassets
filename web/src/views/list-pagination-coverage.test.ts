import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

describe('all view pagination uses the shared scroll contract', () => {
  it('covers primary lists and secondary drawer/dialog pagination', () => {
    const views = readdirSync(__dirname).filter(name => name.endsWith('.vue'))
    let count = 0
    for (const name of views) {
      const source = readFileSync(join(__dirname, name), 'utf8')
      expect(source, name).not.toContain('<el-pagination')
      const paginations = source.match(/<ListPagination\b[^>]*>/g) ?? []
      for (const pagination of paginations) expect(pagination, name).toContain(':reset-key=')
      count += paginations.length
    }
    expect(count).toBeGreaterThanOrEqual(45)
  })

  it('removes only the work-order statistics and keeps candidate statistics', () => {
    expect(readFileSync(join(__dirname, 'SecurityWorkOrdersView.vue'), 'utf8')).not.toContain('work-order-stat-list')
    expect(readFileSync(join(__dirname, 'AssetCandidatesView.vue'), 'utf8')).toContain('filter-stat-list')
  })
})
