import { expect, test, type APIRequestContext, type APIResponse, type BrowserContext } from '@playwright/test'
import { writeFile } from 'node:fs/promises'
import { clickRowAction } from '../helpers/row-actions'

async function body(response: APIResponse, status = 200) {
  const text = await response.text()
  expect(response.status(), text).toBe(status)
  return text ? JSON.parse(text) : null
}

test('安全测试新建编辑字段错误精确、空白不请求、历史时间与中文英文满额保存且越权不泄露校验', async ({ browser, baseURL }, info) => {
  test.setTimeout(240_000)
  const contexts: BrowserContext[] = []
  const evidence: Record<string, unknown> = { boundaries: [], rejections: [] }
  const pageErrors: string[] = []
  try {
    const admin = await browser.newContext({ baseURL }); contexts.push(admin)
    await body(await admin.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }))
    const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
    const root = (await body(await admin.request.get('/api/v1/organizations/tree'))).items.find((item: { type: string }) => item.type === 'group')
    const org = (await body(await admin.request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `ST_INPUT_${stamp}`, name: `批次字段回归_${stamp}`, type: 'subsidiary' } }), 201)).item
    const asset = (await body(await admin.request.post('/api/v1/assets', { data: { type: 'domain', value: `st-input-${stamp.replaceAll('_', '-')}.example.com`, organization_id: org.id, environment: 'testing', network_scope: 'public', status: 'active', importance: 'normal', direct_change_reason: '安全测试表单回归' } }), 201)).item
    const roles = (await body(await admin.request.get('/api/v1/roles?page=1&page_size=100'))).items
    async function actor(key: string, global: boolean) {
      const role = roles.find((item: { key: string }) => item.key === key)
      expect(role).toBeTruthy()
      const username = `${global ? 'sti_op' : 'sti_child'}_${stamp}`
      const password = 'BatchInput123!'
      await body(await admin.request.post('/api/v1/users', { data: { username, password, display_name: `批次字段_${key}`, global_role_ids: global ? [role.id] : [], appointments: global ? [] : [{ organization_id: org.id, is_primary: true, assignments: [{ role_id: role.id, data_scope: 'organization' }] }] } }), 201)
      const context = await browser.newContext({ baseURL }); contexts.push(context)
      const login = await body(await context.request.post('/api/v1/auth/login', { data: { username, password } }))
      expect(login.user.roles).toEqual([key])
      if (login.user.must_change_password) await body(await context.request.put('/api/v1/auth/profile/password', { data: { current_password: password, new_password: password + 'Changed' } }), 204)
      return context
    }
    const operator = await actor('security_operator', true)
    const child = await actor('organization_admin', false)
    const payload = { batch_name: `字段_${stamp}`, provider: '隔离回归单位', authorization_note: '仅隔离环境授权', notes: '历史补录', test_type: 'manual', start_at: '2020-01-01T08:00:00Z', end_at: '2020-01-02T08:00:00Z', asset_ids: [asset.id] }
    const batch = (await body(await operator.request.post('/api/v1/security-test-batches', { data: payload }), 201)).item
    const path = `/api/v1/security-test-batches/${batch.id}`
    async function snapshot(request: APIRequestContext) {
      return { item: (await body(await request.get(path))).item, events: await body(await request.get(`${path}/events?page=1&page_size=100`)) }
    }
    const before = await snapshot(operator.request)
    const invalid: Array<[string, unknown, string]> = [
      ['batch_name', '　 ', '批次名称不能为空'], ['batch_name', '名'.repeat(256), '批次名称最多 255 字'],
      ['provider', '　 ', '测试单位不能为空'], ['provider', 'P'.repeat(256), '测试单位最多 255 字'],
      ['authorization_note', '\n ', '授权说明不能为空'], ['authorization_note', '授'.repeat(20001), '授权说明最多 20000 字'],
      ['notes', 'T'.repeat(20001), '测试约束最多 20000 字'], ['test_type', 'unknown', '请选择有效的测试类型'],
      ['start_at', '0001-01-01T00:00:00Z', '请选择计划开始时间'], ['end_at', '0001-01-01T00:00:00Z', '请选择计划结束时间'],
      ['end_at', payload.start_at, '计划结束时间必须晚于计划开始时间'], ['asset_ids', [], '请至少选择 1 项公网资产'],
      ['asset_ids', ['invalid'], '公网资产范围包含无效的资产标识'],
      ['asset_ids', Array.from({ length: 201 }, (_, i) => `11111111-1111-4111-8111-${String(i).padStart(12, '0')}`), '公网资产范围最多 200 项'],
    ]
    for (const [field, value, message] of invalid) {
      const data = { ...payload, [field]: value }
      for (const action of ['create', 'update']) {
        const response = action === 'create' ? await operator.request.post('/api/v1/security-test-batches', { data }) : await operator.request.patch(path, { data })
        expect((await body(response, 400)).error).toEqual({ code: 'invalid_security_test_batch', message })
        const denied = action === 'create' ? await child.request.post('/api/v1/security-test-batches', { data }) : await child.request.patch(path, { data })
        expect((await body(denied, 403)).error.code).toBe('forbidden')
        ;(evidence.rejections as unknown[]).push({ field, action, status: 400, message, childStatus: 403 })
      }
    }
    expect(await snapshot(operator.request)).toEqual(before)
    const page = await operator.newPage()
    page.on('pageerror', error => pageErrors.push(error.message))
    await page.goto('/security-test-batches')
    await page.getByTestId('create-security-test-batch').click()
    let saves = 0
    page.on('request', request => { if (['POST', 'PATCH'].includes(request.method()) && /\/api\/v1\/security-test-batches(?:\/[^/]+)?$/.test(new URL(request.url()).pathname)) saves++ })
    const dialog = page.getByRole('dialog', { name: '新建安全测试批次', exact: true })
    for (const label of ['批次名称', '测试单位', '授权说明']) await dialog.getByLabel(label, { exact: true }).fill('　 ')
    await dialog.getByTestId('save-security-test-batch').click()
    for (const message of ['请输入批次名称', '请输入测试单位或团队', '请输入授权说明']) await expect(dialog.getByText(message, { exact: true })).toBeVisible()
    expect(saves).toBe(0)
    await page.screenshot({ path: info.outputPath('security-batch-create-required-errors.png'), fullPage: true })
    await dialog.getByRole('button', { name: '取消', exact: true }).click()
    await clickRowAction(page, page.locator('.el-table__row').filter({ hasText: payload.batch_name }), '编辑')
    const edit = page.getByRole('dialog', { name: '编辑安全测试批次', exact: true })
    for (const label of ['批次名称', '测试单位', '授权说明']) await edit.getByLabel(label, { exact: true }).fill('　 ')
    await edit.getByTestId('save-security-test-batch').click()
    for (const message of ['请输入批次名称', '请输入测试单位或团队', '请输入授权说明']) await expect(edit.getByText(message, { exact: true })).toBeVisible()
    expect(saves).toBe(0)
    expect(await snapshot(operator.request)).toEqual(before)
    await page.screenshot({ path: info.outputPath('security-batch-edit-required-errors.png'), fullPage: true })
    await edit.getByRole('button', { name: '取消', exact: true }).click()
    for (const char of ['测', 'T']) {
      const data = { ...payload, batch_name: char.repeat(255), provider: char.repeat(255), authorization_note: char.repeat(20000), notes: char.repeat(20000) }
      const item = (await body(await operator.request.post('/api/v1/security-test-batches', { data }), 201)).item
      expect(item).toMatchObject({ name: data.batch_name, provider: data.provider, authorization_note: data.authorization_note, notes: data.notes, start_at: payload.start_at, end_at: payload.end_at, asset_count: 1 })
      const changed = (await body(await operator.request.patch(`/api/v1/security-test-batches/${item.id}`, { data: { ...data, notes: data.notes.slice(0, -1) } }))).item
      expect(changed.notes).toBe(data.notes.slice(0, -1))
      ;(evidence.boundaries as unknown[]).push({ id: item.id, char, nameLength: [...item.name].length, authorizationLength: [...item.authorization_note].length, notesLength: [...changed.notes].length })
    }
    evidence.original = before
    evidence.afterRejected = await snapshot(operator.request)
    evidence.pageErrors = pageErrors
    evidence.savesFromInvalidForms = saves
    expect(pageErrors).toEqual([])
    await page.screenshot({ path: info.outputPath('security-batch-input.png'), fullPage: true })
  } finally {
    await writeFile(info.outputPath('security-batch-input-evidence.json'), JSON.stringify(evidence, null, 2))
    for (const context of contexts.reverse()) await context.close()
  }
})
