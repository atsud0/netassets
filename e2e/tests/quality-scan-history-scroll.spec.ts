import { expect, test } from '@playwright/test'

if (process.env.E2E_CHROMIUM_PATH) {
  test.use({ launchOptions: { executablePath: process.env.E2E_CHROMIUM_PATH } })
}

const batches = Array.from({ length: 20 }, (_, index) => ({
  id: `scan-${index + 1}`,
  organization_name: `组织 ${index + 1}`,
  asset_count: 10,
  system_count: 2,
  enabled_rule_count: 5,
  total_issue_count: index,
  error_count: 0,
  warning_count: index,
  auto_closed_count: 0,
  trigger_type: 'manual',
  created_by_name: '管理员',
  created_at: '2026-10-31T08:00:00Z',
}))

test('巡检批次在桌面和窄屏内滚动，表头及分页保持可见', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  let rowCount = 20
  const requestedPages: number[] = []
  await page.route('**/api/v1/**', async route => {
    const url = new URL(route.request().url())
    if (url.pathname === '/api/v1/auth/me') {
      await route.fulfill({ json: { user: { id: 'admin', username: 'admin', display_name: '管理员', must_change_password: false, organizations: [], roles: ['group_admin'], permissions: ['data_quality.manage'] } } })
    } else if (url.pathname === '/api/v1/data-quality-scan-batches') {
      const pageNumber = Number(url.searchParams.get('page'))
      requestedPages.push(pageNumber)
      await route.fulfill({ json: { items: batches.slice(0, rowCount), total: 40, page: pageNumber, page_size: 20 } })
    } else {
      await route.fulfill({ json: { items: [], total: 0 } })
    }
  })
  await page.goto('/data-quality-scan-history')
  await expect(page.getByRole('heading', { name: '巡检历史', exact: true })).toBeVisible()
  await expect(page.locator('.inspection-history-table-scroll .el-table__row')).toHaveCount(20)

  async function inspect() {
    return page.locator('.inspection-history-panel').evaluate(panel => {
      const outer = panel.querySelector<HTMLElement>('.inspection-history-table-scroll')!
      const table = outer.querySelector<HTMLElement>('.el-table')!
      const scroller = table.querySelector<HTMLElement>('.el-scrollbar__wrap')!
      const header = table.querySelector<HTMLElement>('.el-table__header-wrapper')!
      const pagination = panel.querySelector<HTMLElement>('.table-pagination')!
      return {
        tableHeight: table.getBoundingClientRect().height,
        outerWidth: outer.clientWidth,
        outerScrollWidth: outer.scrollWidth,
        scrollHeight: scroller.scrollHeight,
        clientHeight: scroller.clientHeight,
        headerTop: header.getBoundingClientRect().top,
        paginationTop: pagination.getBoundingClientRect().top,
        tableBottom: table.getBoundingClientRect().bottom,
      }
    })
  }

  const desktop = await inspect()
  expect(desktop.tableHeight).toBeLessThanOrEqual(0.58 * 900 + 2)
  expect(desktop.scrollHeight).toBeGreaterThan(desktop.clientHeight)
  expect(desktop.paginationTop).toBeGreaterThan(desktop.tableBottom)
  expect(desktop.outerScrollWidth).toBeGreaterThan(desktop.outerWidth)

  await page.locator('.inspection-history-table-scroll .el-scrollbar__wrap').evaluate(element => { element.scrollTop = 300 })
  expect((await inspect()).headerTop).toBe(desktop.headerTop)
  await page.locator('.inspection-history-table-scroll').evaluate(element => { element.scrollLeft = 200 })
  expect(await page.locator('.inspection-history-table-scroll').evaluate(element => element.scrollLeft)).toBeGreaterThan(0)

  await page.locator('.inspection-history-panel .el-pagination .btn-next').click()
  await expect.poll(() => requestedPages.at(-1)).toBe(2)
  await expect(page.locator('.inspection-history-table-scroll .el-table__row')).toHaveCount(20)

  await page.setViewportSize({ width: 390, height: 760 })
  const mobile = await inspect()
  expect(mobile.tableHeight).toBeLessThanOrEqual(0.46 * 760 + 2)
  expect(mobile.scrollHeight).toBeGreaterThan(mobile.clientHeight)
  expect(mobile.paginationTop).toBeGreaterThan(mobile.tableBottom)

  rowCount = 2
  await page.getByRole('button', { name: '刷新', exact: true }).click()
  await expect(page.locator('.inspection-history-table-scroll .el-table__row')).toHaveCount(2)
  const shortList = await inspect()
  expect(shortList.scrollHeight).toBeLessThanOrEqual(shortList.clientHeight + 1)
  expect(shortList.tableHeight).toBeLessThan(mobile.tableHeight)
})
