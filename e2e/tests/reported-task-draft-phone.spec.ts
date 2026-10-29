import { expect, test, type APIResponse, type BrowserContext } from '@playwright/test'
import { writeFile } from 'node:fs/promises'

// Own fixtures only: do not reset the retained database or alter shared rules.
test.setTimeout(240_000)
const childRole = process.env.E2E_BUSINESS_PHONE_ROLE ?? 'organization_admin'
if (!['organization_admin', 'organization_editor'].includes(childRole)) throw new Error(`不支持的电话验收角色：${childRole}`)
const childActor = childRole === 'organization_editor' ? '子公司填报员' : '子管理员'
async function body(response: APIResponse, status = 200) {
  const text = await response.text()
  expect(response.status(), text).toBe(status)
  return text ? JSON.parse(text) : null
}
type Org = { id: string; type: string; children?: Org[] }
function flatten(items: Org[]): Org[] { return items.flatMap(item => [item, ...flatten(item.children ?? [])]) }

test(`真实${childActor}任务内业务系统电话：字段提示、规范保存、CAS、提交锁定及审核发布`, async ({ browser, baseURL }, testInfo) => {
  test.skip(!process.env.E2E_PASSWORD, 'E2E_PASSWORD 未配置')
  const group = await browser.newContext({ baseURL })
  let child: BrowserContext | undefined
  const pageErrors: string[] = []
  const consoleErrors: string[] = []
  const evidence: Record<string, unknown> = { childRole, pageErrors, consoleErrors }
  try {
    await body(await group.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }))
    const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
    const root = flatten((await body(await group.request.get('/api/v1/organizations/tree'))).items).find(item => item.type === 'group')!
    const org = (await body(await group.request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `TASK_PHONE_${stamp}`, name: `任务电话专项_${stamp}`, type: 'subsidiary' } }), 201)).item
    const role = (await body(await group.request.get('/api/v1/roles?page_size=100'))).items.find((item: { key: string }) => item.key === childRole)
    expect(role).toBeTruthy()
    const username = `task_phone_${stamp}`
    const password = 'TaskPhone123!'
    await body(await group.request.post('/api/v1/users', { data: { username, password, display_name: `任务电话${childActor}`, global_role_ids: [], appointments: [{ organization_id: org.id, is_primary: true, assignments: [{ role_id: role.id, data_scope: 'organization' }] }] } }), 201)
    child = await browser.newContext({ baseURL })
    const login = await body(await child.request.post('/api/v1/auth/login', { data: { username, password } }))
    expect(login.user.roles).toEqual([childRole])
    expect(login.user.roles).not.toContain('group_admin')
    if (login.user.must_change_password) await body(await child.request.put('/api/v1/auth/profile/password', { data: { current_password: password, new_password: password + 'Changed' } }), 204)
    const task = (await body(await group.request.post('/api/v1/inventory-tasks', { data: {
      name: `任务电话验收_${stamp}`, description: '保留草稿、版本、审计和发布证据',
      start_at: new Date(Date.now() - 300_000).toISOString(), deadline_at: new Date(Date.now() + 86_400_000).toISOString(),
      status: 'active', asset_network_scope: 'all', required_fields: ['business_systems', 'data_security'], quality_rules: [],
      require_review: true, allow_late_submission: false, organization_ids: [org.id],
    } }), 201)).item
    const assignment = `/api/v1/inventory-tasks/${task.id}/organizations/${org.id}`
    const endpoint = `${assignment}/business-system-drafts`
    await body(await child.request.post(`${assignment}/start`))
    const definitions = (await body(await child.request.get('/api/v1/custom-fields/fillable?resource_type=business_system&page_size=100'))).items
    const custom_values = definitions.filter((field: { is_required: boolean }) => field.is_required).map((field: { field_key: string; field_type: string; options: string[] }) => ({
      field_key: field.field_key,
      value: field.field_type === 'boolean' ? false : field.field_type === 'number' ? 1 : field.field_type === 'date' ? '2026-10-28' : field.field_type === 'select' ? field.options[0] : field.field_type === 'multiselect' ? [field.options[0]] : '任务电话验收',
    }))
    const input = {
      code: `TASK_PHONE_${stamp}`, name: `任务内电话系统_${stamp}`, organization_id: org.id,
      owner_name: '电话负责人', owner_contact: '13800000018', status: 'running', level: 'normal',
      deployment_type: '集团私有云', internet_exposure: 'no', description: '电话回归草稿',
      contains_personal_information: 'no', contains_sensitive_personal_information: 'no', personal_information_record_count: null,
      data_volume_over_100k: 'no', contains_important_data: 'no', contains_core_data: 'no', data_storage_location: '上海数据中心', security_notes: '', custom_values,
    }
    evidence.fixture = { organization: org, task, input }
    async function drafts(context = child!) { return body(await context.request.get(`${endpoint}?page_size=100`)) }
    async function formal() { return body(await group.request.get(`/api/v1/business-systems?q=${input.code}&page_size=20`)) }
    async function audits(id: string) {
      return (await body(await group.request.get(`/api/v1/audit-logs?q=${id}&page_size=100`))).items.filter((item: { resource_id: string }) => item.resource_id === id)
    }
    const invalidValues = ['x'.repeat(64), 'x'.repeat(65), '错误电话', 'invalid-phone', '1380000000', '12800000000', '12', '13800000000转1']
    const beforeCreate = await drafts()
    const invalidCreates = []
    for (const value of invalidValues) {
      const rejected = await body(await child.request.post(endpoint, { data: { ...input, owner_contact: value } }), 400)
      invalidCreates.push({ value, ...rejected })
      expect.soft(rejected.error.code, '非法电话必须保留具体字段错误码').toBe('invalid_owner_contact')
      expect.soft(rejected.error.message).toContain('联系方式')
      expect(await drafts()).toEqual(beforeCreate)
    }
    evidence.invalidCreates = invalidCreates
    let draft = (await body(await child.request.post(endpoint, { data: input }), 201)).item
    evidence.draftID = draft.id
    expect(draft.version).toBe(1)
    expect(draft.draft_status).toBe('editing')
    const initial = await drafts()
    const initialAudits = await audits(draft.id)
    const invalidPatches = []
    for (const value of invalidValues) {
      const rejected = await body(await child.request.patch(`${endpoint}/${draft.id}`, { data: { ...input, owner_contact: value, expected_version: draft.version } }), 400)
      invalidPatches.push({ value, ...rejected })
      expect.soft(rejected.error.code).toBe('invalid_owner_contact')
      expect.soft(rejected.error.message).toContain('联系方式')
      expect(await drafts()).toEqual(initial)
      expect(await audits(draft.id)).toEqual(initialAudits)
    }
    evidence.invalidPatches = invalidPatches
    expect((await formal()).total).toBe(0)
    expect((await drafts(group)).total).toBe(0)

    const page = await child.newPage()
    page.setDefaultTimeout(20_000)
    page.on('pageerror', error => pageErrors.push(error.message))
    page.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()) })
    await page.goto(`/systems?mode=inventory&task_id=${task.id}&organization_id=${org.id}&task_name=${encodeURIComponent(task.name)}`)
    const openEdit = async () => {
      await page.locator('.inventory-draft-panel').getByRole('button', { name: input.name, exact: true }).click()
      const dialog = page.getByRole('dialog', { name: '编辑业务系统', exact: true })
      await expect(dialog).toBeVisible()
      return dialog
    }
    let dialog = await openEdit()
    const mutations: string[] = []
    const track = (request: { url(): string; method(): string }) => {
      if (['POST', 'PATCH', 'PUT', 'DELETE'].includes(request.method()) && /\/api\/v1\/(business-systems|inventory-tasks)(?:\/|$|\?)/.test(request.url())) mutations.push(request.url())
    }
    page.on('request', track)
    const phone = dialog.getByLabel('联系方式', { exact: true })
    await phone.fill('x'.repeat(65))
    await expect(phone).toHaveValue('x'.repeat(64))
    for (const value of ['x'.repeat(64), '错误电话', 'invalid-phone', '1380000000', '12800000000', '12', '13800000000转1']) {
      await phone.fill(value)
      await dialog.getByRole('button', { name: '保存修改', exact: true }).click()
      await expect(dialog.locator('.el-form-item').filter({ hasText: '联系方式' })).toContainText('请填写手机号码、座机号码或分机号')
      await expect(dialog).toBeVisible()
      await page.evaluate(() => new Promise(resolve => setTimeout(resolve, 50)))
      expect(mutations, `非法联系方式 ${value} 不应发送变更请求`).toEqual([])
    }
    page.off('request', track)
    expect(await drafts()).toEqual(initial)
    expect(await audits(draft.id)).toEqual(initialAudits)
    const saved = [draft]
    const valid = [['0755-88886666-8001', '0755888866668001'], ['+86 (139) 0000-0018', '+8613900000018'], ['400-123-4567', '4001234567'], ['8001', '8001'], ['   ', ''], ['138-0000-0018', '13800000018']]
    for (const [value, normalized] of valid) {
      await dialog.getByLabel('联系方式', { exact: true }).fill(value)
      const response = page.waitForResponse(res => res.url().endsWith(`${endpoint}/${draft.id}`) && res.request().method() === 'PATCH')
      await dialog.getByRole('button', { name: '保存修改', exact: true }).click()
      const next = (await body(await response)).item
      expect(next.version).toBe(draft.version + 1)
      expect(next.owner_contact ?? '').toBe(normalized)
      draft = next
      saved.push(next)
      await expect(dialog).toBeHidden()
      expect((await drafts()).items.find((item: { id: string }) => item.id === draft.id)).toEqual(draft)
      expect((await formal()).total).toBe(0)
      dialog = await openEdit()
      await expect(dialog.getByLabel('联系方式', { exact: true })).toHaveValue(normalized)
    }
    await dialog.getByRole('button', { name: '取消', exact: true }).click()
    const stable = await drafts()
    const stableAudits = await audits(draft.id)
    const stale = await body(await child.request.patch(`${endpoint}/${draft.id}`, { data: { ...input, owner_contact: '13900000018', expected_version: 1 } }), 409)
    expect(stale.error.code).toBe('draft_edit_conflict')
    expect(await drafts()).toEqual(stable)
    expect(await audits(draft.id)).toEqual(stableAudits)
    expect(stableAudits.filter((item: { action: string }) => item.action === 'inventory.business_system_draft.create')).toHaveLength(1)
    expect(stableAudits.filter((item: { action: string }) => item.action === 'inventory.business_system_draft.update')).toHaveLength(valid.length)
    const submission = (await body(await child.request.post(`${assignment}/submit`, { data: { note: '电话保存正确，提交审核' } }))).item
    expect(submission.status).toBe('submitted')
    const submitted = await drafts()
    const submittedAudits = await audits(draft.id)
    const locked = await body(await child.request.patch(`${endpoint}/${draft.id}`, { data: { ...input, owner_contact: '13900000018', expected_version: submitted.items[0].version } }), 409)
    expect(locked.error.code).toBe('invalid_state')
    expect(await drafts()).toEqual(submitted)
    expect(await audits(draft.id)).toEqual(submittedAudits)
    expect((await formal()).total).toBe(0)
    await body(await group.request.post(`${assignment}/review`, { data: { decision: 'approved', return_reason: '', comments: [] } }))
    const published = await formal()
    expect(published.total).toBe(1)
    expect(published.items[0].id).toBe(draft.id)
    expect(published.items[0].owner_contact).toBe('13800000018')
    const publishedDrafts = await drafts()
    expect(publishedDrafts.items[0].draft_status).toBe('published')
    const publishedAudits = await audits(draft.id)
    expect(publishedAudits.filter((item: { action: string }) => item.action === 'business_system.create')).toHaveLength(1)
    evidence.result = { saved, auditActions: stableAudits.map((item: { action: string }) => item.action), stale, submission, locked, published, publishedDrafts, publishedAudits }
    await page.screenshot({ path: testInfo.outputPath('task-phone-saved.png'), fullPage: true })
    expect(pageErrors).toEqual([])
    expect(consoleErrors).toEqual([])
  } finally {
    const path = testInfo.outputPath('task-phone-evidence.json')
    await writeFile(path, JSON.stringify(evidence, null, 2))
    await testInfo.attach('task-phone-evidence', { path, contentType: 'application/json' })
    await child?.close()
    await group.close()
  }
})
