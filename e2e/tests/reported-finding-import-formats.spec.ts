import { expect, test, type APIRequestContext, type Page, type TestInfo } from '@playwright/test'
import { writeFile } from 'node:fs/promises'
import { xlsxFixture } from '../helpers/xlsx-fixture'

test.setTimeout(300_000)

const count = 5000
const headers = ['资产类型', '资产值', '漏洞标题', '严重等级', '漏洞描述', '发现时间']
const description = (index: number) => `多格式内容${index} 中文🙂 <script> & "引号" '单引号' \\原样保留`
const title = (stamp: string, index: number) => `格式容量${stamp}-${index}`

async function fixture(request: APIRequestContext) {
  const login = await request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME, password: process.env.E2E_PASSWORD } })
  expect(login.status(), await login.text()).toBe(200)
  const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 7)}`
  const roots = await request.get('/api/v1/organizations?page=1&page_size=100')
  expect(roots.status(), await roots.text()).toBe(200)
  const group = (await roots.json()).items.find((item: { type: string }) => item.type === 'group')
  expect(group).toBeTruthy()
  const organization = await request.post('/api/v1/organizations', { data: { parent_id: group.id, code: `FMT_${stamp}`, name: `格式容量${stamp}`, type: 'subsidiary' } })
  expect(organization.status(), await organization.text()).toBe(201)
  const org = (await organization.json()).item
  const createdAsset = await request.post('/api/v1/assets', { data: { type: 'domain', value: `formats-${stamp}.example.com`, organization_id: org.id, environment: 'production', network_scope: 'public', status: 'active', importance: 'normal', direct_change_reason: '多格式浏览器容量隔离回归' } })
  expect(createdAsset.status(), await createdAsset.text()).toBe(201)
  const asset = (await createdAsset.json()).item
  const createdBatch = await request.post('/api/v1/security-test-batches', { data: { batch_name: `格式容量${stamp}`, test_type: 'penetration', provider: '隔离回归', authorization_note: '仅测试隔离环境已授权资产', start_at: new Date(Date.now() - 3600000).toISOString(), end_at: new Date(Date.now() + 86400000).toISOString(), asset_ids: [asset.id] } })
  expect(createdBatch.status(), await createdBatch.text()).toBe(201)
  const batch = (await createdBatch.json()).item
  const authorized = await request.post(`/api/v1/security-test-batches/${batch.id}/authorize`, { data: { comment: '多格式浏览器隔离容量验证' } })
  expect(authorized.status(), await authorized.text()).toBe(200)
  return { stamp, org, asset, batch }
}

function formatFile(format: 'json' | 'xlsx', rows: number, stamp: string, assetValue: string): Buffer {
  if (format === 'json') return Buffer.from(JSON.stringify({ findings: Array.from({ length: rows }, (_, index) => ({ name: title(stamp, index), severity: 'high', summary: description(index), assets: [{ type: 'domain', value: assetValue }], discovered_at: '2026-10-28T00:00:00Z' })) }))
  return xlsxFixture([headers, ...Array.from({ length: rows }, (_, index) => ['domain', assetValue, title(stamp, index), '高危', description(index), '2026-10-28T00:00:00Z'])])
}

async function browserUpload(page: Page, testInfo: TestInfo, batchName: string, file: { name: string; mimeType: string; buffer: Buffer }, summary: Record<string, unknown>) {
  await page.goto('/security-work-orders?tab=findings')
  await page.getByRole('button', { name: '批量导入漏洞', exact: true }).click({ timeout: 60_000 })
  await expect(page.getByRole('dialog', { name: '批量导入漏洞与安全发现', exact: true })).toBeVisible({ timeout: 60_000 })
  const dialog = page.getByRole('dialog', { name: '批量导入漏洞与安全发现', exact: true })
  await dialog.locator('.el-select').click()
  await page.locator('.el-select-dropdown:visible .el-select-dropdown__item').filter({ hasText: batchName }).click()
  await dialog.locator('input[type=file]').setInputFiles(file)
  const started = Date.now()
  const responsePromise = page.waitForResponse(response => response.url().endsWith('/api/v1/security-findings/import') && response.request().method() === 'POST', { timeout: 125_000 })
  await dialog.getByRole('button', { name: '开始导入', exact: true }).click()
  const response = await responsePromise
  summary.upload_ms = Date.now() - started
  summary.http_status = response.status()
  summary.response = await response.json()
  await writeFile(testInfo.outputPath('format-summary.json'), JSON.stringify(summary, null, 2))
  expect(response.status(), JSON.stringify(summary.response)).toBe(201)
  const result = (summary.response as { item: { id: string; total_count: number; success_count: number; pending_count: number; duplicate_count: number; failure_count: number } }).item
  for (const [label, value] of Object.entries({ 总行数: result.total_count, 成功: result.success_count, 待关联: result.pending_count, 重复: result.duplicate_count, 失败: result.failure_count })) {
    const statistic = dialog.locator('.el-statistic').filter({ has: page.locator('.el-statistic__head', { hasText: new RegExp(`^${label}$`) }) })
    await expect(statistic.locator('.el-statistic__number')).toHaveText(value.toLocaleString('en-US'), { timeout: 60_000 })
  }
  const historyRow = dialog.locator('.el-table__body-wrapper tr').filter({ hasText: file.name })
  await expect(historyRow).toHaveCount(1, { timeout: 60_000 })
  await expect(historyRow).toContainText(`成功 ${result.success_count} / 待关联 ${result.pending_count} / 重复 ${result.duplicate_count} / 失败 ${result.failure_count}`)
  await page.screenshot({ path: testInfo.outputPath('browser-import-result.png'), fullPage: true })
  return { result, dialog }
}

for (const format of ['json', 'xlsx'] as const) {
  test(`${format.toUpperCase()}浏览器上传5000条精确匹配、完整读回、重传去重与5001行拒绝`, async ({ page }, testInfo) => {
    page.setDefaultTimeout(20_000)
    const { stamp, org, asset, batch } = await fixture(page.request)
    const name = `browser-format-${stamp}.${format}`
    const mimeType = format === 'json' ? 'application/json' : 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet'
    const buffer = formatFile(format, count, stamp, asset.value)
    const summary: Record<string, unknown> = { format, name, rows: count, bytes: buffer.length, organization_id: org.id, asset_id: asset.id, test_batch_id: batch.id }
    const { result } = await browserUpload(page, testInfo, batch.name, { name, mimeType, buffer }, summary)
    expect(result).toMatchObject({ file_type: format, status: 'completed', total_count: count, success_count: count, pending_count: 0, duplicate_count: 0, failure_count: 0, errors: [] })
    const ids = new Set<string>()
    const titles = new Set<string>()
    for (let pageNumber = 1; pageNumber <= 50; pageNumber++) {
      const response = await page.request.get(`/api/v1/security-findings?organization_id=${org.id}&page=${pageNumber}&page_size=100`)
      expect(response.status(), await response.text()).toBe(200)
      const listed = await response.json()
      expect(listed.total).toBe(count)
      expect(listed.items).toHaveLength(100)
      for (const finding of listed.items) {
        const index = Number(finding.title.slice(`格式容量${stamp}-`.length))
        expect(Number.isInteger(index) && index >= 0 && index < count).toBe(true)
        expect(finding).toMatchObject({ title: title(stamp, index), description: description(index), asset_id: asset.id, assignee_organization_id: org.id, test_batch_id: batch.id, severity: 'high', status: 'pending', source: 'import' })
        expect(new Date(finding.discovered_at).toISOString()).toBe('2026-10-28T00:00:00.000Z')
        ids.add(finding.id)
        titles.add(finding.title)
      }
    }
    expect(ids.size).toBe(count)
    expect(titles).toEqual(new Set(Array.from({ length: count }, (_, index) => title(stamp, index))))
    const repeated = await page.request.post('/api/v1/security-findings/import', { timeout: 120_000, multipart: { test_batch_id: batch.id, file: { name: `repeat-${name}`, mimeType, buffer } } })
    expect(repeated.status(), await repeated.text()).toBe(201)
    const repeatedResult = (await repeated.json()).item
    expect(repeatedResult).toMatchObject({ file_type: format, total_count: count, success_count: 0, pending_count: 0, duplicate_count: count, failure_count: 0 })
    expect(repeatedResult.errors).toHaveLength(count)
    expect(repeatedResult.errors.map((error: { code: string; row: number }) => ({ code: error.code, row: error.row }))).toEqual(Array.from({ length: count }, (_, index) => ({ code: 'duplicate', row: index + 2 })))
    const rejected = await page.request.post('/api/v1/security-findings/import', { timeout: 120_000, multipart: { test_batch_id: batch.id, file: { name: `excess-${name}`, mimeType, buffer: formatFile(format, count + 1, stamp, asset.value) } } })
    expect(rejected.status(), await rejected.text()).toBe(400)
    expect((await rejected.json()).error.code).toBe('invalid_finding_import')
    const history = await page.request.get('/api/v1/security-findings/imports?page=1&page_size=100')
    expect(history.status(), await history.text()).toBe(200)
    const histories = (await history.json()).items.filter((item: { file_name: string }) => item.file_name.includes(stamp))
    expect(histories).toHaveLength(2)
    expect(histories).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: result.id, total_count: count, success_count: count, duplicate_count: 0 }),
      expect.objectContaining({ id: repeatedResult.id, total_count: count, success_count: 0, duplicate_count: count }),
    ]))
    const after = await page.request.get(`/api/v1/security-findings?organization_id=${org.id}&page=1&page_size=1`)
    expect(after.status(), await after.text()).toBe(200)
    expect((await after.json()).total).toBe(count)
    summary.verified_unique_ids = ids.size
    summary.verified_unique_titles = titles.size
    summary.repeat = { id: repeatedResult.id, duplicate_count: repeatedResult.duplicate_count }
    summary.rejected_status = rejected.status()
    summary.verified_histories = histories
    await writeFile(testInfo.outputPath('format-summary.json'), JSON.stringify(summary, null, 2))
  })
}

test('近10MiB CSV浏览器上传5000个不同未知资产并完整读回待关联项', async ({ page }, testInfo) => {
  page.setDefaultTimeout(20_000)
  const { stamp, org, batch } = await fixture(page.request)
  const prefix = `浏览器大文件${stamp}-`
  const assetValue = (index: number) => `browser-missing-${stamp}-${index}.invalid`
  const header = `${headers.join(',')}\n`
  const baseRows = Array.from({ length: count }, (_, index) => `domain,${assetValue(index)},${prefix}${index},high,,2026-10-28T00:00:00Z`)
  const targetBytes = (10 << 20) - 1024
  const fixedBytes = Buffer.byteLength(header + baseRows.join('\n'))
  const remaining = targetBytes - fixedBytes
  const descriptionSize = Math.floor(remaining / count)
  const remainder = remaining % count
  expect(descriptionSize).toBeGreaterThan(0)
  expect(descriptionSize + 1).toBeLessThanOrEqual(20_000)
  const rowDescription = (index: number) => 'x'.repeat(descriptionSize + (index < remainder ? 1 : 0))
  const buffer = Buffer.from(header + baseRows.map((row, index) => row.replace(',high,,', `,high,${rowDescription(index)},`)).join('\n'))
  expect(buffer.length).toBe(targetBytes)
  const name = `browser-near-limit-${stamp}.csv`
  const summary: Record<string, unknown> = { format: 'csv', name, rows: count, bytes: buffer.length, organization_id: org.id, test_batch_id: batch.id }
  const { result, dialog } = await browserUpload(page, testInfo, batch.name, { name, mimeType: 'text/csv', buffer }, summary)
  expect(result).toMatchObject({ file_type: 'csv', status: 'partial', total_count: count, success_count: 0, pending_count: count, duplicate_count: 0, failure_count: 0 })
  const ids = new Set<string>()
  const values = new Set<string>()
  const titles = new Set<string>()
  for (let pageNumber = 1; pageNumber <= 50; pageNumber++) {
    const response = await page.request.get(`/api/v1/security-findings/import-candidates?test_batch_id=${batch.id}&status=pending&keyword=${encodeURIComponent(prefix)}&page=${pageNumber}&page_size=100`)
    expect(response.status(), await response.text()).toBe(200)
    const listed = await response.json()
    expect(listed.total).toBe(count)
    expect(listed.items).toHaveLength(100)
    for (const candidate of listed.items) {
      const index = Number(candidate.title.slice(prefix.length))
      expect(Number.isInteger(index) && index >= 0 && index < count).toBe(true)
      expect(candidate).toMatchObject({ finding_import_id: result.id, test_batch_id: batch.id, source_row_number: index + 2, asset_type: 'domain', asset_value: assetValue(index), normalized_value: assetValue(index), title: `${prefix}${index}`, severity: 'high', description: rowDescription(index), status: 'pending' })
      expect(new Date(candidate.discovered_at).toISOString()).toBe('2026-10-28T00:00:00.000Z')
      ids.add(candidate.id)
      values.add(candidate.asset_value)
      titles.add(candidate.title)
    }
  }
  summary.verified_unique_candidate_ids = ids.size
  summary.verified_unique_asset_values = values.size
  summary.verified_unique_titles = titles.size
  await writeFile(testInfo.outputPath('format-summary.json'), JSON.stringify(summary, null, 2))
  expect(ids.size).toBe(count)
  expect(values).toEqual(new Set(Array.from({ length: count }, (_, index) => assetValue(index))))
  expect(titles).toEqual(new Set(Array.from({ length: count }, (_, index) => `${prefix}${index}`)))
  const findings = await page.request.get(`/api/v1/security-findings?organization_id=${org.id}&page=1&page_size=1`)
  expect(findings.status(), await findings.text()).toBe(200)
  expect((await findings.json()).total).toBe(0)
  await dialog.getByRole('button', { name: `处理 ${count} 条待关联漏洞`, exact: true }).click()
  await expect(dialog).not.toBeVisible()
  const search = page.getByPlaceholder('资产值或漏洞标题')
  await search.fill(prefix)
  const filtered = page.waitForResponse(response => response.url().includes('/api/v1/security-findings/import-candidates?') && response.url().includes(encodeURIComponent(prefix)) && response.request().method() === 'GET')
  await search.press('Enter')
  const filteredResponse = await filtered
  expect(filteredResponse.status()).toBe(200)
  expect((await filteredResponse.json()).total).toBe(count)
  await expect(page.locator('.el-pagination__total')).toHaveText('共 5000 条')
  const rows = page.locator('.el-table__body-wrapper tr')
  await expect(rows).toHaveCount(20)
  for (const row of await rows.all()) await expect(row).toContainText(prefix)
  await page.screenshot({ path: testInfo.outputPath('browser-pending-candidates.png'), fullPage: true })
  summary.browser_filtered_total = count
  await writeFile(testInfo.outputPath('format-summary.json'), JSON.stringify(summary, null, 2))
})
