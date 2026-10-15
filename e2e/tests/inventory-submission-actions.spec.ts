import { expect, test } from '@playwright/test'

test.beforeEach(async ({ page }) => { page.setDefaultTimeout(8000) })
const adminPassword = process.env.E2E_PASSWORD

test('organization editor can open submission quality check from row actions', async ({ page }) => {
  test.setTimeout(300_000)
  await page.goto('/login')
  await page.getByPlaceholder('请输入用户名').fill(process.env.E2E_USERNAME ?? 'admin')
  await page.getByPlaceholder('请输入密码').fill(adminPassword!)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page).not.toHaveURL(/\/login(?:\?|$)/)

  const suffix = Date.now()
  const out = await page.evaluate(async (suffix) => {
    const req = async (p: string, init?: RequestInit) => {
      const r = await fetch(`/api/v1${p}`, { ...init, headers: { 'Content-Type': 'application/json', 'X-Request-ID': `ui-${p}-${suffix}`, ...(init?.headers ?? {}) } })
      const t = await r.text(); let d: any = null; try { d = t ? JSON.parse(t) : null } catch { d = t }
      if (!r.ok) throw new Error(`${init?.method ?? 'GET'} ${p} ${r.status} ${t.slice(0,200)}`)
      return d
    }
    const orgs = (await req('/organizations?page=1&page_size=100')).items
    const group = orgs.find((o: any) => o.type === 'group')
    const sub = (await req('/organizations', { method: 'POST', body: JSON.stringify({ parent_id: group.id, code: `UI_${suffix}`, name: `UI子公司_${suffix}`, short_name: `UI${suffix}`, type: 'subsidiary', manager: '负责人', contact: '13800000997', notes: 'repro' }) })).item
    const editor = (await req('/roles?page=1&page_size=100')).items.find((r: any) => r.key === 'organization_editor')
    await req('/users', { method: 'POST', body: JSON.stringify({ username: `ui_user_${suffix}`, password: 'QualityCheck123!', display_name: '填报员', organization_id: sub.id, role_key: editor.key }) })
    const task = (await req('/inventory-tasks', { method: 'POST', body: JSON.stringify({ name: `UI质检任务_${suffix}`, description: 'repro', start_at: new Date(Date.now() - 3600000).toISOString(), deadline_at: new Date(Date.now() + 7*86400000).toISOString(), status: 'active', asset_network_scope: 'all', organization_ids: [sub.id], required_fields: ['business_systems','network_assets'], quality_rules: [] }) })).item
    return { username: `ui_user_${suffix}`, taskID: task.id, orgID: sub.id }
  }, suffix)

  // change password through the UI exactly as a real first-time user does
  await page.context().clearCookies()
  await page.goto('/login')
  await page.getByPlaceholder('请输入用户名').fill(out.username)
  await page.getByPlaceholder('请输入密码').fill('QualityCheck123!')
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page).toHaveURL(/\/profile(?:\?|$)/)
  await page.locator('input[autocomplete="current-password"]').fill('QualityCheck123!')
  await page.locator('input[autocomplete="new-password"]').nth(0).fill('QualityCheck123!X')
  await page.locator('input[autocomplete="new-password"]').nth(1).fill('QualityCheck123!X')
  await page.getByRole('button', { name: '修改密码', exact: true }).click()
  await expect(page).toHaveURL(/\/dashboard$/)

  // Start the assigned progress so the detail row exposes the real 提交 action.
  const startResponse = await page.evaluate(async ({ taskID, orgID }) => {
    const response = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${orgID}/start`, { method: 'POST' })
    return { status: response.status, body: await response.text() }
  }, out)
  expect(startResponse.status).toBe(200)

  await page.goto('/inventory-tasks')
  await expect(page.getByText('填报任务', { exact: true }).first()).toBeVisible()
  await page.getByText('填报任务', { exact: true }).first().click()

  const row = page.locator('tr').filter({ hasText: 'UI质检任务' }).first()
  await expect(row).toBeVisible()
  await row.locator('button, a, .row-actions-trigger').first().click()
  await expect(page.getByText('填报任务详情', { exact: true })).toBeVisible()

  const progressTable = page.locator('.task-organization-table')
  const trigger = progressTable.getByRole('button', { name: /^操作：/ }).first()
  await expect(trigger).toBeVisible()
  await trigger.click()
  const submitBtn = page.getByRole('button', { name: '提交', exact: true }).last()
  await expect(submitBtn).toBeVisible()
  await submitBtn.click()

  await expect(page.getByText('提交填报结果', { exact: true })).toBeVisible()
  await expect(page.getByText('尚未录入业务系统', { exact: true })).toBeVisible()
  await expect(page.getByText('尚未录入网络资产', { exact: true })).toBeVisible()
  await expect(page.getByText('执行填报质量检查失败', { exact: true })).toHaveCount(0)
})

