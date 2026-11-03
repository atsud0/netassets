import { expect, test, type Page } from '@playwright/test'

const timestamp = '2026-10-30T00:00:00Z'
const organization = { id: 'org-one', code: 'ORG', name: '测试组织', type: 'company', status: 'enabled' }
const permissions = ['asset.manage', 'system.manage', 'organization.read', 'user.manage', 'inventory.manage', 'data_quality.manage', 'security_test.manage', 'security_test.request', 'security_finding.manage', 'security_workorder.manage', 'remediation.assign', 'remediation.review', 'remediation.people.manage']
const longViewName = '跨组织公网资产与安全整改跟进视图'.repeat(5)

async function fixtures(page: Page, role = 'group_admin', empty = false) {
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    if (path === '/auth/me') {
      return route.fulfill({ json: { user: {
        id: 'user-one', username: 'tester', display_name: '布局测试', must_change_password: false,
        organization, organizations: [organization], roles: [role === 'workorder_operator' ? 'security_operator' : role], repair_enabled: true,
        execution_only: role === 'executor', permissions: role === 'group_admin' ? permissions : role === 'workorder_operator' ? ['security_workorder.manage'] : ['asset.manage', 'system.manage', 'inventory.manage'],
      } } })
    }
    if (path === '/saved-filter-views') {
      return route.fulfill({ json: { items: [{ id: 'view-one', page_key: new URL(route.request().url()).searchParams.get('page_key'), name: longViewName, is_default: true, filters: {} }], total: 1 } })
    }
    const common = { title: '待处理安全问题', code: 'SF-001', number: 'WO-001', name: '测试资源', status: 'pending', severity: 'high', updated_at: timestamp, created_at: timestamp, discovered_at: timestamp, due_at: timestamp, assignee_organization_id: organization.id, assignee_organization_name: organization.name, organization_id: organization.id, organization_name: organization.name }
    if (path === '/security-work-orders/order-1') return route.fulfill({ json: { item: { ...common, id: 'order-1', execution_mode: 'organization', status: 'draft', finding_count: 0, remediation_counts: {} } } })
    let items: object[] = []
    if (path === '/security-work-orders') items = [1, 2].map(index => ({ ...common, id: `order-${index}`, number: `WO-00${index}`, status: 'draft', execution_mode: 'organization', finding_count: 1, remediation_counts: {} }))
    if (path === '/security-findings') items = [1, 2].map(index => ({ ...common, id: `finding-${index}`, code: `SF-00${index}` }))
    if (path === '/security-findings/import-candidates') items = [1, 2].map(index => ({ ...common, id: `candidate-${index}`, asset_type: 'ip', asset_value: `198.51.100.${index}` }))
    if (path === '/assets') items = [{ ...common, id: 'asset-one', value: '198.51.100.1', type: 'ip', status: 'active', name: '测试资产' }]
    if (path === '/data-quality-issues') items = [{ ...common, id: 'quality-one', rule_name: '必填项检查', target_field: 'owner', resource_type: 'asset', resource_name: '测试资产', severity: 'error', status: 'pending' }]
    if (path.endsWith('/organizations') || path === '/organizations') items = [organization]
    if (empty) items = []
    const summary = { total: items.length, unread_count: 0, error: items.length, warning: 0, pending: items.length, confirmed: 0, in_progress: 0, resolved: 0, critical: 0, high: items.length, medium: 0, low: 0 }
    if (path === '/dashboard/security-overview') return route.fulfill({ json: { overview: { finding_severity: [], finding_status: [], finding_trend: [], organization_performance: [], test_coverage: {} } } })
    return route.fulfill({ json: { items, total: items.length, page: 1, page_size: 20, summary, counts: {}, by_status: {}, by_severity: {}, by_execution_blocked_reason: {}, backups: [] } })
  })
}

async function checkToolbars(page: Page) {
  const errors = await page.locator('.list-toolbar:visible').evaluateAll(groups => groups.flatMap(group => {
    const bounds = group.getBoundingClientRect()
    const errors: string[] = []
    for (const control of group.querySelectorAll('.el-button, .el-input, .el-select, summary')) {
      const box = control.getBoundingClientRect()
      if (!box.width || !box.height) continue
      if (box.left < bounds.left - 1 || box.right > bounds.right + 1) errors.push(`${group.getAttribute('aria-label')}: ${control.textContent} 超出工具栏`)
    }
    const main = group.querySelector(':scope > .list-toolbar-main')?.getBoundingClientRect()
    const end = group.querySelector(':scope > .list-toolbar-end')?.getBoundingClientRect()
    if (main && end && Math.abs(main.y - end.y) < 4) {
      if (end.left < main.right - 1 || end.right < bounds.right - 24) errors.push(`${group.getAttribute('aria-label')}: 左右分区未对齐`)
    }
    return errors
  }))
  expect(errors).toEqual([])
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)
  expect(overflow).toBe(false)
}

for (const width of [1440, 1280, 768, 390]) {
  test(`三个安全 Tab、空选择、多选与长视图名 ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 })
    await fixtures(page)
    await page.goto('/security-work-orders')
    await expect(page.getByRole('heading', { name: '安全工单', exact: true })).toBeVisible()
    for (const tab of ['安全整改工单', '漏洞与安全发现', '待关联漏洞']) {
      await page.getByRole('tab', { name: tab }).click()
      await expect(page.locator('.el-table__body .el-checkbox').first()).toBeVisible()
      await expect(page.getByRole('group', { name: '批量操作', exact: true })).toHaveCount(0)
      await checkToolbars(page)
      await page.locator('.el-table__body .el-checkbox').first().click()
      await expect(page.getByRole('group', { name: '批量操作', exact: true }).getByRole('status')).toHaveText('已选 1 项')
      await page.locator('.el-table__body .el-checkbox').nth(1).click()
      await expect(page.getByRole('group', { name: '批量操作', exact: true }).getByRole('status')).toHaveText('已选 2 项')
      await expect(page.getByText('风险接受、重复关联及其他状态请单独处理；受工单约束的记录将返回原因。', { exact: true })).toHaveCount(0)
      await checkToolbars(page)
      await page.screenshot({ path: testInfo.outputPath(`${width}-${tab}.png`), fullPage: true })
      await page.getByRole('button', { name: '清空选择', exact: true }).click()
      await expect(page.getByRole('group', { name: '批量操作', exact: true })).toHaveCount(0)
    }
    await page.getByRole('tab', { name: '安全整改工单', exact: true }).click()
    await expect(page.getByRole('combobox', { name: '已保存视图' })).toBeVisible()
    await expect(page.getByRole('button', { name: '管理已选视图' })).toBeEnabled()
  })

  test(`共享保存视图调用方 ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 })
    await fixtures(page)
    for (const path of ['/assets', '/data-quality-issues']) {
      await page.goto(path)
      await expect(page.getByRole('combobox', { name: '已保存视图' })).toBeVisible()
      await expect(page.getByRole('button', { name: '管理已选视图' })).toBeEnabled()
      await checkToolbars(page)
      await page.screenshot({ path: testInfo.outputPath(`${width}-${path.slice(1)}.png`), fullPage: true })
      await page.locator('.el-table__body .el-checkbox').first().click()
      await expect(page.locator('.list-toolbar-selection')).toBeVisible()
      await checkToolbars(page)
    }
  })

  test(`登录与邀请表单保留安全提示 ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1000 })
    await page.route('**/api/v1/auth/me', route => route.fulfill({ status: 401, json: {} }))
    await page.goto('/login')
    await expect(page.getByRole('button', { name: '登录', exact: true })).toBeVisible()
    await checkToolbars(page)
    await page.goto('/repair/invite')
    await expect(page.getByRole('heading', { name: '加入修复工作台' })).toBeVisible()
    await expect(page.getByText('邀请链接缺少有效凭证，请使用管理员提供的完整链接。')).toBeVisible()
    await checkToolbars(page)
  })
}

test('子公司和执行人员不显示集团批量入口', async ({ page }) => {
  await fixtures(page, 'organization_admin')
  await page.goto('/security-work-orders')
  await expect(page.getByRole('heading', { name: '安全工单', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '新建整改工单' })).toHaveCount(0)
  await expect(page.getByRole('group', { name: '批量操作', exact: true })).toHaveCount(0)
  await expect(page.getByRole('tab', { name: '待关联漏洞' })).toHaveCount(0)
  await page.unrouteAll()
  await fixtures(page, 'executor', true)
  await page.goto('/repair/tasks')
  await expect(page.getByRole('heading', { name: '我的修复任务', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '选择工单派发' })).toHaveCount(0)
  await expect(page.getByText('当前视图没有修复任务', { exact: true })).toBeVisible()
  await checkToolbars(page)
})

test('保存视图管理、默认禁用及删除确认', async ({ page }) => {
  await fixtures(page)
  await page.goto('/assets')
  await page.getByRole('button', { name: '管理已选视图' }).click()
  await expect(page.getByRole('menuitem', { name: '设为默认' })).toHaveAttribute('aria-disabled', 'true')
  await page.getByRole('menuitem', { name: '删除', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '删除保存视图' })).toContainText(longViewName)
  await page.getByRole('button', { name: '取消', exact: true }).click()
  await page.getByRole('button', { name: '保存当前筛选', exact: true }).click()
  await expect(page.getByRole('dialog', { name: '保存当前筛选' })).toBeVisible()
})

for (const width of [1440, 1280, 768, 390]) {
  test(`全站页面空数据与公共工具区 ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 })
    await fixtures(page, 'group_admin', true)
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    for (const path of ['/systems', '/organizations', '/users', '/asset-candidates', '/export-jobs', '/security-test-batches', '/security-test-requests', '/data-governance', '/inventory-tasks', '/recycle-bin', '/remediation-people', '/remediation', '/notifications', '/audit-logs', '/backups', '/data-quality-scan-history', '/dashboard', '/profile']) {
      await page.goto(path)
      await expect(page.locator('.page-content h1')).toBeVisible()
      await checkToolbars(page)
      await page.screenshot({ path: testInfo.outputPath(`${width}-${path.slice(1)}.png`), fullPage: true })
    }
    expect(errors).toEqual([])
  })
}

test('只拥有工单权限时不泄露漏洞批量状态操作', async ({ page }) => {
  await fixtures(page, 'workorder_operator')
  await page.goto('/security-work-orders?tab=findings')
  await page.locator('.el-table__body .el-checkbox').first().click()
  const toolbar = page.getByRole('group', { name: '批量操作', exact: true })
  await expect(toolbar.getByRole('button', { name: '生成工单' })).toBeVisible()
  await expect(toolbar.getByRole('button', { name: '批量修改状态' })).toHaveCount(0)
  await expect(toolbar.getByRole('combobox', { name: '批量目标状态' })).toHaveCount(0)
})

test('加载中不出现无效批量栏，受限项仍有逐项失败原因', async ({ page }) => {
  await fixtures(page)
  let release: () => void = () => {}
  const gate = new Promise<void>(resolve => { release = resolve })
  await page.route('**/api/v1/security-work-orders?*', async route => {
    await gate
    await route.fallback()
  })
  await page.goto('/security-work-orders')
  await expect(page.locator('.security-panel .el-loading-mask')).toBeVisible()
  await expect(page.getByRole('group', { name: '批量操作', exact: true })).toHaveCount(0)
  release()
  await page.locator('.el-table__body .el-checkbox').first().click()
  await page.locator('.el-table__body .el-checkbox').nth(1).click()
  await page.route('**/api/v1/security-work-orders/batch/cancel', route => route.fulfill({ json: {
    succeeded: 1, failed: 1, items: [{ id: 'order-1', success: true }, { id: 'order-2', success: false, code: 'invalid_state', message: '工单已关闭，不能取消' }],
  } }))
  await page.getByRole('button', { name: '批量取消工单', exact: true }).click()
  const prompt = page.locator('.el-message-box')
  await prompt.locator('textarea').fill('回归验证')
  await prompt.getByRole('button', { name: '确认取消工单', exact: true }).click()
  const result = page.getByRole('dialog', { name: '批量处理结果' })
  await expect(result.getByText('成功 1 项，失败 1 项')).toBeVisible()
  await expect(result.getByText('工单已关闭，不能取消')).toBeVisible()
  await result.getByRole('button', { name: /关闭此对话框|Close this dialog/ }).click()
  await expect(page.getByRole('group', { name: '批量操作', exact: true }).getByRole('status')).toHaveText('已选 1 项')
})

for (const width of [1440, 390]) {
  test(`工单详情和导入弹窗的工具区 ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1000 })
    await fixtures(page)
    await page.goto('/security-work-orders')
    await page.getByRole('button', { name: '待处理安全问题', exact: true }).first().click()
    const detail = page.getByRole('dialog', { name: '安全工单详情', exact: true })
    await expect(detail).toBeVisible()
    await expect(detail.getByText('逐项整改与复测说明', { exact: true })).toBeVisible()
    await expect(detail.getByRole('group', { name: '工单逐项操作' })).toHaveCount(0)
    await checkToolbars(page)
    await detail.getByRole('button', { name: /关闭此对话框|Close this dialog/ }).click()
    await page.getByRole('button', { name: '批量导入漏洞', exact: true }).click()
    await expect(page.getByRole('dialog', { name: '批量导入漏洞与安全发现', exact: true })).toBeVisible()
    await checkToolbars(page)
  })
}
