import { expect, test, type Locator, type Page } from '@playwright/test'
import { clickRowAction } from '../helpers/row-actions'

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

  await expect(page.locator('.organization-summary')).toHaveCount(0)
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
  await expect(navigation.getByRole('button', { name: '工作台', exact: true })).toBeVisible()
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

  expect(await measureActionGaps('.asset-page-actions')).toEqual([10, 10])
  await expect(page.locator('.asset-page-actions .el-dropdown')).toHaveCount(2)
  await page.getByRole('button', { name: '更多操作', exact: true }).click()
  await page.getByRole('menuitem', { name: '批量导入', exact: true }).click()
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
  expect(ledgerBaseline.radius).toBe('10px')
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

test('主要列表页操作列右对齐且不再按最大场景过度留白', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')

  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/systems')
  await login(page, adminUsername, adminPassword!)

  const cases = [
    { route: '/systems', label: '操作', maxWidth: 212 },
    { route: '/assets', label: '操作', maxWidth: 212 },
    { route: '/security-test-batches', label: '操作', maxWidth: 192 },
    { route: '/security-work-orders', label: '操作', maxWidth: 222 },
    { route: '/inventory-tasks', label: '任务操作', maxWidth: 132 },
    { route: '/asset-candidates', label: '操作', maxWidth: 242 },
    { route: '/data-governance', label: '操作', maxWidth: 122 },
    { route: '/data-quality-issues', label: '操作', maxWidth: 212 },
    { route: '/organizations', label: '操作', maxWidth: 212 },
    { route: '/users', label: '操作', maxWidth: 222 },
    { route: '/recycle-bin', label: '操作', maxWidth: 86 },
    { route: '/audit-logs', label: '详情', maxWidth: 84 },
    { route: '/backups', label: '操作', maxWidth: 192 },
    { route: '/export-jobs', label: '操作', maxWidth: 122 },
  ]

  for (const item of cases) {
    await page.goto(item.route)
    await expect(page.locator('h1')).toBeVisible()
    let header = page.locator('.el-table__header-wrapper th').filter({ hasText: new RegExp(String.raw`^\s*${item.label}\s*$`) }).first()
    if (item.route === '/backups' && await header.count() === 0) {
      await page.getByRole('button', { name: '创建首个备份', exact: true }).click()
      await page.getByRole('dialog', { name: '创建系统备份' }).getByRole('button', { name: '开始备份', exact: true }).click()
      await expect(page.getByText('系统备份已创建并完成校验')).toBeVisible({ timeout: 30_000 })
      header = page.locator('.el-table__header-wrapper th').filter({ hasText: new RegExp(String.raw`^\s*${item.label}\s*$`) }).first()
    }
    await expect(header, `${item.route} 缺少${item.label}列`).toBeVisible()
    const metrics = await header.evaluate((element) => {
      const cell = element.querySelector<HTMLElement>('.cell') ?? element as HTMLElement
      return {
        width: element.getBoundingClientRect().width,
        textAlign: getComputedStyle(cell).textAlign,
      }
    })
    expect(metrics.textAlign, `${item.route} ${item.label}列未右对齐`).toBe('right')
    expect(metrics.width, `${item.route} ${item.label}列仍然过宽`).toBeLessThanOrEqual(item.maxWidth)
  }
})

test('业务系统和安全测试的数据行操作紧贴列右侧', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')

  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/dashboard')
  await login(page, adminUsername, adminPassword!)

  await page.route('**/api/v1/business-systems**', async (route) => {
    const url = new URL(route.request().url())
    if (!url.pathname.endsWith('/business-systems')) return route.continue()
    await route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        items: [{
          id: 'layout-system', code: 'SYS-LAYOUT', name: '操作列布局验证系统', organization_id: 'layout-org', organization_name: '端到端测试集团',
          owner_name: '测试负责人', status: 'active', level: 'important', internet_exposure: 'yes', contains_personal_information: 'no',
          contains_sensitive_personal_information: 'no', data_volume_over_100k: 'no', contains_important_data: 'no', contains_core_data: 'no',
          asset_count: 1, reporting_status: 'reported', is_working_copy: false, created_at: '2026-08-30T00:00:00Z', updated_at: '2026-08-30T00:00:00Z',
        }],
        total: 1, page: 1, page_size: 20,
      }),
    })
  })
  await page.goto('/systems')
  await expect(page.getByText('操作列布局验证系统', { exact: true })).toBeVisible()

  const assertRightGap = async (route: string) => {
    const cell = page.locator('td.table-action-column:visible').first()
    await expect(cell, `${route} 缺少操作单元格`).toBeVisible()
    const gap = await cell.evaluate((element) => {
      const actions = Array.from(element.querySelectorAll<HTMLElement>('button, .el-dropdown, a, .el-tag'))
        .filter((item) => {
          const rect = item.getBoundingClientRect()
          const style = getComputedStyle(item)
          return rect.width > 0 && rect.height > 0 && style.display !== 'none' && style.visibility !== 'hidden'
        })
      if (actions.length === 0) throw new Error('操作单元格中没有可见操作')
      const cellRight = element.getBoundingClientRect().right
      const actionRight = Math.max(...actions.map(item => item.getBoundingClientRect().right))
      return cellRight - actionRight
    })
    expect(gap, `${route} 操作按钮右侧仍有明显空白`).toBeLessThanOrEqual(16)
  }
  await assertRightGap('/systems')

  await page.route('**/api/v1/security-test-batches**', async (route) => {
    const url = new URL(route.request().url())
    if (!url.pathname.endsWith('/security-test-batches')) return route.continue()
    await route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        items: [{
          id: 'layout-batch', code: 'ST-LAYOUT', name: '操作列布局验证批次', test_type: 'penetration', status: 'draft', provider: '集团安全团队',
          authorization_note: '仅用于布局回归', start_at: '2026-09-01T01:00:00Z', end_at: '2026-09-02T01:00:00Z', asset_count: 1,
          organization_count: 1, created_at: '2026-08-30T00:00:00Z', updated_at: '2026-08-30T00:00:00Z',
        }],
        total: 1, page: 1, page_size: 20,
      }),
    })
  })
  await page.goto('/security-test-batches')
  await expect(page.getByText('操作列布局验证批次', { exact: true })).toBeVisible()
  await assertRightGap('/security-test-batches')
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

test('网络资产筛选结果与查询控件同排，保存视图操作使用统一间距', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')

  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto('/assets')
  await login(page, adminUsername, adminPassword!)
  await expect(page.getByRole('heading', { name: '网络资产', exact: true })).toBeVisible()

  const layout = await page.evaluate(() => {
    const controls = document.querySelector<HTMLElement>('.network-asset-filter-controls')
    const count = controls?.querySelector<HTMLElement>('.asset-filter-count')
    const queryButton = Array.from(controls?.querySelectorAll<HTMLElement>('.el-button') ?? []).find(button => button.textContent?.trim() === '查询')
    const savedViews = document.querySelector<HTMLElement>('.network-asset-filter-panel .saved-filter-views')
    const savedItems = savedViews
      ? Array.from(savedViews.children).filter((item): item is HTMLElement => item instanceof HTMLElement && (item.classList.contains('el-select') || item.classList.contains('el-button')))
      : []
    if (!controls || !count || !queryButton || !savedViews || savedItems.length < 4) throw new Error('网络资产筛选布局节点不存在')
    const controlsRect = controls.getBoundingClientRect()
    const countRect = count.getBoundingClientRect()
    const queryRect = queryButton.getBoundingClientRect()
    const savedRect = savedViews.getBoundingClientRect()
    const itemRects = savedItems.map(item => item.getBoundingClientRect())
    return {
      countCenterOffset: Math.abs((countRect.top + countRect.bottom) / 2 - (queryRect.top + queryRect.bottom) / 2),
      savedRowGap: savedRect.top - controlsRect.bottom,
      savedItemGaps: itemRects.slice(1).map((rect, index) => rect.left - itemRects[index].right),
    }
  })

  expect(layout.countCenterOffset).toBeLessThanOrEqual(1)
  expect(layout.savedRowGap).toBe(12)
  expect(layout.savedItemGaps.every(gap => Math.abs(gap - 8) <= 1)).toBe(true)
})

test('列表操作按钮保持统一高度、基线和间距', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')

  await page.setViewportSize({ width: 1440, height: 950 })
  await page.goto('/assets')
  await login(page, adminUsername, adminPassword!)

  // 新数据库可能只有集团根节点，先补一个空子组织，确保组织操作列存在多个按钮可供几何校验。
  await page.evaluate(async (suffix) => {
    const treeResponse = await fetch('/api/v1/organizations/tree')
    if (!treeResponse.ok) throw new Error(`读取组织树失败：${treeResponse.status}`)
    const tree = await treeResponse.json() as { items: Array<{ id: string; type: string; children?: unknown[] }> }
    const root = tree.items.find(item => item.type === 'group')
    if (!root) throw new Error('未找到集团根组织')
    const response = await fetch('/api/v1/organizations', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        parent_id: root.id,
        code: `LAYOUT_${suffix}`,
        name: `布局检查子公司_${suffix}`,
        type: 'subsidiary',
      }),
    })
    if (!response.ok && response.status !== 409) throw new Error(`创建布局检查组织失败：${response.status}`)
  }, Date.now())

  const routes = [
    '/assets', '/systems', '/asset-candidates', '/data-governance', '/data-quality-issues',
    '/inventory-tasks', '/security-test-batches', '/security-work-orders', '/organizations',
    '/users', '/recycle-bin', '/audit-logs', '/backups', '/export-jobs',
  ]
  let inspectedGroups = 0

  for (const route of routes) {
    await page.goto(route)
    await expect(page.locator('h1')).toBeVisible()

    const groups = await page.evaluate(() => {
      const isVisible = (element: HTMLElement) => {
        const rect = element.getBoundingClientRect()
        const style = getComputedStyle(element)
        return rect.width > 0 && rect.height > 0 && style.display !== 'none' && style.visibility !== 'hidden'
      }

      return Array.from(document.querySelectorAll<HTMLElement>('td.el-table__cell'))
        .filter(isVisible)
        .map((cell) => Array.from(cell.querySelectorAll<HTMLElement>('.el-button')).filter(isVisible))
        .filter((buttons) => buttons.length > 1)
        .map((buttons) => buttons.map((button) => {
          const rect = button.getBoundingClientRect()
          return {
            text: button.textContent?.trim() || '',
            left: rect.left,
            right: rect.right,
            top: rect.top,
            height: rect.height,
            centerY: (rect.top + rect.bottom) / 2,
          }
        }))
    })

    inspectedGroups += groups.length
    for (const buttons of groups) {
      const lines: Array<typeof buttons> = []
      for (const button of buttons) {
        const matchingLine = lines.find(line => Math.abs(line[0].centerY - button.centerY) <= 8)
        if (matchingLine) matchingLine.push(button)
        else lines.push([button])
      }

      for (const lineButtons of lines) {
        const sorted = [...lineButtons].sort((a, b) => a.left - b.left)
        const heights = sorted.map(button => button.height)
        const centers = sorted.map(button => button.centerY)
        expect(Math.max(...heights) - Math.min(...heights), `${route} 操作按钮高度不一致`).toBeLessThanOrEqual(0.5)
        expect(Math.max(...centers) - Math.min(...centers), `${route} 操作按钮基线不一致`).toBeLessThanOrEqual(1)
        for (let index = 1; index < sorted.length; index += 1) {
          expect(sorted[index].left - sorted[index - 1].right, `${route} 操作按钮间距过小`).toBeGreaterThanOrEqual(8)
        }
      }
    }
  }

  expect(inspectedGroups).toBeGreaterThan(0)
})

test('填报任务列表以已提交和待提交表达提交进度', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')

  await page.setViewportSize({ width: 1440, height: 950 })
  await page.goto('/inventory-tasks')
  await login(page, adminUsername, adminPassword!)
  await expect(page.getByRole('heading', { name: '填报任务', exact: true })).toBeVisible()

  const suffix = Date.now().toString()
  const taskName = `提交进度展示验证_${suffix}`
  const organizationName = `进度展示子公司_${suffix}`
  await page.evaluate(async ({ suffix, taskName, organizationName }) => {
    const treeResponse = await fetch('/api/v1/organizations/tree')
    if (!treeResponse.ok) throw new Error(`读取组织树失败：${treeResponse.status}`)
    const tree = await treeResponse.json() as { items: Array<{ id: string; type: string }> }
    const root = tree.items.find(item => item.type === 'group')
    if (!root) throw new Error('未找到集团根组织')

    const organizationResponse = await fetch('/api/v1/organizations', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        parent_id: root.id,
        code: `TASK_PROGRESS_${suffix}`,
        name: organizationName,
        type: 'subsidiary',
      }),
    })
    if (!organizationResponse.ok) throw new Error(`创建进度展示组织失败：${organizationResponse.status}`)
    const organization = await organizationResponse.json() as { item: { id: string } }

    const startAt = new Date()
    const deadlineAt = new Date(startAt.getTime() + 24 * 60 * 60 * 1000)
    const taskResponse = await fetch('/api/v1/inventory-tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: taskName,
        description: '验证提交进度只展示已提交与待提交数量',
        start_at: startAt.toISOString(),
        deadline_at: deadlineAt.toISOString(),
        status: 'active',
        asset_network_scope: 'all',
        required_fields: ['business_systems', 'network_assets'],
        quality_rules: [],
        require_review: true,
        allow_late_submission: false,
        organization_ids: [organization.item.id],
      }),
    })
    if (!taskResponse.ok) throw new Error(`创建填报任务失败：${taskResponse.status}`)
  }, { suffix, taskName, organizationName })

  await page.reload()
  const table = page.locator('.data-table-panel > .el-table')
  await expect(table.locator('.el-table__header-wrapper').first()).toContainText('提交进度')
  await expect(table.locator('.el-table__header-wrapper').first()).not.toContainText('填报进度')
  await expect(table.locator('.el-table__header-wrapper').first()).not.toContainText('创建人')
  await expect(table.locator('.el-table__expand-icon')).toHaveCount(0)

  const taskRow = table.locator('.el-table__body-wrapper tbody tr').filter({ hasText: taskName }).first()
  await expect(taskRow).toBeVisible()
  const taskNameLink = taskRow.getByRole('button', { name: taskName, exact: true })
  await expect(taskNameLink).toBeVisible()
  await expect(taskNameLink).toHaveClass(/table-link/)
  const progress = taskRow.locator('.submission-progress')
  await expect(progress).toContainText('已提交 0')
  await expect(progress).toContainText('待提交 1')
  await expect(progress).not.toContainText('已通过')
  expect(await progress.locator('strong').allTextContents()).toEqual(['0', '1'])

  await taskNameLink.click()
  const detailDrawer = page.locator('.el-drawer:visible').filter({ hasText: '填报任务详情' }).last()
  await expect(detailDrawer).toBeVisible()
  await expect(detailDrawer.getByRole('heading', { name: taskName, exact: true })).toBeVisible()
  await expect(detailDrawer).toContainText('组织填报进度')
  await expect(detailDrawer.locator('.task-organization-table')).toBeVisible()
  await expect(detailDrawer.locator('.task-organization-table .el-table__body-wrapper tbody tr').filter({ hasText: organizationName }).first()).toBeVisible()
})

test('业务系统列表突出名称，详情保留系统编码', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')

  await page.setViewportSize({ width: 1440, height: 950 })
  await page.goto('/systems')
  await login(page, adminUsername, adminPassword!)
  await expect(page.getByRole('heading', { name: '业务系统', exact: true })).toBeVisible()

  const table = page.locator('.data-table-panel > .el-table')
  const headerLabels = await table.locator('.el-table__header-wrapper').first().locator('th .cell').allTextContents()
  expect(headerLabels.map(label => label.trim())).toEqual(expect.arrayContaining(['业务系统', '所属组织', '数据安全']))
  expect(headerLabels.map(label => label.trim())).not.toContain('系统 / 编码')

  const firstRow = table.locator('.el-table__body-wrapper tbody tr').first()
  if (await firstRow.count()) {
    const cells = firstRow.locator('td')
    await expect(cells.nth(1).locator('.table-link')).toBeVisible()
    await expect(cells.nth(1).locator('.mono-text')).toHaveCount(0)
    await firstRow.locator('.table-link').click()
    const detailDrawer = page.locator('.el-drawer:visible').filter({ hasText: '业务系统详情' }).last()
    await expect(detailDrawer).toBeVisible()
    await expect(detailDrawer).toContainText('系统编码（系统字段）')
    await expect(detailDrawer.locator('.detail-system-code code')).not.toHaveText('')
  }
})

test('网络资产名称与访问标识聚合展示且名称打开详情', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')

  await page.setViewportSize({ width: 1440, height: 950 })
  await page.goto('/assets')
  await login(page, adminUsername, adminPassword!)
  await expect(page.getByRole('heading', { name: '网络资产', exact: true })).toBeVisible()

  const suffix = Date.now().toString()
  const systemCode = `ASSET_UI_${suffix}`
  const systemName = `资产交互测试系统_${suffix}`
  const assetName = `资产名称点击测试_${suffix}`
  const assetValue = `asset-ui-${suffix}.example.com`
  await page.evaluate(async ({ systemCode, systemName, assetName, assetValue }) => {
    const organizationsResponse = await fetch('/api/v1/organizations?page=1&page_size=100')
    if (!organizationsResponse.ok) throw new Error(`organizations request failed: ${organizationsResponse.status}`)
    const organizations = await organizationsResponse.json()
    const group = organizations.items.find((item: { type: string }) => item.type === 'group')
    if (!group) throw new Error('group organization not found')

    const systemResponse = await fetch('/api/v1/business-systems', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        code: systemCode, name: systemName, organization_id: group.id, status: 'running', level: 'normal',
        internet_exposure: 'unknown', contains_personal_information: 'unknown',
        contains_sensitive_personal_information: 'unknown', data_volume_over_100k: 'unknown',
        contains_important_data: 'unknown', contains_core_data: 'unknown',
      }),
    })
    if (!systemResponse.ok) throw new Error(`create system failed: ${systemResponse.status}`)
    const system = await systemResponse.json()

    const assetResponse = await fetch('/api/v1/assets', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        type: 'domain', value: assetValue, name: assetName, organization_id: group.id,
        business_system_id: system.item.id, environment: 'production', network_scope: 'public',
        status: 'active', importance: 'normal',
      }),
    })
    if (!assetResponse.ok) throw new Error(`create asset failed: ${assetResponse.status}`)
  }, { systemCode, systemName, assetName, assetValue })

  await page.getByPlaceholder('搜索资产值、名称、系统或负责人').fill(assetValue)
  await page.getByPlaceholder('搜索资产值、名称、系统或负责人').press('Enter')
  const tablePanel = page.locator('.data-table-panel')
  const headerLabels = await tablePanel.locator('.el-table__header-wrapper').first().locator('th .cell').allTextContents()
  expect(headerLabels.map(label => label.trim())).toEqual(expect.arrayContaining(['资产 / 访问标识', '业务归属', '责任人']))
  expect(headerLabels.map(label => label.trim())).not.toContain('资产')
  const row = tablePanel.locator('.el-table__body-wrapper tbody tr').filter({ hasText: assetValue }).first()
  await expect(row).toBeVisible()
  const cells = row.locator('td')
  await expect(cells.nth(1).getByRole('button', { name: assetName, exact: true })).toBeVisible()
  await expect(cells.nth(1).locator('.mono-text')).toHaveText(assetValue)
  await expect(cells.nth(3)).toContainText(systemName)
  await expect(row.getByText(systemCode, { exact: true })).toHaveCount(0)
  await expect(tablePanel.getByRole('button', { name: '详情', exact: true })).toHaveCount(0)

  const assetNameLink = row.getByRole('button', { name: assetName, exact: true })
  const assetNameStyle = await assetNameLink.locator('span').evaluate(element => {
    const style = getComputedStyle(element)
    return { color: style.color, fontSize: style.fontSize, fontWeight: style.fontWeight, lineHeight: style.lineHeight }
  })
  await assetNameLink.click()
  const drawer = page.getByRole('dialog', { name: '网络资产详情' })
  await expect(drawer).toBeVisible()
  await expect(drawer.getByText(assetName, { exact: true })).toBeVisible()
  await expect(drawer).toContainText(assetValue)
  await page.keyboard.press('Escape')
  await expect(drawer).toBeHidden()

  await page.goto('/systems')
  await page.getByPlaceholder('搜索系统名称、组织或负责人，也可输入编码').fill(systemCode)
  await page.getByPlaceholder('搜索系统名称、组织或负责人，也可输入编码').press('Enter')
  const systemRow = page.locator('.data-table-panel .el-table__body-wrapper tbody tr').filter({ hasText: systemName }).first()
  const systemNameLink = systemRow.getByRole('button', { name: systemName, exact: true })
  await expect(systemNameLink).toBeVisible()
  const systemNameStyle = await systemNameLink.locator('span').evaluate(element => {
    const style = getComputedStyle(element)
    return { color: style.color, fontSize: style.fontSize, fontWeight: style.fontWeight, lineHeight: style.lineHeight }
  })
  expect(assetNameStyle).toEqual(systemNameStyle)
  expect(assetNameStyle.color).not.toBe('rgb(107, 119, 133)')
})

test('组织列表突出名称且集团管理员可编辑已有组织', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')

  await page.setViewportSize({ width: 1440, height: 950 })
  await page.goto('/organizations')
  await login(page, adminUsername, adminPassword!)
  await expect(page.getByRole('heading', { name: '组织架构', exact: true })).toBeVisible()

  const table = page.locator('.organization-tree-panel .el-table')
  const headerLabels = (await table.locator('.el-table__header-wrapper').first().locator('th .cell').allTextContents()).map(label => label.trim())
  expect(headerLabels).toEqual(expect.arrayContaining(['组织', '类型', '状态', '操作']))
  expect(headerLabels).not.toContain('上级组织')

  const rows = table.locator('.el-table__body-wrapper tbody tr')
  let editableRow: Locator | undefined
  for (let index = 0; index < await rows.count(); index += 1) {
    const row = rows.nth(index)
    const type = (await row.locator('.el-tag').first().textContent())?.trim()
    if (type === '子公司') {
      editableRow = row
      break
    }
  }

  if (!editableRow) {
    const subsidiaryName = `E2E 子公司 ${Date.now()}`
    await page.getByRole('button', { name: '新增组织', exact: true }).click()
    const createDialog = page.getByRole('dialog', { name: '新增组织机构' })
    await expect(createDialog).toBeVisible()
    await createDialog.getByLabel('组织编码').fill(`E2E_SUB_${Date.now()}`)
    await createDialog.getByLabel('组织名称').fill(subsidiaryName)
    await createDialog.getByRole('button', { name: '确认新增', exact: true }).click()
    await expect(createDialog).toBeHidden()
    editableRow = rows.filter({ hasText: subsidiaryName }).first()
    await expect(editableRow).toBeVisible()
  }

  await clickRowAction(page, editableRow, '编辑信息')
  const dialog = page.getByRole('dialog', { name: '编辑子公司基本信息' })
  await expect(dialog).toBeVisible()
  await expect(dialog.getByLabel('组织名称')).not.toHaveValue('')
  const updatedContact = `e2e-${Date.now()}@example.test`
  await dialog.getByLabel('联系方式').fill(updatedContact)
  await dialog.getByRole('button', { name: '保存基本信息', exact: true }).click()
  await expect(dialog).toBeHidden()
  await expect(editableRow).toContainText(updatedContact)
})

test('消息中心使用统一列表卡片、内部滚动和卡片内分页', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')

  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/notifications')
  await login(page, adminUsername, adminPassword!)
  await expect(page.getByRole('heading', { name: '消息中心', exact: true })).toBeVisible()

  const layout = await page.evaluate(() => {
    const filters = document.querySelector<HTMLElement>('.notification-summary-panel')
    const panel = document.querySelector<HTMLElement>('.notification-list-panel')
    const scroll = document.querySelector<HTMLElement>('.notification-list-scroll')
    const pagination = document.querySelector<HTMLElement>('.notification-list-panel > .table-pagination')
    if (!filters || !panel || !scroll) throw new Error('消息中心统一列表布局节点不存在')
    const filterRect = filters.getBoundingClientRect()
    const panelRect = panel.getBoundingClientRect()
    const panelStyle = getComputedStyle(panel)
    const scrollStyle = getComputedStyle(scroll)
    const paginationStyle = pagination ? getComputedStyle(pagination) : null
    return {
      cardGap: panelRect.top - filterRect.bottom,
      panelRadius: panelStyle.borderTopLeftRadius,
      panelBorderStyle: panelStyle.borderTopStyle,
      scrollOverflowY: scrollStyle.overflowY,
      scrollMaxHeight: scrollStyle.maxHeight,
      scrollWithinPanel: scroll.getBoundingClientRect().right <= panelRect.right + 1,
      paginationMarginTop: paginationStyle?.marginTop ?? null,
      paginationWithinPanel: pagination ? panel.contains(pagination) : true,
    }
  })

  expect(layout.cardGap).toBe(16)
  expect(layout.panelRadius).toBe('14px')
  expect(layout.panelBorderStyle).toBe('solid')
  expect(layout.scrollOverflowY).toBe('auto')
  expect(layout.scrollMaxHeight).toBe('520px')
  expect(layout.scrollWithinPanel).toBe(true)
  expect(layout.paginationWithinPanel).toBe(true)
  if (layout.paginationMarginTop !== null) expect(layout.paginationMarginTop).toBe('18px')
})

test('集团工作台卡片操作对齐、零值图表语义正确并适配常用窄屏', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')

  await page.setViewportSize({ width: 1024, height: 768 })
  await page.goto('/dashboard')
  await login(page, adminUsername, adminPassword!)
  await expect(page.getByRole('heading', { name: '集团安全治理工作台', exact: true })).toBeVisible()

  const panelHeaders = page.locator('.panel-header').filter({ has: page.locator('.el-button') })
  await expect(panelHeaders).toHaveCount(5)
  for (let index = 0; index < await panelHeaders.count(); index += 1) {
    const alignment = await panelHeaders.nth(index).evaluate((element) => {
      const header = element.getBoundingClientRect()
      const button = element.querySelector<HTMLElement>('.el-button')!.getBoundingClientRect()
      return { topDifference: Math.abs(button.top - header.top), rightDifference: Math.abs(header.right - button.right) }
    })
    expect(alignment.topDifference).toBeLessThanOrEqual(2)
    expect(alignment.rightDifference).toBeLessThanOrEqual(2)
  }

  const zeroTrendBars = page.locator('.trend-bar')
  await expect(zeroTrendBars).toHaveCount(28)
  const visibleZeroBars = await zeroTrendBars.evaluateAll(elements => elements.filter(element => element.getBoundingClientRect().height > 0).length)
  expect(visibleZeroBars).toBe(0)
  const coverageRows = page.locator('.test-coverage-panel .coverage-progress-row')
  await expect(coverageRows).toHaveCount(2)
  await expect(coverageRows.locator('.el-progress__text')).toHaveCount(0)
  const percentageRightEdges = await coverageRows.locator('strong').evaluateAll(elements => elements.map(element => element.getBoundingClientRect().right))
  expect(Math.abs(percentageRightEdges[0] - percentageRightEdges[1])).toBeLessThanOrEqual(1)

  for (const viewport of [{ width: 1024, height: 768 }, { width: 800, height: 900 }]) {
    await page.setViewportSize(viewport)
    const layout = await page.evaluate(() => {
      const cards = Array.from(document.querySelectorAll<HTMLElement>('.stat-card')).map(element => element.getBoundingClientRect())
      const contentPanels = Array.from(document.querySelectorAll<HTMLElement>('.content-grid > .panel')).map(element => element.getBoundingClientRect())
      const securityPanels = Array.from(document.querySelectorAll<HTMLElement>('.security-content-grid > .panel')).map(element => element.getBoundingClientRect())
      return {
        documentOverflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
        contentOverflow: document.querySelector<HTMLElement>('.page-content')!.scrollWidth - document.querySelector<HTMLElement>('.page-content')!.clientWidth,
        statThirdRowOffset: cards[2].top - cards[0].top,
        contentSecondRowOffset: contentPanels[1].top - contentPanels[0].top,
        securitySecondRowOffset: securityPanels[1].top - securityPanels[0].top,
      }
    })
    expect(layout.documentOverflow, `${viewport.width}px 下页面出现整体横向溢出`).toBeLessThanOrEqual(1)
    expect(layout.contentOverflow, `${viewport.width}px 下内容区出现横向溢出`).toBeLessThanOrEqual(1)
    expect(layout.statThirdRowOffset).toBeGreaterThan(0)
    expect(layout.contentSecondRowOffset).toBeGreaterThan(0)
    expect(layout.securitySecondRowOffset).toBeGreaterThan(0)
  }
})

test('质量巡检历史从质量整改拆分为独立子页面', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')

  await page.goto('/data-quality-issues')
  await login(page, adminUsername, adminPassword!)
  await expect(page.getByRole('heading', { name: '质量整改', exact: true })).toBeVisible()
  await expect(page.getByText('最近巡检批次', { exact: true })).toHaveCount(0)

  const navigation = page.getByRole('navigation', { name: '主导航' })
  const historyEntry = navigation.getByRole('button', { name: '巡检历史', exact: true })
  await expect(historyEntry).toBeVisible()
  await expect(historyEntry).toHaveClass(/nav-nested-item/)
  await historyEntry.click()

  await expect(page).toHaveURL(/\/data-quality-scan-history$/)
  await expect(page.getByRole('heading', { name: '巡检历史', exact: true })).toBeVisible()
  await expect(page.getByText('质量巡检批次', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '返回质量整改', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '执行质量巡检', exact: true })).toBeVisible()
})
