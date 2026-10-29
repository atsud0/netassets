import { expect, test } from '@playwright/test'
import { loginSavedViewOwner } from '../helpers/saved-view-owner'

// Saving a filter view must not depend on whether the current filter happens to match
// anything. The reported failure was "请检查页面、名称和筛选条件；名称最多 100 字" when
// the list was empty, which points at a filter value the backend rejects - and the only
// value on this page that changes shape with the data is the organization filter, whose
// value is a UUID only while an organization is selected.
//
// This test drives the real page rather than the API, because the bug is in what the
// page chooses to send.
test.beforeEach(async ({ page }) => {
  await loginSavedViewOwner(page)
})

async function saveView(page: import('@playwright/test').Page, name: string) {
  await page.getByRole('button', { name: '保存当前筛选' }).click()
  const dialog = page.getByRole('dialog').filter({ hasText: '保存当前筛选' })
  await expect(dialog).toBeVisible()
  await dialog.getByPlaceholder('例如：待确认的公网域名').fill(name)
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
}

test('列表为空时保存当前筛选不再报参数错误', async ({ page }) => {
  test.setTimeout(180_000)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto('/assets')
  await expect(page).toHaveURL(/\/assets$/)

  // A keyword that cannot match anything, so the list is genuinely empty.
  const impossible = `不存在-${Date.now().toString(36)}`
  await page.getByPlaceholder('搜索资产值、名称、系统或负责人').fill(impossible)
  await page.getByRole('button', { name: '查询', exact: true }).click()
  await expect(page.locator('.el-table__row')).toHaveCount(0)
  await expect(page.locator('.asset-filter-count')).toHaveText(/共\s*0\s*条/)

  const name = `空列表视图-${Date.now().toString(36)}`
  const create = page.waitForResponse(response => response.url().includes('/saved-filter-views') && response.request().method() === 'POST')
  await saveView(page, name)
  const response = await create
  // The page's own payload is printed on failure, because the defect lives in which
  // filter value the page chose to send.
  expect(response.status(), `sent ${response.request().postData()}: ${await response.text()}`).toBe(201)
  await expect(page.getByText('当前筛选已保存')).toBeVisible()
  await expect(page.locator('.saved-filter-views .el-select')).toContainText(name)
})

test('选中组织且列表为空时保存当前筛选不再报参数错误', async ({ page }) => {
  test.setTimeout(180_000)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto('/assets')
  await expect(page).toHaveURL(/\/assets$/)

  // The reported failure needs an organization filter: it is the one asset filter whose
  // value is a UUID, so it is the only one the backend can reject. Create the subsidiary
  // here rather than assuming the shared e2e database has one.
  const group = await page.evaluate(async () => {
    const response = await fetch('/api/v1/organizations')
    if (!response.ok) throw new Error(`organizations failed: ${response.status}`)
    const data = await response.json()
    return data.items.find((item: { type: string }) => item.type === 'group')
  })
  const organization = await page.evaluate(async ({ parent }) => {
    const code = `E2E_SUB_${Date.now().toString(36)}`
    const response = await fetch('/api/v1/organizations', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ parent_id: parent.id, code, name: `空列表子公司-${code}`, type: 'subsidiary' }),
    })
    if (!response.ok) throw new Error(`create organization failed: ${response.status} ${await response.text()}`)
    return response.json()
  }, { parent: group })
  test.skip(!organization?.item?.id, '未能创建子公司')

  await page.reload()
  await page.waitForLoadState('networkidle')
  const organizationSelect = page.locator('.network-asset-filter-controls .el-select').filter({ hasText: '全部组织' })
  await organizationSelect.click()
  await page.locator('.el-select-dropdown:visible .el-select-dropdown__item').filter({ hasText: organization.item.name }).last().click()
  await page.waitForLoadState('networkidle')

  // Narrow the now organization-scoped list to nothing.
  await page.getByPlaceholder('搜索资产值、名称、系统或负责人').fill(`不存在-${Date.now().toString(36)}`)
  await page.getByRole('button', { name: '查询', exact: true }).click()
  await expect(page.locator('.asset-filter-count')).toHaveText(/共\s*0\s*条/)

  const name = `组织空列表视图-${Date.now().toString(36)}`
  const create = page.waitForResponse(response => response.url().includes('/saved-filter-views') && response.request().method() === 'POST')
  await saveView(page, name)
  const response = await create
  // The page's own payload is printed on failure, because the defect lives in which
  // filter value the page chose to send.
  expect(response.status(), `sent ${response.request().postData()}: ${await response.text()}`).toBe(201)
  await expect(page.locator('.saved-filter-views .el-select')).toContainText(name)
})

// The user-facing half of the same defect: the search box used to accept more than the
// saved-view keyword limit, so the flow could reach a refusal. The box now stops at the
// limit, so the filter that gets saved is always one the backend will accept.
test('搜索框不接受超过保存视图上限的关键词', async ({ page }) => {
  test.setTimeout(180_000)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto('/assets')
  await expect(page).toHaveURL(/\/assets$/)

  const search = page.getByPlaceholder('搜索资产值、名称、系统或负责人')
  await search.fill('长'.repeat(400))
  await page.getByRole('button', { name: '查询', exact: true }).click()

  const value = await search.inputValue()
  expect(value.length, '搜索框接受了超过 200 字的关键词').toBe(200)
  await expect(page.locator('.asset-filter-count')).toHaveText(/共\s*0\s*条/)

  const name = `超长关键词视图-${Date.now().toString(36)}`
  const create = page.waitForResponse(response => response.url().includes('/saved-filter-views') && response.request().method() === 'POST')
  await saveView(page, name)
  const response = await create
  expect(response.status(), `sent ${response.request().postData()}: ${await response.text()}`).toBe(201)
})
