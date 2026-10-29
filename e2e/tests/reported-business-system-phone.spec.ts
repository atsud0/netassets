import { expect, test, type APIResponse, type BrowserContext, type Locator, type Page } from '@playwright/test'
import { writeFile } from 'node:fs/promises'
import { clickRowAction } from '../helpers/row-actions'

// Keep fixtures in the retained isolated database; never reset other regression data.
test.setTimeout(240_000)
const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
const phoneError = '请填写手机号码、座机号码或分机号'
const childRole = process.env.E2E_BUSINESS_PHONE_ROLE ?? 'organization_admin'
if (!['organization_admin', 'organization_editor'].includes(childRole)) throw new Error(`不支持的电话验收角色：${childRole}`)
const childActor = childRole === 'organization_editor' ? '子公司填报员' : '子系统管理员'
type Org = { id: string; name: string; type: string; children?: Org[] }
type System = { id: string; code: string; name: string; owner_contact?: string; version: number; is_working_copy: boolean; reporting_status: string }
let group: BrowserContext
let child: BrowserContext
let organization: Org
let organizationPagination: { total: number; firstPageIDs: string[] }
async function body(response: APIResponse, status = 200) {
  const text = await response.text()
  expect(response.status(), text).toBe(status)
  return text ? JSON.parse(text) : null
}
function flatten(items: Org[]): Org[] { return items.flatMap(item => [item, ...flatten(item.children ?? [])]) }
async function read(context: BrowserContext, id: string): Promise<System> {
  return (await body(await context.request.get(`/api/v1/business-systems/${id}`))).item
}
async function audits(id: string) {
  return (await body(await group.request.get(`/api/v1/audit-logs?q=${id}&page_size=100`))).items
    .filter((item: { resource_id: string; action: string }) => item.resource_id === id && item.action.startsWith('business_system.'))
}
async function fillRequiredCustomFields(context: BrowserContext, dialog: Locator) {
  const definitions = (await body(await context.request.get('/api/v1/custom-fields/fillable?resource_type=business_system&page_size=100'))).items
  for (const field of definitions.filter((item: { is_required: boolean }) => item.is_required)) {
    const item = dialog.locator('.el-form-item').filter({ has: dialog.page().locator('.el-form-item__label').filter({ hasText: field.field_name }) })
    if (['select', 'multiselect'].includes(field.field_type)) {
      await item.locator('.el-select').click()
      await dialog.page().getByRole('option', { name: field.options[0], exact: true }).last().click()
    } else if (field.field_type === 'boolean') {
      // false is a valid boolean; leave the default unchanged.
    } else await item.locator('input, textarea').fill(field.field_type === 'number' ? '1' : field.field_type === 'date' ? '2026-10-28' : '电话校验专项')
  }
  return definitions.length > 0
}
async function save(page: Page, dialog: Locator, endpoint: string, method: string, status: number): Promise<System> {
  const response = page.waitForResponse(res => res.url().endsWith(endpoint) && res.request().method() === method)
  await dialog.getByRole('button', { name: method === 'POST' ? '确认新增' : '保存修改', exact: true }).click()
  const result = await body(await response, status)
  await expect(dialog).toBeHidden()
  return result.item
}
async function invalidPhones(page: Page, dialog: Locator, button: string) {
  const requests: string[] = []
  const track = (request: { url(): string; method(): string }) => {
    if (/\/api\/v1\/business-systems(?:\/[^/?]+)?$/.test(request.url()) && ['POST', 'PATCH'].includes(request.method())) requests.push(request.url())
  }
  page.on('request', track)
  try {
    const phone = dialog.getByLabel('联系方式', { exact: true })
    await phone.fill('x'.repeat(65))
    await expect(phone).toHaveValue('x'.repeat(64))
    for (const value of ['x'.repeat(64), '错误电话', 'invalid-phone', '1380000000', '12800000000', '12', '13800000000转1']) {
      await phone.fill(value)
      await dialog.getByRole('button', { name: button, exact: true }).click()
      await expect(dialog.locator('.el-form-item').filter({ hasText: '联系方式' })).toContainText(phoneError)
      await expect(dialog).toBeVisible()
      // Wait for the invalid submit handler to settle, not just a blur error.
      await page.evaluate(() => new Promise(resolve => setTimeout(resolve, 50)))
      expect(requests, `非法联系方式 ${value} 不应发送变更请求`).toEqual([])
    }
  } finally { page.off('request', track) }
}
async function openEdit(page: Page, item: System) {
  await page.getByPlaceholder('搜索系统名称、组织或负责人，也可输入编码').fill(item.code)
  await page.getByRole('button', { name: '查询', exact: true }).click()
  const row = page.locator('tbody tr').filter({ hasText: item.name }).first()
  await clickRowAction(page, row, '编辑')
  const dialog = page.getByRole('dialog', { name: '编辑业务系统', exact: true })
  await expect(dialog).toBeVisible()
  return dialog
}

test.beforeAll(async ({ browser, baseURL }) => {
  test.skip(!process.env.E2E_PASSWORD, 'E2E_PASSWORD 未配置')
  group = await browser.newContext({ baseURL })
  await body(await group.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }))
  const root = flatten((await body(await group.request.get('/api/v1/organizations/tree'))).items).find(item => item.type === 'group')!
  organization = (await body(await group.request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `ZZ_PHONE_${stamp}`, name: `业务系统电话专项_${stamp}`, type: 'subsidiary' } }), 201)).item
  let firstPage = await body(await group.request.get('/api/v1/organizations?page=1&page_size=100'))
  if (firstPage.items.some((item: Org) => item.id === organization.id)) {
    // A fresh isolated database also exercises page 2+, without relying on
    // accumulated fixtures and without changing/deleting existing organizations.
    for (let index = 0; index < 100; index++) await body(await group.request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `00_PHONE_${stamp}_${index}`, name: `电话专项分页前置_${stamp}_${index}`, type: 'subsidiary' } }), 201)
    firstPage = await body(await group.request.get('/api/v1/organizations?page=1&page_size=100'))
  }
  expect(firstPage.items.some((item: Org) => item.id === organization.id), '目标组织必须在第 100 条之后，防止漏测组织选项分页').toBe(false)
  organizationPagination = { total: firstPage.total, firstPageIDs: firstPage.items.map((item: Org) => item.id) }
  const roles = (await body(await group.request.get('/api/v1/roles?page_size=100'))).items
  const role = roles.find((item: { key: string }) => item.key === childRole)
  expect(role).toBeTruthy()
  const username = `system_phone_${stamp}`
  const password = 'BusinessPhone123!'
  await body(await group.request.post('/api/v1/users', { data: { username, password, display_name: `业务系统电话${childActor}`, global_role_ids: [], appointments: [{ organization_id: organization.id, is_primary: true, assignments: [{ role_id: role.id, data_scope: 'organization' }] }] } }), 201)
  child = await browser.newContext({ baseURL })
  const login = await body(await child.request.post('/api/v1/auth/login', { data: { username, password } }))
  expect(login.user.roles).toEqual([childRole])
  expect(login.user.roles).not.toContain('group_admin')
  if (login.user.must_change_password) await body(await child.request.put('/api/v1/auth/profile/password', { data: { current_password: password, new_password: password + 'Changed' } }), 204)
})
test.afterAll(async () => { await child?.close(); await group?.close() })

for (const actor of ['集团管理员', childActor]) {
  test(`${actor}新增和编辑：非法电话不提交，合法电话规范保存且拒绝后数据审计不变`, async ({}, testInfo) => {
    const context = actor === '集团管理员' ? group : child
    const page = await context.newPage()
    page.setDefaultTimeout(20_000)
    const pageErrors: string[] = []
    const consoleErrors: string[] = []
    page.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()) })
    page.on('pageerror', error => pageErrors.push(error.message))
    const code = `PHONE_${actor === '集团管理员' ? 'G' : 'C'}_${stamp}`
    const name = `电话验收_${actor}_${stamp}`
    const saved: System[] = []
    try {
      await page.goto('/systems')
      await page.getByRole('button', { name: '新增业务系统', exact: true }).click()
      const create = page.getByRole('dialog', { name: '新增业务系统', exact: true })
      await create.getByLabel('系统编码', { exact: true }).fill(code)
      await create.getByLabel('系统名称', { exact: true }).fill(name)
      if (actor === '集团管理员') {
        await create.locator('.el-form-item').filter({ hasText: '所属组织' }).locator('.el-select').click()
        await page.getByRole('option', { name: organization.name, exact: true }).click()
      } else await expect(create.getByLabel('所属组织', { exact: true })).toBeDisabled()
      const hasCustomFields = await fillRequiredCustomFields(context, create)
      await invalidPhones(page, create, '确认新增')
      const empty = await body(await context.request.get(`/api/v1/business-systems?q=${code}&page_size=20`))
      expect(empty.total).toBe(0)
      await create.getByLabel('联系方式', { exact: true }).fill(' 138-0000-0018 ')
      let item = await save(page, create, '/api/v1/business-systems', 'POST', 201)
      expect(item.owner_contact).toBe('13800000018')
      expect(Boolean(item.is_working_copy)).toBe(actor === childActor)
      if (actor === childActor) expect(item.reporting_status).toBe('unreported')
      saved.push(await read(context, item.id))
      let edit = await openEdit(page, item)
      await expect(edit.getByLabel('联系方式', { exact: true })).toHaveValue('13800000018')
      const before = await read(context, item.id)
      const beforeAudits = await audits(item.id)
      await invalidPhones(page, edit, '保存修改')
      expect(await read(context, item.id)).toEqual(before)
      expect(await audits(item.id)).toEqual(beforeAudits)
      // Bypassing the browser must also reject invalid input, without advancing
      // an official/working version or recording a successful mutation audit.
      const rejected = await body(await context.request.patch(`/api/v1/business-systems/${item.id}`, { data: { ...before, owner_contact: '非法电话', expected_version: before.version } }), 400)
      expect(rejected.error.code).toBe('invalid_owner_contact')
      expect(await read(context, item.id)).toEqual(before)
      expect(await audits(item.id)).toEqual(beforeAudits)
      const valid = [
        ['0755-88886666-8001', '0755888866668001'],
        ['+86 (139) 0000-0018', '+8613900000018'],
        ['400-123-4567', '4001234567'],
        ['8001', '8001'],
        ['   ', ''],
      ]
      for (const [input, normalized] of valid) {
        await edit.getByLabel('联系方式', { exact: true }).fill(input)
        const previousVersion = (await read(context, item.id)).version
        item = await save(page, edit, `/api/v1/business-systems/${item.id}`, 'PATCH', 200)
        expect(item.owner_contact ?? '').toBe(normalized)
        const persisted = await read(context, item.id)
        expect(persisted.owner_contact ?? '').toBe(normalized)
        expect(persisted.version).toBe(previousVersion + (actor === '集团管理员' && hasCustomFields ? 2 : 1))
        saved.push(persisted)
        edit = await openEdit(page, item)
        await expect(edit.getByLabel('联系方式', { exact: true })).toHaveValue(normalized)
      }
      await edit.getByRole('button', { name: '取消', exact: true }).click()
      const events = await audits(item.id)
      const prefix = actor === '集团管理员' ? 'business_system.' : 'business_system.working_'
      expect(events.filter((event: { action: string }) => event.action === `${prefix}create`)).toHaveLength(1)
      expect(events.filter((event: { action: string }) => event.action === `${prefix}update`)).toHaveLength(valid.length)
      if (actor === childActor) {
        const formal = await body(await group.request.get(`/api/v1/business-systems?q=${code}&page_size=20`))
        expect(formal.total, '未上报工作版本不能进入集团正式台账').toBe(0)
      }
      const path = testInfo.outputPath('phone-readback.json')
      await writeFile(path, JSON.stringify({ actor, childRole, organization, organizationPagination, saved, auditActions: events.map((event: { action: string }) => event.action), pageErrors, consoleErrors }, null, 2))
      await testInfo.attach('phone-readback', { path, contentType: 'application/json' })
      await page.screenshot({ path: testInfo.outputPath('phone-saved.png'), fullPage: true })
      expect(pageErrors, '字段校验失败应正常返回，不能抛出未处理异常').toEqual([])
      expect(consoleErrors, '字段校验失败不应被 Vue 记为未处理错误').toEqual([])
    } finally {
      const path = testInfo.outputPath('browser-errors.json')
      await writeFile(path, JSON.stringify({ pageErrors, consoleErrors }, null, 2))
      await testInfo.attach('browser-errors', { path, contentType: 'application/json' })
      await page.close()
    }
  })
}
