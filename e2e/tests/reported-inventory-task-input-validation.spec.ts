import { expect, test, type APIResponse, type BrowserContext } from '@playwright/test'
import { writeFile } from 'node:fs/promises'
import { findRecordPage } from '../helpers/pagination'

async function body(response: APIResponse, status = 200) {
  const text = await response.text()
  expect(response.status(), text).toBe(status)
  return text ? JSON.parse(text) : null
}

test('填报任务字段错误精确、越权先拒绝、空白不请求、中英文满额草稿完整保存且拒绝无副作用', async ({ browser, baseURL }, info) => {
  test.setTimeout(300_000)
  const contexts: BrowserContext[] = []
  const evidence: Record<string, unknown> = { boundaries: [], rejections: [] }
  const pageErrors: string[] = []
  try {
    const admin = await browser.newContext({ baseURL }); contexts.push(admin)
    await body(await admin.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }))
    const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
    const root = (await body(await admin.request.get('/api/v1/organizations/tree'))).items.find((item: { type: string }) => item.type === 'group')
    const org = (await body(await admin.request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `TASK_INPUT_${stamp}`, name: `填报字段回归_${stamp}`, type: 'subsidiary' } }), 201)).item
    const roles = (await body(await admin.request.get('/api/v1/roles?page_size=100'))).items
    const deniedActors: BrowserContext[] = []
    for (const key of ['organization_admin', 'security_operator']) {
      const role = roles.find((item: { key: string }) => item.key === key)
      expect(role).toBeTruthy()
      const username = `task_${key === 'organization_admin' ? 'child' : 'op'}_${stamp}`
      const password = 'TaskInput123!'
      const global = key === 'security_operator'
      await body(await admin.request.post('/api/v1/users', { data: { username, password, display_name: `填报字段_${key}`, global_role_ids: global ? [role.id] : [], appointments: global ? [] : [{ organization_id: org.id, is_primary: true, assignments: [{ role_id: role.id, data_scope: 'organization' }] }] } }), 201)
      const context = await browser.newContext({ baseURL }); contexts.push(context)
      const login = await body(await context.request.post('/api/v1/auth/login', { data: { username, password } }))
      expect(login.user.roles).toEqual([key])
      if (login.user.must_change_password) await body(await context.request.put('/api/v1/auth/profile/password', { data: { current_password: password, new_password: password + 'Changed' } }), 204)
      deniedActors.push(context)
    }
    const path = '/api/v1/inventory-tasks'
    const payload = { name: `填报字段_${stamp}`, description: '历史填报草稿', start_at: '2020-01-01T08:00:00Z', deadline_at: '2020-01-02T08:00:00Z', status: 'draft', organization_ids: [org.id], required_fields: ['business_systems', 'network_assets'], asset_network_scope: 'all', quality_rules: [] }
    const original = (await body(await admin.request.post(path, { data: payload }), 201)).item
    const snapshot = async () => ({
      tasks: await body(await admin.request.get(`${path}?task_id=${original.id}&page_size=100`)),
      total: (await body(await admin.request.get(`${path}?page_size=1`))).total,
      audit: await body(await admin.request.get(`/api/v1/audit-logs?q=${original.id}&page_size=100`)),
    })
    const before = await snapshot()
    const many = (n: number) => Array.from({ length: n }, (_, i) => `value-${i}`)
    const invalid: Array<[string, unknown, string, boolean]> = [
      ['name', '　 ', '任务名称不能为空', false], ['name', '名'.repeat(256), '任务名称最多 255 字', false],
      ['description', 'D'.repeat(5001), '任务说明最多 5000 字', false],
      ['start_at', '0001-01-01T00:00:00Z', '请选择开始时间', false], ['deadline_at', '0001-01-01T00:00:00Z', '请选择截止时间', false],
      ['deadline_at', payload.start_at, '截止时间必须晚于开始时间', false], ['status', 'closed', '任务状态仅支持草稿或进行中', false],
      ['asset_network_scope', 'bad', '请选择有效的网络资产核查范围', true],
      ['organization_ids', [' ', '　'], '请选择至少一个填报组织', true], ['organization_ids', many(1001), '填报组织最多选择 1000 个', true],
      ['required_fields', many(101), '填报内容最多选择 100 项', true], ['required_fields', ['bad'], '请选择有效的填报内容', true],
      ['required_fields', ['owner_contacts'], '请至少选择业务系统、数据安全属性或网络资产中的一项填报内容', true],
      ['quality_rules', many(101), '质量规则最多选择 100 项', false],
    ]
    for (const [field, value, message, preview] of invalid) {
      for (const endpoint of preview ? [path, `${path}/scope-preview`] : [path]) {
        const data = { ...payload, [field]: value }
        expect((await body(await admin.request.post(endpoint, { data }), 400)).error).toEqual({ code: endpoint === path ? 'invalid_inventory_task' : 'invalid_inventory_task_scope', message })
        for (const actor of deniedActors) expect((await body(await actor.request.post(endpoint, { data }), 403)).error.code).toBe('forbidden')
        ;(evidence.rejections as unknown[]).push({ field, endpoint, message, status: 400, deniedRoles: 2 })
      }
    }
    expect(await snapshot()).toEqual(before)
    const page = await admin.newPage()
    page.on('pageerror', error => pageErrors.push(error.message))
    let saves = 0
    page.on('request', request => { if (request.method() === 'POST' && new URL(request.url()).pathname === path) saves++ })
    for (const char of ['填', 'T']) {
      await page.goto('/inventory-tasks')
      await page.getByRole('button', { name: '创建任务', exact: true }).click()
      const dialog = page.getByRole('dialog', { name: '创建填报任务', exact: true })
      const organizationSelect = dialog.locator('.el-form-item').filter({ has: page.getByText('填报组织', { exact: true }) }).locator('.el-select')
      await organizationSelect.click()
      await page.getByRole('option', { name: org.name, exact: true }).click()
      await dialog.getByLabel('任务名称', { exact: true }).click() // Close the multiple-select dropdown.
      await dialog.locator('.el-form-item').filter({ has: page.getByText('任务状态', { exact: true }) }).locator('.el-select').click()
      await page.getByRole('option', { name: '保存草稿', exact: true }).click()
      await dialog.getByLabel('任务名称', { exact: true }).fill('　 ')
      const save = dialog.getByRole('button', { name: '保存草稿', exact: true })
      const beforeSave = saves
      await save.click()
      await expect(dialog.getByText('请输入任务名称', { exact: true })).toBeVisible()
      expect(saves).toBe(beforeSave)
      await expect(dialog.getByTestId('scope-preview-summary')).toContainText('1 家组织')
      await dialog.getByLabel('任务名称', { exact: true }).fill(char.repeat(255))
      await dialog.getByLabel('任务说明', { exact: true }).fill(char.repeat(5000))
      const saved = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === path)
      await save.click()
      const item = (await body(await saved, 201)).item
      expect(item).toMatchObject({ name: char.repeat(255), description: char.repeat(5000), status: 'draft', target_count: 1 })
      expect(item.organizations.map((organization: { organization_id: string }) => organization.organization_id)).toEqual([org.id])
      await expect(dialog).not.toBeVisible()
      expect((await findRecordPage<{ id: string; name: string; description: string }>(admin.request, path, task => task.id === item.id))!.item).toMatchObject({ name: item.name, description: item.description, status: 'draft' })
      ;(evidence.boundaries as unknown[]).push({ id: item.id, char, nameLength: [...item.name].length, descriptionLength: [...item.description].length, status: item.status, organizationID: org.id, invalidFormRequested: false })
    }
    expect(pageErrors).toEqual([])
    evidence.rejectedUnchanged = true
    evidence.pageErrors = pageErrors
    await page.screenshot({ path: info.outputPath('inventory-task-input.png'), fullPage: true })
  } finally {
    await writeFile(info.outputPath('inventory-task-input-evidence.json'), JSON.stringify(evidence, null, 2))
    for (const context of contexts.reverse()) await context.close()
  }
})
