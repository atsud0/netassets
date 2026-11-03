import { expect, test, type Page } from '@playwright/test'

const timestamp = '2026-10-30T00:00:00Z'
const findingID = '00000000-0000-0000-0000-000000000001'
const candidateID = '00000000-0000-0000-0000-000000000002'

async function login(page: Page) {
  const response = await page.request.post('/api/v1/auth/login', { data: {
    username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD,
  } })
  expect(response.status(), await response.text()).toBe(200)
}


async function selectDraftStatus(page: Page) {
  const filtered = page.waitForResponse(response => {
    const url = new URL(response.url())
    return url.pathname === '/api/v1/security-work-orders' && url.searchParams.get('status') === 'draft' && url.searchParams.get('page') === '1'
  })
  await page.getByRole('group', { name: '安全工单筛选', exact: true }).getByText('全部状态', { exact: true }).click()
  await page.getByRole('option', { name: '草稿', exact: true }).click()
  expect((await filtered).status()).toBe(200)
}

function finding() {
  return { id: findingID, code: 'SF-TEST', title: '标题直达安全发现', status: 'pending', severity: 'high', assignee_organization_id: '00000000-0000-0000-0000-000000000100', assignee_organization_name: '测试组织', source: 'manual', description: '完整漏洞描述', discovered_at: timestamp, created_at: timestamp, updated_at: timestamp }
}

function candidate(status = 'pending') {
  return { ...finding(), id: candidateID, title: '标题直达候选详情', asset_type: 'ip', asset_value: '198.51.100.10', source_row_number: 2,
    finding_import_id: 'IMPORT-TEST', status, raw_data: { '原始字段': '原始证据' }, review_note: status === 'pending' ? '' : '处理原因', reviewed_by_name: '测试处理人', reviewed_at: timestamp,
    ...(status === 'imported' ? { imported_finding_id: findingID, imported_finding_code: 'SF-TEST' } : {}),
  }
}

async function stubLists(page: Page, options: { orderCount?: number; findingCount?: number; candidateStatus?: string; failDetail?: boolean } = {}) {
  let cancelled = false
  let ignored = false
  let confirmed = false
  await page.route(/\/api\/v1\/security-/, async route => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace('/api/v1', '')
    const currentPage = Number(url.searchParams.get('page') ?? 1)
    const pageSize = Number(url.searchParams.get('page_size') ?? 20)
    if (request.method() !== 'GET') {
      const payload = request.postDataJSON()
      expect(payload.items.length).toBeGreaterThan(0)
      expect(payload.items.length).toBeLessThanOrEqual(200)
      cancelled ||= path === '/security-work-orders/batch/cancel'
      ignored ||= path === '/security-findings/import-candidates/batch/ignore'
      confirmed ||= path === '/security-findings/batch'
      const result = { succeeded: payload.items.length - 1, failed: 1, items: payload.items.map((item: { id: string }, index: number) => ({ id: item.id, success: index > 0, ...(index === 0 ? { code: 'invalid_state', message: '状态已变更，请刷新' } : {}) })) }
      await route.fulfill({ json: result })
      return
    }
    if (path === `/security-findings/import-candidates/${candidateID}`) {
      await route.fulfill(options.failDetail ? { status: 500, json: { error: { message: '详情暂不可用' } } } : { json: { item: candidate(ignored ? 'ignored' : options.candidateStatus) } })
      return
    }
    if (path === `/security-findings/${findingID}`) {
      await route.fulfill({ json: { item: finding() } })
      return
    }
    if (path === '/security-findings/summary') {
      await route.fulfill({ json: { summary: { total: 2, pending: 2 } } })
      return
    }
    let items: object[] = []
    let total = 0
    if (path === '/security-work-orders') {
      total = options.orderCount ?? 3
      items = Array.from({ length: Math.min(pageSize, Math.max(0, total - (currentPage - 1) * pageSize)) }, (_, index) => {
        const number = (currentPage - 1) * pageSize + index + 1
        return { ...finding(), id: `order-${number}`, title: `测试工单 ${number}`, number: `WO-${number}`, status: cancelled || (total === 3 && number === 3) ? 'cancelled' : 'draft', execution_mode: 'organization', finding_count: 1, findings: [], remediation_counts: {}, due_at: timestamp }
      })
    } else if (path === '/security-findings') {
      total = options.findingCount ?? 2
      items = options.findingCount ? Array.from({ length: Math.min(pageSize, Math.max(0, total - (currentPage - 1) * pageSize)) }, (_, index) => {
        const number = (currentPage - 1) * pageSize + index + 1
        return { ...finding(), id: `finding-${number}`, title: `多选发现 ${number}`, status: number === 21 ? 'confirmed' : 'pending' }
      }) : [finding(), { ...finding(), id: 'finding-two', title: '第二项安全发现', status: confirmed ? 'confirmed' : 'pending' }]
    } else if (path === '/security-findings/import-candidates') {
      total = 2
      items = [candidate(ignored ? 'ignored' : options.candidateStatus), { ...candidate(ignored ? 'ignored' : options.candidateStatus), id: 'candidate-two', title: '第二项候选' }]
    }
    await route.fulfill({ json: { items, total, page: currentPage, page_size: pageSize } })
  })
}

for (const width of [1440, 390]) {
  test(`翻页返回列表头部，筛选回到第一页（${width}px）`, async ({ page }) => {
    await page.setViewportSize({ width, height: 800 })
    await login(page)
    await stubLists(page, { orderCount: 60 })
    await page.goto('/security-work-orders')
    await expect(page.getByRole('button', { name: '测试工单 1', exact: true })).toBeVisible()
    await page.locator('.page-content').evaluate(element => { element.scrollTop = element.scrollHeight })
    await page.locator('.el-pagination .btn-next').click()
    await expect(page.getByRole('button', { name: '测试工单 21', exact: true })).toBeVisible()
    await expect.poll(async () => page.locator('.page-content').evaluate(container => {
      const table = container.querySelector('.el-table')!
      return Math.abs(table.getBoundingClientRect().top - container.getBoundingClientRect().top - 12)
    })).toBeLessThan(5)
    await expect(page.locator('.work-order-stat-list')).toHaveCount(0)
    await selectDraftStatus(page)
    await expect(page.getByRole('button', { name: '测试工单 1', exact: true })).toBeVisible()
  })
}

test('三个 Tab 全选、逐项失败反馈及失败项保留选择', async ({ page }) => {
  await login(page)
  await stubLists(page)
  await page.goto('/security-work-orders')
  for (const tab of ['orders', 'findings', 'candidates']) {
    if (tab !== 'orders') await page.getByRole('tab', { name: tab === 'findings' ? '漏洞与安全发现' : '待关联漏洞', exact: true }).click()
    await page.locator('.security-panel .el-table__header-wrapper .el-checkbox').first().click()
    await expect(page.getByText('已选 2 项', { exact: false })).toBeVisible()
    await page.getByRole('button', { name: tab === 'orders' ? '批量取消工单' : tab === 'findings' ? '批量修改状态' : '批量忽略', exact: true }).click()
    const prompt = page.locator('.el-message-box')
    if (tab !== 'findings') await prompt.locator('textarea').fill('统一处理原因')
    await prompt.getByRole('button', { name: tab === 'orders' ? '确认取消工单' : tab === 'candidates' ? '确认忽略' : '确定', exact: true }).click()
    const result = page.getByRole('dialog', { name: '批量处理结果' })
    await expect(result.getByText('成功 1 项，失败 1 项')).toBeVisible()
    await expect(result.getByText('状态已变更，请刷新')).toBeVisible()
    await result.getByRole('button', { name: /关闭此对话框|Close this dialog/ }).click()
    await expect(page.getByText('已选 1 项', { exact: false })).toBeVisible()
  }
})

test('跨页、筛选和标签保留选择，刷新页面才清空', async ({ page }) => {
  await login(page)
  await stubLists(page, { orderCount: 25 })
  await page.goto('/security-work-orders')
  const checkboxes = () => page.locator('.security-panel .el-table__body-wrapper .el-checkbox')
  await checkboxes().first().click()
  await page.locator('.security-panel .el-pagination .btn-next').click()
  await expect(page.getByRole('button', { name: '测试工单 21', exact: true })).toBeVisible()
  await expect(page.getByText('已选 1 项', { exact: false })).toBeVisible()
  await checkboxes().first().click()
  await expect(page.getByText('已选 2 项', { exact: false })).toBeVisible()
  await selectDraftStatus(page)
  await expect(page.getByRole('button', { name: '测试工单 1', exact: true })).toBeVisible()
  await expect(checkboxes().first()).toHaveClass(/is-checked/)
  await expect(page.getByText('已选 2 项', { exact: false })).toBeVisible()
  await checkboxes().first().click()
  await expect(page.getByText('已选 1 项', { exact: false })).toBeVisible()
  await page.getByRole('tab', { name: '漏洞与安全发现', exact: true }).click()
  await expect(page.getByRole('group', { name: '批量操作', exact: true })).toHaveCount(0)
  await checkboxes().first().click()
  await page.getByRole('tab', { name: '安全整改工单', exact: true }).click()
  await expect(page.getByText('已选 1 项', { exact: false })).toBeVisible()
  await page.locator('.security-panel .el-pagination .btn-next').click()
  await expect(checkboxes().first()).toHaveClass(/is-checked/)
  await page.reload()
  await expect(page.getByRole('group', { name: '批量操作', exact: true })).toHaveCount(0)
})

test('跨页多选发现后预填工单并提交全部选择', async ({ page }) => {
  await login(page)
  await stubLists(page, { findingCount: 21 })
  await page.route(/\/api\/v1\/organizations(?:\?|$)/, route => route.fulfill({ json: {
    items: [{ id: finding().assignee_organization_id, name: '测试组织', status: 'enabled', type: 'subsidiary' }], total: 1, page: 1, page_size: 100,
  } }))
  let createdPayload: Record<string, unknown> | undefined
  await page.route(/\/api\/v1\/security-work-orders(?:\?|$)/, async route => {
    if (route.request().method() !== 'POST') return route.fallback()
    createdPayload = route.request().postDataJSON()
    await route.fulfill({ status: 201, json: { item: { id: 'created-order' } } })
  })
  await page.goto('/security-work-orders?tab=findings')
  const checkboxes = () => page.locator('.security-panel .el-table__body-wrapper .el-checkbox')
  await checkboxes().first().click()
  await page.locator('.security-panel .el-pagination .btn-next').click()
  await expect(page.getByRole('button', { name: '多选发现 21', exact: true })).toBeVisible()
  await checkboxes().first().click()
  await expect(page.getByText('已选 2 项', { exact: false })).toBeVisible()
  const generate = page.getByRole('group', { name: '批量操作', exact: true }).getByRole('button', { name: '生成工单', exact: true })
  await generate.click()
  const dialog = page.getByRole('dialog', { name: '新建安全整改工单', exact: true })
  await expect(dialog.getByRole('textbox').first()).toHaveValue('2 项安全发现整改')
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
  await expect(page.getByText('已选 2 项', { exact: false })).toBeVisible()
  await generate.click()
  await dialog.locator('.el-form-item').filter({ hasText: '关联安全发现' }).locator('.el-select').click()
  await expect(page.getByRole('option', { selected: true })).toHaveCount(2)
  await dialog.getByRole('textbox').first().click()
  await dialog.locator('.el-switch').click()
  await dialog.getByRole('button', { name: '保存草稿', exact: true }).click()
  await expect(dialog).not.toBeVisible()
  expect(createdPayload).toMatchObject({ finding_ids: ['finding-1', 'finding-21'], assignee_organization_id: finding().assignee_organization_id })
  await page.getByRole('tab', { name: '漏洞与安全发现', exact: true }).click()
  await expect(page.getByRole('group', { name: '批量操作', exact: true })).toHaveCount(0)
})

test('漏洞标题支持键盘直达，候选只读详情与已导入发现导航', async ({ page }) => {
  await login(page)
  await stubLists(page, { candidateStatus: 'imported' })
  await page.goto('/security-work-orders?tab=findings')
  const title = page.getByRole('button', { name: '标题直达安全发现', exact: true })
  await title.focus()
  await title.press('Enter')
  await expect(page.getByRole('dialog', { name: '安全发现详情', exact: true })).toBeVisible()
  await page.getByRole('dialog', { name: '安全发现详情', exact: true }).getByRole('button', { name: /关闭此对话框|Close this dialog/ }).click()
  await page.getByRole('tab', { name: '待关联漏洞', exact: true }).click()
  await page.getByRole('button', { name: '标题直达候选详情', exact: true }).click()
  const detail = page.getByRole('dialog', { name: '待关联漏洞详情', exact: true })
  await expect(detail.getByText('处理原因', { exact: true }).last()).toBeVisible()
  await expect(detail.getByRole('button', { name: '关联资产', exact: true })).toHaveCount(0)
  await detail.getByRole('button', { name: '查看安全发现 SF-TEST' }).click()
  await expect(page.getByRole('dialog', { name: '安全发现详情', exact: true })).toBeVisible()
})

test('候选详情失败明确提示且不误开关联流程', async ({ page }) => {
  await login(page)
  await stubLists(page, { failDetail: true })
  await page.goto('/security-work-orders?tab=candidates')
  await page.getByRole('button', { name: '标题直达候选详情', exact: true }).click()
  await expect(page.getByText('详情暂不可用', { exact: true })).toBeVisible()
  await expect(page.getByRole('dialog', { name: '关联漏洞到资产', exact: true })).toHaveCount(0)
})

test('真实候选详情与批量忽略接口保留证据，重试及部分失败逐项报告', async ({ page }) => {
  await login(page)
  const imported = await page.request.post('/api/v1/security-findings/import', { multipart: { file: {
    name: 'issue-candidates.csv', mimeType: 'text/csv', buffer: Buffer.from(`资产类型,资产值,漏洞标题,严重等级,漏洞描述\nip,198.51.100.52,issue-${Date.now()},高危,真实接口测试\n`),
  } } })
  expect(imported.status(), await imported.text()).toBe(201)
  const importID = (await imported.json()).item.id
  const listed = await page.request.get('/api/v1/security-findings/import-candidates?status=pending&page_size=100')
  const item = (await listed.json()).items.find((entry: { finding_import_id: string }) => entry.finding_import_id === importID)
  expect(item).toBeTruthy()
  const detail = await page.request.get(`/api/v1/security-findings/import-candidates/${item.id}`)
  expect(detail.status(), await detail.text()).toBe(200)
  const payload = { items: [{ id: item.id }, { id: '00000000-0000-0000-0000-000000000099' }], reason: '统一忽略，保留原始证据' }
  const response = await page.request.post('/api/v1/security-findings/import-candidates/batch/ignore', { data: payload })
  expect(response.status(), await response.text()).toBe(200)
  expect(await response.json()).toMatchObject({ succeeded: 1, failed: 1 })
  const retry = await page.request.post('/api/v1/security-findings/import-candidates/batch/ignore', { data: payload })
  expect(await retry.json()).toMatchObject({ succeeded: 0, failed: 2, items: [{ code: 'invalid_state' }, { code: 'not_found' }] })
  const ignoredDetail = await page.request.get(`/api/v1/security-findings/import-candidates/${item.id}`)
  expect((await ignoredDetail.json()).item).toMatchObject({ status: 'ignored', review_note: payload.reason })
  const invalid = await page.request.post('/api/v1/security-work-orders/batch/cancel', { data: { items: [] } })
  expect(invalid.status()).toBe(400)
})

test('未登录及组织填报员不能越权调用批量端点', async ({ page, request, browser, baseURL }) => {
  const paths = ['/security-work-orders/batch/cancel', '/security-findings/batch', '/security-findings/import-candidates/batch/ignore']
  const payload = { items: [{ id: findingID, expected_updated_at: timestamp }], status: 'confirmed', reason: '越权测试' }
  for (const path of paths) {
    const response = await request.fetch(`/api/v1${path}`, { method: path === '/security-findings/batch' ? 'PATCH' : 'POST', data: payload })
    expect(response.status(), await response.text()).toBe(401)
  }
  const detail = await request.get(`/api/v1/security-findings/import-candidates/${candidateID}`)
  expect(detail.status()).toBe(401)
  await login(page)
  const roles = (await (await page.request.get('/api/v1/roles?page_size=100')).json()).items
  const editorRole = roles.find((role: { key: string }) => role.key === 'organization_editor')
  const organizations = (await (await page.request.get('/api/v1/organizations?page_size=100')).json()).items
  const root = organizations.find((item: { type: string }) => item.type === 'group')
  const suffix = Date.now()
  const createdOrganization = await page.request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `BATCH_${suffix}`, name: '批量越权回归组织', type: 'subsidiary' } })
  expect(createdOrganization.status(), await createdOrganization.text()).toBe(201)
  const organization = (await createdOrganization.json()).item
  const password = `Batch-role-test-${suffix}`
  const username = `batch_editor_${suffix}`
  const createdUser = await page.request.post('/api/v1/users', { data: {
    username, password, display_name: '批量回归填报员', global_role_ids: [],
    appointments: [{ organization_id: organization.id, is_primary: true, assignments: [{ role_id: editorRole.id, data_scope: 'organization' }] }],
  } })
  expect(createdUser.status(), await createdUser.text()).toBe(201)
  const context = await browser.newContext({ baseURL })
  try {
    const response = await context.request.post('/api/v1/auth/login', { data: { username, password } })
    expect(response.status(), await response.text()).toBe(200)
    const changed = await context.request.put('/api/v1/auth/profile/password', { data: { current_password: password, new_password: `${password}-changed` } })
    expect(changed.status(), await changed.text()).toBe(204)
    for (const path of paths) {
      const forbidden = await context.request.fetch(`/api/v1${path}`, { method: path === '/security-findings/batch' ? 'PATCH' : 'POST', data: payload })
      expect(forbidden.status(), await forbidden.text()).toBe(403)
      expect((await forbidden.json()).error.code).toBe('forbidden')
    }
  } finally {
    await context.close()
  }
})
