import { expect, test, type Page } from '@playwright/test'

// "填报任务 创建填报任务，任务说明输入过程报错".
//
// The dialog says 任务说明 accepts 5000 字, but the API measured the input with
// len(), which counts UTF-8 BYTES. A Chinese glyph is three bytes, so an ordinary
// 2000 字 explanation was rejected as invalid long before the advertised limit -
// and the browser's maxlength happily let the reviewer type all of it in.
//
// The unit test proves the validator counts characters. This drives the real page
// and API with a full-length Chinese 说明, so an off-by-unit in the HTTP layer or a
// varchar limit tighter than the check still fails here.

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

test('创建填报任务时满额中文任务说明可以提交', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  test.setTimeout(240_000)

  const stamp = Date.now()
  await login(page)

  const organizations = (await api<{ items: Array<{ id: string; type: string; status: string }> }>(page, '/organizations?page=1&page_size=100', `e2e-inv-len-${stamp}-orgs`)).body.items
  const target = organizations.find((item) => item.type !== 'group' && item.status === 'enabled')
  expect(target, '未找到可用的填报组织').toBeTruthy()

  // 4480 characters of Chinese - past the ~1666 that a byte-based 5000 limit allows,
  // well inside the 5000 字 the dialog advertises.
  const description = '本季度网络资产填报范围说明。'.repeat(320)
  expect([...description].length).toBe(4480)

  const name = `填报任务中文长度-${stamp}`
  const start = new Date(Date.now() + 24 * 60 * 60 * 1000)
  const deadline = new Date(Date.now() + 8 * 24 * 60 * 60 * 1000)
  const created = await api<{ item: { id: string } }>(page, '/inventory-tasks', `e2e-inv-len-${stamp}-create`, 'POST', {
    name,
    description,
    start_at: start.toISOString(),
    deadline_at: deadline.toISOString(),
    status: 'draft',
    asset_network_scope: 'all',
    organization_ids: [target!.id],
    required_fields: ['business_systems', 'network_assets'],
    quality_rules: [],
  })
  expect(created.status, `满额中文任务说明被拒绝: ${JSON.stringify(created.body)}`).toBe(201)

  // The full text must really be stored, not truncated server-side. There is no
  // single-task GET, so the list - which returns the same record - is read back.
  const list = await api<{ items: Array<{ id: string; description: string }> }>(
    page, `/inventory-tasks?page=1&page_size=100`, `e2e-inv-len-${stamp}-list`)
  expect(list.status).toBe(200)
  const stored = list.body.items.find((item) => item.id === created.body.item.id)
  expect(stored, '新建的任务未出现在任务列表中').toBeTruthy()
  expect(stored!.description).toBe(description)

  // And it must be visible on the list page the reviewer returns to.
  await page.goto('/inventory-tasks')
  await expect(page.getByText(name, { exact: false }).first()).toBeVisible()
})
