import { expect, test, type APIRequestContext } from '@playwright/test'
import { writeFile } from 'node:fs/promises'

test.setTimeout(240_000)

async function fixture(request: APIRequestContext) {
  const logged = await request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME, password: process.env.E2E_PASSWORD } })
  expect(logged.status(), await logged.text()).toBe(200)
  const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 7)}`
  const roots = await request.get('/api/v1/organizations?page=1&page_size=100')
  expect(roots.status(), await roots.text()).toBe(200)
  const group = (await roots.json()).items.find((item: { type: string }) => item.type === 'group')
  const organization = await request.post('/api/v1/organizations', { data: { parent_id: group.id, code: `MATCH_${stamp}`, name: `精确匹配容量${stamp}`, type: 'subsidiary' } })
  expect(organization.status(), await organization.text()).toBe(201)
  const org = (await organization.json()).item
  const createdAsset = await request.post('/api/v1/assets', { data: { type: 'domain', value: `matched-${stamp}.example.com`, organization_id: org.id, environment: 'production', network_scope: 'public', status: 'active', importance: 'normal', direct_change_reason: '精确匹配导入容量隔离回归' } })
  expect(createdAsset.status(), await createdAsset.text()).toBe(201)
  const asset = (await createdAsset.json()).item
  const createdBatch = await request.post('/api/v1/security-test-batches', { data: { batch_name: `精确匹配容量${stamp}`, test_type: 'penetration', provider: '隔离回归', authorization_note: '仅测试隔离环境已授权资产', start_at: new Date(Date.now() - 3600000).toISOString(), end_at: new Date(Date.now() + 86400000).toISOString(), asset_ids: [asset.id] } })
  expect(createdBatch.status(), await createdBatch.text()).toBe(201)
  const batch = (await createdBatch.json()).item
  const authorized = await request.post(`/api/v1/security-test-batches/${batch.id}/authorize`, { data: { comment: '隔离容量验证' } })
  expect(authorized.status(), await authorized.text()).toBe(200)
  return { stamp, org, asset, batch }
}

type ImportResult = {
  id: string
  status: string
  total_count: number
  success_count: number
  pending_count: number
  failure_count: number
  duplicate_count: number
  errors: { row: number; code: string; message: string }[]
}

const header = '资产类型,资产值,漏洞标题,严重等级,漏洞描述,发现时间\n'

test('5000行全部精确匹配入库、分页完整且重复导入不重复创建', async ({ request }, testInfo) => {
  const { stamp, org, asset, batch } = await fixture(request)
  const count = 5000
  const name = `matched-${stamp}.csv`
  const csv = Buffer.from(header + Array.from({ length: count }, (_, index) => `domain,${asset.value},精确容量${stamp}-${index},high,精确匹配完整入库${index},2026-10-28 08:00:00`).join('\n'))
  const summary: Record<string, unknown> = { name, rows: count, bytes: csv.length, organization_id: org.id, asset_id: asset.id, test_batch_id: batch.id }
  const started = Date.now()
  let uploadError: unknown
  let result: ImportResult | undefined
  try {
    const response = await request.post('/api/v1/security-findings/import', { timeout: 120_000, multipart: { test_batch_id: batch.id, file: { name, mimeType: 'text/csv', buffer: csv } } })
    summary.status = response.status()
    summary.response = await response.json()
    result = (summary.response as { item: ImportResult }).item
  } catch (error) {
    uploadError = error
    summary.error = String(error)
  }
  summary.duration_ms = Date.now() - started
  const first = await request.get(`/api/v1/security-findings?organization_id=${org.id}&page=1&page_size=100`)
  expect(first.status(), await first.text()).toBe(200)
  const firstPage = await first.json()
  summary.persisted_count = firstPage.total
  const history = await request.get('/api/v1/security-findings/imports?page=1&page_size=100')
  expect(history.status(), await history.text()).toBe(200)
  summary.history = (await history.json()).items.filter((item: { file_name: string }) => item.file_name === name)
  await writeFile(testInfo.outputPath('matched-volume-summary.json'), JSON.stringify(summary, null, 2))
  if (uploadError) throw uploadError
  expect(summary.status, JSON.stringify(summary.response)).toBe(201)
  if (!result) throw new Error('导入响应缺少结果')
  expect(result).toMatchObject({ status: 'completed', total_count: count, success_count: count, pending_count: 0, failure_count: 0, duplicate_count: 0 })
  expect(firstPage.total).toBe(count)
  expect(summary.history).toEqual([expect.objectContaining({ id: result.id, success_count: count, total_count: count })])
  const ids = new Set<string>()
  const titles = new Set<string>()
  for (let page = 1; page <= count / 100; page++) {
    const response = page === 1 ? first : await request.get(`/api/v1/security-findings?organization_id=${org.id}&page=${page}&page_size=100`)
    expect(response.status(), await response.text()).toBe(200)
    const listed = await response.json()
    expect(listed.total).toBe(count)
    expect(listed.items).toHaveLength(100)
    for (const finding of listed.items) {
      expect(finding).toMatchObject({ asset_id: asset.id, assignee_organization_id: org.id, test_batch_id: batch.id, status: 'pending', source: 'import' })
      expect(finding.title).toMatch(new RegExp(`^精确容量${stamp}-\\d+$`))
      expect(finding.description).toBe(`精确匹配完整入库${finding.title.split('-').at(-1)}`)
      ids.add(finding.id)
      titles.add(finding.title)
    }
  }
  expect(ids.size).toBe(count)
  expect(titles.size).toBe(count)
  expect(titles).toEqual(new Set(Array.from({ length: count }, (_, index) => `精确容量${stamp}-${index}`)))
  const repeatStarted = Date.now()
  const repeated = await request.post('/api/v1/security-findings/import', { timeout: 120_000, multipart: { test_batch_id: batch.id, file: { name: `repeat-${name}`, mimeType: 'text/csv', buffer: csv } } })
  expect(repeated.status(), await repeated.text()).toBe(201)
  const repeatedResult = (await repeated.json()).item
  expect(repeatedResult).toMatchObject({ total_count: count, success_count: 0, pending_count: 0, failure_count: 0, duplicate_count: count })
  expect(repeatedResult.errors).toHaveLength(count)
  expect(repeatedResult.errors[0]).toMatchObject({ row: 2, code: 'duplicate' })
  expect(repeatedResult.errors.at(-1)).toMatchObject({ row: count + 1, code: 'duplicate' })
  const after = await request.get(`/api/v1/security-findings?organization_id=${org.id}&page=1&page_size=1`)
  expect(after.status(), await after.text()).toBe(200)
  expect((await after.json()).total).toBe(count)
  summary.verified_unique_ids = ids.size
  summary.verified_unique_titles = titles.size
  const repeatedHistory = await request.get('/api/v1/security-findings/imports?page=1&page_size=100')
  expect(repeatedHistory.status(), await repeatedHistory.text()).toBe(200)
  const histories = (await repeatedHistory.json()).items.filter((item: { file_name: string }) => item.file_name === name || item.file_name === `repeat-${name}`)
  expect(histories).toHaveLength(2)
  expect(histories).toEqual(expect.arrayContaining([
    expect.objectContaining({ id: result.id, total_count: count, success_count: count, duplicate_count: 0 }),
    expect.objectContaining({ id: repeatedResult.id, total_count: count, success_count: 0, duplicate_count: count }),
  ]))
  summary.repeat = { id: repeatedResult.id, total_count: repeatedResult.total_count, duplicate_count: repeatedResult.duplicate_count, duration_ms: Date.now() - repeatStarted }

  summary.verified_histories = histories.map((item: ImportResult) => ({ id: item.id, total_count: item.total_count, success_count: item.success_count, duplicate_count: item.duplicate_count }))
  await writeFile(testInfo.outputPath('matched-volume-summary.json'), JSON.stringify(summary, null, 2))
})

for (const withBatch of [true, false]) {
  test(`两个同时导入${withBatch ? '同一批次' : '无批次'}资产的64行仅创建一次并完整记录重复行`, async ({ request }, testInfo) => {
    const { stamp, org, asset, batch } = await fixture(request)
    const testBatchID = withBatch ? batch.id : ''
    const name = `concurrent-${stamp}.csv`
    const summary: Record<string, unknown> = { organization_id: org.id, asset_id: asset.id, test_batch_id: testBatchID }
    const concurrentCount = 64
    const concurrentCSV = Buffer.from(header + Array.from({ length: concurrentCount }, (_, index) => `domain,${asset.value},并发唯一${stamp}-${index},high,并发去重竞争${index},2026-10-28 08:00:00`).join('\n'))
    const concurrentStarted = Date.now()
    const [concurrentA, concurrentB] = await Promise.all([
      request.post('/api/v1/security-findings/import', { timeout: 120_000, multipart: { test_batch_id: testBatchID, file: { name: `concurrent-a-${name}`, mimeType: 'text/csv', buffer: concurrentCSV } } }),
      request.post('/api/v1/security-findings/import', { timeout: 120_000, multipart: { test_batch_id: testBatchID, file: { name: `concurrent-b-${name}`, mimeType: 'text/csv', buffer: concurrentCSV } } }),
    ])
    summary.responses = await Promise.all([concurrentA, concurrentB].map(async response => ({ status: response.status(), body: await response.json() })))
    summary.duration_ms = Date.now() - concurrentStarted
    await writeFile(testInfo.outputPath('concurrent-summary.json'), JSON.stringify(summary, null, 2))
    expect(concurrentA.status(), await concurrentA.text()).toBe(201)
    expect(concurrentB.status(), await concurrentB.text()).toBe(201)
    const concurrentResults = [(await concurrentA.json()).item as ImportResult, (await concurrentB.json()).item as ImportResult]
    for (const concurrentResult of concurrentResults) {
      expect(concurrentResult.total_count).toBe(concurrentCount)
      expect(concurrentResult.failure_count).toBe(0)
      expect(concurrentResult.pending_count).toBe(0)
      expect(concurrentResult.success_count + concurrentResult.duplicate_count).toBe(concurrentCount)
    }
    expect(concurrentResults.reduce((total, item) => total + item.success_count, 0)).toBe(concurrentCount)
    expect(concurrentResults.reduce((total, item) => total + item.duplicate_count, 0)).toBe(concurrentCount)
    const afterConcurrent = await request.get(`/api/v1/security-findings?organization_id=${org.id}&page=1&page_size=1`)
    expect(afterConcurrent.status(), await afterConcurrent.text()).toBe(200)
    expect((await afterConcurrent.json()).total).toBe(concurrentCount)
    const historiesResponse = await request.get('/api/v1/security-findings/imports?page=1&page_size=100')
    expect(historiesResponse.status(), await historiesResponse.text()).toBe(200)
    const histories = (await historiesResponse.json()).items.filter((item: ImportResult) => concurrentResults.some(result => result.id === item.id))
    expect(histories).toHaveLength(2)
    for (const result of concurrentResults) {
      expect(result.errors).toHaveLength(result.duplicate_count)
      expect(new Set(result.errors.map(error => error.row)).size).toBe(result.duplicate_count)
      for (const error of result.errors) {
        expect(error).toMatchObject({ code: 'duplicate' })
        expect(error.row).toBeGreaterThanOrEqual(2)
        expect(error.row).toBeLessThanOrEqual(concurrentCount + 1)
      }
      expect(histories).toContainEqual(expect.objectContaining({ id: result.id, total_count: concurrentCount, success_count: result.success_count, duplicate_count: result.duplicate_count, failure_count: 0, pending_count: 0 }))
    }
    summary.verified_histories = histories
    summary.verified = { count: concurrentCount, success: concurrentResults.map(item => item.success_count), duplicate: concurrentResults.map(item => item.duplicate_count), duration_ms: Date.now() - concurrentStarted }
    await writeFile(testInfo.outputPath('concurrent-summary.json'), JSON.stringify(summary, null, 2))
  })
}
