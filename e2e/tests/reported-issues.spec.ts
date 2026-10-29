import { findRecordPage } from '../helpers/pagination'
import { expect, test, type APIRequestContext } from '@playwright/test'

async function login(request: APIRequestContext) {
  const response = await request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME, password: process.env.E2E_PASSWORD } })
  expect(response.status(), await response.text()).toBe(200)
}
async function createOrganization(request: APIRequestContext, contact = '') {
  const roots = await request.get('/api/v1/organizations?page=1&page_size=100')
  const root = (await roots.json()).items.find((o: { type: string }) => o.type === 'group')
  return request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `REPRO_${Date.now()}_${Math.random().toString(36).slice(2, 6)}`, name: '问题回归组织', type: 'subsidiary', contact } })
}

test.beforeEach(async ({ request }) => login(request))

test('导入历史接口在导入前后均可读取，实际模板表头和示例不计入数据', async ({ request }) => {
  const org = (await (await createOrganization(request)).json()).item
  for (const path of ['/security-findings/imports?page=1&page_size=100', '/business-systems/imports?page=1&page_size=20']) {
    const response = await request.get(`/api/v1${path}`)
    expect(response.status(), `${path}: ${await response.text()}`).toBe(200)
  }
  for (const resource of ['business-systems', 'assets']) {
    const template = await request.get(`/api/v1/${resource}/import-template?format=csv&organization_id=${org.id}`)
    expect(template.status(), await template.text()).toBe(200)
    const csv = await template.text()
    // Use the exact downloaded template, including its complete sample row.
    // A shortened home-made sample is real input, not necessarily a template row.
    const sample = csv.trimEnd().split('\n')[1]
    const suffix = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
    const real = resource === 'business-systems'
      ? sample.replace('CRM_001', `IMPORT_${suffix}`).replace('客户关系管理系统', '真正的数据')
      : `domain,repro-${suffix}.example.com,真正的资产,,production,private,,normal,,,,`
    const data = { organization_id: org.id, direct_change_reason: '隔离容器问题回归', file: { name: 'template.csv', mimeType: 'text/csv', buffer: Buffer.from(`${csv.trimEnd()}\n${real}\n`) } }
    for (const endpoint of ['columns', 'preview', '']) {
      const response = await request.post(`/api/v1/${resource}/import${endpoint ? '/'+endpoint : ''}`, { multipart: data })
      expect(response.status(), await response.text()).toBe(200)
      const body = await response.json()
      if (endpoint === 'columns') {
        expect(JSON.stringify(body)).not.toContain(resource === 'assets' ? 'portal.example.com' : 'CRM_001')
        continue
      }
      expect(body.result.total_count).toBe(1)
      expect(body.result.success_count, JSON.stringify(body)).toBe(1)
    }
  }
  const history = await request.get('/api/v1/business-systems/imports?page=1&page_size=20')
  expect(history.status(), await history.text()).toBe(200)
  expect((await history.json()).items.some((i: { organization_id: string }) => i.organization_id === org.id)).toBeTruthy()
})

test('新增及编辑组织拒绝无效电话，并明确指出联系方式', async ({ request }) => {
  const invalid = await createOrganization(request, 'not-a-phone')
  expect(invalid.status(), await invalid.text()).toBe(400)
  expect((await invalid.json()).error.message).toContain('联系方式')
  const valid = await createOrganization(request, '138-0000-0000')
  expect(valid.status(), await valid.text()).toBe(201)
  const item = (await valid.json()).item
  const update = await request.patch(`/api/v1/organizations/${item.id}`, { data: { name: '问题回归组织', contact: '错误电话' } })
  expect(update.status(), await update.text()).toBe(400)
  expect((await update.json()).error.message).toContain('联系方式')
})

test('合作厂商拒绝无效电话且校验提示对应页面字段', async ({ request }) => {
  const org = (await (await createOrganization(request)).json()).item
  const invalid = await request.post('/api/v1/vendors', { data: { organization_id: org.id, name: `回归厂商${Date.now()}`, contact: 'abc', status: 'enabled' } })
  expect(invalid.status(), await invalid.text()).toBe(400)
  expect((await invalid.json()).error.message).toContain('联系方式')
  const empty = await request.post('/api/v1/vendor-catalog', { data: { name: '', status: 'enabled', reason: '' } })
  expect(empty.status()).toBe(400)
  expect((await empty.json()).error.message).toContain('厂商')
})

test('填报模式业务系统有返回任务入口', async ({ page, request }) => {
  // Share authentication with the browser context rather than using the standalone request fixture.
  const response = await page.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME, password: process.env.E2E_PASSWORD } })
  expect(response.ok()).toBeTruthy()
  const org = (await (await createOrganization(request)).json()).item
  await page.goto(`/systems?mode=inventory&task_id=00000000-0000-0000-0000-000000000001&task_name=回归任务&organization_id=${org.id}`)
  await expect(page.getByRole('button', { name: '返回填报任务', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '返回填报任务', exact: true }).click()
  await expect(page).toHaveURL(/\/inventory-tasks$/)
})


test('过期填报任务不再显示进行中', async ({ page, request }) => {
  await login(page.request)
  const org = (await (await createOrganization(request)).json()).item
  const now = Date.now()
  const name = `已过期回归任务${now}`
  const response = await request.post('/api/v1/inventory-tasks', { data: {
    name, description: '已过期状态回归', start_at: new Date(now - 86400000 * 2).toISOString(), deadline_at: new Date(now - 86400000).toISOString(),
    status: 'active', asset_network_scope: 'all', required_fields: ['network_assets'], quality_rules: [], require_review: true, allow_late_submission: false, organization_ids: [org.id],
  } })
  expect(response.status(), await response.text()).toBe(201)
  await page.goto('/inventory-tasks')
  const row = page.locator('.el-table__body-wrapper tr').filter({ hasText: name })
  await expect(row.locator('.el-tag')).toHaveText('已过期')
  await expect(row).not.toContainText('进行中')
  await row.getByRole('button', { name, exact: true }).click()
  await expect(page.getByRole('dialog', { name: '填报任务详情' })).toContainText('已过期')
})

test('全组织数据质量巡检在大数据量下可完成', async ({ page }) => {
  test.setTimeout(150_000)
  await login(page.request)
  await page.goto('/data-quality-issues')
  const [response] = await Promise.all([
    page.waitForResponse(response => response.url().includes('/api/v1/data-quality-rules/scan'), { timeout: 130_000 }),
    page.getByRole('button', { name: '执行质量巡检', exact: true }).click(),
  ])
  expect(new URL(response.url()).search).toBe('')
  expect(response.status(), await response.text()).toBe(200)
  const result = (await response.json()).result
  expect(result.summary.asset_count).toBeGreaterThan(0)
  expect(result.summary.system_count).toBeGreaterThan(0)
  expect(result.summary.enabled_rule_count).toBeGreaterThan(0)
  expect(result.summary.total_issue_count).toBeGreaterThan(0)
  expect(result.summary.error_count + result.summary.warning_count).toBe(result.summary.total_issue_count)
  expect(result.issues.length).toBe(result.summary.returned_count)
  await expect(page.getByText(`质量巡检完成，共发现 ${result.summary.total_issue_count} 项问题`)).toBeVisible()
  await expect(page).toHaveURL(/\/data-quality-issues$/)
})

test('质量巡检页面请求超过默认客户端时限时仍等待结果', async ({ page }) => {
  test.setTimeout(45_000)
  await login(page.request)
  await page.route('**/api/v1/data-quality-rules/scan**', async route => {
    await new Promise(resolve => setTimeout(resolve, 16_000))
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ result: { summary: { total_issue_count: 3 } } }) })
  })
  await page.goto('/data-quality-issues')
  const [scanRequest] = await Promise.all([
    page.waitForRequest(request => request.url().includes('/api/v1/data-quality-rules/scan')),
    page.getByRole('button', { name: '执行质量巡检', exact: true }).click(),
  ])
  expect(scanRequest.method()).toBe('GET')
  await expect(page.getByText('质量巡检完成，共发现 3 项问题')).toBeVisible({ timeout: 25_000 })
})

test('数据治理巡检页面超过默认客户端时限时仍等待真实结果', async ({ page }) => {
  test.setTimeout(90_000)
  await login(page.request)
  await page.route('**/api/v1/data-quality-rules/scan**', async route => {
    const started = Date.now()
    const response = await route.fetch({ timeout: 60_000 })
    await new Promise(resolve => setTimeout(resolve, Math.max(0, 16_000 - (Date.now() - started))))
    await route.fulfill({ response })
  })
  await page.goto('/data-governance')
  await page.getByRole('tab', { name: '数据质量规则', exact: true }).click()
  await page.getByRole('button', { name: '执行当前台账检测', exact: true }).click()
  await expect(page.getByText(/检测完成，共发现/)).toBeVisible({ timeout: 70_000 })
  await expect(page).toHaveURL(/\/data-governance$/)
})

test('质量整改原地执行巡检而非跳转数据治理', async ({ page }) => {
  test.setTimeout(60_000)
  await login(page.request)
  await page.goto('/data-quality-issues')
  // This case verifies the current-filter action, not a whole-database load test.
  // Repeated fixtures deliberately remain in the isolated database for diagnosis.
  await page.locator('.quality-workflow-filters .el-select').nth(3).click()
  await page.locator('.el-select-dropdown:visible').getByRole('option', { name: '业务系统', exact: true }).click()
  await page.locator('.quality-workflow-filters .el-select').nth(2).click()
  await page.locator('.el-select-dropdown:visible').getByRole('option', { name: '错误', exact: true }).click()
  const [scanned] = await Promise.all([
    page.waitForResponse(response => response.url().includes('/api/v1/data-quality-rules/scan') && response.request().method() === 'GET'),
    page.getByRole('button', { name: '执行质量巡检', exact: true }).click(),
  ])
  expect(scanned.status(), await scanned.text()).toBe(200)
  const params = new URL(scanned.url()).searchParams
  expect(params.get('resource_type')).toBe('business_system')
  expect(params.get('severity')).toBe('error')
  await expect(page).toHaveURL(/\/data-quality-issues$/)
  await expect(page.getByText(/质量巡检完成，共发现/)).toBeVisible()
})

test('人员厂商表单提供必填与电话校验，避免发送无效请求', async ({ page }) => {
  await login(page.request)
  await page.goto('/remediation-people')
  await page.getByRole('button', { name: '添加人员 / 邀请', exact: true }).first().click()
  const person = page.getByRole('dialog', { name: '添加修复人员' })
  await person.getByRole('button', { name: '创建账号与授权', exact: true }).click()
  await expect(person.getByText('请选择授权业务系统', { exact: true })).toBeVisible()
  await expect(person.locator('.el-form-item.is-required')).toHaveCount(5)
  await person.getByRole('button', { name: '关闭', exact: true }).click()
  await page.getByRole('tab', { name: '合作厂商', exact: true }).click()
  await page.getByRole('button', { name: '登记厂商', exact: true }).click()
  const vendor = page.getByRole('dialog', { name: '合作厂商', exact: true })
  await vendor.getByRole('button', { name: '保存', exact: true }).click()
  await expect(vendor.getByText('请选择合作组织', { exact: true })).toBeVisible()
  await expect(vendor.getByText('请输入厂商名称', { exact: true })).toBeVisible()
  await vendor.getByLabel('联系方式', { exact: true }).fill('abc')
  await vendor.getByRole('button', { name: '保存', exact: true }).click()
  await expect(vendor.getByText('请填写手机号码、座机号码或分机号', { exact: true })).toBeVisible()
  await expect(vendor.getByLabel('厂商名称', { exact: true })).toHaveAttribute('maxlength', '255')
  await expect(vendor.getByLabel('联系方式', { exact: true })).toHaveAttribute('maxlength', '64')
})

test('数据治理字段及规则名称按字符而非 UTF-8 字节限制', async ({ request }) => {
  for (const resource_type of ['asset', 'business_system']) {
    const suffix = `${Date.now()}_${Math.random().toString(36).slice(2, 5)}`
    const response = await request.post('/api/v1/custom-fields', { data: {
      resource_type, field_key: `repro_${suffix}`, field_name: '名'.repeat(128), field_type: 'text', options: [], is_required: false, is_enabled: false, sort_order: 0,
    } })
    expect(response.status(), await response.text()).toBe(201)
  }
  const target = `custom:repro_${Date.now()}_${Math.random().toString(36).slice(2, 5)}`
  const field = await request.post('/api/v1/custom-fields', { data: { resource_type: 'asset', field_key: target.slice('custom:'.length), field_name: '规则验证字段', field_type: 'text', options: [], is_required: false, is_enabled: false, sort_order: 0 } })
  expect(field.status(), await field.text()).toBe(201)
  const response = await request.post('/api/v1/data-quality-rules', { data: {
    rule_name: '规'.repeat(128), resource_type: 'asset', rule_type: 'required', target_field: target, severity: 'warning', is_enabled: false, sort_order: 0,
  } })
  expect(response.status(), await response.text()).toBe(201)
  const tooLong = await request.post('/api/v1/data-quality-rules', { data: {
    rule_name: '规'.repeat(129), resource_type: 'asset', rule_type: 'required', target_field: target, severity: 'warning', is_enabled: false, sort_order: 0,
  } })
  expect(tooLong.status()).toBe(400)
})

test('20 个业务系统批量归档和 20 个资产长中文原因批量设置状态', async ({ request }) => {
  test.setTimeout(120_000)
  const org = (await (await createOrganization(request)).json()).item
  const suffix = Date.now()
  const systems: { id: string; expected_version: number }[] = []
  const assets: string[] = []
  for (let index = 0; index < 20; index++) {
    const system = await request.post('/api/v1/business-systems', { data: {
      code: `BULK_${suffix}_${index}`, name: `批量回归系统${index}`, organization_id: org.id, status: 'running', level: 'normal', deployment_type: 'private_cloud', internet_exposure: 'unknown',
      contains_personal_information: 'unknown', contains_sensitive_personal_information: 'unknown', data_volume_over_100k: 'unknown', contains_important_data: 'unknown', contains_core_data: 'unknown',
    } })
    expect(system.status(), await system.text()).toBe(201)
    const item = (await system.json()).item
    systems.push({ id: item.id, expected_version: item.version })
    const asset = await request.post('/api/v1/assets', { data: {
      type: 'domain', value: `bulk-${suffix}-${index}.example.com`, name: `批量回归资产${index}`, organization_id: org.id, environment: 'production', network_scope: 'private', status: 'active', importance: 'normal', direct_change_reason: '隔离回归创建资产',
    } })
    expect(asset.status(), await asset.text()).toBe(201)
    assets.push((await asset.json()).item.id)
  }
  const archive = await request.post('/api/v1/business-systems/batch/archive', { data: { items: systems, archived: true } })
  expect(archive.status(), await archive.text()).toBe(200)
  expect((await archive.json()).result.success_count).toBe(20)
  const update = await request.patch('/api/v1/assets/batch', { data: { ids: assets, status: 'disabled', direct_change_reason: '中文原因'.repeat(250) } })
  expect(update.status(), await update.text()).toBe(200)
  const result = (await update.json()).result
  expect(result.success_count, JSON.stringify(result)).toBe(20)
  const persisted = await request.get(`/api/v1/assets?organization_id=${org.id}&page=1&page_size=100`)
  expect(persisted.status()).toBe(200)
  const saved = (await persisted.json()).items
  expect(saved).toHaveLength(20)
  expect(saved.every((item: { status: string }) => item.status === 'disabled')).toBeTruthy()
})

test('安全发现和整改工单的责任组织下拉含启用子组织', async ({ page, request }) => {
  await login(page.request)
  const org = (await (await createOrganization(request)).json()).item
  const uniqueName = `责任组织回归-${org.id}`
  const renamed = await request.patch(`/api/v1/organizations/${org.id}`, { data: { ...org, name: uniqueName } })
  expect(renamed.status(), await renamed.text()).toBe(200)
  const reference = await findRecordPage<{ id: string; status: string }>(request, '/api/v1/security-reference/organizations', item => item.id === org.id, 100)
  expect(reference?.item.status).toBe('enabled')
  await page.goto('/security-work-orders')
  for (const [button, title] of [['登记安全发现', '登记安全发现'], ['新建整改工单', '新建安全整改工单']]) {
    await page.getByRole('button', { name: button, exact: true }).click()
    const dialog = page.getByRole('dialog', { name: title, exact: true })
    const organization = dialog.locator('.el-form-item').filter({ has: page.locator('.el-form-item__label', { hasText: /^责任组织$/ }) })
    await organization.locator('.el-select').click()
    await organization.getByRole('combobox').fill(uniqueName)
    const option = page.getByRole('option', { name: uniqueName, exact: true })
    await expect(option).toBeVisible()
    await option.click()
    await expect(organization).toContainText(uniqueName)
    await dialog.getByRole('button', { name: '取消', exact: true }).click()
  }
})

test('子系统管理员能从任务选择入口打开详情并往返业务系统', async ({ page, request }) => {
  const org = (await (await createOrganization(request)).json()).item
  const now = Date.now()
  const username = `repro_admin_${now}`
  const password = 'ReproTask123!'
  const roles = (await (await request.get('/api/v1/roles?page=1&page_size=100')).json()).items
  const role = roles.find((item: { key: string }) => item.key === 'organization_admin')
  const user = await request.post('/api/v1/users', { data: {
    username, password, display_name: '回归子系统管理员', global_role_ids: [], appointments: [{ organization_id: org.id, is_primary: true, assignments: [{ role_id: role.id, data_scope: 'organization' }] }],
  } })
  expect(user.status(), await user.text()).toBe(201)
  const name = `管理员回归任务${now}`
  const created = await request.post('/api/v1/inventory-tasks', { data: {
    name, start_at: new Date(now - 3600000).toISOString(), deadline_at: new Date(now + 86400000).toISOString(), status: 'active', asset_network_scope: 'all', required_fields: ['business_systems'], quality_rules: [], organization_ids: [org.id],
  } })
  expect(created.status(), await created.text()).toBe(201)
  const response = await page.request.post('/api/v1/auth/login', { data: { username, password } })
  expect(response.status()).toBe(200)
  if ((await response.json()).user.must_change_password) {
    const changed = await page.request.put('/api/v1/auth/profile/password', { data: { current_password: password, new_password: `${password}Changed` } })
    expect(changed.status(), await changed.text()).toBe(204)
  }
  const task = (await created.json()).item
  const started = await page.request.post(`/api/v1/inventory-tasks/${task.id}/organizations/${org.id}/start`)
  expect(started.status(), await started.text()).toBe(200)
  await page.goto('/inventory-tasks')
  await page.getByRole('button', { name: '从具体任务进入上报内容', exact: true }).click()
  await page.getByRole('menuitem', { name, exact: true }).click()
  const detail = page.getByRole('dialog', { name: '填报任务详情', exact: true })
  await expect(detail).toBeVisible()
  await detail.locator('.task-organization-table').getByRole('button', { name: /^操作：/ }).click()
  const checked = page.waitForResponse(response => response.url().includes(`/inventory-tasks/${task.id}/organizations/${org.id}/quality-check`))
  await page.locator('.row-actions-popover:visible').getByRole('button', { name: '提交', exact: true }).click()
  expect((await checked).status()).toBe(200)
  const submit = page.getByRole('dialog', { name: '提交填报结果', exact: true })
  await expect(submit.getByText('尚未录入业务系统', { exact: true })).toBeVisible()
  await expect(page.getByText('执行填报质量检查失败', { exact: true })).toHaveCount(0)
  await submit.getByRole('button', { name: '取消', exact: true }).click()
  await detail.locator('.task-organization-table').getByRole('button', { name: /^操作：/ }).click()
  await page.locator('.row-actions-popover:visible').getByRole('button', { name: '维护业务系统', exact: true }).click()
  await expect(page).toHaveURL(/\/systems\?.*mode=inventory/)
  await page.getByRole('button', { name: '返回填报任务', exact: true }).click()
  await expect(page).toHaveURL(/\/inventory-tasks$/)
})

test('业务系统新增与编辑都拒绝无效联系电话', async ({ request }) => {
  const org = (await (await createOrganization(request)).json()).item
  const input = { code: `PHONE_${Date.now()}`, name: '电话校验回归系统', organization_id: org.id, status: 'running', level: 'normal', internet_exposure: 'no', contains_personal_information: 'unknown', contains_sensitive_personal_information: 'unknown', data_volume_over_100k: 'unknown', contains_important_data: 'unknown', contains_core_data: 'unknown', owner_name: '责任人', owner_contact: 'invalid-phone', custom_values: [] }
  const invalid = await request.post('/api/v1/business-systems', { data: input })
  expect(invalid.status(), await invalid.text()).toBe(400)
  input.owner_contact = '138-0000-0000'
  const created = await request.post('/api/v1/business-systems', { data: input })
  expect(created.status(), await created.text()).toBe(201)
  const item = (await created.json()).item
  expect(item.owner_contact).toBe('13800000000')
  const updated = await request.patch(`/api/v1/business-systems/${item.id}`, { data: { ...input, owner_contact: '错误电话', expected_version: item.version } })
  expect(updated.status(), await updated.text()).toBe(400)
})

test('业务系统与资产的真实操作历史显示上海时区的完整时间', async ({ browser, request }) => {
  const org = (await (await createOrganization(request)).json()).item
  const stamp = Date.now()
  const system = await request.post('/api/v1/business-systems', { data: { code: `TIME_${stamp}`, name: `历史时间系统${stamp}`, organization_id: org.id, status: 'running', level: 'normal', internet_exposure: 'no', contains_personal_information: 'unknown', contains_sensitive_personal_information: 'unknown', data_volume_over_100k: 'unknown', contains_important_data: 'unknown', contains_core_data: 'unknown', custom_values: [] } })
  expect(system.status(), await system.text()).toBe(201)
  const asset = await request.post('/api/v1/assets', { data: { type: 'domain', value: `time-${stamp}.example.com`, name: `历史时间资产${stamp}`, organization_id: org.id, environment: 'production', network_scope: 'private', status: 'active', importance: 'normal', direct_change_reason: '历史时间回归' } })
  expect(asset.status(), await asset.text()).toBe(201)
  const context = await browser.newContext({ timezoneId: 'Asia/Shanghai' })
  try {
    await login(context.request)
    const page = await context.newPage()
    for (const [resource, route, item] of [['business-systems', '/systems', (await system.json()).item], ['assets', '/assets', (await asset.json()).item]] as const) {
      await page.goto(route)
      // Isolated E2E data is intentionally retained between runs; search avoids
      // relying on list sort order or the current page when old rows accumulate.
      const search = page.getByPlaceholder(resource === 'business-systems' ? '搜索系统名称、组织或负责人，也可输入编码' : '搜索资产值、名称、系统或负责人')
      await search.fill(item.name)
      await search.press('Enter')
      const row = page.locator('.el-table__body-wrapper tr').filter({ hasText: item.name }).first()
      await expect(row).toBeVisible()
      await row.getByRole('button', { name: /^操作：/ }).click()
      const [response] = await Promise.all([
        page.waitForResponse(response => response.url().includes(`/${resource}/${item.id}/change-history`)),
        page.locator('.row-actions-popover:visible').getByRole('button', { name: resource === 'business-systems' ? '历史' : '变更历史', exact: true }).click(),
      ])
      expect(response.status()).toBe(200)
      const history = (await response.json()).items
      expect(history.length).toBeGreaterThan(0)
      // Compare the UI with an explicitly fixed zone, not the container's default UTC zone.
      const expected = new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(new Date(history[0].created_at))
      await expect(page.locator('.el-timeline-item__timestamp').first()).toHaveText(expected)
    }
  } finally { await context.close() }
})

test('新增质量规则提示与字段一致并可保存128字中文名称', async ({ page, request }) => {
  test.setTimeout(60_000)
  // Rule uniqueness is defined by resource/type/target, not by its display name.
  // A unique optional field keeps retained E2E data from conflicting with this case.
  const key = `rule_name_${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
  const fieldName = `规则名称边界${key}`
  const field = await request.post('/api/v1/custom-fields', { data: { resource_type: 'asset', field_key: key, field_name: fieldName, field_type: 'text', options: [], is_required: false, is_enabled: true, sort_order: 0 } })
  expect(field.status(), await field.text()).toBe(201)
  await login(page.request)
  await page.goto('/data-governance')
  await page.getByRole('tab', { name: '数据质量规则', exact: true }).click()
  await page.getByRole('button', { name: '新增质量规则', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '新增数据质量规则', exact: true })
  const nameInput = dialog.getByPlaceholder('请输入规则名称，最多 128 字', { exact: true })
  await expect(nameInput).toHaveAttribute('maxlength', '128')
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(dialog.getByText('请输入规则名称', { exact: true })).toBeVisible()
  await expect(dialog).toBeVisible()
  const name = '规'.repeat(128)
  await nameInput.fill(name + '多')
  await expect(nameInput).toHaveValue(name)
  await dialog.locator('.el-form-item').filter({ hasText: '目标字段' }).getByRole('combobox').click()
  await page.getByRole('option', { name: `自定义字段：${fieldName}`, exact: true }).click()
  // Leave the fixture disabled so later whole-organization scans are unaffected.
  await dialog.locator('.el-switch').click()
  await expect(dialog.getByRole('switch')).not.toBeChecked()
  const [created] = await Promise.all([
    page.waitForResponse(r => r.url().endsWith('/api/v1/data-quality-rules') && r.request().method() === 'POST'),
    dialog.getByRole('button', { name: '保存', exact: true }).click(),
  ])
  expect(created.status(), await created.text()).toBe(201)
  const item = (await created.json()).item
  expect(item.rule_name).toBe(name)
  expect(item.target_field).toBe(`custom:${key}`)
  expect(item.is_enabled).toBe(false)
  await expect(dialog).toBeHidden()
  const tooLong = await request.post('/api/v1/data-quality-rules', { data: { ...item, rule_name: name + '多' } })
  expect(tooLong.status(), await tooLong.text()).toBe(400)
})
