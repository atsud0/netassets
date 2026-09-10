import { expect, test, type APIResponse, type Browser, type BrowserContext } from '@playwright/test'

const adminUsername = process.env.E2E_USERNAME ?? 'admin'
const adminPassword = process.env.E2E_PASSWORD
const temporaryPassword = 'AssetOwner123!'

type RoleItem = { id: string; key: string }
type OrganizationItem = { id: string; code: string; name: string; type: string }
type CurrentUser = {
  id: string
  username: string
  display_name: string
  must_change_password: boolean
}
type AssetItem = {
  id: string
  type: string
  value: string
  name?: string
  organization_id: string
  business_system_id?: string
  environment: string
  network_scope: string
  network_area_id?: string
  status: string
  importance: string
  owner_user_id?: string
  owner_username?: string
  owner_user_display_name?: string
  owner_name?: string
  notes?: string
  expires_at?: string
}
type NotificationItem = { key: string; category: string; title: string; message: string; resource_id: string }

async function expectStatus(response: APIResponse, expected: number, label: string) {
  if (response.status() !== expected) {
    throw new Error(`${label}: HTTP ${response.status()}，预期 ${expected}；响应：${await response.text()}`)
  }
}

async function loginContext(browser: Browser, baseURL: string, username: string, password: string) {
  const context = await browser.newContext({ baseURL })
  const loginResponse = await context.request.post('/api/v1/auth/login', { data: { username, password } })
  await expectStatus(loginResponse, 200, `${username} 登录`)
  let user = (await loginResponse.json() as { user: CurrentUser }).user
  let activePassword = password
  if (user.must_change_password) {
    activePassword = `${password}Changed`
    const changeResponse = await context.request.put('/api/v1/auth/profile/password', {
      data: { current_password: password, new_password: activePassword },
    })
    await expectStatus(changeResponse, 204, `${username} 修改临时密码`)
    const meResponse = await context.request.get('/api/v1/auth/me')
    await expectStatus(meResponse, 200, `${username} 读取当前用户`)
    user = (await meResponse.json() as { user: CurrentUser }).user
  }
  return { context, user, password: activePassword }
}

function editableAsset(item: AssetItem, overrides: Partial<Record<string, unknown>> = {}) {
  return {
    type: item.type,
    value: item.value,
    name: item.name ?? '',
    organization_id: item.organization_id,
    business_system_id: item.business_system_id ?? '',
    environment: item.environment,
    network_scope: item.network_scope,
    network_area_id: item.network_area_id ?? '',
    status: item.status,
    importance: item.importance,
    owner_user_id: item.owner_user_id ?? '',
    owner_name: item.owner_name ?? '',
    notes: item.notes ?? '',
    expires_at: item.expires_at ?? null,
    ...overrides,
  }
}

async function assetMessages(context: BrowserContext, assetID: string) {
  const response = await context.request.get('/api/v1/notifications?page=1&page_size=100&category=asset_change')
  await expectStatus(response, 200, '读取资产变更消息')
  const payload = await response.json() as { items: NotificationItem[] }
  return payload.items.filter((item) => item.resource_id === assetID)
}

test('网络资产责任人绑定、候选范围和精确通知回归', async ({ browser }, testInfo) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  test.setTimeout(300_000)

  const baseURL = String(testInfo.project.use.baseURL ?? 'http://127.0.0.1:8080')
  const suffix = Date.now().toString()
  const organizationCode = `OWNER_${suffix}`
  const assetValue = `owner-${suffix}.example.com`
  const usernames = {
    actor: `owner_actor_${suffix}`,
    owner: `owner_target_${suffix}`,
    peer: `owner_peer_${suffix}`,
  }
  const contexts: BrowserContext[] = []

  const admin = await loginContext(browser, baseURL, adminUsername, adminPassword!)
  contexts.push(admin.context)
  try {
    const rolesResponse = await admin.context.request.get('/api/v1/roles?page=1&page_size=100')
    await expectStatus(rolesResponse, 200, '读取角色')
    const roles = (await rolesResponse.json() as { items: RoleItem[] }).items
    const roleByKey = new Map(roles.map((item) => [item.key, item]))
    const groupAdminRole = roleByKey.get('group_admin')
    const organizationAdminRole = roleByKey.get('organization_admin')
    const organizationEditorRole = roleByKey.get('organization_editor')
    expect(groupAdminRole, '缺少 group_admin 内置角色').toBeTruthy()
    expect(organizationAdminRole, '缺少 organization_admin 内置角色').toBeTruthy()
    expect(organizationEditorRole, '缺少 organization_editor 内置角色').toBeTruthy()

    const organizationsResponse = await admin.context.request.get('/api/v1/organizations?page=1&page_size=100')
    await expectStatus(organizationsResponse, 200, '读取组织')
    const group = (await organizationsResponse.json() as { items: OrganizationItem[] }).items.find((item) => item.type === 'group')
    expect(group, '缺少集团根组织').toBeTruthy()

    const createOrganizationResponse = await admin.context.request.post('/api/v1/organizations', {
      data: {
        parent_id: group!.id,
        code: organizationCode,
        name: `责任人通知子公司_${suffix}`,
        short_name: '责任人通知测试',
        type: 'subsidiary',
        manager: 'E2E',
        contact: 'owner-e2e@example.invalid',
        notes: '网络资产责任人精确通知自动化回归数据',
      },
    })
    await expectStatus(createOrganizationResponse, 201, '创建责任人通知测试组织')
    const organization = (await createOrganizationResponse.json() as { item: OrganizationItem }).item

    const createUser = async (username: string, displayName: string, roleID: string, globalRoleIDs: string[] = []) => {
      const response = await admin.context.request.post('/api/v1/users', {
        data: {
          username,
          password: temporaryPassword,
          display_name: displayName,
          email: `${username}@example.invalid`,
          mobile_phone: '',
          office_phone: '',
          global_role_ids: globalRoleIDs,
          appointments: [{
            organization_id: organization.id,
            is_primary: true,
            assignments: [{ role_id: roleID, data_scope: 'organization' }],
          }],
        },
      })
      await expectStatus(response, 201, `创建用户 ${username}`)
    }

    await createUser(usernames.actor, '责任人通知操作管理员', organizationAdminRole!.id, [groupAdminRole!.id])
    await createUser(usernames.owner, '网络资产责任人', organizationEditorRole!.id)
    await createUser(usernames.peer, '同组织其他填报员', organizationEditorRole!.id)

    const actor = await loginContext(browser, baseURL, usernames.actor, temporaryPassword)
    const owner = await loginContext(browser, baseURL, usernames.owner, temporaryPassword)
    const peer = await loginContext(browser, baseURL, usernames.peer, temporaryPassword)
    contexts.push(actor.context, owner.context, peer.context)

    const ownerOptionsResponse = await actor.context.request.get(`/api/v1/assets/owner-options?organization_id=${organization.id}&page=1&page_size=100`)
    await expectStatus(ownerOptionsResponse, 200, '读取责任人候选')
    const ownerOptions = (await ownerOptionsResponse.json() as { items: Array<{ id: string; username: string }> }).items
    expect(ownerOptions.map((item) => item.username)).toEqual(expect.arrayContaining([usernames.actor, usernames.owner, usernames.peer]))
    expect(ownerOptions.find((item) => item.username === usernames.owner)?.id).toBe(owner.user.id)

    const createAssetResponse = await actor.context.request.post('/api/v1/assets', {
      data: {
        type: 'domain',
        value: assetValue,
        name: `责任人精确通知资产_${suffix}`,
        organization_id: organization.id,
        business_system_id: '',
        environment: 'production',
        network_scope: 'public',
        network_area_id: '',
        status: 'active',
        importance: 'important',
        owner_user_id: owner.user.id,
        owner_name: '外部联系方式见补充说明',
        notes: 'E2E 责任人精确通知测试',
        expires_at: null,
      },
    })
    await expectStatus(createAssetResponse, 201, '创建绑定责任人的网络资产')
    let asset = (await createAssetResponse.json() as { item: AssetItem }).item
    expect(asset.owner_user_id).toBe(owner.user.id)
    expect(asset.owner_username).toBe(usernames.owner)
    expect(asset.owner_user_display_name).toBe('网络资产责任人')

    const ownerPage = await actor.context.newPage()
    await ownerPage.goto('/assets')
    await ownerPage.getByPlaceholder('搜索资产值、名称、系统或负责人').fill(assetValue)
    await ownerPage.getByRole('button', { name: '查询', exact: true }).click()
    const assetRow = ownerPage.getByRole('row').filter({ hasText: assetValue })
    await expect(assetRow).toContainText('网络资产责任人')

    const archiveResponse = await actor.context.request.patch(`/api/v1/assets/${asset.id}`, {
      data: editableAsset(asset, { status: 'archived' }),
    })
    await expectStatus(archiveResponse, 200, '归档绑定责任人的网络资产')
    asset = (await archiveResponse.json() as { item: AssetItem }).item

    await expect.poll(async () => (await assetMessages(owner.context, asset.id)).length).toBe(1)
    expect(await assetMessages(peer.context, asset.id)).toHaveLength(0)
    expect(await assetMessages(admin.context, asset.id)).toHaveLength(1)

    const reactivateResponse = await actor.context.request.patch(`/api/v1/assets/${asset.id}`, {
      data: editableAsset(asset, { status: 'active', owner_user_id: '' }),
    })
    await expectStatus(reactivateResponse, 200, '清空责任人并启用网络资产')
    asset = (await reactivateResponse.json() as { item: AssetItem }).item
    expect(asset.owner_user_id ?? '').toBe('')

    await expect.poll(async () => (await assetMessages(peer.context, asset.id)).length).toBe(1)
    expect(await assetMessages(owner.context, asset.id)).toHaveLength(2)
    expect(await assetMessages(admin.context, asset.id)).toHaveLength(2)
  } finally {
    await Promise.all(contexts.map((context) => context.close()))
  }
})
