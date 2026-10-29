import { expect, test, type APIResponse, type BrowserContext, type Locator, type Page } from '@playwright/test'

// The retained isolated database contains the accumulated organization tree.
// Group-admin pages render the full tree; allow a whole create+edit workflow
// without changing per-assertion, request, or product time limits.
test.setTimeout(240_000)
type Org = { id: string; name: string; short_name: string; manager: string; contact: string; notes: string; code: string; type: string; parent_id: string | null; children?: Org[] }
const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
let group: BrowserContext
let child: BrowserContext
let root: Org
let managed: Org
let outside: Org
let sequence = 0
async function body(response: APIResponse, status = 200) {
  const text = await response.text()
  expect(response.status(), text).toBe(status)
  return text ? JSON.parse(text) : null
}
function flatten(items: Org[]): Org[] { return items.flatMap(item => [item, ...flatten(item.children ?? [])]) }
async function organizations(context: BrowserContext): Promise<Org[]> { return flatten((await body(await context.request.get('/api/v1/organizations/tree'))).items) }
async function read(id: string) { const item = (await organizations(group)).find(item => item.id === id); expect(item).toBeTruthy(); return item! }
async function audits(id: string) {
  const data = await body(await group.request.get(`/api/v1/audit-logs?q=${id}&page_size=100`))
  return data.items.filter((item: { resource_id: string; action: string }) => item.resource_id === id && item.action.startsWith('organization.'))
}
function values(letter: string, tag: string) {
  const prefix = `边界_${tag}_`
  return { name: prefix + letter.repeat(255 - prefix.length), short_name: letter.repeat(128), manager: letter.repeat(128), contact: '0755-88886666-8001', notes: letter.repeat(5000) }
}
async function fill(dialog: Locator, data: ReturnType<typeof values>, isGroup = false) {
  for (const [field, label] of Object.entries(isGroup ? { name: '集团名称', short_name: '集团简称', manager: '集团负责人', notes: '集团说明' } : { name: '组织名称', short_name: '组织简称', manager: '负责人', notes: '备注' })) {
    const input = dialog.getByLabel(label, { exact: true })
    const value = data[field as keyof typeof data]
    await input.fill(value + 'X')
    await expect(input).toHaveValue(value)
  }
  const phone = dialog.getByLabel('联系方式', { exact: true })
  await phone.fill('x'.repeat(65))
  await expect(phone).toHaveValue('x'.repeat(64))
}
async function expectNoInvalidMutation(page: Page, dialog: Locator, save: string, endpoint: string, nameLabel: string) {
  let mutations = 0
  const pageErrors: string[] = []
  const onPageError = (error: Error) => pageErrors.push(error.message)
  page.on('pageerror', onPageError)
  const track = (req: { url(): string; method(): string }) => { if (req.url().endsWith(endpoint) && ['POST', 'PATCH'].includes(req.method())) mutations++ }
  page.on('request', track)
  try {
    await dialog.getByLabel('联系方式', { exact: true }).fill('abc')
    await dialog.getByRole('button', { name: save, exact: true }).click()
    await expect(dialog).toContainText('请填写手机号码、座机号码或分机号')
    expect(mutations).toBe(0)
    await dialog.getByLabel('联系方式', { exact: true }).fill('13800000018')
    await dialog.getByLabel(nameLabel, { exact: true }).fill('   ')
    await dialog.getByRole('button', { name: save, exact: true }).click()
    await expect(dialog).toContainText(`请输入${nameLabel}`)
    expect(mutations).toBe(0)
    expect(pageErrors, '无效表单不应产生未处理异常').toEqual([])
  } finally { page.off('request', track); page.off('pageerror', onPageError) }
}
async function save(page: Page, dialog: Locator, button: string, endpoint: string, method: string, status: number) {
  const response = page.waitForResponse(res => res.url().endsWith(endpoint) && res.request().method() === method)
  await dialog.getByRole('button', { name: button, exact: true }).click()
  const result = await body(await response, status)
  await expect(dialog).toBeHidden()
  return result.item as Org
}
async function openEdit(page: Page, org: Org) {
  await page.getByRole('textbox', { name: '搜索组织', exact: true }).fill(org.code)
  await page.getByRole('button', { name: `操作：${org.name}`, exact: true }).click()
  await page.locator('.row-actions-panel:visible').getByRole('button', { name: '编辑信息', exact: true }).click()
  return page.getByRole('dialog', { name: /^编辑.*基本信息$/ })
}

test.beforeAll(async ({ browser, baseURL }) => {
  test.skip(!process.env.E2E_PASSWORD, 'E2E_PASSWORD 未配置')
  group = await browser.newContext({ baseURL })
  await body(await group.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }))
  root = (await organizations(group)).find(item => item.type === 'group')!
  for (const which of ['managed', 'outside']) {
    const item = (await body(await group.request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `OB_${stamp}_${which}`, name: `组织边界_${stamp}_${which}`, type: 'subsidiary' } }), 201)).item
    if (which === 'managed') managed = item; else outside = item
  }
  const roles = await body(await group.request.get('/api/v1/roles?page_size=100'))
  const role = roles.items.find((item: { key: string }) => item.key === 'organization_admin')
  const username = `org_boundary_${stamp}`
  const password = 'OrganizationBoundary123!'
  await body(await group.request.post('/api/v1/users', { data: { username, password, display_name: '组织边界子管理员', global_role_ids: [], appointments: [{ organization_id: managed.id, is_primary: true, assignments: [{ role_id: role.id, data_scope: 'organization' }] }] } }), 201)
  child = await browser.newContext({ baseURL })
  const login = await body(await child.request.post('/api/v1/auth/login', { data: { username, password } }))
  expect(login.user.roles).toContain('organization_admin')
  expect(login.user.roles).not.toContain('group_admin')
  if (login.user.must_change_password) await body(await child.request.put('/api/v1/auth/profile/password', { data: { current_password: password, new_password: password + 'Changed' } }), 204)
})
test.afterAll(async () => { await child?.close(); await group?.close() })

for (const [language, letter] of [['中文', '因'], ['英文', 'R']]) {
  test(`${language}组织字段 API 超一字精确提示，拒绝后记录及审计不变`, async () => {
    const before = await read(managed.id)
    const beforeAudits = await audits(managed.id)
    const treeBefore = (await organizations(group)).map(item => item.id).sort()
    const valid = { name: '组织边界', short_name: '简称', manager: '负责人', contact: '13800000018', notes: '备注' }
    const cases: [string, string, string][] = [
      ['name', letter.repeat(256), '组织名称'], ['short_name', letter.repeat(129), '组织简称'],
      ['manager', letter.repeat(129), '负责人'], ['notes', letter.repeat(5001), '备注'], ['contact', '1'.repeat(65), '联系方式'],
    ]
    for (const [field, value, label] of cases) {
      for (const method of ['POST', 'PATCH']) {
        const endpoint = `/api/v1/organizations${method === 'PATCH' ? '/' + managed.id : ''}`
        const data = { ...valid, parent_id: managed.id, type: 'department', code: `INVALID_${stamp}_${++sequence}`, [field]: value }
        const response = await child.request.fetch(endpoint, { method, data })
        expect((await body(response, 400)).error.message).toContain(label)
        expect(await read(managed.id)).toEqual(before)
        expect(await audits(managed.id)).toEqual(beforeAudits)
        expect((await organizations(group)).map(item => item.id).sort()).toEqual(treeBefore)
      }
    }
    const invalidCode = await child.request.post('/api/v1/organizations', { data: { ...valid, parent_id: managed.id, type: 'department', code: 'A'.repeat(65) } })
    expect((await body(invalidCode, 400)).error.message).toContain('组织编码')
    const denied = await child.request.patch(`/api/v1/organizations/${outside.id}`, { data: valid })
    await body(denied, 403)
    expect(await read(outside.id)).toMatchObject({ name: outside.name })
  })
  for (const role of ['集团管理员', '子管理员']) {
    test(`${language}${role}组织新增与编辑：255/128/5000 字完整保存、空白和电话不请求`, async () => {
      const context = role === '集团管理员' ? group : child
      const page = await context.newPage()
      try {
        await page.goto('/organizations')
        await page.getByRole('button', { name: '新增组织', exact: true }).click()
        const dialog = page.getByRole('dialog', { name: '新增组织机构', exact: true })
        const code = `ORG_${stamp}_${++sequence}`.padEnd(64, 'A')
        await dialog.getByLabel('组织编码', { exact: true }).fill(code + 'X')
        await expect(dialog.getByLabel('组织编码', { exact: true })).toHaveValue(code)
        const data = values(letter, code.slice(0, 36))
        await fill(dialog, data)
        await expectNoInvalidMutation(page, dialog, '确认新增', '/api/v1/organizations', '组织名称')
        await dialog.getByLabel('组织名称', { exact: true }).fill(data.name)
        await dialog.getByLabel('联系方式', { exact: true }).fill(data.contact)
        const created = await save(page, dialog, '确认新增', '/api/v1/organizations', 'POST', 201)
        expect(await read(created.id)).toMatchObject({ ...data, code, contact: '0755888866668001', parent_id: role === '集团管理员' ? root.id : managed.id })
        const createAudits = await audits(created.id)
        expect(createAudits.filter((event: { action: string }) => event.action === 'organization.create')).toHaveLength(1)
        expect(createAudits[0].details.name).toBe(data.name)
        await page.reload()
        const edit = await openEdit(page, created)
        for (const [field, label] of Object.entries({ name: '组织名称', short_name: '组织简称', manager: '负责人', notes: '备注' })) await expect(edit.getByLabel(label, { exact: true })).toHaveValue(data[field as keyof typeof data])
        const updated = values(letter === '因' ? '改' : 'S', code.slice(0, 36))
        await fill(edit, updated)
        await expectNoInvalidMutation(page, edit, '保存基本信息', `/api/v1/organizations/${created.id}`, '组织名称')
        await edit.getByLabel('组织名称', { exact: true }).fill(updated.name)
        await edit.getByLabel('联系方式', { exact: true }).fill(updated.contact)
        await save(page, edit, '保存基本信息', `/api/v1/organizations/${created.id}`, 'PATCH', 200)
        expect(await read(created.id)).toMatchObject({ ...updated, code, contact: '0755888866668001' })
        const events = (await audits(created.id)).filter((event: { action: string }) => event.action === 'organization.update')
        expect(events).toHaveLength(1)
        expect(events[0].details).toMatchObject({ name: updated.name, short_name: updated.short_name, manager: updated.manager, contact: '0755888866668001' })
      } finally { await page.close() }
    })
  }
}

test('集团信息中文满额编辑与恢复，空白和非法电话不发送请求', async () => {
  const page = await group.newPage()
  const before = await read(root.id)
  try {
    await page.goto('/organizations')
    await page.getByRole('button', { name: '编辑集团信息', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: '编辑集团基本信息', exact: true })
    const data = values('集', `group_${stamp}`)
    await fill(dialog, data, true)
    await expectNoInvalidMutation(page, dialog, '保存集团信息', `/api/v1/organizations/${root.id}`, '集团名称')
    await dialog.getByLabel('集团名称', { exact: true }).fill(data.name)
    await dialog.getByLabel('联系方式', { exact: true }).fill(data.contact)
    await save(page, dialog, '保存集团信息', `/api/v1/organizations/${root.id}`, 'PATCH', 200)
    expect(await read(root.id)).toMatchObject({ ...data, contact: '0755888866668001' })
    await page.reload()
    await page.getByRole('button', { name: '编辑集团信息', exact: true }).click()
    for (const [field, label] of Object.entries({ name: '集团名称', short_name: '集团简称', manager: '集团负责人', notes: '集团说明' })) await expect(dialog.getByLabel(label, { exact: true })).toHaveValue(data[field as keyof typeof data])
  } finally {
    await body(await group.request.patch(`/api/v1/organizations/${root.id}`, { data: before }))
    const restored = await read(root.id)
    // Optional empty fields are omitted from the JSON response.
    for (const field of ['name', 'short_name', 'manager', 'contact', 'notes', 'code'] as const) expect(restored[field] ?? '').toBe(before[field] ?? '')
    await page.close()
  }
})
