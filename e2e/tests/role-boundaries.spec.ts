import { expect, test, type APIResponse, type Browser, type BrowserContext, type Page } from '@playwright/test'

const adminUsername = process.env.E2E_USERNAME ?? 'admin'
const adminPassword = process.env.E2E_PASSWORD
const temporaryPassword = 'RoleBoundary123!'

type RoleItem = { id: string; key: string; name: string; scope: string }
type OrganizationItem = { id: string; code: string; name: string; type: string }
type CurrentUser = {
  id: string
  username: string
  roles: string[]
  permissions: string[]
  organization?: OrganizationItem
  organizations: OrganizationItem[]
  must_change_password: boolean
}

async function expectStatus(response: APIResponse, expected: number, label: string) {
  if (response.status() !== expected) {
    const body = await response.text()
    throw new Error(`${label}: HTTP ${response.status()}，预期 ${expected}；响应：${body}`)
  }
}

async function loginContext(browser: Browser, baseURL: string, username: string, password: string, changeTemporaryPassword = false) {
  const context = await browser.newContext({ baseURL })
  const loginResponse = await context.request.post('/api/v1/auth/login', { data: { username, password } })
  await expectStatus(loginResponse, 200, `${username} 登录`)
  let payload = await loginResponse.json() as { user: CurrentUser }
  let activePassword = password

  if (changeTemporaryPassword || payload.user.must_change_password) {
    activePassword = `${password}Changed`
    const changeResponse = await context.request.put('/api/v1/auth/profile/password', {
      data: { current_password: password, new_password: activePassword },
    })
    await expectStatus(changeResponse, 204, `${username} 修改临时密码`)
    const meResponse = await context.request.get('/api/v1/auth/me')
    await expectStatus(meResponse, 200, `${username} 修改密码后读取当前用户`)
    payload = await meResponse.json() as { user: CurrentUser }
  }

  return { context, user: payload.user, password: activePassword }
}

async function assertEndpointMatrix(context: BrowserContext, matrix: Record<string, number>, roleLabel: string) {
  for (const [path, status] of Object.entries(matrix)) {
    const response = await context.request.get(path)
    await expectStatus(response, status, `${roleLabel} GET ${path}`)
  }
}

async function assertNavigation(page: Page, expected: string[], forbidden: string[]) {
  const navigation = page.getByRole('navigation', { name: '主导航' })
  for (const label of expected) {
    await expect(navigation.getByRole('button', { name: label, exact: true }), `应显示导航：${label}`).toBeVisible()
  }
  for (const label of forbidden) {
    await expect(navigation.getByRole('button', { name: label, exact: true }), `不应显示导航：${label}`).toHaveCount(0)
  }
}

test('集团管理员、安全运营、子公司管理员和填报员权限边界回归', async ({ browser }, testInfo) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  test.setTimeout(300_000)

  const baseURL = String(testInfo.project.use.baseURL ?? 'http://127.0.0.1:8080')
  const suffix = Date.now().toString()
  const organizationCode = `ROLE_PW_${suffix}`
  const organizationName = `角色回归子公司_${suffix}`
  const usernames = {
    security: `role_pw_security_${suffix}`,
    admin: `role_pw_admin_${suffix}`,
    editor: `role_pw_editor_${suffix}`,
  }

  const adminSession = await loginContext(browser, baseURL, adminUsername, adminPassword!)
  const createdContexts: BrowserContext[] = [adminSession.context]
  try {
    expect(adminSession.user.roles).toContain('group_admin')

    const rolesResponse = await adminSession.context.request.get('/api/v1/roles?page=1&page_size=100')
    await expectStatus(rolesResponse, 200, '集团管理员读取角色')
    const roles = (await rolesResponse.json() as { items: RoleItem[] }).items
    const roleByKey = new Map(roles.map((role) => [role.key, role]))
    for (const key of ['group_admin', 'security_operator', 'organization_admin', 'organization_editor']) {
      expect(roleByKey.get(key), `缺少内置角色 ${key}，请确认迁移 00031 已执行`).toBeTruthy()
    }
    expect(roleByKey.get('security_operator')?.scope).toBe('group')

    const organizationsResponse = await adminSession.context.request.get('/api/v1/organizations?page=1&page_size=100')
    await expectStatus(organizationsResponse, 200, '集团管理员读取组织')
    const group = (await organizationsResponse.json() as { items: OrganizationItem[] }).items.find((item) => item.type === 'group')
    expect(group, '缺少集团根组织').toBeTruthy()

    const organizationResponse = await adminSession.context.request.post('/api/v1/organizations', {
      data: {
        parent_id: group!.id,
        code: organizationCode,
        name: organizationName,
        short_name: '角色回归子公司',
        type: 'subsidiary',
        manager: 'E2E',
        contact: 'e2e@example.invalid',
        notes: '全角色权限边界自动化回归数据',
      },
    })
    await expectStatus(organizationResponse, 201, '创建角色回归组织')
    const organization = (await organizationResponse.json() as { item: OrganizationItem }).item

    const createUser = async (username: string, displayName: string, roleKey: string, global: boolean) => {
      const role = roleByKey.get(roleKey)!
      const response = await adminSession.context.request.post('/api/v1/users', {
        data: {
          username,
          password: temporaryPassword,
          display_name: displayName,
          email: `${username}@example.invalid`,
          mobile_phone: '',
          office_phone: '',
          global_role_ids: global ? [role.id] : [],
          appointments: global ? [] : [{
            organization_id: organization.id,
            is_primary: true,
            assignments: [{ role_id: role.id, data_scope: 'organization' }],
          }],
        },
      })
      await expectStatus(response, 201, `创建 ${roleKey} 测试账号`)
    }

    await createUser(usernames.security, '角色回归安全运营', 'security_operator', true)
    await createUser(usernames.admin, '角色回归子公司管理员', 'organization_admin', false)
    await createUser(usernames.editor, '角色回归子公司填报员', 'organization_editor', false)

    const securitySession = await loginContext(browser, baseURL, usernames.security, temporaryPassword, true)
    const organizationAdminSession = await loginContext(browser, baseURL, usernames.admin, temporaryPassword, true)
    const editorSession = await loginContext(browser, baseURL, usernames.editor, temporaryPassword, true)
    createdContexts.push(securitySession.context, organizationAdminSession.context, editorSession.context)

    expect(securitySession.user.roles).toEqual(['security_operator'])
    expect(securitySession.user.organization).toBeUndefined()
    expect(securitySession.user.organizations).toEqual([])
    expect(securitySession.user.permissions.sort()).toEqual([
      'asset_candidate.follow_up', 'asset_candidate.view',
      'security_finding.manage', 'security_test.manage',
      'security_workorder.assign', 'security_workorder.close', 'security_workorder.execute',
      'security_workorder.manage', 'security_workorder.review', 'security_workorder.view',
    ])
    expect(organizationAdminSession.user.roles).toContain('organization_admin')
    expect(organizationAdminSession.user.organization?.id).toBe(organization.id)
    expect(editorSession.user.roles).toContain('organization_editor')
    expect(editorSession.user.organization?.id).toBe(organization.id)

    await assertEndpointMatrix(adminSession.context, {
      '/api/v1/users?page=1&page_size=20': 200,
      '/api/v1/roles?page=1&page_size=20': 200,
      '/api/v1/assets?page=1&page_size=20': 200,
      '/api/v1/business-systems?page=1&page_size=20': 200,
      '/api/v1/security-reference/organizations?page=1&page_size=20': 200,
      '/api/v1/security-test-batches?page=1&page_size=20': 200,
      '/api/v1/security-findings?page=1&page_size=20': 200,
      '/api/v1/security-findings/import-candidates?page=1&page_size=20': 200,
      '/api/v1/asset-candidates?page=1&page_size=20': 200,
      '/api/v1/security-work-orders?page=1&page_size=20': 200,
      '/api/v1/audit-logs?page=1&page_size=20': 200,
      '/api/v1/backups?page=1&page_size=20': 200,
      '/api/v1/recycle-bin?page=1&page_size=20': 200,
    }, '集团管理员')

    await assertEndpointMatrix(securitySession.context, {
      '/api/v1/security-reference/organizations?page=1&page_size=20': 200,
      '/api/v1/security-reference/assets?page=1&page_size=20': 200,
      '/api/v1/security-reference/business-systems?page=1&page_size=20': 200,
      '/api/v1/security-test-batches?page=1&page_size=20': 200,
      '/api/v1/security-findings?page=1&page_size=20': 200,
      '/api/v1/security-findings/import-candidates?page=1&page_size=20': 200,
      '/api/v1/security-work-orders?page=1&page_size=20': 200,
      '/api/v1/notifications/summary': 200,
      '/api/v1/users?page=1&page_size=20': 403,
      '/api/v1/roles?page=1&page_size=20': 403,
      '/api/v1/assets?page=1&page_size=20': 403,
      '/api/v1/business-systems?page=1&page_size=20': 403,
      '/api/v1/audit-logs?page=1&page_size=20': 403,
      '/api/v1/backups?page=1&page_size=20': 403,
      '/api/v1/recycle-bin?page=1&page_size=20': 403,
    }, '集团安全运营管理员')

    await assertEndpointMatrix(organizationAdminSession.context, {
      '/api/v1/users?page=1&page_size=100': 200,
      '/api/v1/roles/assignable?page=1&page_size=100': 200,
      '/api/v1/assets?page=1&page_size=20': 200,
      '/api/v1/business-systems?page=1&page_size=20': 200,
      '/api/v1/security-findings?page=1&page_size=20': 200,
      '/api/v1/security-findings/import-candidates?page=1&page_size=20': 403,
      '/api/v1/asset-candidates?page=1&page_size=20': 200,
      '/api/v1/security-work-orders?page=1&page_size=20': 200,
      '/api/v1/security-reference/organizations?page=1&page_size=20': 403,
      '/api/v1/security-test-batches?page=1&page_size=20': 403,
      '/api/v1/roles?page=1&page_size=20': 403,
      '/api/v1/audit-logs?page=1&page_size=20': 403,
      '/api/v1/backups?page=1&page_size=20': 403,
      '/api/v1/recycle-bin?page=1&page_size=20': 403,
    }, '子公司管理员')

    const scopedUsersResponse = await organizationAdminSession.context.request.get('/api/v1/users?page=1&page_size=100')
    const scopedUsers = (await scopedUsersResponse.json() as { items: Array<{ username: string }> }).items
    expect(scopedUsers.map((item) => item.username)).toEqual(expect.arrayContaining([usernames.admin, usernames.editor]))
    expect(scopedUsers.map((item) => item.username)).not.toContain(usernames.security)
    expect(scopedUsers.map((item) => item.username)).not.toContain(adminUsername)

    await assertEndpointMatrix(editorSession.context, {
      '/api/v1/assets?page=1&page_size=20': 200,
      '/api/v1/business-systems?page=1&page_size=20': 200,
      '/api/v1/inventory-tasks?page=1&page_size=20': 200,
      '/api/v1/security-findings?page=1&page_size=20': 200,
      '/api/v1/security-findings/import-candidates?page=1&page_size=20': 403,
      '/api/v1/asset-candidates?page=1&page_size=20': 200,
      '/api/v1/security-work-orders?page=1&page_size=20': 200,
      '/api/v1/users?page=1&page_size=20': 403,
      '/api/v1/roles/assignable?page=1&page_size=20': 403,
      '/api/v1/security-reference/organizations?page=1&page_size=20': 403,
      '/api/v1/security-test-batches?page=1&page_size=20': 403,
      '/api/v1/audit-logs?page=1&page_size=20': 403,
      '/api/v1/backups?page=1&page_size=20': 403,
      '/api/v1/recycle-bin?page=1&page_size=20': 403,
    }, '子公司填报员')

    const adminPage = await adminSession.context.newPage()
    await adminPage.goto('/dashboard')
    await expect(adminPage.getByRole('heading', { name: '今天，先处理关键事项', exact: true })).toBeVisible()
    await assertNavigation(adminPage,
      ['资产治理', '安全运营', '系统设置', '安全测试', '安全工单', '用户和权限', '审计日志', '备份管理'], [])

    const securityPage = await securitySession.context.newPage()
    await securityPage.goto('/dashboard')
    await expect(securityPage.getByText('集团安全运营视角', { exact: true })).toBeVisible()
    await expect(securityPage.getByRole('heading', { name: '今天，先处理关键事项', exact: true })).toBeVisible()
    await assertNavigation(securityPage,
      ['安全运营', '安全测试', '安全工单'],
      ['资产治理', '系统设置', '业务系统', '网络资产', '用户和权限', '用户管理', '审计日志', '备份管理'])
    await securityPage.goto('/users')
    await expect(securityPage).toHaveURL(/\/dashboard$/)

    const organizationAdminPage = await organizationAdminSession.context.newPage()
    await organizationAdminPage.goto('/dashboard')
    await expect(organizationAdminPage.getByText('所属组织视角', { exact: true })).toBeVisible()
    await expect(organizationAdminPage.getByRole('heading', { name: '今天，先处理关键事项', exact: true })).toBeVisible()
    await assertNavigation(organizationAdminPage,
      ['资产治理', '安全运营', '系统设置', '业务系统', '网络资产', '填报任务', '质量整改', '安全工单', '组织架构', '用户管理'],
      ['安全测试', '用户和权限', '审计日志', '备份管理'])
    await organizationAdminPage.goto('/security-test-batches')
    await expect(organizationAdminPage).toHaveURL(/\/dashboard$/)

    const editorPage = await editorSession.context.newPage()
    await editorPage.goto('/dashboard')
    await expect(editorPage.getByRole('heading', { name: '今天，先处理关键事项', exact: true })).toBeVisible()
    await assertNavigation(editorPage,
      ['资产治理', '安全运营', '系统设置', '业务系统', '网络资产', '填报任务', '安全工单', '组织架构'],
      ['安全测试', '质量整改', '用户和权限', '用户管理', '审计日志', '备份管理'])
    await editorPage.goto('/users')
    await expect(editorPage).toHaveURL(/\/dashboard$/)
  } finally {
    await Promise.all(createdContexts.map((context) => context.close()))
  }
})
