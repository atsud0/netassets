import { expect, test, type APIResponse, type Browser, type BrowserContext } from '@playwright/test'

const groupUsername = process.env.E2E_USERNAME ?? 'admin'
const groupPassword = process.env.E2E_PASSWORD
const temporaryPassword = 'OrgHierarchy123!'

type Organization = {
  id: string
  parent_id?: string
  code: string
  name: string
  type: 'group' | 'subsidiary' | 'department' | 'other'
  status: 'enabled' | 'disabled'
  can_update: boolean
  can_create_child: boolean
  children?: Organization[]
}

type Role = { id: string; key: string }
type CurrentUser = { permissions: string[]; must_change_password: boolean }

async function expectStatus(response: APIResponse, expected: number, step: string) {
  const text = await response.text()
  expect(response.status(), `${step}: ${text}`).toBe(expected)
  return text ? JSON.parse(text) : null
}

function flatten(items: Organization[]): Organization[] {
  return items.flatMap((item) => [item, ...flatten(item.children ?? [])])
}

async function loginContext(browser: Browser, baseURL: string, username: string, password: string, changePassword = false) {
  const context = await browser.newContext({ baseURL })
  const login = await context.request.post('/api/v1/auth/login', { data: { username, password } })
  const payload = await expectStatus(login, 200, `${username} 登录`) as { user: CurrentUser }
  if (changePassword || payload.user.must_change_password) {
    await expectStatus(await context.request.put('/api/v1/auth/profile/password', {
      data: { current_password: password, new_password: `${password}Changed` },
    }), 204, `${username} 修改临时密码`)
  }
  return context
}

async function createOrganization(context: BrowserContext, parentID: string, code: string, name: string, type: Organization['type']) {
  const payload = await expectStatus(await context.request.post('/api/v1/organizations', {
    data: {
      parent_id: parentID,
      code,
      name,
      short_name: name,
      type,
      manager: 'E2E',
      contact: '13800000018',
      notes: '子公司组织管理权限回归',
    },
  }), 201, `新增组织 ${name}`) as { item: Organization }
  return payload.item
}

test('子公司管理员可维护授权组织树并新增下级组织，但不能执行集团治理操作', async ({ browser }, testInfo) => {
  test.skip(!groupPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  test.setTimeout(180_000)

  const baseURL = String(testInfo.project.use.baseURL ?? 'http://127.0.0.1:8080')
  const suffix = Date.now().toString()
  const groupContext = await loginContext(browser, baseURL, groupUsername, groupPassword!)
  const contexts: BrowserContext[] = [groupContext]

  try {
    const tree = await expectStatus(await groupContext.request.get('/api/v1/organizations/tree'), 200, '读取集团组织树') as { items: Organization[] }
    const group = flatten(tree.items).find((item) => item.type === 'group')
    expect(group, '缺少集团根组织').toBeTruthy()

    const managed = await createOrganization(groupContext, group!.id, `ORG_ADMIN_${suffix}`, `子公司管理范围_${suffix}`, 'subsidiary')
    const outside = await createOrganization(groupContext, group!.id, `ORG_OUT_${suffix}`, `范围外子公司_${suffix}`, 'subsidiary')

    const rolesPayload = await expectStatus(await groupContext.request.get('/api/v1/roles?page=1&page_size=100'), 200, '读取角色') as { items: Role[] }
    const adminRole = rolesPayload.items.find((item) => item.key === 'organization_admin')
    const editorRole = rolesPayload.items.find((item) => item.key === 'organization_editor')
    expect(adminRole, '缺少子公司管理员角色').toBeTruthy()
    expect(editorRole, '缺少子公司填报员角色').toBeTruthy()

    const createUser = async (username: string, role: Role) => {
      await expectStatus(await groupContext.request.post('/api/v1/users', {
        data: {
          username,
          password: temporaryPassword,
          display_name: username,
          email: `${username}@example.invalid`,
          mobile_phone: '',
          office_phone: '',
          global_role_ids: [],
          appointments: [{
            organization_id: managed.id,
            is_primary: true,
            assignments: [{ role_id: role.id, data_scope: 'organization' }],
          }],
        },
      }), 201, `创建账号 ${username}`)
    }

    const adminUsername = `org_tree_admin_${suffix}`
    const editorUsername = `org_tree_editor_${suffix}`
    await createUser(adminUsername, adminRole!)
    await createUser(editorUsername, editorRole!)

    const adminContext = await loginContext(browser, baseURL, adminUsername, temporaryPassword, true)
    const editorContext = await loginContext(browser, baseURL, editorUsername, temporaryPassword, true)
    contexts.push(adminContext, editorContext)

    const me = await expectStatus(await adminContext.request.get('/api/v1/auth/me'), 200, '读取子公司管理员权限') as { user: CurrentUser }
    expect(me.user.permissions).toEqual(expect.arrayContaining(['organization.read', 'organization.create', 'organization.update']))

    let scopedTree = await expectStatus(await adminContext.request.get('/api/v1/organizations/tree'), 200, '读取授权组织树') as { items: Organization[] }
    let scopedItems = flatten(scopedTree.items)
    expect(scopedItems.map((item) => item.id)).toContain(managed.id)
    expect(scopedItems.map((item) => item.id)).not.toContain(outside.id)
    expect(scopedItems.find((item) => item.id === managed.id)).toEqual(expect.objectContaining({ can_update: true, can_create_child: true }))

    const updatedName = `子公司管理范围_已更新_${suffix}`
    await expectStatus(await adminContext.request.patch(`/api/v1/organizations/${managed.id}`, {
      data: { name: updatedName, short_name: '子公司自维护', manager: '子公司管理员', contact: '13800000018', notes: '由子公司管理员维护' },
    }), 200, '子公司管理员编辑本组织基本信息')

    const child = await createOrganization(adminContext, managed.id, `ORG_OFFICE_${suffix}`, `下级办事处_${suffix}`, 'department')
    const grandchild = await createOrganization(adminContext, child.id, `ORG_TEAM_${suffix}`, `办事处下级部门_${suffix}`, 'department')
    await expectStatus(await adminContext.request.patch(`/api/v1/organizations/${child.id}`, {
      data: { name: `${child.name}_已更新`, short_name: '办事处', manager: '办事处负责人', contact: '', notes: '' },
    }), 200, '子公司管理员编辑下级组织基本信息')

    scopedTree = await expectStatus(await adminContext.request.get('/api/v1/organizations/tree'), 200, '重新读取授权组织树') as { items: Organization[] }
    scopedItems = flatten(scopedTree.items)
    expect(scopedItems.map((item) => item.id)).toEqual(expect.arrayContaining([managed.id, child.id, grandchild.id]))
    expect(scopedItems.find((item) => item.id === child.id)).toEqual(expect.objectContaining({ can_update: true, can_create_child: true }))

    await expectStatus(await adminContext.request.post('/api/v1/organizations', {
      data: { parent_id: outside.id, code: `ORG_FORBIDDEN_${suffix}`, name: '越权组织', short_name: '', type: 'department', manager: '', contact: '', notes: '' },
    }), 403, '禁止在范围外新增下级组织')
    await expectStatus(await adminContext.request.patch(`/api/v1/organizations/${outside.id}`, {
      data: { name: '越权修改', short_name: '', manager: '', contact: '', notes: '' },
    }), 403, '禁止修改范围外组织')
    await expectStatus(await adminContext.request.patch(`/api/v1/organizations/${managed.id}/structure`, {
      data: { parent_id: group!.id, type: 'department' },
    }), 403, '禁止子公司管理员调整组织层级与类型')
    await expectStatus(await adminContext.request.get(`/api/v1/organizations/${managed.id}/impact`), 403, '禁止子公司管理员管理组织生命周期')

    await expectStatus(await editorContext.request.patch(`/api/v1/organizations/${managed.id}`, {
      data: { name: '填报员越权修改', short_name: '', manager: '', contact: '', notes: '' },
    }), 403, '禁止子公司填报员编辑组织')
    await expectStatus(await editorContext.request.post('/api/v1/organizations', {
      data: { parent_id: managed.id, code: `ORG_EDITOR_${suffix}`, name: '填报员越权新增', short_name: '', type: 'department', manager: '', contact: '', notes: '' },
    }), 403, '禁止子公司填报员新增组织')

    const page = await adminContext.newPage()
    await page.goto('/organizations')
    await expect(page.getByRole('heading', { name: '组织架构', exact: true })).toBeVisible()
    await page.getByRole('textbox', { name: '搜索组织', exact: true }).fill(managed.code)
    const managedRow = page.locator('.organization-tree-panel .el-table__body-wrapper tbody tr').filter({ hasText: updatedName }).first()
    await expect(managedRow).toBeVisible()
    await managedRow.getByRole('button', { name: /^操作：/ }).click()
    const actions = page.locator('.row-actions-panel:visible')
    await expect(actions.getByRole('button', { name: '编辑信息', exact: true })).toBeVisible()
    await expect(actions.getByRole('button', { name: '新增下级组织', exact: true })).toBeVisible()
    await expect(page.getByText(outside.name, { exact: true })).toHaveCount(0)
    await actions.getByRole('button', { name: '编辑信息', exact: true }).click()
    await expect(page.getByRole('dialog', { name: '编辑子公司基本信息', exact: true })).toBeVisible()
    await expect(page.getByText('组织编码、上级组织和组织类型由集团管理员维护。', { exact: false })).toBeVisible()
  } finally {
    await Promise.all(contexts.map((context) => context.close()))
  }
})
