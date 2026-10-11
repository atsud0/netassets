import { expect, test, type APIResponse, type Page } from '@playwright/test'

const adminUsername = process.env.E2E_USERNAME ?? 'admin'
const adminPassword = process.env.E2E_PASSWORD

type Organization = {
  id: string
  parent_id?: string
  code: string
  name: string
  type: 'group' | 'subsidiary' | 'department' | 'other'
  status: 'enabled' | 'disabled'
  children?: Organization[]
}
type OrganizationImpact = {
  user_count: number
  asset_count: number
  business_system_count: number
  enabled_child_count: number
  unfinished_inventory_task_count: number
  can_disable: boolean
  can_delete: boolean
  disable_blockers: string[]
}

async function login(page: Page) {
  await page.goto('/login')
  await page.getByPlaceholder('请输入用户名').fill(adminUsername)
  await page.getByPlaceholder('请输入密码').fill(adminPassword!)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page).not.toHaveURL(/\/login(?:\?|$)/)
}

async function expectStatus(response: APIResponse, expected: number, step: string) {
  const body = await response.text()
  expect(response.status(), `${step}: ${body}`).toBe(expected)
  return body ? JSON.parse(body) : null
}

function flatten(items: Organization[]): Organization[] {
  return items.flatMap((item) => [item, ...flatten(item.children ?? [])])
}

function findNode(items: Organization[], id: string): Organization | undefined {
  for (const item of items) {
    if (item.id === id) return item
    const child = findNode(item.children ?? [], id)
    if (child) return child
  }
  return undefined
}

test('集团管理员可管理组织完整生命周期、影响检查和树形层级', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  test.setTimeout(180_000)

  await login(page)
  const request = page.context().request
  const suffix = Date.now().toString()

  const organizations = await expectStatus(
    await request.get('/api/v1/organizations/tree'), 200, '读取组织树',
  ) as { items: Organization[] }
  const group = flatten(organizations.items).find((item) => item.type === 'group')
  expect(group, '缺少集团根组织').toBeTruthy()

  const createOrganization = async (parentID: string, code: string, name: string, type: Organization['type']) => {
    const payload = await expectStatus(await request.post('/api/v1/organizations', {
      data: { parent_id: parentID, code, name, short_name: name, type, manager: 'E2E', contact: 'e2e@example.invalid', notes: '组织生命周期自动化回归' },
    }), 201, `创建组织 ${name}`) as { item: Organization }
    return payload.item
  }

  const parent = await createOrganization(group!.id, `LIFE_PARENT_${suffix}`, `生命周期父组织_${suffix}`, 'subsidiary')
  const child = await createOrganization(parent.id, `LIFE_CHILD_${suffix}`, `生命周期子组织_${suffix}`, 'department')
  const blocked = await createOrganization(group!.id, `LIFE_BLOCK_${suffix}`, `影响检查组织_${suffix}`, 'subsidiary')

  const roles = await expectStatus(await request.get('/api/v1/roles?page=1&page_size=100'), 200, '读取角色') as { items: Array<{ id: string; key: string }> }
  const editorRole = roles.items.find((item) => item.key === 'organization_editor')
  expect(editorRole, '缺少 organization_editor 内置角色').toBeTruthy()
  await expectStatus(await request.post('/api/v1/users', {
    data: {
      username: `life_${suffix}`,
      password: 'Lifecycle123!',
      display_name: `生命周期用户_${suffix}`,
      email: `life_${suffix}@example.invalid`,
      mobile_phone: '', office_phone: '', global_role_ids: [],
      appointments: [{ organization_id: blocked.id, is_primary: true, assignments: [{ role_id: editorRole!.id, data_scope: 'organization' }] }],
    },
  }), 201, '创建组织用户')

  await expectStatus(await request.post('/api/v1/business-systems', {
    data: {
      code: `LIFESYS_${suffix}`, name: `生命周期系统_${suffix}`, short_name: '', organization_id: blocked.id,
      owner_name: 'E2E', owner_contact: 'e2e@example.invalid', status: 'running', level: 'normal', deployment_type: 'other',
      internet_exposure: 'yes', description: '', contains_personal_information: 'no', contains_sensitive_personal_information: 'no',
      personal_information_record_count: null, data_volume_over_100k: 'no', contains_important_data: 'no', contains_core_data: 'no',
      data_storage_location: '', security_notes: '',
    },
  }), 201, '创建组织业务系统')

  await expectStatus(await request.post('/api/v1/assets', {
    data: {
      type: 'domain', value: `life-${suffix}.example.com`, name: `生命周期资产_${suffix}`, organization_id: blocked.id,
      business_system_id: '', environment: 'production', network_scope: 'public', network_area_id: '', status: 'active',
      importance: 'normal', owner_user_id: '', owner_name: 'E2E', notes: '', expires_at: null,
    },
  }), 201, '创建组织网络资产')

  const startAt = new Date(Date.now() + 60 * 60 * 1000)
  const deadlineAt = new Date(startAt.getTime() + 24 * 60 * 60 * 1000)
  await expectStatus(await request.post('/api/v1/inventory-tasks', {
    data: {
      name: `生命周期填报任务_${suffix}`, description: '用于验证组织停用影响检查', start_at: startAt.toISOString(), deadline_at: deadlineAt.toISOString(),
      status: 'active', asset_network_scope: 'all', required_fields: ['business_systems', 'network_assets'], quality_rules: [],
      require_review: true, allow_late_submission: false, organization_ids: [blocked.id],
    },
  }), 201, '创建未完成填报任务')

  const blockedImpact = await expectStatus(await request.get(`/api/v1/organizations/${blocked.id}/impact`), 200, '读取组织影响') as { item: OrganizationImpact }
  expect(blockedImpact.item.user_count).toBeGreaterThanOrEqual(1)
  expect(blockedImpact.item.asset_count).toBeGreaterThanOrEqual(1)
  expect(blockedImpact.item.business_system_count).toBeGreaterThanOrEqual(1)
  expect(blockedImpact.item.unfinished_inventory_task_count).toBeGreaterThanOrEqual(1)
  expect(blockedImpact.item.can_disable).toBeFalsy()
  expect(blockedImpact.item.disable_blockers).toContain('存在未完成的填报任务')
  await expectStatus(await request.post(`/api/v1/organizations/${blocked.id}/disable`, { data: { confirm_resources: true } }), 409, '阻止停用存在未完成事项的组织')

  const parentImpact = await expectStatus(await request.get(`/api/v1/organizations/${parent.id}/impact`), 200, '读取父组织影响') as { item: OrganizationImpact }
  expect(parentImpact.item.enabled_child_count).toBe(1)
  await expectStatus(await request.post(`/api/v1/organizations/${parent.id}/disable`, { data: { confirm_resources: false } }), 409, '阻止停用仍有启用子组织的父组织')

  await expectStatus(await request.post(`/api/v1/organizations/${child.id}/disable`, { data: { confirm_resources: false } }), 200, '停用空子组织')
  await expectStatus(await request.post(`/api/v1/organizations/${parent.id}/disable`, { data: { confirm_resources: false } }), 200, '停用父组织')
  await expectStatus(await request.post(`/api/v1/organizations/${parent.id}/enable`), 204, '恢复启用父组织')
  await expectStatus(await request.post(`/api/v1/organizations/${child.id}/enable`), 204, '恢复启用子组织')

  await expectStatus(await request.patch(`/api/v1/organizations/${child.id}/structure`, {
    data: { parent_id: group!.id, type: 'other' },
  }), 200, '迁移子树并调整组织类型')
  let tree = await expectStatus(await request.get('/api/v1/organizations/tree'), 200, '验证迁移后的组织树') as { items: Organization[] }
  expect(findNode(tree.items, child.id)?.parent_id).toBe(group!.id)
  expect(findNode(tree.items, child.id)?.type).toBe('other')

  await expectStatus(await request.patch(`/api/v1/organizations/${child.id}/structure`, {
    data: { parent_id: parent.id, type: 'department' },
  }), 200, '迁回原父组织')
  await expectStatus(await request.post(`/api/v1/organizations/${child.id}/disable`, { data: { confirm_resources: false } }), 200, '删除前停用空组织')
  await expectStatus(await request.delete(`/api/v1/organizations/${child.id}`), 204, '将空组织移入回收站')

  tree = await expectStatus(await request.get('/api/v1/organizations/tree'), 200, '确认回收站组织不在组织树') as { items: Organization[] }
  expect(findNode(tree.items, child.id)).toBeUndefined()
  const recycle = await expectStatus(await request.get('/api/v1/recycle-bin?resource_type=organization&page=1&page_size=100'), 200, '读取组织回收站') as { items: Array<{ id: string; resource_type: string }> }
  expect(recycle.items).toContainEqual(expect.objectContaining({ id: child.id, resource_type: 'organization' }))

  await expectStatus(await request.post(`/api/v1/recycle-bin/organizations/${child.id}/restore`), 204, '从回收站恢复组织')
  tree = await expectStatus(await request.get('/api/v1/organizations/tree'), 200, '验证恢复后的组织树') as { items: Organization[] }
  expect(findNode(tree.items, child.id)?.status).toBe('disabled')
  expect(findNode(tree.items, child.id)?.parent_id).toBe(parent.id)
  await expectStatus(await request.post(`/api/v1/organizations/${child.id}/enable`), 204, '启用已恢复组织')

  await page.goto('/organizations')
  await expect(page.getByRole('heading', { name: '组织架构', exact: true })).toBeVisible()
  const table = page.locator('.organization-tree-panel .el-table')
  const headers = (await table.locator('.el-table__header-wrapper').first().locator('th .cell').allTextContents()).map((item) => item.trim())
  expect(headers).toEqual(expect.arrayContaining(['组织', '类型', '状态', '操作']))
  expect(headers).not.toContain('上级组织')
  const parentRow = table.locator('.el-table__body-wrapper tbody tr').filter({ hasText: parent.name }).first()
  const childRow = table.locator('.el-table__body-wrapper tbody tr').filter({ hasText: child.name }).first()
  await expect(parentRow).toBeVisible()
  await expect(childRow).toBeVisible()
  await expect(childRow).toContainText('部门')
  await expect(childRow).toContainText('启用')
})
