import { expect, test, type APIResponse, type Page } from '@playwright/test'

async function body(response: APIResponse, status = 200) {
  expect(response.status()).toBe(status)
  return response.json()
}
async function login(page: Page) {
  test.skip(!process.env.E2E_PASSWORD, 'E2E_PASSWORD 未配置')
  await body(await page.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }))
}
const rows = '.policy-table-panel .el-table__body-wrapper tbody tr'

test('待确认资产真实浏览器：空白和超限不请求，中英文及表情满额完整创建', async ({ page }) => {
  test.setTimeout(180_000)
  await login(page)
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  let saves = 0
  page.on('request', request => { if (request.method() === 'POST' && new URL(request.url()).pathname === '/api/v1/asset-candidates') saves++ })
  const supporting = Promise.all(['/organizations', '/business-systems', '/network-areas', '/assets'].map(path => page.waitForResponse(response => response.request().method() === 'GET' && new URL(response.url()).pathname === `/api/v1${path}` && response.ok())))
  await page.goto('/asset-candidates')
  await supporting
  const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 7)}`
  for (const [i, unit] of ['a', '中', '🙂'].entries()) {
    await page.getByRole('button', { name: '新增待确认资产', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: '新增待确认资产', exact: true })
    const save = dialog.getByRole('button', { name: '保存线索', exact: true })
    const before = saves
    await dialog.getByLabel('资产值', { exact: true }).fill('　 ')
    await save.click()
    await expect(dialog.getByText('请输入资产值', { exact: true })).toBeVisible()
    expect(saves).toBe(before)
    await dialog.getByLabel('资产值', { exact: true }).fill(`candidate-${stamp}-${i}.example.com`)
    for (const [field, limit, message] of [['资产名称', 255, '资产名称最多 255 字'], ['来源参考', 255, '来源参考最多 255 字'], ['备注', 5000, '备注最多 5000 字']] as const) {
      const input = dialog.getByLabel(field, { exact: true })
      const invalid = unit.repeat(limit + 1)
      await input.fill(invalid)
      await save.click()
      await expect(dialog.getByText(message, { exact: true })).toBeVisible()
      await expect(input).toHaveValue(invalid)
      expect(saves).toBe(before)
      await input.fill(unit.repeat(limit))
    }
    const response = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/asset-candidates')
    await save.click()
    const created = (await body(await response, 201)).item
    await expect(dialog).toBeHidden()
    expect(created).toMatchObject({ name: unit.repeat(255), source_reference: unit.repeat(255), suggested_data: { name: unit.repeat(255), notes: unit.repeat(5000) }, status: 'pending' })
    const stored = (await body(await page.request.get(`/api/v1/asset-candidates/${created.id}`))).item
    expect(stored).toMatchObject({ name: created.name, source_reference: created.source_reference, suggested_data: created.suggested_data })
    expect(saves).toBe(before + 1)
  }
  expect(errors).toEqual([])
})

test('合成5001策略：DOM分页有界、末页搜索及未保存编辑保留，不发写请求', async ({ page }) => {
  test.setTimeout(90_000)
  await login(page)
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  let reads = 0
  let writes = 0
  page.on('request', request => { if (new URL(request.url()).pathname.startsWith('/api/v1/asset-candidates') && request.method() !== 'GET') writes++ })
  const policies = Array.from({ length: 5001 }, (_, i) => ({ organization_id: `synthetic-${i}`, organization_name: `合成策略组织_${i}`, allow_self_service: false, allowed_asset_types: [], require_enabled_organization: true }))
  await page.route('**/api/v1/asset-candidates/self-service-policies', route => { reads++; return route.fulfill({ json: { items: policies } }) })
  // This request starts only after all real supporting lists have loaded.
  const loaded = page.waitForResponse(response => new URL(response.url()).pathname === '/api/v1/asset-candidates/self-service-policies' && response.request().method() === 'GET' && response.ok())
  await page.goto('/asset-candidates')
  await loaded
  const table = page.locator(rows)
  await expect(table).toHaveCount(50)
  const panel = page.locator('.policy-table-panel')
  await panel.locator('.el-pager li.number').filter({ hasText: /^101$/ }).click()
  await expect(table).toHaveCount(1)
  await expect(table.first()).toContainText('合成策略组织_5000')
  const toggle = () => table.first().getByRole('switch').first()
  // Element Plus exposes the switch role on a visually hidden native input.
  // Use its visible wrapper, then assert the accessible input state.
  await table.first().locator('.el-switch').first().click()
  await expect(toggle()).toBeChecked()
  const search = page.getByRole('textbox', { name: '搜索组织自助确认策略', exact: true })
  await search.fill('合成策略组织_5000')
  await expect(table).toHaveCount(1)
  await expect(toggle()).toBeChecked()
  await search.clear()
  await expect(table).toHaveCount(50)
  await panel.locator('.el-pager li.number').filter({ hasText: /^101$/ }).click()
  await expect(table).toHaveCount(1)
  await expect(toggle()).toBeChecked()
  expect(reads).toBe(1)
  expect(writes).toBe(0)
  expect(errors).toEqual([])
})

test('真实集团授权策略：首屏DOM有界，完整末尾组织可搜索', async ({ page }) => {
  test.setTimeout(90_000)
  await login(page)
  const policies = (await body(await page.request.get('/api/v1/asset-candidates/self-service-policies'))).items
  expect(policies.length).toBeGreaterThan(0)
  const loaded = page.waitForResponse(response => new URL(response.url()).pathname === '/api/v1/asset-candidates/self-service-policies' && response.request().method() === 'GET' && response.ok())
  await page.goto('/asset-candidates')
  await loaded
  await expect(page.locator(rows)).toHaveCount(Math.min(50, policies.length))
  const last = policies.at(-1)
  await page.getByRole('textbox', { name: '搜索组织自助确认策略', exact: true }).fill(last.organization_name)
  const count = policies.filter((item: { organization_name: string }) => item.organization_name.toLocaleLowerCase().includes(last.organization_name.toLocaleLowerCase())).length
  await expect(page.locator(rows)).toHaveCount(Math.min(50, count))
  await expect(page.locator(rows).first()).toContainText(last.organization_name)
})
