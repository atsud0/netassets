import { expect, test, type APIRequestContext, type APIResponse } from '@playwright/test'

const adminUsername = process.env.E2E_USERNAME ?? 'admin'
const adminPassword = process.env.E2E_PASSWORD

type ScanBatch = {
  id: string
  trigger_type: 'manual' | 'scheduled'
  scheduled_for?: string
  created_by_name?: string
  created_at: string
}

async function expectStatus(response: APIResponse, expected: number, label: string) {
  if (response.status() !== expected) {
    throw new Error(`${label}: HTTP ${response.status()}，预期 ${expected}；响应：${await response.text()}`)
  }
}

async function waitForScheduledBatch(request: APIRequestContext) {
  const deadline = Date.now() + 30_000
  while (Date.now() < deadline) {
    const response = await request.get('/api/v1/data-quality-scan-batches?page=1&page_size=100')
    await expectStatus(response, 200, '读取自动质量巡检批次')
    const items = (await response.json() as { items: ScanBatch[] }).items
    const scheduled = items.find((item) => item.trigger_type === 'scheduled')
    if (scheduled) return scheduled
    await new Promise((resolve) => setTimeout(resolve, 500))
  }
  throw new Error('系统启动后 30 秒内未生成每日自动质量巡检批次')
}

test('每日质量巡检使用系统身份、批次来源和审计留痕', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  const login = await page.request.post('/api/v1/auth/login', { data: { username: adminUsername, password: adminPassword } })
  await expectStatus(login, 200, '集团管理员登录')

  const batch = await waitForScheduledBatch(page.request)
  expect(batch.id).toMatch(/^[0-9a-f-]{36}$/i)
  expect(batch.scheduled_for).toMatch(/^\d{4}-\d{2}-\d{2}$/)
  expect(batch.created_by_name ?? '').toBe('')

  const audits = await page.request.get('/api/v1/audit-logs?page=1&page_size=100&action=data_quality.scan.scheduled')
  await expectStatus(audits, 200, '读取自动质量巡检审计')
  const auditItems = (await audits.json() as {
    items: Array<{ user_id?: string; resource_id?: string; result: string; details: { trigger_type?: string; scheduled_for?: string } }>
  }).items
  const audit = auditItems.find((item) => item.resource_id === batch.id)
  expect(audit, '自动巡检批次缺少对应审计日志').toBeTruthy()
  expect(audit?.user_id ?? '').toBe('')
  expect(audit?.result).toBe('success')
  expect(audit?.details.trigger_type).toBe('scheduled')
  expect(audit?.details.scheduled_for).toBe(batch.scheduled_for)

  await page.goto('/data-quality-issues')
  await expect(page.getByText('系统自动', { exact: true }).first()).toBeVisible()
  await expect(page.getByText('系统调度器', { exact: true }).first()).toBeVisible()
})
