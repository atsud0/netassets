import { expect, test, type APIResponse, type Response } from '@playwright/test'
import { writeFile } from 'node:fs/promises'

async function body(response: APIResponse | Response, status = 200) {
  expect(response.status()).toBe(status)
  return response.json()
}

test('近10MiB精确匹配CSV真实浏览器完整提交、50页读回并安全重传去重', async ({ page }, testInfo) => {
  test.setTimeout(300_000)
  test.skip(!process.env.E2E_PASSWORD, 'E2E_PASSWORD 未配置')
  await body(await page.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }))
  const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 7)}`
  const roots = (await body(await page.request.get('/api/v1/organizations?page=1&page_size=100'))).items
  const group = roots.find((item: { type: string }) => item.type === 'group')
  expect(group).toBeTruthy()
  const org = (await body(await page.request.post('/api/v1/organizations', { data: { parent_id: group.id, code: `ATOMIC_${stamp}`, name: `文件原子导入${stamp}`, type: 'subsidiary' } }), 201)).item
  const asset = (await body(await page.request.post('/api/v1/assets', { data: { type: 'domain', value: `atomic-${stamp}.example.com`, organization_id: org.id, environment: 'production', network_scope: 'public', status: 'active', importance: 'normal', direct_change_reason: '隔离浏览器文件原子导入回归' } }), 201)).item
  const batch = (await body(await page.request.post('/api/v1/security-test-batches', { data: { batch_name: `原子导入${stamp}`, test_type: 'penetration', provider: '隔离回归', authorization_note: '仅操作自有隔离资产', start_at: new Date(Date.now() - 3600000).toISOString(), end_at: new Date(Date.now() + 86400000).toISOString(), asset_ids: [asset.id] } }), 201)).item
  await body(await page.request.post(`/api/v1/security-test-batches/${batch.id}/authorize`, { data: { comment: '真实浏览器大文件精确匹配回归' } }))
  const count = 5000
  const header = '资产类型,资产值,漏洞标题,严重等级,漏洞描述,发现时间\n'
  const title = (i: number) => `原子容量${stamp}-${i}`
  const baseRows = Array.from({ length: count }, (_, i) => `domain,${asset.value},${title(i)},high,,2026-10-28T00:00:00Z`)
  const targetBytes = (10 << 20) - 1024
  const remaining = targetBytes - Buffer.byteLength(header + baseRows.join('\n'))
  const size = Math.floor(remaining / count)
  const remainder = remaining % count
  const description = (i: number) => 'x'.repeat(size + (i < remainder ? 1 : 0))
  expect(size).toBeGreaterThan(0)
  expect(size + 1).toBeLessThanOrEqual(20_000)
  const csv = Buffer.from(header + baseRows.map((row, i) => row.replace(',high,,', `,high,${description(i)},`)).join('\n'))
  expect(csv.byteLength).toBe(targetBytes)
  const summary: Record<string, unknown> = { rows: count, bytes: csv.byteLength, organization_id: org.id, asset_id: asset.id, batch_id: batch.id }
  const pageErrors: string[] = []
  page.on('pageerror', error => pageErrors.push(error.message))
  async function upload(name: string) {
    await page.goto('/security-work-orders?tab=findings')
    await page.getByRole('button', { name: '批量导入漏洞', exact: true }).click({ timeout: 60_000 })
    const dialog = page.getByRole('dialog', { name: '批量导入漏洞与安全发现', exact: true })
    await expect(dialog).toBeVisible()
    await dialog.locator('.el-select').click()
    await page.locator('.el-select-dropdown:visible .el-select-dropdown__item').filter({ hasText: batch.name }).click()
    await dialog.locator('input[type=file]').setInputFiles({ name, mimeType: 'text/csv', buffer: csv })
    const response = page.waitForResponse(response => new URL(response.url()).pathname === '/api/v1/security-findings/import' && response.request().method() === 'POST', { timeout: 125_000 })
    const start = Date.now()
    await dialog.getByRole('button', { name: '开始导入', exact: true }).click()
    const result = (await body(await response, 201)).item
    for (const [label, value] of Object.entries({ 总行数: result.total_count, 成功: result.success_count, 待关联: result.pending_count, 重复: result.duplicate_count, 失败: result.failure_count })) {
      const statistic = dialog.locator('.el-statistic').filter({ has: page.locator('.el-statistic__head', { hasText: new RegExp(`^${label}$`) }) })
      await expect(statistic.locator('.el-statistic__number')).toHaveText(Number(value).toLocaleString('en-US'), { timeout: 60_000 })
    }
    const history = dialog.locator('.el-table__body-wrapper tr').filter({ hasText: name })
    await expect(history).toHaveCount(1, { timeout: 60_000 })
    await expect(history).toContainText(`成功 ${result.success_count} / 待关联 ${result.pending_count} / 重复 ${result.duplicate_count} / 失败 ${result.failure_count}`)
    return { result, duration_ms: Date.now() - start }
  }
  const firstName = `atomic-browser-${stamp}.csv`
  const first = await upload(firstName)
  summary.first = first
  await writeFile(testInfo.outputPath('atomic-browser-summary.json'), JSON.stringify(summary, null, 2))
  expect(first.result).toMatchObject({ status: 'completed', total_count: count, success_count: count, pending_count: 0, failure_count: 0, duplicate_count: 0 })
  const ids = new Set<string>()
  const titles = new Set<string>()
  for (let n = 1; n <= 50; n++) {
    const result = await body(await page.request.get(`/api/v1/security-findings?organization_id=${org.id}&page=${n}&page_size=100`))
    expect(result.total).toBe(count)
    expect(result.items).toHaveLength(100)
    for (const item of result.items) {
      const i = Number(item.title.slice(`原子容量${stamp}-`.length))
      expect(Number.isInteger(i) && i >= 0 && i < count).toBe(true)
      expect(item).toMatchObject({ title: title(i), asset_id: asset.id, test_batch_id: batch.id, severity: 'high', description: description(i), assignee_organization_id: org.id, source: 'import', status: 'pending' })
      expect(ids.has(item.id)).toBe(false)
      expect(titles.has(item.title)).toBe(false)
      ids.add(item.id)
      titles.add(item.title)
    }
  }
  expect(ids.size).toBe(count)
  const repeatName = `repeat-${firstName}`
  const repeat = await upload(repeatName)
  summary.repeat = repeat
  await writeFile(testInfo.outputPath('atomic-browser-summary.json'), JSON.stringify(summary, null, 2))
  // Existing import contract: no newly accepted rows is 'failed', even when all are safe duplicates.
  expect(repeat.result).toMatchObject({ status: 'failed', total_count: count, success_count: 0, pending_count: 0, failure_count: 0, duplicate_count: count })
  expect(repeat.result.errors).toHaveLength(count)
  expect(repeat.result.errors.map((item: { row: number; code: string }) => ({ row: item.row, code: item.code }))).toEqual(Array.from({ length: count }, (_, i) => ({ row: i + 2, code: 'duplicate' })))
  const after = await body(await page.request.get(`/api/v1/security-findings?organization_id=${org.id}&page=1&page_size=1`))
  expect(after.total).toBe(count)
  const histories = (await body(await page.request.get('/api/v1/security-findings/imports?page=1&page_size=100'))).items.filter((item: { file_name: string }) => [firstName, repeatName].includes(item.file_name))
  expect(histories).toHaveLength(2)
  expect(histories).toEqual(expect.arrayContaining([
    expect.objectContaining({ id: first.result.id, status: 'completed', total_count: count, success_count: count, duplicate_count: 0 }),
    expect.objectContaining({ id: repeat.result.id, status: 'failed', total_count: count, success_count: 0, duplicate_count: count }),
  ]))
  expect(pageErrors).toEqual([])
  summary.verified = { unique_ids: ids.size, unique_titles: titles.size, histories: histories.map((item: { id: string }) => item.id), page_errors: pageErrors }
  await writeFile(testInfo.outputPath('atomic-browser-summary.json'), JSON.stringify(summary, null, 2))
})
