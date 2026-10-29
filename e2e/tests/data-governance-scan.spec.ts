import { expect, test, type Page, type BrowserContext } from '@playwright/test'

import { showRecordPage } from '../helpers/pagination'

const adminUsername = process.env.E2E_USERNAME ?? 'admin'
const adminPassword = process.env.E2E_PASSWORD

type APIResult<T> = { status: number; body: T }

async function login(page: Page, username: string, password: string) {
  await page.goto('/login')
  await page.getByPlaceholder('请输入用户名').fill(username)
  await page.getByPlaceholder('请输入密码').fill(password)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page).not.toHaveURL(/\/login(?:\?|$)/)
}

async function api<T>(page: Page, path: string, requestID: string, method = 'GET', body?: unknown): Promise<APIResult<T>> {
  return page.evaluate(async ({ path, requestID, method, body }) => {
    const response = await fetch(`/api/v1${path}`, {
      method,
      credentials: 'include',
      headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestID },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    const text = await response.text()
    let payload: unknown = null
    if (text) {
      try { payload = JSON.parse(text) } catch { payload = text }
    }
    return { status: response.status, body: payload }
  }, { path, requestID, method, body }) as Promise<APIResult<T>>
}

function requireStatus<T>(result: APIResult<T>, expected: number, step: string): T {
  expect(result.status, `${step}: ${JSON.stringify(result.body)}`).toBe(expected)
  return result.body
}

test('独立测试组织完成质量巡检、子公司整改和集团复核闭环', async ({ browser }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  test.setTimeout(180_000)

  const stamp = Date.now()
  const requestPrefix = `e2e-data-quality-${stamp}`
  const organizationCode = `SCOPE_PW_${stamp}`
  const organizationName = `数据质量整改测试子公司_PW_${stamp}`
  const username = `pw_user_${stamp}`
  const childDisplayName = `质量整改管理员_${stamp}`
  const initialPassword = `Init-PW-${stamp}!`
  const permanentPassword = `Changed-PW-${stamp}!`
  const fieldKey = `pw_quality_${stamp}`
  const ruleName = `DATA_QUALITY_PW_${stamp}`
  const staleRuleName = `DATA_QUALITY_PW_STALE_${stamp}`
  const assetName = `集团权限测试资产_${stamp}`

  const initialContexts = new Set(browser.contexts())
  const contexts = new Set<BrowserContext>()
  async function createContext() {
    const context = await browser.newContext()
    contexts.add(context)
    return context
  }
  async function closeContext(context: BrowserContext) {
    await context.close()
    contexts.delete(context)
  }
  const ownedConfigs: Array<{ path: string; body: Record<string, unknown> }> = []
  try {
    const adminContext = await createContext()
    const adminPage = await adminContext.newPage()
    await login(adminPage, adminUsername, adminPassword!)
    const organizations = requireStatus(
      await api<{ items: Array<{ id: string; type: string }> }>(adminPage, '/organizations/tree', `${requestPrefix}-organizations`),
      200,
      '读取组织',
    ).items
    const group = organizations.find((item) => item.type === 'group')
    expect(group, '未找到集团根组织').toBeTruthy()

    const roles = requireStatus(
      await api<{ items: Array<{ id: string; key: string }> }>(adminPage, '/roles/assignable', `${requestPrefix}-roles`),
      200,
      '读取可分配角色',
    ).items
    const organizationAdminRole = roles.find((item) => item.key === 'organization_admin')
    expect(organizationAdminRole, '未找到子公司管理员系统角色').toBeTruthy()

    const organization = requireStatus(
      await api<{ item: { id: string } }>(adminPage, '/organizations', `${requestPrefix}-organization-create`, 'POST', {
        parent_id: group!.id,
        code: organizationCode,
        name: organizationName,
        short_name: '质量整改测试公司',
        type: 'subsidiary',
        manager: 'E2E',
        contact: '',
        notes: '严格隔离的数据质量整改 E2E 数据',
      }),
      201,
      '创建测试子公司',
    ).item

    const childUser = requireStatus(
      await api<{ item: { id: string } }>(adminPage, '/users', `${requestPrefix}-user-create`, 'POST', {
        username,
        password: initialPassword,
        display_name: childDisplayName,
        email: '',
        mobile_phone: '',
        office_phone: '',
        appointments: [{
          organization_id: organization.id,
          is_primary: true,
          assignments: [{ role_id: organizationAdminRole!.id, data_scope: 'organization' }],
        }],
        global_role_ids: [],
      }),
      201,
      '创建子公司管理员',
    ).item

    const asset = requireStatus(
      await api<{ item: { id: string } }>(adminPage, '/assets', `${requestPrefix}-asset-create`, 'POST', {
        type: 'domain',
        value: `quality-${stamp}.example.com`,
        name: assetName,
        organization_id: organization.id,
        business_system_id: '',
        environment: 'testing',
        network_scope: 'public',
        network_area_id: '',
        status: 'active',
        importance: 'normal',
        owner_name: `责任人_${stamp}`,
        notes: '用于验证集团数据质量治理闭环',
        direct_change_reason: '验证集团数据质量治理闭环',
      }),
      201,
      '创建测试资产',
    ).item

    const customField = requireStatus(
      await api<{ item: { id: string } }>(adminPage, '/custom-fields', `${requestPrefix}-field-create`, 'POST', {
        resource_type: 'asset',
        field_key: fieldKey,
        field_name: `质量整改验证字段_${stamp}`,
        description: 'E2E 专用必填检查字段',
        field_type: 'text',
        options: [],
        is_required: false,
        is_enabled: true,
        sort_order: 90000,
      }),
      201,
      '创建测试自定义字段',
    ).item
    ownedConfigs.push({ path: `/custom-fields/${customField.id}`, body: {
      resource_type: 'asset',
      field_key: fieldKey,
      field_name: `质量整改验证字段_${stamp}`,
      description: 'E2E 专用必填检查字段',
      field_type: 'text',
      options: [],
      is_required: false,
      is_enabled: false,
      sort_order: 90000,
    } })

    const requiredRule = requireStatus(
      await api<{ item: { id: string } }>(adminPage, '/data-quality-rules', `${requestPrefix}-rule-create`, 'POST', {
        rule_name: ruleName,
        description: 'E2E 专用质量规则',
        resource_type: 'asset',
        rule_type: 'required',
        target_field: `custom:${fieldKey}`,
        severity: 'error',
        is_enabled: true,
        conditions: [{ field: 'organization_id', operator: 'eq', values: [organization.id] }],
        sort_order: 90000,
      }),
      201,
      '创建测试质量规则',
    ).item
    ownedConfigs.push({ path: `/data-quality-rules/${requiredRule.id}`, body: {
      rule_name: ruleName,
      description: 'E2E 专用质量规则',
      resource_type: 'asset',
      rule_type: 'required',
      target_field: `custom:${fieldKey}`,
      severity: 'error',
      stale_after_days: 0,
      conditions: [{ field: 'organization_id', operator: 'eq', values: [organization.id] }],
      is_enabled: false,
      sort_order: 90000,
    } })

    const staleRule = requireStatus(
      await api<{ item: { id: string } }>(adminPage, '/data-quality-rules', `${requestPrefix}-stale-rule-create`, 'POST', {
        rule_name: staleRuleName,
        description: 'E2E 专用长期未更新规则',
        resource_type: 'asset',
        rule_type: 'stale',
        target_field: 'updated_at',
        stale_after_days: 1,
        conditions: [{ field: 'organization_id', operator: 'eq', values: [organization.id] }],
        severity: 'warning',
        is_enabled: true,
        sort_order: 90010,
      }),
      201,
      '创建测试长期未更新规则',
    ).item
    ownedConfigs.push({ path: `/data-quality-rules/${staleRule.id}`, body: {
      rule_name: staleRuleName,
      description: 'E2E 专用长期未更新规则',
      resource_type: 'asset',
      rule_type: 'stale',
      target_field: 'updated_at',
      severity: 'warning',
      stale_after_days: 1,
      conditions: [{ field: 'organization_id', operator: 'eq', values: [organization.id] }],
      is_enabled: false,
      sort_order: 90010,
    } })

    await adminPage.goto('/data-governance')
    await adminPage.getByRole('tab', { name: '数据质量规则' }).click()
    await showRecordPage(adminPage, '/api/v1/data-quality-rules', staleRule.id, adminPage.locator('.el-pagination:visible'))
    const staleRuleRow = adminPage.locator('.el-table__row').filter({ hasText: staleRuleName })
    await expect(staleRuleRow).toContainText('长期未更新')
    await expect(staleRuleRow).toContainText('超过 1 天未维护')

    const scan = requireStatus(
      await api<{ result: { batch_id: string; issues: Array<{ issue_id: string; rule_name: string; resource_id: string }> } }>(
        adminPage,
        `/data-quality-rules/scan?organization_id=${organization.id}&resource_type=asset`,
        `${requestPrefix}-scan`,
      ),
      200,
      '执行测试组织质量巡检',
    ).result
    expect(scan.batch_id).toBeTruthy()
    const issue = scan.issues.find((item) => item.rule_name === ruleName && item.resource_id === asset.id)
    expect(issue, '巡检未生成预期质量问题').toBeTruthy()
    expect(scan.issues.some((item) => item.rule_name === staleRuleName), '刚创建的资产不应命中长期未更新规则').toBeFalsy()

    const assignees = requireStatus(
      await api<{ items: Array<{ id: string; username: string }> }>(adminPage, `/data-quality-issues/${issue!.issue_id}/assignees`, `${requestPrefix}-assignees`),
      200,
      '读取整改负责人候选项',
    ).items
    expect(assignees.map((item) => item.id)).toContain(childUser.id)
    expect(assignees.map((item) => item.username)).not.toContain(adminUsername)

    await adminPage.goto('/data-quality-issues')
    await adminPage.locator('.quality-workflow-filters .el-select').filter({ hasText: '全部组织' }).getByRole('combobox').fill(organizationName)
    await adminPage.getByRole('option', { name: organizationName, exact: true }).click()
    const issueListLoaded = adminPage.waitForResponse(response => response.url().includes('/api/v1/data-quality-issues?') && new URL(response.url()).searchParams.get('organization_id') === organization.id)
    await adminPage.getByRole('button', { name: '查询', exact: true }).click()
    expect((await issueListLoaded).status()).toBe(200)
    await showRecordPage(adminPage, `/api/v1/data-quality-issues?organization_id=${organization.id}`, issue!.issue_id, adminPage.locator('.quality-issue-panel .el-pagination'))
    const issueRow = adminPage.locator('.quality-issue-panel .el-table__row')
      .filter({ hasText: ruleName })
      .filter({ hasText: assetName })
    await expect(issueRow).toBeVisible()
    await issueRow.getByRole('button', { name: /^操作：/ }).click()
    const rowActions = adminPage.getByRole('dialog', { name: /^操作：/ })
    await expect(rowActions).toBeVisible()
    await rowActions.getByRole('button', { name: '开始整改', exact: true }).click()
    const assigneeDialog = adminPage.getByRole('dialog', { name: '开始整改' })
    await expect(assigneeDialog).toBeVisible()
    await assigneeDialog.locator('.el-select').click()
    await expect(adminPage.getByRole('option', { name: `${childDisplayName}（${username}）` })).toBeVisible()
    await expect(adminPage.getByRole('option', { name: /集团管理员/ })).toHaveCount(0)
    await adminPage.keyboard.press('Escape')

    requireStatus(await api(adminPage, '/auth/logout', `${requestPrefix}-admin-logout-1`, 'POST'), 204, '集团管理员退出')
    // Logout does not close pages, timers or worker-owned browser contexts.
    await closeContext(adminContext)

    const childContext = await createContext()
    const childPage = await childContext.newPage()
    await login(childPage, username, initialPassword)
    requireStatus(
      await api(childPage, '/auth/profile/password', `${requestPrefix}-child-password`, 'PUT', {
        current_password: initialPassword,
        new_password: permanentPassword,
      }),
      204,
      '子公司管理员修改临时密码',
    )

    const scopedIssues = requireStatus(
      await api<{ items: Array<{ id: string; organization_id: string }> }>(childPage, '/data-quality-issues?page=1&page_size=100', `${requestPrefix}-child-list`),
      200,
      '子公司管理员读取质量问题',
    ).items
    expect(scopedIssues.some((item) => item.id === issue!.issue_id && item.organization_id === organization.id)).toBeTruthy()
    expect(scopedIssues.every((item) => item.organization_id === organization.id)).toBeTruthy()

    const batchStart = requireStatus(
      await api<{ result: { total_count: number; success_count: number; failure_count: number; items: Array<{ id: string; success: boolean }> } }>(childPage, '/data-quality-issues/batch', `${requestPrefix}-batch-start`, 'POST', {
        ids: [issue!.issue_id],
        action: 'start',
        assignee_user_id: childUser.id,
        note: '子公司批量开始整改',
      }),
      200,
      '批量开始整改',
    ).result
    expect(batchStart).toMatchObject({ total_count: 1, success_count: 1, failure_count: 0 })
    expect(batchStart.items).toEqual([expect.objectContaining({ id: issue!.issue_id, success: true })])

    const batchSubmit = requireStatus(
      await api<{ result: { total_count: number; success_count: number; failure_count: number; items: Array<{ id: string; success: boolean }> } }>(childPage, '/data-quality-issues/batch', `${requestPrefix}-batch-submit`, 'POST', {
        ids: [issue!.issue_id],
        action: 'submit',
        note: '已补充核验材料，批量提交集团复核',
      }),
      200,
      '批量提交集团复核',
    ).result
    expect(batchSubmit).toMatchObject({ total_count: 1, success_count: 1, failure_count: 0 })
    expect(batchSubmit.items).toEqual([expect.objectContaining({ id: issue!.issue_id, success: true })])
    requireStatus(
      await api(childPage, `/data-quality-issues/${issue!.issue_id}/review`, `${requestPrefix}-child-review-denied`, 'POST', {
        approved: true,
        note: '',
      }),
      403,
      '阻止子公司自行复核',
    )
    requireStatus(await api(childPage, '/auth/logout', `${requestPrefix}-child-logout`, 'POST'), 204, '子公司管理员退出')
    await closeContext(childContext)

    const reviewContext = await createContext()
    const reviewPage = await reviewContext.newPage()
    await login(reviewPage, adminUsername, adminPassword!)

    requireStatus(await api(reviewPage, '/notifications/summary', `${requestPrefix}-notifications-summary`), 200, '读取消息汇总')
    requireStatus(await api(reviewPage, '/notifications?limit=50', `${requestPrefix}-notifications-list`), 200, '读取消息列表')

    requireStatus(
      await api(reviewPage, `/data-quality-issues/${issue!.issue_id}/review`, `${requestPrefix}-approve`, 'POST', {
        approved: true,
        note: '集团复核通过',
      }),
      200,
      '集团复核通过',
    )
    const detail = requireStatus(
      await api<{ issue: { status: string; resolution_type: string } }>(reviewPage, `/data-quality-issues/${issue!.issue_id}`, `${requestPrefix}-detail`),
      200,
      '读取闭环结果',
    )
    const events = requireStatus(
      await api<{ items: Array<{ event_type: string }> }>(reviewPage, `/data-quality-issues/${issue!.issue_id}/events?page=1&page_size=100`, `${requestPrefix}-events`),
      200,
      '读取问题处理记录',
    ).items
    expect(detail.issue.status).toBe('closed')
    expect(detail.issue.resolution_type).toBe('corrected')
    expect(events.map((item) => item.event_type)).toEqual(expect.arrayContaining(['started', 'submitted', 'approved']))

    requireStatus(await api(reviewPage, '/auth/logout', `${requestPrefix}-admin-logout-2`, 'POST'), 204, '集团管理员退出')
    await closeContext(reviewContext)
  } finally {
    try {
      // Group-wide test rules must not change later tests' quality gates, even on failure.
      const cleanupContext = await createContext()
      try {
        const response = await cleanupContext.request.post('/api/v1/auth/login', { data: { username: adminUsername, password: adminPassword } })
        expect(response.status(), '清理专属规则前登录').toBe(200)
        for (const config of [...ownedConfigs].reverse()) {
          const result = await cleanupContext.request.patch(`/api/v1${config.path}`, {
            headers: { 'X-Request-ID': `${requestPrefix}-finally-disable` }, data: config.body,
          })
          expect(result.status(), `停用本用例创建的配置 ${config.path}`).toBe(200)
        }
        await cleanupContext.request.post('/api/v1/auth/logout')
      } finally {
        await closeContext(cleanupContext)
      }
    } finally {
      // Also runs after failed login/action/cleanup. No cross-test context leak.
      const closeErrors: unknown[] = []
      for (const context of [...contexts].reverse()) {
        try { await closeContext(context) } catch (error) { closeErrors.push(error) }
      }
      try { expect(new Set(browser.contexts())).toEqual(initialContexts) } catch (error) { closeErrors.push(error) }
      if (closeErrors.length) throw new AggregateError(closeErrors, '质量治理专项浏览器上下文收尾失败')
    }
  }
})
