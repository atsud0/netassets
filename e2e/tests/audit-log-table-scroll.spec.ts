import { expect, test, type Page } from '@playwright/test'

const adminUsername = process.env.E2E_USERNAME ?? 'admin'
const adminPassword = process.env.E2E_PASSWORD

async function login(page: Page, username: string, password: string) {
  await page.getByPlaceholder('请输入用户名').fill(username)
  await page.getByPlaceholder('请输入密码').fill(password)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page).not.toHaveURL(/\/login(?:\?|$)/)
}

function auditLog(index: number) {
  return {
    id: index,
    user_id: `user-${index}`,
    username: `auditor-${index}`,
    display_name: `审计用户 ${index}`,
    organization_id: 'organization-1',
    organization_code: 'GROUP',
    organization_name: '集团总部',
    action: 'asset.update',
    action_label: '更新资产',
    resource_type: 'asset',
    resource_type_label: '网络资产',
    resource_id: `asset-${index}`,
    summary: `用于验证长列表内部滚动的审计记录 ${index}`,
    details: {},
    request_ip: '127.0.0.1',
    request_id: `e2e-layout-${index}`,
    result: 'success',
    result_label: '成功',
    created_at: '2026-07-25T08:00:00Z',
  }
}

test('审计日志长列表在表格内滚动，不拉长右侧页面', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')

  await page.route('**/api/v1/audit-logs**', async (route) => {
    const url = new URL(route.request().url())
    if (url.pathname !== '/api/v1/audit-logs') {
      await route.continue()
      return
    }
    await route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ items: Array.from({ length: 100 }, (_, index) => auditLog(index + 1)), total: 100, page: 1, page_size: 20 }),
    })
  })

  await page.goto('/audit-logs')
  await expect(page).toHaveURL(/\/login\?redirect=(?:%2F|\/)audit-logs$/)
  await login(page, adminUsername, adminPassword!)
  await expect(page).toHaveURL(/\/audit-logs$/)
  await expect(page.getByRole('heading', { name: '审计日志', exact: true })).toBeVisible()

  const table = page.locator('.audit-table-panel .el-table')
  await expect(table).toBeVisible()
  await expect(table.locator('.el-table__body-wrapper tbody tr')).toHaveCount(100)

  const scrollState = await table.evaluate((element) => {
    const scrollWrap = element.querySelector<HTMLElement>('.el-table__body-wrapper .el-scrollbar__wrap')
    if (!scrollWrap) throw new Error('审计日志表格内部滚动容器不存在')
    const header = element.querySelector<HTMLElement>('.el-table__header-wrapper')
    const headerTop = header?.getBoundingClientRect().top
    scrollWrap.scrollTop = scrollWrap.scrollHeight
    return {
      clientHeight: scrollWrap.clientHeight,
      scrollHeight: scrollWrap.scrollHeight,
      scrollTop: scrollWrap.scrollTop,
      headerTop,
    }
  })

  expect(scrollState.scrollHeight).toBeGreaterThan(scrollState.clientHeight)
  expect(scrollState.scrollTop).toBeGreaterThan(0)
  const headerTopAfterScroll = await table.locator('.el-table__header-wrapper').evaluate((element) => element.getBoundingClientRect().top)
  expect(headerTopAfterScroll).toBe(scrollState.headerTop)
})
