import { expect, test, type Page } from '@playwright/test'

// "网络资产 已到期但没有显示已到期，还显示即将过期".
//
// The stored deadline and the API response were always correct; the list and the
// detail drawer rounded the remaining days with Math.ceil, so any deadline less
// than a day in the past counted as zero remaining days and fell through to the
// 即将到期 branch, tagged warning instead of danger.
//
// The asset is created through the API so the deadline really is in the past,
// then read back from the rendered page - a unit test of the arithmetic would not
// prove the page renders it.

const adminUsername = process.env.E2E_USERNAME ?? 'admin'
const adminPassword = process.env.E2E_PASSWORD

async function login(page: Page) {
  await page.goto('/login')
  await page.getByPlaceholder('请输入用户名').fill(adminUsername)
  await page.getByPlaceholder('请输入密码').fill(adminPassword!)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page).not.toHaveURL(/\/login(?:\?|$)/)
}

async function api<T>(page: Page, path: string, requestID: string, method = 'GET', body?: unknown): Promise<{ status: number; body: T }> {
  return page.evaluate(async ({ path, requestID, method, body }) => {
    const response = await fetch(`/api/v1${path}`, {
      method,
      credentials: 'include',
      headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestID },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    const text = await response.text()
    let payload: unknown = null
    if (text) {
      try { payload = JSON.parse(text) } catch { payload = text }
    }
    return { status: response.status, body: payload }
  }, { path, requestID, method, body }) as Promise<{ status: number; body: T }>
}

test('已到期的网络资产在列表和详情中都显示已到期', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  test.setTimeout(180_000)

  const stamp = Date.now()
  const prefix = `e2e-asset-expiry-label-${stamp}`
  await login(page)

  const organizations = (await api<{ items: Array<{ id: string; type: string }> }>(page, '/organizations?page=1&page_size=100', `${prefix}-orgs`)).body.items
  const group = organizations.find((item) => item.type === 'group')
  expect(group, '未找到集团根组织').toBeTruthy()

  // One hour in the past is the exact window the ceil-based label misread. An
  // expired asset further in the past would pass even with the old code, so the
  // short window is what makes this case meaningful.
  const expiredAt = new Date(Date.now() - 60 * 60 * 1000).toISOString()
  const soonAt = new Date(Date.now() + 2 * 24 * 60 * 60 * 1000).toISOString()

  const expiredValue = `expired-${stamp}.example.com`
  const soonValue = `soon-${stamp}.example.com`
  for (const [value, name, expiresAt] of [
    [expiredValue, `已过期资产_${stamp}`, expiredAt],
    [soonValue, `即将到期资产_${stamp}`, soonAt],
  ] as const) {
    const created = await api<{ item: { id: string } }>(page, '/assets', `${prefix}-create-${value}`, 'POST', {
      type: 'domain', value, name, organization_id: group!.id,
      business_system_id: '', environment: 'production', network_scope: 'public', network_area_id: '',
      status: 'active', importance: 'normal', owner_name: '有效期标签责任人', notes: '验证到期标签', expires_at: expiresAt,
      // A group administrator creating a formal asset directly must record why.
      direct_change_reason: '验证已到期标签显示',
    })
    expect(created.status, `创建 ${name} 失败: ${JSON.stringify(created.body)}`).toBe(201)
  }

  await page.goto(`/assets?q=${encodeURIComponent(expiredValue)}`)
  await page.waitForLoadState('networkidle')
  // 有效期 sits in the "更多字段" column set; the reported screenshot shows it
  // because that is the view the user had open.
  await page.getByRole('button', { name: '更多字段', exact: true }).click()
  const expiredRow = page.locator('.el-table__row').filter({ hasText: expiredValue }).first()
  await expect(expiredRow, '列表中找不到刚创建的已过期资产').toBeVisible()
  // The reported symptom, asserted on the rendered text.
  await expect(expiredRow).toContainText('已到期')
  await expect(expiredRow, '已过期资产不得显示为即将到期').not.toContainText('即将到期')
  await expect(expiredRow.locator('.el-tag--danger'), '已过期资产必须用危险色标签').toContainText('已到期')

  await page.goto(`/assets?q=${encodeURIComponent(soonValue)}`)
  await page.waitForLoadState('networkidle')
  const soonRow = page.locator('.el-table__row').filter({ hasText: soonValue }).first()
  await expect(soonRow).toBeVisible()
  await expect(soonRow, '尚未到期的资产仍应显示即将到期').toContainText('即将到期')
  await expect(soonRow, '尚未到期的资产不得显示为已到期').not.toContainText('已到期')

  // The detail drawer shares the same label, and it is a separate render path.
  await page.getByRole('button', { name: `已过期资产_${stamp}`, exact: true }).click()
  const detail = page.locator('.el-drawer:visible')
  await expect(detail).toBeVisible()
  await expect(detail).toContainText('已到期')
  await expect(detail, '详情同样不得显示为即将到期').not.toContainText('即将到期')
})
