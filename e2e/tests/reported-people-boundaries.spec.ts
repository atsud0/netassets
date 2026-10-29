import { expect, test, type APIRequestContext, type APIResponse, type BrowserContext, type Locator, type Page } from '@playwright/test'

test.setTimeout(90_000)
const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
let group: BrowserContext
let child: BrowserContext
let org: { id: string; name: string }
let systemID: string
let vendorID: string
const until = new Date(Date.now() + 30 * 86400000).toISOString()
let sequence = 0
async function body(response: APIResponse, status = 200) {
  const text = await response.text()
  expect(response.status(), text).toBe(status)
  return text ? JSON.parse(text) : null
}
async function audit(id: string, action: string) {
  const result = await body(await group.request.get(`/api/v1/audit-logs?action=remediation.${action}&q=${id}&page_size=100`))
  return result.items.filter((item: { resource_id: string }) => item.resource_id === id)
}
async function record(request: APIRequestContext, resource: string, id: string) {
  for (let page = 1; ; page++) {
    const result = await body(await request.get(`/api/v1/${resource}?page_size=100&page=${page}`))
    const item = result.items.find((item: { id: string }) => item.id === id)
    if (item) return item
    if (page * 100 >= result.total) break
  }
  throw new Error(`${resource} 缺少 ${id}`)
}
async function person(invitation = false, participantType: 'internal' | 'external' = 'external') {
  const tag = `${stamp}_${++sequence}`
  const result = await body(await child.request.post(`/api/v1/${invitation ? 'remediation-invitations' : 'remediation-grants'}`, { data: {
    organization_id: org.id, business_system_id: systemID, vendor_id: participantType === 'external' ? vendorID : '', participant_type: participantType,
    username: `boundary_${tag}`, display_name: `边界人员_${tag}`, password: 'BoundaryFixture123!', valid_until: until,
  } }))
  return { id: result.id, name: `边界人员_${tag}` }
}
async function openAction(page: Page, name: string, action: string) {
  const row = page.locator('.el-table__body-wrapper tr').filter({ hasText: name })
  await expect(row).toHaveCount(1)
  await row.getByRole('button', { name: /^操作：/ }).click()
  await page.locator('.row-actions-panel:visible').getByRole('button', { name: action, exact: true }).click()
}
async function invalidPrompt(dialog: Locator, value: string) {
  await dialog.locator('textarea').fill(value)
  await dialog.getByRole('button', { name: '确定', exact: true }).click()
  await expect(dialog).toBeVisible()
  await expect(dialog).toContainText('撤销原因：请输入 1 至 2000 字')
}

test.beforeAll(async ({ browser, baseURL }) => {
  test.skip(!process.env.E2E_PASSWORD, 'E2E_PASSWORD 未配置')
  group = await browser.newContext({ baseURL })
  await body(await group.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }))
  const roots = await body(await group.request.get('/api/v1/organizations?page_size=100'))
  const root = roots.items.find((item: { type: string }) => item.type === 'group')
  org = (await body(await group.request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `PEOPLE_${stamp}`, name: `人员边界组织_${stamp}`, type: 'subsidiary' } }), 201)).item
  systemID = (await body(await group.request.post('/api/v1/business-systems', { data: { code: `PEOPLE_${stamp}`, name: `人员边界系统_${stamp}`, organization_id: org.id, status: 'running', level: 'normal', internet_exposure: 'unknown', contains_personal_information: 'unknown', contains_sensitive_personal_information: 'unknown', data_volume_over_100k: 'unknown', contains_important_data: 'unknown', contains_core_data: 'unknown' } }), 201)).item.id
  vendorID = (await body(await group.request.post('/api/v1/vendors', { data: { organization_id: org.id, name: `人员边界厂商_${stamp}`, contact: '13800000018', status: 'enabled' } }))).id
  const roles = await body(await group.request.get('/api/v1/roles?page_size=100'))
  const role = roles.items.find((item: { key: string }) => item.key === 'organization_admin')
  const password = 'PeopleBoundary123!'
  const username = `people_admin_${stamp}`
  await body(await group.request.post('/api/v1/users', { data: { username, password, display_name: '人员边界子管理员', global_role_ids: [], appointments: [{ organization_id: org.id, is_primary: true, assignments: [{ role_id: role.id, data_scope: 'organization' }] }] } }), 201)
  child = await browser.newContext({ baseURL })
  const login = await body(await child.request.post('/api/v1/auth/login', { data: { username, password } }))
  expect(login.user.roles).toContain('organization_admin')
  expect(login.user.roles).not.toContain('group_admin')
  if (login.user.must_change_password) await body(await child.request.put('/api/v1/auth/profile/password', { data: { current_password: password, new_password: `${password}Changed` } }), 204)
})
test.afterAll(async () => { await child?.close(); await group?.close() })

for (const [language, letter] of [['中文', '因'], ['英文', 'R']]) {
  for (const invitation of [false, true]) {
    test(`${language}撤销${invitation ? '邀请' : '协作授权'}：空值与 2001 字不请求，2000 字完整保存`, async () => {
      const target = await person(invitation)
      const resource = invitation ? 'remediation-invitations' : 'remediation-grants'
      const action = invitation ? 'invitation.revoke' : 'grant.update'
      const endpoint = `/api/v1/${resource}/${target.id}${invitation ? '/revoke' : ''}`
      const method = invitation ? 'POST' : 'PATCH'
      const data = (reason: string) => invitation ? { reason } : { status: 'revoked', reason }
      const before = await record(child.request, resource, target.id)
      for (const reason of ['', letter.repeat(2001)]) {
        const invalid = await child.request.fetch(endpoint, { method, data: data(reason) })
        expect((await body(invalid, 400)).error.message).toContain('撤销原因')
        expect(await record(child.request, resource, target.id)).toEqual(before)
        expect(await audit(target.id, action)).toHaveLength(0)
      }
      const page = await child.newPage()
      try {
        await page.goto('/remediation-people')
        if (invitation) {
          await page.getByRole('tab', { name: '邀请记录', exact: true }).click()
          await page.locator('.el-table__body-wrapper tr').filter({ hasText: target.name }).getByRole('button', { name: '撤销邀请', exact: true }).click()
        } else await openAction(page, target.name, '撤销')
        const dialog = page.getByRole('dialog', { name: invitation ? '撤销邀请' : '撤销协作授权', exact: true })
        let mutations = 0
        page.on('request', req => { if (req.url().endsWith(endpoint) && req.method() === method) mutations++ })
        await invalidPrompt(dialog, '   ')
        await invalidPrompt(dialog, letter.repeat(2001))
        expect(mutations).toBe(0)
        const reason = letter.repeat(2000)
        await dialog.locator('textarea').fill(reason)
        const response = page.waitForResponse(res => res.url().endsWith(endpoint) && res.request().method() === method)
        await dialog.getByRole('button', { name: '确定', exact: true }).click()
        await body(await response, 204)
        await expect(dialog).toBeHidden()
        expect(mutations).toBe(1)
        const saved = await record(child.request, resource, target.id)
        if (invitation) expect(saved.revoked_at).toBeTruthy()
        else expect(saved).toMatchObject({ status: 'revoked', available: false, valid_until: before.valid_until })
        const events = await audit(target.id, action)
        expect(events).toHaveLength(1)
        expect(events[0].details.reason).toBe(reason)
        if (invitation) {
          const duplicate = await child.request.post(endpoint, { data: { reason: '不能覆盖首次原因' } })
          await body(duplicate, 404)
          expect(await audit(target.id, action)).toEqual(events)
        }
      } finally { await page.close() }
    })
  }
  for (const [participantType, participantLabel] of [['external', '外包'], ['internal', '内部']] as const) {
    test(`${language}${participantLabel}人员续期：空原因精确提示，2000 字保存并读回期限与审计`, async () => {
      const target = await person(false, participantType)
      const endpoint = `/api/v1/remediation-grants/${target.id}`
      const before = await record(child.request, 'remediation-grants', target.id)
      const newUntil = new Date(Date.now() + 60 * 86400000).toISOString()
      const invalid = await child.request.patch(endpoint, { data: { status: 'enabled', valid_until: newUntil, reason: letter.repeat(2001) } })
      expect((await body(invalid, 400)).error.message).toContain('变更原因')
      expect(await record(child.request, 'remediation-grants', target.id)).toEqual(before)
      const page = await child.newPage()
      try {
        await page.goto('/remediation-people')
        await openAction(page, target.name, '续期')
        const dialog = page.getByRole('dialog', { name: '更新协作授权', exact: true })
        let mutations = 0
        page.on('request', req => { if (req.url().endsWith(endpoint) && req.method() === 'PATCH') mutations++ })
        await dialog.getByRole('button', { name: '保存授权', exact: true }).click()
        await expect(dialog).toContainText('请输入变更原因')
        expect(mutations).toBe(0)
        const reason = letter.repeat(2000)
        const input = dialog.getByLabel('变更原因', { exact: true })
        await input.fill(reason + letter)
        await expect(input).toHaveValue(reason)
        const dateInput = dialog.getByLabel('新的有效期', { exact: true })
        await dateInput.fill(newUntil.slice(0, 19).replace('T', ' '))
        await dateInput.press('Enter')
        // Enter commits and closes Element Plus' datetime picker; Escape can close the parent dialog.
        await expect(dateInput).toHaveValue(newUntil.slice(0, 19).replace('T', ' '))
        const response = page.waitForResponse(res => res.url().endsWith(endpoint) && res.request().method() === 'PATCH')
        await dialog.getByRole('button', { name: '保存授权', exact: true }).click()
        const res = await response
        const requestedUntil = res.request().postDataJSON().valid_until
        expect(new Date(requestedUntil).getTime()).toBeGreaterThan(new Date(until).getTime())
        await body(res, 204)
        await expect(dialog).toBeHidden()
        const saved = await record(child.request, 'remediation-grants', target.id)
        expect(saved).toMatchObject({ status: 'enabled', available: true, participant_type: participantType })
        if (participantType === 'internal') expect(saved.vendor_id).toBeNull()
        expect(new Date(saved.valid_until).getTime()).toBe(new Date(requestedUntil).getTime())
        const events = await audit(target.id, 'grant.update')
        expect(events).toHaveLength(1)
        expect(events[0].details.reason).toBe(reason)
      } finally { await page.close() }
    })
  }
  test(`${language}合作厂商：空字段及电话提示正确，255 字名称完整保存`, async () => {
    const page = await group.newPage()
    const prefix = `合作_${stamp}_${language}_`
    const name = prefix + letter.repeat(255 - prefix.length)
    try {
      await page.goto('/remediation-people')
      await page.getByRole('tab', { name: '合作厂商', exact: true }).click()
      await page.getByRole('button', { name: '登记厂商', exact: true }).click()
      const dialog = page.getByRole('dialog', { name: '合作厂商', exact: true })
      let mutations = 0
      page.on('request', req => { if (req.url().endsWith('/api/v1/vendors') && req.method() === 'POST') mutations++ })
      await dialog.getByRole('button', { name: '保存', exact: true }).click()
      await expect(dialog).toContainText('请选择合作组织')
      await expect(dialog).toContainText('请输入厂商名称')
      const organizationSelect = dialog.locator('.el-form-item').filter({ hasText: '合作组织' }).locator('.el-select')
      await organizationSelect.click()
      await organizationSelect.getByRole('combobox').fill(org.name)
      await page.getByRole('option', { name: org.name, exact: true }).click()
      await dialog.getByLabel('厂商名称', { exact: true }).fill(name + letter)
      await expect(dialog.getByLabel('厂商名称', { exact: true })).toHaveValue(name)
      const phone = dialog.getByLabel('联系方式', { exact: true })
      await phone.fill('x'.repeat(65))
      await expect(phone).toHaveValue('x'.repeat(64))
      await dialog.getByRole('button', { name: '保存', exact: true }).click()
      await expect(dialog).toContainText('请填写手机号码、座机号码或分机号')
      expect(mutations).toBe(0)
      await phone.fill('0755-88886666-8001')
      const response = page.waitForResponse(res => res.url().endsWith('/api/v1/vendors') && res.request().method() === 'POST')
      await dialog.getByRole('button', { name: '保存', exact: true }).click()
      const created = await body(await response)
      await expect(dialog).toBeHidden()
      const saved = await record(child.request, 'vendors', created.id)
      expect(saved).toMatchObject({ name, contact: '0755888866668001', organization_id: org.id, status: 'enabled' })
      const events = await audit(created.id, 'vendor.save')
      expect(events).toHaveLength(1)
      expect(events[0].details.after).toMatchObject({ name, contact: saved.contact })
      for (const data of [{ name: name + letter, contact: saved.contact }, { name, contact: '1'.repeat(65) }]) {
        const invalid = await group.request.patch(`/api/v1/vendors/${created.id}`, { data: { ...data, organization_id: org.id, status: 'enabled' } })
        expect((await body(invalid, 400)).error.message).toContain(data.name.length > 255 ? '厂商名称' : '联系方式')
        expect(await record(child.request, 'vendors', created.id)).toEqual(saved)
        expect(await audit(created.id, 'vendor.save')).toEqual(events)
      }
    } finally { await page.close() }
  })
  test(`${language}集团厂商：空字段提示正确，255 字名称及 2000 字原因完整保存`, async () => {
    const page = await group.newPage()
    const prefix = `厂商_${stamp}_${language}_`
    const name = prefix + letter.repeat(255 - prefix.length)
    const reason = letter.repeat(2000)
    try {
      await page.goto('/remediation-people')
      await page.getByRole('tab', { name: '厂商目录与规则', exact: true }).click()
      await page.getByRole('button', { name: '新增集团厂商', exact: true }).click()
      const dialog = page.getByRole('dialog', { name: '集团厂商档案', exact: true })
      let mutations = 0
      page.on('request', req => { if (req.url().endsWith('/api/v1/vendor-catalog') && req.method() === 'POST') mutations++ })
      await dialog.getByRole('button', { name: '保存集团档案', exact: true }).click()
      await expect(page.getByText('请输入统一厂商名称', { exact: true })).toBeVisible()
      await dialog.getByLabel('统一厂商名称', { exact: true }).fill(name + letter)
      await expect(dialog.getByLabel('统一厂商名称', { exact: true })).toHaveValue(name)
      await dialog.getByRole('button', { name: '保存集团档案', exact: true }).click()
      await expect(page.getByText('请输入变更原因', { exact: true })).toBeVisible()
      expect(mutations).toBe(0)
      await dialog.getByLabel('变更原因', { exact: true }).fill(reason + letter)
      await expect(dialog.getByLabel('变更原因', { exact: true })).toHaveValue(reason)
      const response = page.waitForResponse(res => res.url().endsWith('/api/v1/vendor-catalog') && res.request().method() === 'POST')
      await dialog.getByRole('button', { name: '保存集团档案', exact: true }).click()
      const created = await body(await response)
      await expect(dialog).toBeHidden()
      expect(await record(group.request, 'vendor-catalog', created.id)).toMatchObject({ name, status: 'enabled' })
      const events = await audit(created.id, 'catalog.save')
      expect(events).toHaveLength(1)
      expect(events[0].details).toMatchObject({ after: { name, status: 'enabled' }, reason })
      // Updating the same resource avoids relying on name-sorted first-page results.
      for (const data of [{ name: name + letter, reason }, { name, reason: reason + letter }]) {
        const invalid = await group.request.patch(`/api/v1/vendor-catalog/${created.id}`, { data: { ...data, status: 'enabled' } })
        expect((await body(invalid, 400)).error.message).toContain(data.name.length > 255 ? '统一厂商名称' : '变更原因')
        expect(await audit(created.id, 'catalog.save')).toEqual(events)
        expect(await record(group.request, 'vendor-catalog', created.id)).toMatchObject({ name, status: 'enabled' })
      }
      const denied = await child.request.patch(`/api/v1/vendor-catalog/${created.id}`, { data: { name, reason: '非集团管理员拒绝', status: 'disabled' } })
      await body(denied, 403)
      expect(await audit(created.id, 'catalog.save')).toEqual(events)
    } finally { await page.close() }
  })
}
