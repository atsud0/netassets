import { expect, test, type APIResponse, type Browser, type BrowserContext } from '@playwright/test'

const adminUsername = process.env.E2E_USERNAME ?? 'admin'
const adminPassword = process.env.E2E_PASSWORD
const temporaryPassword = 'RoleFlow123!'

type Role = { id: string; key: string }
type User = { id: string; username: string; must_change_password: boolean }
type Organization = { id: string; code: string; name: string; type: string }
type Task = { id: string; name: string }

async function expectStatus(response: APIResponse, expected: number, label: string) {
  if (response.status() !== expected) {
    throw new Error(`${label}: HTTP ${response.status()}，预期 ${expected}；响应：${await response.text()}`)
  }
}

async function jsonRequest<T>(context: BrowserContext, method: string, path: string, data: unknown, expected: number, label: string): Promise<T> {
  const response = await context.request.fetch(path, {
    method,
    data,
    headers: { 'Content-Type': 'application/json' },
  })
  await expectStatus(response, expected, label)
  if (expected === 204) return undefined as T
  return await response.json() as T
}

async function loginContext(browser: Browser, baseURL: string, username: string, password: string) {
  const context = await browser.newContext({ baseURL })
  const login = await context.request.post('/api/v1/auth/login', { data: { username, password } })
  await expectStatus(login, 200, `${username} 登录`)
  let payload = await login.json() as { user: User }
  let activePassword = password
  if (payload.user.must_change_password) {
    activePassword = `${password}Changed`
    await expectStatus(await context.request.put('/api/v1/auth/profile/password', {
      data: { current_password: password, new_password: activePassword },
    }), 204, `${username} 修改临时密码`)
    const me = await context.request.get('/api/v1/auth/me')
    await expectStatus(me, 200, `${username} 读取当前用户`)
    payload = await me.json() as { user: User }
  }
  return { context, user: payload.user, password: activePassword }
}

async function findBy<T extends Record<string, unknown>>(context: BrowserContext, path: string, field: string, value: string, label: string): Promise<T> {
  const response = await context.request.get(path)
  await expectStatus(response, 200, label)
  const data = await response.json() as { items: T[] }
  const item = data.items.find((candidate) => String(candidate[field]) === value)
  if (!item) throw new Error(`${label} 未找到 ${field}=${value}`)
  return item
}

test('集团安全管理角色化全链路：下发填报、资产入库、漏洞关联新资产与安全工单', async ({ browser }, testInfo) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  test.setTimeout(360_000)

  const baseURL = String(testInfo.project.use.baseURL ?? 'http://127.0.0.1:8080')
  const suffix = Date.now().toString()
  const organizationCode = `FLOW_${suffix}`
  const organizationName = `角色流程组织_${suffix}`
  const usernames = {
    security: `flow_security_${suffix}`,
    organizationAdmin: `flow_org_admin_${suffix}`,
    editor: `flow_editor_${suffix}`,
  }
  const systemCode = `FLOW_SYS_${suffix}`
  const systemName = `角色流程业务系统_${suffix}`
  const assetValue = `roleflow-${suffix}.example.com`
  const assetName = `角色流程新资产_${suffix}`
  const taskName = `角色流程填报任务_${suffix}`
  const findingTitle = `角色流程高危漏洞_${suffix}`
  const orderTitle = `角色流程安全工单_${suffix}`
  const contexts: BrowserContext[] = []

  const groupAdmin = await loginContext(browser, baseURL, adminUsername, adminPassword!)
  contexts.push(groupAdmin.context)
  try {
    // 1. 集团管理员建立组织、角色和填报任务。
    const rolesResponse = await groupAdmin.context.request.get('/api/v1/roles?page=1&page_size=100')
    await expectStatus(rolesResponse, 200, '集团管理员读取角色')
    const roles = (await rolesResponse.json() as { items: Role[] }).items
    const role = (key: string) => {
      const item = roles.find((candidate) => candidate.key === key)
      if (!item) throw new Error(`缺少角色 ${key}`)
      return item
    }
    const groupResponse = await groupAdmin.context.request.get('/api/v1/organizations?page=1&page_size=100')
    await expectStatus(groupResponse, 200, '集团管理员读取集团组织')
    const group = (await groupResponse.json() as { items: Organization[] }).items.find((item) => item.type === 'group')
    if (!group) throw new Error('缺少集团根组织')

    const organizationResult = await jsonRequest<{ item: Organization }>(groupAdmin.context, 'POST', '/api/v1/organizations', {
      parent_id: group.id, code: organizationCode, name: organizationName, short_name: '角色流程组织', type: 'subsidiary',
      manager: '角色流程测试', contact: '13800000018', notes: '角色化全链路 E2E 数据',
    }, 201, '集团管理员创建组织')
    const organization = organizationResult.item

    const createUser = async (username: string, displayName: string, roleKey: string, global: boolean): Promise<User> => {
      const result = await jsonRequest<{ item: User }>(groupAdmin.context, 'POST', '/api/v1/users', {
        username, password: temporaryPassword, display_name: displayName, email: `${username}@example.invalid`,
        mobile_phone: '', office_phone: '', global_role_ids: global ? [role(roleKey).id] : [],
        appointments: global ? [] : [{ organization_id: organization.id, is_primary: true,
          assignments: [{ role_id: role(roleKey).id, data_scope: 'organization' }] }],
      }, 201, `集团管理员创建 ${roleKey}`)
      return result.item
    }
    await createUser(usernames.security, '集团安全管理员', 'security_operator', true)
    await createUser(usernames.organizationAdmin, '组织管理员', 'organization_admin', false)
    await createUser(usernames.editor, '组织填报员', 'organization_editor', false)

    const startAt = new Date(Date.now() - 60_000).toISOString()
    const deadlineAt = new Date(Date.now() + 7 * 24 * 60 * 60_000).toISOString()
    const taskResult = await jsonRequest<{ item: Task }>(groupAdmin.context, 'POST', '/api/v1/inventory-tasks', {
      name: taskName, description: '角色化全链路资产填报', start_at: startAt, deadline_at: deadlineAt, status: 'active',
      asset_network_scope: 'public', required_fields: ['business_systems', 'network_assets'], quality_rules: [],
      require_review: true, allow_late_submission: false, organization_ids: [organization.id],
    }, 201, '集团管理员创建并下发填报任务')
    const task = taskResult.item

    // 2. 登录四个角色，并先验证每个角色的边界。
    const security = await loginContext(browser, baseURL, usernames.security, temporaryPassword)
    const organizationAdmin = await loginContext(browser, baseURL, usernames.organizationAdmin, temporaryPassword)
    const editor = await loginContext(browser, baseURL, usernames.editor, temporaryPassword)
    contexts.push(security.context, organizationAdmin.context, editor.context)
    expect(groupAdmin.user).toBeTruthy()
    expect(security.user).toBeTruthy()
    expect(organizationAdmin.user).toBeTruthy()
    expect(editor.user).toBeTruthy()

    await expectStatus(await editor.context.request.post('/api/v1/inventory-tasks', {
      data: { name: `越权任务_${suffix}`, start_at: startAt, deadline_at: deadlineAt, status: 'active', organization_ids: [organization.id] },
    }), 403, '组织填报员不得下发任务')
    await expectStatus(await security.context.request.post('/api/v1/assets', {
      data: { type: 'ip', value: `198.51.100.${Number(suffix.slice(-2)) % 240 + 10}`, organization_id: organization.id },
    }), 403, '集团安全管理员不得修改资产台账')
    await expectStatus(await organizationAdmin.context.request.get(`/api/v1/inventory-tasks/${task.id}/organizations?organization_id=${organization.id}`), 200, '组织管理员查看本组织任务')

    // 3. 组织填报员开始填报，新增业务系统和网络资产工作版本。
    await jsonRequest(editor.context, 'POST', `/api/v1/inventory-tasks/${task.id}/organizations/${organization.id}/start`, undefined, 200, '组织填报员开始填报')
    const systemDraftResult = await jsonRequest<{ item: Record<string, unknown> }>(editor.context, 'POST', `/api/v1/inventory-tasks/${task.id}/organizations/${organization.id}/business-system-drafts`, {
      code: systemCode, name: systemName, short_name: '角色流程系统', owner_name: '组织运维', owner_contact: '13800000000', status: 'running',
      level: 'important', deployment_type: 'private_cloud', internet_exposure: 'yes', description: '角色流程新业务系统',
      contains_personal_information: 'no', contains_sensitive_personal_information: 'no', personal_information_record_count: 0,
      data_volume_over_100k: 'no', contains_important_data: 'yes', contains_core_data: 'no', data_storage_location: '组织数据中心', security_notes: 'E2E', custom_values: [],
    }, 201, '组织填报员新增业务系统草稿')
    const systemDraft = systemDraftResult.item
    const assetDraftResult = await jsonRequest<{ item: Record<string, unknown> }>(editor.context, 'POST', `/api/v1/inventory-tasks/${task.id}/organizations/${organization.id}/asset-drafts`, {
      type: 'domain', value: assetValue, name: assetName, organization_id: organization.id, business_system_id: systemDraft.id,
      environment: 'production', network_scope: 'public', status: 'active', importance: 'important', notes: '填报新增资产', custom_values: [],
    }, 201, '组织填报员新增网络资产草稿')
    const assetDraft = assetDraftResult.item

    const orgDrafts = await editor.context.request.get(`/api/v1/inventory-tasks/${task.id}/organizations/${organization.id}/asset-drafts?page=1&page_size=20`)
    await expectStatus(orgDrafts, 200, '组织填报员查看资产草稿')
    expect((await orgDrafts.json() as { items: Array<{ value: string }> }).items.map((item) => item.value)).toContain(assetValue)
    await jsonRequest(editor.context, 'POST', `/api/v1/inventory-tasks/${task.id}/organizations/${organization.id}/submit`, {
      note: '组织填报员已完成新系统和新资产填报',
    }, 200, '组织填报员提交填报结果')

    // 4. 集团管理员审核工作版本，正式发布新业务系统和新资产。
    const progressResult = await groupAdmin.context.request.get(`/api/v1/inventory-tasks/${task.id}/organizations?page=1&page_size=20`)
    await expectStatus(progressResult, 200, '集团管理员查看填报进度')
    const progress = (await progressResult.json() as { items: Array<{ organization_id: string; status: string }> }).items.find((item) => item.organization_id === organization.id)
    expect(progress?.status).toBe('submitted')
    await jsonRequest(groupAdmin.context, 'POST', `/api/v1/inventory-tasks/${task.id}/organizations/${organization.id}/review`, {
      decision: 'approved', return_reason: '', comments: [],
    }, 200, '集团管理员审核并发布工作版本')

    const formalAsset = await findBy<Record<string, unknown>>(groupAdmin.context, `/api/v1/assets?q=${encodeURIComponent(assetValue)}&page=1&page_size=20`, 'value', assetValue, '集团管理员确认新资产已入库')
    const formalSystem = await findBy<Record<string, unknown>>(groupAdmin.context, `/api/v1/business-systems?q=${encodeURIComponent(systemCode)}&page=1&page_size=20`, 'code', systemCode, '集团管理员确认业务系统已入库')
    expect(formalAsset.organization_id).toBe(organization.id)
    expect(formalSystem.organization_id).toBe(organization.id)
    expect(formalAsset.business_system_id).toBe(formalSystem.id)

    // 5. 集团安全管理员录入漏洞，并将漏洞真实关联到本轮刚入库的新资产。
    const findingResult = await jsonRequest<{ item: Record<string, unknown> }>(security.context, 'POST', '/api/v1/security-findings', {
      title: findingTitle, severity: 'high', status: 'pending', assignee_organization_id: organization.id,
      asset_id: formalAsset.id, business_system_id: formalSystem.id, vulnerability_type: '越权访问',
      description: '角色化全链路 E2E 发现的新资产高危漏洞。', evidence: 'E2E 脱敏证据', remediation_advice: '补充鉴权和访问控制。',
      source: 'manual', discovered_at: new Date().toISOString(),
    }, 201, '集团安全管理员录入并关联漏洞')
    const finding = findingResult.item
    expect(finding.asset_id).toBe(formalAsset.id)
    expect(finding.business_system_id).toBe(formalSystem.id)
    expect(finding.assignee_organization_id).toBe(organization.id)

    const organizationFindings = await organizationAdmin.context.request.get(`/api/v1/security-findings?page=1&page_size=100&q=${encodeURIComponent(findingTitle)}`)
    await expectStatus(organizationFindings, 200, '组织管理员查看本组织漏洞')
    expect((await organizationFindings.json() as { items: Array<{ id: string }> }).items.map((item) => item.id)).toContain(finding.id)

    // 6. 集团安全管理员发放安全工单，组织管理员接收，组织方提交整改，安全管理员复测关闭。
    const dueAt = new Date(Date.now() + 14 * 24 * 60 * 60_000).toISOString()
    const orderResult = await jsonRequest<{ item: Record<string, unknown> }>(security.context, 'POST', '/api/v1/security-work-orders', {
      title: orderTitle, assignee_organization_id: organization.id, description: '新资产高危漏洞整改工单。',
      remediation_requirement: '完成鉴权修复、自测并提交整改证明。', due_at: dueAt, finding_ids: [finding.id],
    }, 201, '集团安全管理员创建安全工单')
    const order = orderResult.item
    await jsonRequest(security.context, 'POST', `/api/v1/security-work-orders/${order.id}/issue`, { comment: '集团安全管理员下发整改工单' }, 200, '集团安全管理员下发安全工单')
    const visibleOrder = await organizationAdmin.context.request.get(`/api/v1/security-work-orders/${order.id}`)
    await expectStatus(visibleOrder, 200, '组织管理员查看已下发工单')
    expect((await visibleOrder.json() as { item: { status: string } }).item.status).toBe('issued')
    await jsonRequest(organizationAdmin.context, 'POST', `/api/v1/security-work-orders/${order.id}/accept`, { comment: '组织管理员已接收' }, 200, '组织管理员接收工单')
    await expectStatus(await editor.context.request.post(`/api/v1/security-work-orders/${order.id}/progress`, { data: { progress: '越权进度', comment: '组织填报员无工单执行权限' } }), 403, '组织填报员不得替代组织管理员处理工单')
    await jsonRequest(organizationAdmin.context, 'POST', `/api/v1/security-work-orders/${order.id}/progress`, { progress: '已完成代码修复和自测', comment: '组织管理员提交整改进度' }, 200, '组织管理员更新整改进度')
    await jsonRequest(organizationAdmin.context, 'POST', `/api/v1/security-work-orders/${order.id}/submit`, { remediation_result: '已完成鉴权修复，附自测结论。', comment: '组织管理员提交整改结果' }, 200, '组织管理员提交整改结果')
    const reviewed = await jsonRequest<{ item: Record<string, unknown> }>(security.context, 'POST', `/api/v1/security-work-orders/${order.id}/review`, {
      decision: 'closed', verification_result: '集团安全管理员复测通过', return_reason: '',
    }, 200, '集团安全管理员复测通过')
    expect(reviewed.item.status).toBe('closed')

    // 7. 四个角色都能看到与自身职责对应的最终结果，且通知链路存在。
    const securityFindings = await security.context.request.get(`/api/v1/security-findings?page=1&page_size=100&q=${encodeURIComponent(findingTitle)}`)
    await expectStatus(securityFindings, 200, '集团安全管理员复核漏洞')
    expect((await securityFindings.json() as { items: Array<{ id: string; asset_id: string }> }).items.find((item) => item.id === finding.id)?.asset_id).toBe(formalAsset.id)
    await expectStatus(await organizationAdmin.context.request.get('/api/v1/notifications/summary'), 200, '组织管理员读取通知汇总')

    const rolePages = await Promise.all([
      groupAdmin.context.newPage(), security.context.newPage(), organizationAdmin.context.newPage(), editor.context.newPage(),
    ])
    await Promise.all(rolePages.map((page) => page.goto('/dashboard')))
    await expect(rolePages[0].getByText('集团管理视角', { exact: true })).toBeVisible()
    await expect(rolePages[1].getByText('集团安全运营视角', { exact: true })).toBeVisible()
    await expect(rolePages[2].getByText('所属组织视角', { exact: true })).toBeVisible()
    await expect(rolePages[3].getByRole('heading', { name: '今天，先处理关键事项', exact: true })).toBeVisible()
    await Promise.all(rolePages.map((page) => page.close()))
  } finally {
    await Promise.all(contexts.map((context) => context.close()))
  }
})
