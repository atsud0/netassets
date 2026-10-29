import { expect, test, type APIResponse, type BrowserContext, type Locator, type Page } from '@playwright/test'
import { writeFile } from 'node:fs/promises'
import { clickRowAction } from '../helpers/row-actions'

// Retain independent fixtures and all evidence; never reset shared data/rules.
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
async function invalidPhones(page: Page, dialog: Locator, button: string) {
  const mutations: string[] = []
  const track = (request: { url(): string; method(): string }) => {
    if (['POST', 'PATCH', 'PUT', 'DELETE'].includes(request.method()) && /\/api\/v1\/(business-systems|inventory-tasks)(?:\/|$|\?)/.test(request.url())) mutations.push(request.url())
  }
  page.on('request', track)
  try {
    const phone = dialog.getByLabel('联系方式', { exact: true })
    await phone.fill('x'.repeat(65))
    await expect(phone).toHaveValue('x'.repeat(64))
    for (const value of ['x'.repeat(64), '错误电话', 'invalid-phone', '1380000000', '12800000000', '12', '13800000000转1']) {
      await phone.fill(value)
      await dialog.getByRole('button', { name: button, exact: true }).click()
      await expect(dialog.locator('.el-form-item').filter({ hasText: '联系方式' })).toContainText('请填写手机号码、座机号码或分机号')
      await expect(dialog).toBeVisible()
      await page.evaluate(() => new Promise(resolve => setTimeout(resolve, 50)))
      expect(mutations, `非法电话 ${value} 不应发送变更请求`).toEqual([])
    }
  } finally { page.off('request', track) }
}
async function choose(page: Page, dialog: Locator, label: string, option: string) {
  await dialog.locator('.el-form-item').filter({ has: page.locator('.el-form-item__label').getByText(label, { exact: true }) }).locator('.el-select').click()
  await page.getByRole('option', { name: option, exact: true }).last().click()
}

for (const mode of ['create', 'update'] as const) {
  test(`真实${childActor}任务浏览器全链路 ${mode}：电话拦截、选择上报、提交及集团审核发布`, async ({ browser, baseURL }, testInfo) => {
    test.skip(!process.env.E2E_PASSWORD, 'E2E_PASSWORD 未配置')
    const group = await browser.newContext({ baseURL })
    let child: BrowserContext | undefined
    const pageErrors: string[] = []
    const consoleErrors: string[] = []
    const evidence: Record<string, unknown> = { childRole, mode, pageErrors, consoleErrors }
    try {
      await body(await group.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }))
      const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
      const root = flatten((await body(await group.request.get('/api/v1/organizations/tree'))).items).find(item => item.type === 'group')!
      const org = (await body(await group.request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `TASK_UI_${stamp}`, name: `任务浏览器_${mode}_${stamp}`, type: 'subsidiary' } }), 201)).item
      const role = (await body(await group.request.get('/api/v1/roles?page_size=100'))).items.find((item: { key: string }) => item.key === childRole)
      expect(role).toBeTruthy()
      const username = `task_ui_${stamp}`
      const password = 'TaskBrowser123!'
      await body(await group.request.post('/api/v1/users', { data: { username, password, display_name: `任务浏览器${childActor}`, global_role_ids: [], appointments: [{ organization_id: org.id, is_primary: true, assignments: [{ role_id: role.id, data_scope: 'organization' }] }] } }), 201)
      child = await browser.newContext({ baseURL })
      const login = await body(await child.request.post('/api/v1/auth/login', { data: { username, password } }))
      expect(login.user.roles).toEqual([childRole])
      expect(login.user.roles).not.toContain('group_admin')
      if (login.user.must_change_password) await body(await child.request.put('/api/v1/auth/profile/password', { data: { current_password: password, new_password: password + 'Changed' } }), 204)
      const definitions = (await body(await child.request.get('/api/v1/custom-fields/fillable?resource_type=business_system&page_size=100'))).items
      const required = definitions.filter((field: { is_required: boolean }) => field.is_required)
      const custom_values = required.map((field: { field_key: string; field_type: string; options: string[] }) => ({
        field_key: field.field_key,
        value: field.field_type === 'boolean' ? false : field.field_type === 'number' ? 1 : field.field_type === 'date' ? '2026-10-28' : field.field_type === 'select' ? field.options[0] : field.field_type === 'multiselect' ? [field.options[0]] : '任务浏览器验收',
      }))
      const input = {
        code: `TASK_UI_${stamp}`, name: `任务浏览器系统_${mode}_${stamp}`, organization_id: org.id,
        owner_name: '浏览器负责人', owner_contact: '13800000018', status: 'running', level: 'normal', deployment_type: '集团私有云', internet_exposure: 'no', description: '任务浏览器全链路',
        contains_personal_information: 'no', contains_sensitive_personal_information: 'no', personal_information_record_count: null,
        data_volume_over_100k: 'no', contains_important_data: 'no', contains_core_data: 'no', data_storage_location: '上海数据中心', security_notes: '', custom_values,
      }
      const source = mode === 'update' ? (await body(await group.request.post('/api/v1/business-systems', { data: input }), 201)).item : null
      const task = (await body(await group.request.post('/api/v1/inventory-tasks', { data: {
        name: `任务浏览器验收_${mode}_${stamp}`, description: '保留新建和来源正式台账的浏览器证据', start_at: new Date(Date.now() - 300_000).toISOString(), deadline_at: new Date(Date.now() + 86_400_000).toISOString(),
        status: 'active', asset_network_scope: 'all', required_fields: ['business_systems', 'data_security'], quality_rules: [], require_review: true, allow_late_submission: false, organization_ids: [org.id],
      } }), 201)).item
      evidence.fixture = { org, task, input, source }
      const assignment = `/api/v1/inventory-tasks/${task.id}/organizations/${org.id}`
      const endpoint = `${assignment}/business-system-drafts`
      await body(await child.request.post(`${assignment}/start`))
      async function formal() { return body(await group.request.get(`/api/v1/business-systems?q=${input.code}&page_size=20`)) }
      async function drafts() { return body(await child!.request.get(`${endpoint}?page_size=100`)) }
      async function audits(id: string) { return (await body(await group.request.get(`/api/v1/audit-logs?q=${id}&page_size=100`))).items.filter((item: { resource_id: string }) => item.resource_id === id) }
      const originalFormal = await formal()
      const originalAudits = source ? await audits(source.id) : []
      const page = await child.newPage()
      page.setDefaultTimeout(20_000)
      const trackErrors = (p: Page) => {
        p.on('pageerror', error => pageErrors.push(error.message))
        p.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()) })
      }
      trackErrors(page)
      async function taskDetail() {
        await page.goto('/inventory-tasks')
        await page.getByRole('button', { name: '从具体任务进入上报内容', exact: true }).click()
        await page.getByRole('menuitem', { name: task.name, exact: true }).click()
        const detail = page.getByRole('dialog', { name: '填报任务详情', exact: true })
        await expect(detail).toBeVisible()
        return detail
      }
      async function submissionDialog(detail: Locator) {
        const checked = page.waitForResponse(res => res.url().includes(`${assignment}/quality-check`) && res.request().method() === 'GET')
        await clickRowAction(page, detail.locator('.task-organization-table tbody tr').filter({ hasText: org.name }), '提交')
        const quality = await body(await checked)
        const dialog = page.getByRole('dialog', { name: '提交填报结果', exact: true })
        await expect(dialog).toBeVisible()
        return { dialog, quality }
      }
      let detail = await taskDetail()
      if (mode === 'create') {
        const { dialog, quality } = await submissionDialog(detail)
        expect(quality.quality_check.passed).toBe(false)
        await expect(dialog.getByText('尚未录入业务系统', { exact: true })).toBeVisible()
        await expect(dialog.getByRole('button', { name: '确认提交', exact: true })).toBeDisabled()
        evidence.emptyQuality = quality
        await dialog.getByRole('button', { name: '取消', exact: true }).click()
      }
      await clickRowAction(page, detail.locator('.task-organization-table tbody tr').filter({ hasText: org.name }), '维护业务系统')
      await expect(page).toHaveURL(/\/systems\?.*mode=inventory/)
      const mainPanel = page.locator('.data-table-panel').filter({ hasNot: page.locator('.table-panel-heading') }).last()
      if (mode === 'create') await page.getByRole('button', { name: '新增业务系统', exact: true }).click()
      else await clickRowAction(page, mainPanel.locator('tbody tr').filter({ hasText: input.name }), '修改后上报')
      let dialog = page.getByRole('dialog', { name: mode === 'create' ? '新增业务系统' : '编辑业务系统', exact: true })
      await expect(dialog).toBeVisible()
      if (mode === 'create') {
        for (const [label, value] of [['系统编码', input.code], ['系统名称', input.name], ['负责人', input.owner_name], ['部署方式', input.deployment_type], ['数据存储位置', input.data_storage_location]]) await dialog.getByLabel(label, { exact: true }).fill(value)
        for (const label of ['互联网暴露情况', '是否包含个人信息', '是否包含重要数据', '是否包含核心数据']) await choose(page, dialog, label, '否')
        const volume = dialog.locator('.el-form-item').filter({ hasText: '数据量是否超过 10 万条' }).locator('.el-select')
        if (!(await volume.locator('input').isDisabled())) await choose(page, dialog, '数据量是否超过 10 万条', '否')
        for (const field of required) {
          const item = dialog.locator('.el-form-item').filter({ has: page.locator('.el-form-item__label').getByText(field.field_name, { exact: true }) })
          if (['select', 'multiselect'].includes(field.field_type)) {
            await item.locator('.el-select').click()
            await page.getByRole('option', { name: field.options[0], exact: true }).last().click()
          } else if (field.field_type !== 'boolean') await item.locator('input, textarea').fill(field.field_type === 'number' ? '1' : field.field_type === 'date' ? '2026-10-28' : '任务浏览器验收')
        }
      }
      const saveLabel = mode === 'create' ? '保存待提交' : '保存修改'
      await invalidPhones(page, dialog, saveLabel)
      expect(await formal()).toEqual(originalFormal)
      expect((await drafts()).total).toBe(0)
      if (source) expect(await audits(source.id)).toEqual(originalAudits)
      await dialog.getByLabel('联系方式', { exact: true }).fill('+86 (139) 0000-0018')
      const workingEndpoint = source ? `/api/v1/business-systems/${source.id}` : '/api/v1/business-systems'
      const workingResponse = page.waitForResponse(res => res.url().endsWith(workingEndpoint) && res.request().method() === (source ? 'PATCH' : 'POST'))
      await dialog.getByRole('button', { name: saveLabel, exact: true }).click()
      const working = (await body(await workingResponse, source ? 200 : 201)).item
      evidence.working = working
      expect(working.is_working_copy).toBe(true)
      expect(working.task_id ?? '').toBe('')
      expect(working.version).toBe(1)
      expect(working.owner_contact).toBe('+8613900000018')
      if (source) expect(working.id).not.toBe(source.id)
      await expect(dialog).toBeHidden()
      expect(await formal()).toEqual(originalFormal)
      expect((await drafts()).total).toBe(0)
      const selectedResponse = page.waitForResponse(res => res.url().endsWith(`${endpoint}/${working.id}/select`) && res.request().method() === 'POST')
      await clickRowAction(page, mainPanel.locator('tbody tr').filter({ hasText: input.name }), '选择上报')
      const selected = (await body(await selectedResponse)).item
      evidence.selected = selected
      expect(selected.task_id).toBe(task.id)
      expect(selected.operation).toBe(mode)
      if (source) expect(selected.source_system_id).toBe(source.id)
      const draftPanel = page.locator('.inventory-draft-panel')
      await draftPanel.getByRole('button', { name: input.name, exact: true }).click()
      dialog = page.getByRole('dialog', { name: '编辑业务系统', exact: true })
      await expect(dialog.getByLabel('联系方式', { exact: true })).toHaveValue('+8613900000018')
      const beforeEdit = await drafts()
      const beforeEditAudits = await audits(working.id)
      await invalidPhones(page, dialog, '保存修改')
      expect(await drafts()).toEqual(beforeEdit)
      expect(await audits(working.id)).toEqual(beforeEditAudits)
      await dialog.getByLabel('联系方式', { exact: true }).fill('0755-88886666-8001')
      const editedResponse = page.waitForResponse(res => res.url().endsWith(`${endpoint}/${working.id}`) && res.request().method() === 'PATCH')
      await dialog.getByRole('button', { name: '保存修改', exact: true }).click()
      const edited = (await body(await editedResponse)).item
      evidence.edited = edited
      expect(edited.version).toBe(selected.version + 1)
      expect(edited.owner_contact).toBe('0755888866668001')
      await expect(dialog).toBeHidden()
      expect(await formal()).toEqual(originalFormal)
      await page.getByRole('button', { name: '返回填报任务', exact: true }).click()
      await expect(page).toHaveURL(/\/inventory-tasks$/)
      detail = await taskDetail()
      const { dialog: submit, quality } = await submissionDialog(detail)
      evidence.quality = quality
      expect(quality.quality_check.passed).toBe(true)
      expect(quality.quality_check.system_count).toBe(1)
      await expect(submit.getByRole('button', { name: '确认提交', exact: true })).toBeEnabled()
      await submit.getByLabel('填报说明', { exact: true }).fill(`真实${childActor}浏览器提交，电话及来源隔离核对完成`)
      const submittedResponse = page.waitForResponse(res => res.url().endsWith(`${assignment}/submit`) && res.request().method() === 'POST')
      await submit.getByRole('button', { name: '确认提交', exact: true }).click()
      const submitted = (await body(await submittedResponse)).item
      evidence.submitted = submitted
      expect(submitted.status).toBe('submitted')
      await expect(submit).toBeHidden()
      expect(await formal()).toEqual(originalFormal)
      if (source) expect(await audits(source.id)).toEqual(originalAudits)
      const reviewer = await group.newPage()
      reviewer.setDefaultTimeout(20_000)
      trackErrors(reviewer)
      await reviewer.goto('/inventory-tasks')
      await reviewer.getByRole('button', { name: task.name, exact: true }).click()
      const reviewDetail = reviewer.getByRole('dialog', { name: '填报任务详情', exact: true })
      await clickRowAction(reviewer, reviewDetail.locator('.task-organization-table tbody tr').filter({ hasText: org.name }), '通过')
      const confirm = reviewer.getByRole('dialog', { name: '审核通过', exact: true })
      await expect(confirm).toContainText(org.name)
      const approvedResponse = reviewer.waitForResponse(res => res.url().endsWith(`${assignment}/review`) && res.request().method() === 'POST')
      await confirm.getByRole('button', { name: '通过', exact: true }).click()
      evidence.approved = (await body(await approvedResponse)).item
      await expect(confirm).toBeHidden()
      const published = await formal()
      expect(published.total).toBe(1)
      expect(published.items[0].id).toBe(source?.id ?? working.id)
      expect(published.items[0].owner_contact).toBe('0755888866668001')
      expect(published.items[0].version).toBe((source?.version ?? 0) + 1)
      const publishedDrafts = await drafts()
      expect(publishedDrafts.items[0].draft_status).toBe('published')
      expect(publishedDrafts.items[0].version).toBe(edited.version + 2)
      const formalAudits = await audits(published.items[0].id)
      expect(formalAudits.filter((item: { action: string }) => item.action === `business_system.${source ? 'update' : 'create'}`)).toHaveLength(1)
      evidence.result = { published, publishedDrafts, formalAudits, workingAudits: await audits(working.id) }
      await reviewer.screenshot({ path: testInfo.outputPath('review-approved.png'), fullPage: true })
      expect(pageErrors).toEqual([])
      expect(consoleErrors).toEqual([])
    } finally {
      const path = testInfo.outputPath('task-browser-evidence.json')
      await writeFile(path, JSON.stringify(evidence, null, 2))
      await testInfo.attach('task-browser-evidence', { path, contentType: 'application/json' })
      await child?.close()
      await group.close()
    }
  })
}
