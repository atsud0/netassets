import { expect, test, type APIResponse, type BrowserContext, type Locator, type Page, type Response } from '@playwright/test'
import { writeFile } from 'node:fs/promises'

async function body(response: APIResponse | Response, status = 200) {
  expect(response.status()).toBe(status)
  return status === 204 ? null : response.json()
}
function formItem(dialog: Locator, page: Page, label: string) {
  return dialog.locator('.el-form-item').filter({ has: page.getByText(label, { exact: true }) })
}

for (const mode of ['finding', 'order'] as const) {
  test(`真实安全运营员${mode === 'finding' ? '安全发现' : '整改工单'}文本：Unicode完整边界、超限零请求、字段提示及越权不泄露`, async ({ browser, baseURL }, info) => {
    test.setTimeout(300_000)
    test.skip(!process.env.E2E_PASSWORD, 'E2E_PASSWORD 未配置')
    const contexts: BrowserContext[] = []
    const errors: string[] = []
    const evidence: Record<string, unknown> = { mode, created: [], rejections: [] }
    try {
      const admin = await browser.newContext({ baseURL }); contexts.push(admin)
      await body(await admin.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }))
      const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 7)}`
      const root = (await body(await admin.request.get('/api/v1/organizations?page=1&page_size=100'))).items.find((item: { type: string }) => item.type === 'group')
      expect(root).toBeTruthy()
      const org = (await body(await admin.request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `SEC_TEXT_${stamp}`, name: `安全字段_${stamp}`, type: 'subsidiary' } }), 201)).item
      const asset = (await body(await admin.request.post('/api/v1/assets', { data: { type: 'domain', value: `sec-text-${stamp.replaceAll('_', '-')}.example.com`, organization_id: org.id, environment: 'testing', network_scope: 'public', status: 'active', importance: 'normal', direct_change_reason: '隔离库安全文字边界验证' } }), 201)).item
      const roles = (await body(await admin.request.get('/api/v1/roles?page=1&page_size=100'))).items
      async function actor(key: string, global: boolean) {
        const role = roles.find((item: { key: string }) => item.key === key)
        expect(role).toBeTruthy()
        const username = `${global ? 'sec_text_op' : 'sec_text_child'}_${stamp}`, password = 'SecurityText123!'
        const user = (await body(await admin.request.post('/api/v1/users', { data: { username, password, display_name: `安全字段${key}`, global_role_ids: global ? [role.id] : [], appointments: global ? [] : [{ organization_id: org.id, is_primary: true, assignments: [{ role_id: role.id, data_scope: 'organization' }] }] } }), 201)).item
        const context = await browser.newContext({ baseURL }); contexts.push(context)
        const login = await body(await context.request.post('/api/v1/auth/login', { data: { username, password } }))
        expect(login.user.roles).toEqual([key])
        if (login.user.must_change_password) await body(await context.request.put('/api/v1/auth/profile/password', { data: { current_password: password, new_password: `${password}Changed` } }), 204)
        return { context, id: user.id }
      }
      const operator = await actor('security_operator', true), child = await actor('organization_admin', false)
      const findingPayload = { title: `字段工单前置_${stamp}`, severity: 'high', status: 'pending', assignee_organization_id: org.id, asset_id: asset.id, description: '合法漏洞描述', source: 'manual', discovered_at: new Date().toISOString() }
      const finding = (await body(await operator.context.request.post('/api/v1/security-findings', { data: findingPayload }), 201)).item
      const orderPayload = { title: '合法工单标题', assignee_organization_id: org.id, description: '', remediation_requirement: '合法整改要求', due_at: new Date(Date.now() + 86400000).toISOString(), finding_ids: [finding.id], execution_mode: 'organization' }
      Object.assign(evidence, { org: { id: org.id, code: org.code }, asset_id: asset.id, operator_id: operator.id, child_id: child.id, initial_finding_id: finding.id })
      const page = await operator.context.newPage()
      page.on('pageerror', error => errors.push(error.message))
      const path = mode === 'finding' ? '/api/v1/security-findings' : '/api/v1/security-work-orders'
      let writes = 0
      page.on('request', request => { if (new URL(request.url()).pathname === path && request.method() === 'POST') writes++ })
      const fields: Array<[string, string, number, string]> = mode === 'finding' ? [
        ['title', '漏洞标题', 255, '漏洞标题'], ['vulnerability_type', '漏洞类型', 128, '漏洞类型'], ['description', '漏洞描述', 20000, '漏洞描述'],
        ['evidence', '验证证据', 30000, '漏洞证据'], ['remediation_advice', '整改建议', 20000, '修复建议'],
      ] : [['title', '工单标题', 255, '工单标题'], ['description', '背景说明', 10000, '工单说明'], ['remediation_requirement', '整改要求', 20000, '整改要求']]
      for (const [index, unit] of ['🙂', 'a', '中'].entries()) {
        // Active draft work orders reserve findings too; use an independent finding per order.
        const linkedFinding = mode === 'order' && index > 0 ? (await body(await operator.context.request.post('/api/v1/security-findings', { data: { ...findingPayload, title: `${findingPayload.title}_${index}` } }), 201)).item : finding
        await page.goto('/security-work-orders?tab=findings')
        await page.getByRole('button', { name: mode === 'finding' ? '登记安全发现' : '新建整改工单', exact: true }).click()
        const dialog = page.getByRole('dialog', { name: mode === 'finding' ? '登记安全发现' : '新建安全整改工单', exact: true })
        await formItem(dialog, page, '责任组织').locator('.el-select').click()
        await page.getByRole('option', { name: org.name, exact: true }).click()
        await formItem(dialog, page, mode === 'finding' ? '漏洞标题' : '工单标题').locator('input').click()
        const select = formItem(dialog, page, mode === 'finding' ? '关联网络资产' : '关联安全发现').locator('.el-select')
        await select.click()
        await page.getByRole('option').filter({ hasText: mode === 'finding' ? asset.value : linkedFinding.title }).click()
        await formItem(dialog, page, mode === 'finding' ? '漏洞标题' : '工单标题').locator('input').click()
        if (mode === 'order') {
          await formItem(dialog, page, '下发方式').locator('.el-switch').click()
          await expect(formItem(dialog, page, '下发方式').getByRole('switch')).not.toBeChecked()
        }
        // Put the known UTF-16 truncation first, before other rejection checks.
        const titleInput = formItem(dialog, page, mode === 'finding' ? '漏洞标题' : '工单标题').locator('input')
        await titleInput.fill(unit.repeat(255))
        await expect(titleInput).toHaveValue(unit.repeat(255))
        const save = dialog.getByRole('button', { name: mode === 'finding' ? '保存' : '保存草稿', exact: true })
        const setBase = async () => {
          for (const [field, label] of fields) await formItem(dialog, page, label).locator('input,textarea').fill(field === 'title' ? `合法${mode}_${stamp}_${index}` : (field === 'description' || field === 'remediation_requirement') ? '合法必填内容' : '')
        }
        for (const [field, label, limit, errorLabel] of fields) {
          await setBase()
          const input = formItem(dialog, page, label).locator('input,textarea'), value = unit.repeat(limit + 1)
          await input.fill(value)
          const before = writes
          await save.click()
          await expect(dialog.getByText(`${errorLabel}最多 ${limit} 字`, { exact: true })).toBeVisible()
          await expect(input).toHaveValue(value)
          await expect(dialog).toBeVisible()
          expect(writes).toBe(before)
          ;(evidence.rejections as unknown[]).push({ kind: 'ui', field, unit, limit })
        }
        for (const [field, label] of fields.filter(([field]) => field === 'title' || field === (mode === 'finding' ? 'description' : 'remediation_requirement'))) {
          await setBase()
          await formItem(dialog, page, label).locator('input,textarea').fill('　 \n ')
          const before = writes
          await save.click()
          await expect(dialog.getByText(`请输入${label}`, { exact: true })).toBeVisible()
          expect(writes).toBe(before)
        }
        for (const [, label, limit] of fields) await formItem(dialog, page, label).locator('input,textarea').fill(unit.repeat(limit))
        const response = page.waitForResponse(response => new URL(response.url()).pathname === path && response.request().method() === 'POST')
        await save.click()
        const created = (await body(await response, 201)).item
        await expect(dialog).toBeHidden()
        const saved = (await body(await operator.context.request.get(`${path}/${created.id}`))).item
        for (const [field,,limit] of fields) expect(saved[field]).toBe(unit.repeat(limit))
        if (mode === 'order') expect(saved.status).toBe('draft')
        ;(evidence.created as unknown[]).push({ id: saved.id, unit, lengths: Object.fromEntries(fields.map(([field,,limit]) => [field,limit])) })
      }
      const snapshotPath = `${path}?organization_id=${org.id}&page=1&page_size=100`
      const before = await body(await operator.context.request.get(snapshotPath))
      const base = mode === 'finding' ? findingPayload : orderPayload
      const invalid: Array<[string, unknown, string]> = fields.map(([field,,limit,errorLabel]) => [field, '中'.repeat(limit + 1), `${errorLabel}最多 ${limit} 字`])
      invalid.push(['title','　 ', mode === 'finding' ? '漏洞标题不能为空' : '工单标题不能为空'])
      if (mode === 'finding') invalid.push(['description','　 ','漏洞描述不能为空'], ['severity','bad','请选择有效的严重等级'], ['asset_id','bad','关联网络资产标识不正确'])
      else invalid.push(['remediation_requirement','　 ','整改要求不能为空'], ['finding_ids',[],'请至少选择一条安全发现'], ['execution_mode','bad','请选择有效的工单执行方式'], ['due_at','0001-01-01T00:00:00Z','请选择整改截止时间'])
      for (const [field, value, message] of invalid) {
        const data = { ...base, [field]: value }
        const rejected = await body(await operator.context.request.post(path, { data }), 400)
        expect(rejected.error).toMatchObject({ code: mode === 'finding' ? 'invalid_finding' : 'invalid_work_order', message })
        const denied = await body(await child.context.request.post(path, { data }), 403)
        expect(denied.error.code).toBe('forbidden')
        expect(denied.error.message).not.toBe(message)
        if (mode === 'finding') {
          const updated = await body(await operator.context.request.patch(`${path}/${finding.id}`, { data: { ...data, expected_updated_at: finding.updated_at } }), 400)
          expect(updated.error.message).toBe(message)
        }
        ;(evidence.rejections as unknown[]).push({ kind: 'api', field, message })
      }
      expect(await body(await operator.context.request.get(snapshotPath))).toEqual(before)
      expect((await body(await operator.context.request.get(`/api/v1/security-findings/${finding.id}`))).item).toEqual(finding)
      expect(errors).toEqual([])
    } finally {
      evidence.page_errors = errors
      await writeFile(info.outputPath('security-text-evidence.json'), JSON.stringify(evidence, null, 2))
      for (const context of contexts.reverse()) await context.close()
    }
  })
}
