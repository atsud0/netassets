import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = readFileSync(join(process.cwd(), 'src/views/AssetsView.vue'), 'utf8')

describe('资产正式台账并发保护', () => {
  it('资产 PATCH payload carries the version returned by the list/detail API', () => {
    const start = source.indexOf('function assetPayload(')
    const end = source.indexOf('\nasync function submit', start)
    expect(start).toBeGreaterThanOrEqual(0)
    expect(end).toBeGreaterThan(start)
    const body = source.slice(start, end)
    expect(body).toContain('item.version > 0')
    expect(body).toContain('expected_version: item.version')
  })

  it('归档/启用 uses the same version-aware payload instead of hand-building a stale request', () => {
    const start = source.indexOf('async function toggleArchive(')
    const end = source.indexOf('\nasync function removeAsset', start)
    expect(start).toBeGreaterThanOrEqual(0)
    expect(end).toBeGreaterThan(start)
    expect(source.slice(start, end)).toContain('assetPayload(item, { status: archived ? \'active\' : \'archived\' })')
  })
})
