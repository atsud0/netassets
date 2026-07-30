import { expect, test, type Page } from '@playwright/test'

const adminUsername = process.env.E2E_USERNAME ?? 'admin'
const adminPassword = process.env.E2E_PASSWORD

async function login(page: Page, username: string, password: string) {
  await page.getByPlaceholder('请输入用户名').fill(username)
  await page.getByPlaceholder('请输入密码').fill(password)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page).not.toHaveURL(/\/login(?:\?|$)/)
}

test('应用壳层固定导航，安全测试页面使用统一台账布局', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')

  await page.goto('/security-test-batches')
  await expect(page).toHaveURL(/\/login\?redirect=(?:%2F|\/)security-test-batches$/)
  await login(page, adminUsername, adminPassword!)
  await expect(page).toHaveURL(/\/security-test-batches$/)
  await expect(page.getByRole('heading', { name: '安全测试', exact: true })).toBeVisible()

  await expect(page.locator('.organization-summary > div')).toHaveCount(4)
  await expect(page.locator('.panel.security-test-filter-panel')).toBeVisible()
  await expect(page.locator('.panel.data-table-panel')).toBeVisible()

  const layout = await page.evaluate(() => {
    const shell = document.querySelector<HTMLElement>('.app-shell')
    const sidebar = document.querySelector<HTMLElement>('.sidebar')
    const content = document.querySelector<HTMLElement>('.page-content')
    if (!shell || !sidebar || !content) throw new Error('应用布局节点不存在')
    return {
      viewportHeight: window.innerHeight,
      shellHeight: shell.getBoundingClientRect().height,
      sidebarHeight: sidebar.getBoundingClientRect().height,
      contentOverflowY: getComputedStyle(content).overflowY,
    }
  })
  expect(layout.shellHeight).toBe(layout.viewportHeight)
  expect(layout.sidebarHeight).toBe(layout.viewportHeight)
  expect(layout.contentOverflowY).toBe('auto')

  const sidebarTopBeforeScroll = await page.locator('.sidebar').evaluate((element) => element.getBoundingClientRect().top)
  await page.evaluate(() => {
    const content = document.querySelector<HTMLElement>('.page-content')
    if (!content) throw new Error('内容区不存在')
    const spacer = document.createElement('div')
    spacer.dataset.testid = 'layout-scroll-spacer'
    spacer.style.height = '120vh'
    spacer.style.pointerEvents = 'none'
    content.append(spacer)
    content.scrollTop = 200
  })
  await expect.poll(() => page.locator('.page-content').evaluate((element) => element.scrollTop)).toBeGreaterThan(0)
  const sidebarTopAfterScroll = await page.locator('.sidebar').evaluate((element) => element.getBoundingClientRect().top)
  expect(sidebarTopAfterScroll).toBe(sidebarTopBeforeScroll)
  await page.locator('[data-testid="layout-scroll-spacer"]').evaluate((element) => element.remove())
})

test('左侧导航按业务分组，消息中心仅保留在顶栏', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')

  await page.goto('/security-test-batches')
  await login(page, adminUsername, adminPassword!)

  const navigation = page.getByRole('navigation', { name: '主导航' })
  await expect(navigation.getByRole('button', { name: '集团工作台', exact: true })).toBeVisible()
  await expect(navigation.getByRole('button', { name: '资产治理', exact: true })).toBeVisible()
  await expect(navigation.getByRole('button', { name: '安全运营', exact: true })).toBeVisible()
  await expect(navigation.getByRole('button', { name: '系统设置', exact: true })).toBeVisible()

  await expect(navigation.getByRole('button', { name: '安全测试', exact: true })).toBeVisible()
  await expect(navigation.getByRole('button', { name: '安全工单', exact: true })).toBeVisible()
  await expect(navigation.getByRole('button', { name: '组织架构', exact: true })).toBeVisible()
  await expect(navigation.getByRole('button', { name: '用户和权限', exact: true })).toBeVisible()
  await expect(navigation.getByRole('button', { name: '回收站', exact: true })).toBeVisible()
  await expect(navigation.getByRole('button', { name: '审计日志', exact: true })).toBeVisible()
  await expect(navigation.getByRole('button', { name: '备份管理', exact: true })).toBeVisible()

  await expect(navigation.getByText('消息中心', { exact: true })).toHaveCount(0)
  await expect(page.locator('.topbar').getByText('消息中心', { exact: true })).toBeVisible()

  const topbarActions = page.locator('.topbar-actions')
  const topbarActionButtons = topbarActions.locator(':scope > .el-button')
  const notificationButton = page.getByRole('button', { name: '消息中心', exact: true })
  const userNameButton = topbarActions.locator('.user-name-topbar-button')
  await expect(topbarActionButtons).toHaveCount(3)
  await expect(notificationButton).toBeVisible()
  await expect(userNameButton).toHaveText(/\S+/)
  await expect(topbarActionButtons.nth(2)).toHaveText('退出')
  await expect(topbarActions.getByRole('button', { name: '账户设置', exact: true })).toHaveCount(0)

  const measureTopbarActionGaps = () => topbarActions.evaluate((element) => {
    const buttons = Array.from(element.children) as HTMLElement[]
    return buttons.slice(1).map((button, index) => button.getBoundingClientRect().left - buttons[index].getBoundingClientRect().right)
  })
  expect(await measureTopbarActionGaps()).toEqual([10, 10])

  await page.setViewportSize({ width: 700, height: 900 })
  await expect(notificationButton).toBeVisible()
  expect(await measureTopbarActionGaps()).toEqual([10, 10])
  const notificationButtonBox = await notificationButton.boundingBox()
  expect(notificationButtonBox).not.toBeNull()
  expect(notificationButtonBox!.x).toBeGreaterThanOrEqual(0)
  expect(notificationButtonBox!.x + notificationButtonBox!.width).toBeLessThanOrEqual(700)
  await notificationButton.click()
  await expect(page.getByRole('heading', { name: '消息中心', exact: true })).toBeVisible()
  await userNameButton.click()
  await expect(page.getByRole('heading', { name: '账户设置', exact: true })).toBeVisible()

  const systemSettings = navigation.getByRole('button', { name: '系统设置', exact: true })
  await systemSettings.click()
  await expect(navigation.getByRole('button', { name: '组织架构', exact: true })).toBeHidden()
  await systemSettings.click()
  await expect(navigation.getByRole('button', { name: '组织架构', exact: true })).toBeVisible()
})

test('治理页面内容卡片与台账页面保持相同圆角和间距', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')

  await page.goto('/assets')
  await login(page, adminUsername, adminPassword!)
  await expect(page.getByRole('heading', { name: '网络资产', exact: true })).toBeVisible()

  const measureActionGaps = async (selector: string) => page.locator(selector).evaluate((element) => {
    const children = Array.from(element.children) as HTMLElement[]
    return children.slice(1).map((child, index) => {
      const previous = children[index].getBoundingClientRect()
      return child.getBoundingClientRect().left - previous.right
    })
  })

  expect(await measureActionGaps('.asset-page-actions')).toEqual([10, 10, 10, 10])
  await expect(page.locator('.asset-page-actions .el-dropdown')).toHaveCount(1)
  await page.getByRole('button', { name: '批量导入', exact: true }).click()
  const assetImportDialog = page.getByRole('dialog', { name: '批量导入网络资产' })
  await expect(assetImportDialog).toBeVisible()
  await expect(assetImportDialog.getByRole('button', { name: '下载 XLSX 模板', exact: true })).toBeVisible()
  await assetImportDialog.getByRole('button', { name: '关闭', exact: true }).click()

  await page.goto('/systems')
  await expect(page.getByRole('heading', { name: '业务系统', exact: true })).toBeVisible()
  expect(await measureActionGaps('.business-system-page-actions')).toEqual([10, 10, 10])
  await expect(page.locator('.business-system-page-actions .el-dropdown')).toHaveCount(1)

  await page.goto('/assets')
  await expect(page.getByRole('heading', { name: '网络资产', exact: true })).toBeVisible()

  const measureCards = async (upperSelector: string, lowerSelector: string) => page.evaluate(
    ({ upperSelector, lowerSelector }) => {
      const upper = document.querySelector<HTMLElement>(upperSelector)
      const lower = document.querySelector<HTMLElement>(lowerSelector)
      if (!upper || !lower) throw new Error(`卡片节点不存在：${upperSelector} / ${lowerSelector}`)
      const upperRect = upper.getBoundingClientRect()
      const lowerRect = lower.getBoundingClientRect()
      const lowerStyle = getComputedStyle(lower)
      return {
        gap: lowerRect.top - upperRect.bottom,
        radius: lowerStyle.borderTopLeftRadius,
        borderStyle: lowerStyle.borderTopStyle,
      }
    },
    { upperSelector, lowerSelector },
  )

  const ledgerBaseline = await measureCards('.asset-filter-panel', '.data-table-panel')
  expect(ledgerBaseline.gap).toBe(16)
  expect(ledgerBaseline.radius).toBe('14px')
  expect(ledgerBaseline.borderStyle).toBe('solid')

  await page.goto('/asset-candidates')
  await expect(page.getByRole('heading', { name: '待确认资产', exact: true })).toBeVisible()
  const candidateCards = await measureCards('.candidate-filter-panel', '.data-table-panel')
  expect(candidateCards).toEqual(ledgerBaseline)

  await page.goto('/data-governance')
  await expect(page.getByRole('heading', { name: '数据治理', exact: true })).toBeVisible()
  const governanceCards = await measureCards('.governance-filter-panel', '.data-table-panel')
  expect(governanceCards).toEqual(ledgerBaseline)

  await page.goto('/recycle-bin')
  await expect(page.getByRole('heading', { name: '回收站', exact: true })).toBeVisible()
  const recycleCards = await measureCards('.recycle-bin-notice', '.data-table-panel')
  expect(recycleCards).toEqual(ledgerBaseline)

  await page.goto('/data-quality-issues')
  await expect(page.getByRole('heading', { name: '质量整改', exact: true })).toBeVisible()
  const qualitySummaryAndFilters = await measureCards('.quality-workflow-summary', '.quality-workflow-filters')
  expect(qualitySummaryAndFilters).toEqual(ledgerBaseline)
  const qualityFiltersAndIssues = await measureCards('.quality-workflow-filters', '.quality-issue-panel')
  expect(qualityFiltersAndIssues).toEqual(ledgerBaseline)
  const qualityIssuesAndBatches = await measureCards('.quality-issue-panel', '.quality-scan-batch-panel')
  expect(qualityIssuesAndBatches).toEqual(ledgerBaseline)
})

test('质量整改在窄内容区保持自适应，宽表格仅在卡片内部滚动', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')

  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto('/data-quality-issues')
  await login(page, adminUsername, adminPassword!)
  await expect(page.getByRole('heading', { name: '质量整改', exact: true })).toBeVisible()

  const layout = await page.evaluate(() => {
    const content = document.querySelector<HTMLElement>('.page-content')
    const stack = document.querySelector<HTMLElement>('.page-stack')
    const filters = document.querySelector<HTMLElement>('.quality-workflow-filters')
    const panel = document.querySelector<HTMLElement>('.quality-issue-panel')
    const tableScroll = document.querySelector<HTMLElement>('.quality-issue-table-scroll')
    if (!content || !stack || !filters || !panel || !tableScroll) throw new Error('质量整改自适应布局节点不存在')
    const rect = (element: HTMLElement) => element.getBoundingClientRect()
    const panelRect = rect(panel)
    const tableRect = rect(tableScroll)
    return {
      contentScrollOverflow: content.scrollWidth - content.clientWidth,
      stackScrollOverflow: stack.scrollWidth - stack.clientWidth,
      filtersOverflow: filters.scrollWidth - filters.clientWidth,
      panelOverflow: panel.scrollWidth - panel.clientWidth,
      tableWithinPanel: tableRect.right <= panelRect.right + 1,
    }
  })

  expect(layout.contentScrollOverflow).toBeLessThanOrEqual(1)
  expect(layout.stackScrollOverflow).toBeLessThanOrEqual(1)
  expect(layout.filtersOverflow).toBeLessThanOrEqual(1)
  expect(layout.panelOverflow).toBeLessThanOrEqual(1)
  expect(layout.tableWithinPanel).toBe(true)
})


test('网络资产和业务系统的分页区使用统一表格间距', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')

  await page.goto('/assets')
  await login(page, adminUsername, adminPassword!)
  await expect(page.getByRole('heading', { name: '网络资产', exact: true })).toBeVisible()

  const readPaginationStyle = () => page.locator('.data-table-panel > .table-pagination').evaluate((element) => {
    const style = getComputedStyle(element)
    return { marginTop: style.marginTop, justifyContent: style.justifyContent }
  })
  expect(await readPaginationStyle()).toEqual({ marginTop: '18px', justifyContent: 'flex-end' })

  await page.goto('/systems')
  await expect(page.getByRole('heading', { name: '业务系统', exact: true })).toBeVisible()
  expect(await readPaginationStyle()).toEqual({ marginTop: '18px', justifyContent: 'flex-end' })
})

test('管理页面在常用窄内容宽度下不出现整体横向溢出，安全工单换行操作保持右对齐', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')

  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto('/dashboard')
  await login(page, adminUsername, adminPassword!)

  const routes = [
    '/dashboard', '/assets', '/systems', '/asset-candidates', '/data-governance', '/data-quality-issues',
    '/inventory-tasks', '/security-test-batches', '/security-work-orders', '/notifications', '/organizations',
    '/users', '/recycle-bin', '/audit-logs', '/backups', '/export-jobs', '/profile',
  ]

  for (const route of routes) {
    await page.goto(route)
    await expect(page.locator('h1')).toBeVisible()
    const overflow = await page.evaluate(() => {
      const content = document.querySelector<HTMLElement>('.page-content')
      return {
        document: document.documentElement.scrollWidth - document.documentElement.clientWidth,
        content: content ? content.scrollWidth - content.clientWidth : 0,
      }
    })
    expect(overflow.document, `${route} 页面出现整体横向溢出`).toBeLessThanOrEqual(1)
    expect(overflow.content, `${route} 内容区出现横向溢出`).toBeLessThanOrEqual(1)
  }

  await page.goto('/security-work-orders')
  await expect(page.getByRole('heading', { name: '安全工单', exact: true })).toBeVisible()
  const actionLayout = await page.locator('.heading-actions').evaluate((element) => {
    const group = element.getBoundingClientRect()
    const buttons = Array.from(element.querySelectorAll<HTMLElement>('.el-button')).map((button) => button.getBoundingClientRect())
    const wrappedButtons = buttons.filter((button) => button.top > buttons[0].top + 1)
    return {
      groupRight: group.right,
      hasWrappedButton: wrappedButtons.length > 0,
      wrappedRight: wrappedButtons[0]?.right ?? group.right,
    }
  })
  expect(actionLayout.hasWrappedButton).toBe(true)
  expect(actionLayout.wrappedRight).toBeCloseTo(actionLayout.groupRight, 0)
})
