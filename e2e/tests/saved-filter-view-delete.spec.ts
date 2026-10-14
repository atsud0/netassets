import { expect, test } from '@playwright/test'

// Deleting a saved view has to leave the page in the state the deletion implies:
// the view is gone from the selector, and the asset list is reloaded so the page and
// the view list agree again. Otherwise the user keeps looking at a result set that is
// named by a view which no longer exists, with no indication that anything happened.
test.beforeEach(async ({ page }) => {
  const response = await page.request.post('/api/v1/auth/login', {
    data: { username: process.env.E2E_USERNAME, password: process.env.E2E_PASSWORD },
  })
  expect(response.ok()).toBeTruthy()
})

test('删除保存视图后网络资产列表立即刷新', async ({ page }) => {
  test.setTimeout(180_000)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto('/assets')
  await expect(page).toHaveURL(/\/assets$/)

  const name = `视图删除检查-${Date.now().toString(36)}`
  await page.getByRole('button', { name: '保存当前筛选' }).click()
  const dialog = page.getByRole('dialog').filter({ hasText: '保存当前筛选' })
  await expect(dialog).toBeVisible()
  await dialog.getByPlaceholder('例如：待确认的公网域名').fill(name)
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).toBeHidden()

  const select = page.locator('.saved-filter-views .el-select')
  await expect(select).toContainText(name)

  // Count only requests issued after this point, so the reload the fix causes is the
  // thing being measured rather than the ones caused by navigation and by saving.
  let assetListRequests = 0
  page.on('request', request => {
    if (/\/api\/v1\/assets(\?|$)/.test(request.url())) assetListRequests += 1
  })

  await page.getByRole('button', { name: '删除', exact: true }).click()
  const confirm = page.locator('.el-message-box:visible')
  await expect(confirm).toBeVisible()
  await confirm.getByRole('button', { name: '删除', exact: true }).click()

  await expect(page.getByText('保存视图已删除')).toBeVisible()
  await expect(select).not.toContainText(name)
  await expect
    .poll(() => assetListRequests, { message: '删除视图后网络资产列表没有重新加载' })
    .toBeGreaterThan(0)
})

test('删除的是默认视图时，列表同样刷新', async ({ page }) => {
  test.setTimeout(180_000)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto('/assets')

  const name = `默认视图删除检查-${Date.now().toString(36)}`
  await page.getByRole('button', { name: '保存当前筛选' }).click()
  const dialog = page.getByRole('dialog').filter({ hasText: '保存当前筛选' })
  await expect(dialog).toBeVisible()
  await dialog.getByPlaceholder('例如：待确认的公网域名').fill(name)
  await dialog.getByText('设为本页面默认视图').click()
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog).toBeHidden()

  const select = page.locator('.saved-filter-views .el-select')
  await expect(select).toContainText(name)

  let assetListRequests = 0
  page.on('request', request => {
    if (/\/api\/v1\/assets(\?|$)/.test(request.url())) assetListRequests += 1
  })

  await page.getByRole('button', { name: '删除', exact: true }).click()
  const confirm = page.locator('.el-message-box:visible')
  await expect(confirm).toBeVisible()
  await confirm.getByRole('button', { name: '删除', exact: true }).click()

  await expect(page.getByText('保存视图已删除')).toBeVisible()
  await expect(select).not.toContainText(name)
  await expect
    .poll(() => assetListRequests, { message: '删除默认视图后网络资产列表没有重新加载' })
    .toBeGreaterThan(0)
})
