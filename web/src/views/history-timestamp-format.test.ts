import { describe, expect, it } from 'vitest'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

const viewDir = join(__dirname)
const vueFiles = readdirSync(viewDir).filter((name) => name.endsWith('.vue'))

// Guards the reported "查看操作下拉历史，时间显示错误" defect.
//
// The backend serializes timestamps as RFC 3339 in UTC, for example
// "2026-10-14T01:41:29.125837Z". Binding such a value straight into a table
// column or a timeline timestamp renders that raw string to the user: a UTC
// wall clock that is hours away from local time, with no date or year and no
// seconds. These assertions fail if any history surface regresses to raw output.
const RAW_TIMESTAMP_BINDINGS = [
  { pattern: /:timestamp="item\.created_at"/, what: 'el-timeline-item :timestamp bound to a raw created_at' },
  { pattern: /prop="created_at"/, what: 'el-table-column prop="created_at" rendering the raw value' },
]

describe('history timestamps are formatted, not printed raw', () => {
  for (const file of vueFiles) {
    it(`${file} contains no raw timestamp binding`, () => {
      const source = readFileSync(join(viewDir, file), 'utf8')
      for (const { pattern, what } of RAW_TIMESTAMP_BINDINGS) {
        expect(pattern.test(source), `${file}: ${what}`).toBe(false)
      }
    })
  }

  it('formats the timestamp in both history timelines', () => {
    for (const file of ['AssetsView.vue', 'BusinessSystemsView.vue']) {
      const source = readFileSync(join(viewDir, file), 'utf8')
      expect(source, `${file} must format the change-history timestamp`).toContain(':timestamp="formatDateTime(item.created_at)"')
      expect(source, `${file} must import the shared formatter`).toContain("from '../utils/datetime'")
    }
  })
})
