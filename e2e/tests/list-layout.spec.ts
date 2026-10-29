import { expect, test } from '@playwright/test'

test.beforeEach(async ({ page }) => {
  const response = await page.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME, password: process.env.E2E_PASSWORD } })
  expect(response.ok()).toBeTruthy()
})

test('列表操作按状态收纳，键盘可退出，名称仍打开详情', async ({ page }) => {
  const items = ['draft', 'authorized', 'in_progress', 'completed', 'cancelled'].map((status, i) => ({
    id: `batch-${i}`, code: `BATCH-${i}`, name: `列表检查-${status}`, test_type: 'penetration', status,
    provider: '集团安全团队', authorization_note: '验证数据', asset_count: 12, organization_count: 5,
    start_at: '2026-10-03T08:00:00Z', end_at: '2026-10-10T08:00:00Z', scope_assets: [],
  }))
  await page.route(/\/api\/v1\/security-test-batches/, async route => {
    const path = new URL(route.request().url()).pathname
    const last = path.split('/').pop()
    const item = items.find(item => item.id === last)
    await route.fulfill({ json: item ? { item } : { items: path.endsWith('security-test-batches') ? items : [], total: path.endsWith('security-test-batches') ? items.length : 0 } })
  })
  await page.setViewportSize({ width: 1280, height: 1000 })
  await page.goto('/security-test-batches')
  const draft = page.locator('.el-table__row').filter({ hasText: '列表检查-draft' })
  const trigger = draft.getByRole('button', { name: '操作：列表检查-draft', exact: true })
  await trigger.click()
  const panel = page.locator('.row-actions-panel:visible')
  await expect(panel.getByRole('button')).toHaveText(['详情', '编辑', '授权', '取消'])
  await expect(panel.getByRole('button').first()).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(panel).toHaveCount(0)
  await expect(trigger).toBeFocused()
  for (const [status, actions] of [['authorized', ['详情', '开始', '取消']], ['in_progress', ['详情', '完成', '取消']]] as const) {
    await page.locator('.el-table__row').filter({ hasText: `列表检查-${status}` }).getByRole('button', { name: `操作：列表检查-${status}`, exact: true }).click()
    await expect(panel.getByRole('button')).toHaveText([...actions])
    await page.keyboard.press('Escape')
  }
  for (const status of ['completed', 'cancelled']) {
    const row = page.locator('.el-table__row').filter({ hasText: `列表检查-${status}` })
    await expect(row.getByRole('button', { name: '详情', exact: true })).toBeVisible()
    await expect(row.locator('[aria-haspopup="dialog"]')).toHaveCount(0)
  }
  await draft.getByRole('button', { name: '列表检查-draft', exact: true }).click()
  await expect(page.getByRole('dialog').filter({ hasText: 'BATCH-0' })).toBeVisible()
})

test('主列表及标签页在桌面宽度不横向溢出', async ({ page }) => {
  // This sweep renders 18 real pages against the retained, growing database.
  // Keep the per-table 5s layout assertion; do not conflate sweep runtime with overflow.
  test.setTimeout(600_000)
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  const routes = ['security-test-batches', 'security-test-requests', 'security-work-orders', 'assets', 'systems', 'inventory-tasks', 'remediation', 'remediation-people', 'users', 'organizations', 'asset-candidates', 'data-governance', 'data-quality-issues', 'data-quality-scan-history', 'export-jobs', 'audit-logs', 'backups', 'recycle-bin']
  await page.route(/\/api\/v1\/backups(?:\?.*)?$/, route => route.fulfill({ json: { items: [{ id: 'backup-layout-check-20261003', created_at: '2026-10-03T08:00:00Z', database_size: 1024, data_size: 2048, verified: true }], total: 1, page: 1, page_size: 20 } }))
  for (const route of routes) {
    await page.goto(`/${route}`)
    await page.waitForLoadState('networkidle')
    const tabs = page.locator('.page-content .el-tabs__item:visible')
    const count = await tabs.count()
    for (let tab = -1; tab < count; tab++) {
      if (tab >= 0) { await tabs.nth(tab).click(); await page.waitForLoadState('networkidle') }
      for (const width of [1280, 1440]) {
        await page.setViewportSize({ width, height: 1000 })
        // setViewportSize acknowledges the browser command, not Vue/Element
        // Plus' resize layout. Wait for real rendered frames before starting
        // the short geometry assertion; do not mask overflow or change CSS.
        await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
        await expect.poll(async () => page.locator('.page-content .el-table:visible').evaluateAll(tables => Math.max(0, ...tables.map(table => (table.querySelector('.el-table__body')?.getBoundingClientRect().width || 0) - table.clientWidth))), { message: `${route}, tab ${tab}, width ${width}` }).toBeLessThanOrEqual(1)
      }
    }
  }
  expect(errors).toEqual([])
})

test('折叠后的导出、资产和备份操作仍完整可用', async ({ page }) => {
  test.setTimeout(120_000)
  await page.goto('/assets')
  await page.locator('.el-table__row').first().getByRole('button', { name: /^操作：/ }).click()
  const panel = page.locator('.row-actions-panel:visible')
  await expect(panel.getByRole('button', { name: '编辑', exact: true })).toBeVisible()
  await expect(panel.getByRole('button', { name: '查看详情', exact: true })).toBeVisible()
  await panel.getByRole('button', { name: '删除资产', exact: true }).click()
  const confirmation = page.getByRole('dialog', { name: '删除网络资产' })
  await expect(confirmation).toBeVisible()
  await confirmation.getByRole('button', { name: '取消', exact: true }).click()
  await page.route('**/api/v1/inventory-tasks?*', route => route.fulfill({ json: { items: [{ id: 'inventory-layout-check', name: '布局验证任务', status: 'active', start_at: '2026-10-03T08:00:00Z', deadline_at: '2026-10-10T08:00:00Z', submitted_count: 1, organization_count: 5 }], total: 1 } }))
  await page.goto('/inventory-tasks')
  await page.locator('.el-table__row').first().getByRole('button', { name: /^操作：/ }).click()
  for (const name of ['导出 XLSX', '导出 CSV', '后台生成 XLSX']) await expect(panel.getByRole('button', { name, exact: true })).toBeVisible()
  await page.keyboard.press('Escape')
  await page.route(/\/api\/v1\/backups(?:\?.*)?$/, route => route.fulfill({ json: { items: [{ id: 'backup-layout-check', created_at: '2026-10-03T08:00:00Z', database_size: 1024, data_size: 2048, verified: true }], total: 1 } }))
  await page.goto('/backups')
  await page.locator('.el-table__row').first().getByRole('button', { name: /^操作：/ }).click()
  await expect(panel.getByRole('link')).toHaveText(['数据库备份', '附件归档', '校验清单'])
})

test('填报模式的系统与资产默认列保持紧凑，扩展字段可主动展开', async ({ page }) => {
  test.setTimeout(120_000)
  const organizations = await (await page.request.get('/api/v1/organizations')).json()
  const organizationID = organizations.items[0].id
  const taskID = '00000000-0000-0000-0000-000000000001'
  await page.route(`**/api/v1/inventory-tasks/${taskID}/**`, route => route.fulfill({ json: { items: [], total: 0, page: 1, page_size: 20 } }))
  await page.setViewportSize({ width: 1280, height: 1000 })
  for (const route of ['assets', 'systems']) {
    await page.goto(`/${route}?mode=inventory&task_id=${taskID}&organization_id=${organizationID}`)
    await page.waitForLoadState('networkidle')
    await expect(page.locator('.inventory-draft-panel')).toBeVisible()
    await expect.poll(() => page.locator('.page-content .el-table:visible').evaluateAll(tables => Math.max(0, ...tables.map(table => (table.querySelector('.el-table__body')?.getBoundingClientRect().width || 0) - table.clientWidth)))).toBeLessThanOrEqual(1)
    if (route === 'assets') {
      await expect(page.getByRole('columnheader', { name: '网络区域', exact: true })).toHaveCount(0)
      const draft = page.locator('.inventory-draft-panel')
      await expect(draft.getByRole('columnheader', { name: '暴露范围', exact: true })).toHaveCount(0)
      await page.getByRole('button', { name: '更多字段', exact: true }).click()
      await expect(page.getByRole('columnheader', { name: '网络区域', exact: true })).toBeVisible()
      await expect(draft.getByRole('columnheader', { name: '暴露范围', exact: true })).toBeVisible()
      await page.getByRole('button', { name: '精简字段', exact: true }).click()
    }
  }
})
