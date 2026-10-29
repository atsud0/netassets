import { expect, test as base, type APIResponse, type BrowserContext, type Page } from '@playwright/test'
import { writeFile } from 'node:fs/promises'
import { clickRowAction } from '../helpers/row-actions'

// Independent retained fixtures; never reset the database or grant group_admin to the actors.
async function body(response: APIResponse, status = 200) {
  const text = await response.text()
  expect(response.status(), text).toBe(status)
  return text ? JSON.parse(text) : null
}
type Org = { id: string; type: string; children?: Org[] }
function flatten(items: Org[]): Org[] { return items.flatMap(item => [item, ...flatten(item.children ?? [])]) }
type SecurityFixture = {
  group: BrowserContext; operator: BrowserContext; child: BrowserContext; page: Page
  org: { id: string; name: string }; asset: { id: string; value: string }; stamp: string
  evidence: Record<string, unknown>; pageErrors: string[]; serverErrors: string[]
}
const test = base.extend<{ security: SecurityFixture }>({
  security: async ({ browser, baseURL }, use, testInfo) => {
    const contexts: BrowserContext[] = []
    const evidence: Record<string, unknown> = {}
    const pageErrors: string[] = []
    const serverErrors: string[] = []
    let page: Page | undefined
    try {
      const group = await browser.newContext({ baseURL }); contexts.push(group)
      await body(await group.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }))
      const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
      const root = flatten((await body(await group.request.get('/api/v1/organizations/tree'))).items).find(item => item.type === 'group')!
      const org = (await body(await group.request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `SEC_ROLE_${stamp}`, name: `安全角色状态_${stamp}`, type: 'subsidiary' } }), 201)).item
      const asset = (await body(await group.request.post('/api/v1/assets', { data: { type: 'domain', value: `sec-role-${stamp.replaceAll('_', '-')}.example.com`, organization_id: org.id, environment: 'production', network_scope: 'public', status: 'active', importance: 'normal', direct_change_reason: '仅隔离环境角色状态回归' } }), 201)).item
      const roles = (await body(await group.request.get('/api/v1/roles?page=1&page_size=100'))).items
      async function actor(key: string, global: boolean) {
        const role = roles.find((item: { key: string }) => item.key === key)
        expect(role).toBeTruthy()
        const username = `${global ? 'sec_op' : 'sec_child'}_${stamp}`
        const password = 'SecurityRole123!'
        const created = (await body(await group.request.post('/api/v1/users', { data: { username, password, display_name: `安全回归_${key}`, global_role_ids: global ? [role.id] : [], appointments: global ? [] : [{ organization_id: org.id, is_primary: true, assignments: [{ role_id: role.id, data_scope: 'organization' }] }] } }), 201)).item
        const context = await browser.newContext({ baseURL }); contexts.push(context)
        const login = await body(await context.request.post('/api/v1/auth/login', { data: { username, password } }))
        expect(login.user.roles).toContain(key)
        expect(login.user.roles).not.toContain('group_admin')
        if (global) expect(login.user.roles).not.toContain('organization_admin')
        else expect(login.user.roles).not.toContain('security_operator')
        if (login.user.must_change_password) await body(await context.request.put('/api/v1/auth/profile/password', { data: { current_password: password, new_password: password + 'Changed' } }), 204)
        evidence[key] = { id: created.id, roles: login.user.roles }
        return context
      }
      const operator = await actor('security_operator', true)
      const child = await actor('organization_admin', false)
      page = await operator.newPage()
      page.on('pageerror', error => pageErrors.push(error.message))
      page.on('response', response => { if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`) })
      Object.assign(evidence, { stamp, org, asset, pageErrors, serverErrors })
      await use({ group, operator, child, page, org, asset, stamp, evidence, pageErrors, serverErrors })
    } finally {
      if (page && !page.isClosed()) await page.screenshot({ path: testInfo.outputPath('security-role-state.png'), fullPage: true }).catch(() => {})
      await writeFile(testInfo.outputPath('security-role-state-evidence.json'), JSON.stringify(evidence, null, 2))
      for (const context of contexts.reverse()) await context.close()
    }
  },
})
test.setTimeout(240_000)

for (const withBatch of [false, true]) {
  test(`真实安全运营员待关联漏洞${withBatch ? '授权批次' : '无批次'}浏览器关联，子管理员不可处理全局候选`, async ({ security: s }) => {
    const { operator, child, page, org, asset, stamp, evidence } = s
    let batchID = ''
    if (withBatch) {
      const batch = (await body(await operator.request.post('/api/v1/security-test-batches', { data: { batch_name: `安全运营关联_${stamp}`, test_type: 'penetration', provider: '隔离回归', authorization_note: '仅隔离环境验证', start_at: new Date(Date.now() - 3600000).toISOString(), end_at: new Date(Date.now() + 86400000).toISOString(), asset_ids: [asset.id] } }), 201)).item
      batchID = batch.id
      await body(await operator.request.post(`/api/v1/security-test-batches/${batchID}/authorize`, { data: { comment: '真实安全运营员授权' } }))
    }
    const title = `安全运营关联_${stamp}`
    const template = await operator.request.get('/api/v1/security-findings/import-template?format=csv')
    expect(template.status(), await template.text()).toBe(200)
    const header = (await template.text()).replace(/^\uFEFF/, '').split(/\r?\n/)[0]
    const csv = `${header}\n${['domain', `unknown-${stamp.replaceAll('_', '-')}.invalid`, title, 'high', '真实角色关联', '隔离验证', '', '', new Date().toISOString(), 'pending'].join(',')}\n`
    const imported = (await body(await operator.request.post('/api/v1/security-findings/import', { multipart: { file: { name: 'security-role-candidate.csv', mimeType: 'text/csv', buffer: Buffer.from(csv) }, ...(batchID ? { test_batch_id: batchID } : {}) } }), 201)).item
    expect(imported).toMatchObject({ total_count: 1, pending_count: 1, success_count: 0, failure_count: 0 })
    const candidatePath = `/api/v1/security-findings/import-candidates?status=pending&keyword=${encodeURIComponent(title)}&page=1&page_size=20`
    const candidates = (await body(await operator.request.get(candidatePath))).items
    expect(candidates).toHaveLength(1)
    const candidate = candidates[0]
    expect(candidate.title).toBe(title)
    Object.assign(evidence, { withBatch, batchID, imported, candidate })
    const options = (await body(await operator.request.get(`/api/v1/security-findings/import-candidates/${candidate.id}/scope-assets?keyword=${encodeURIComponent(asset.value)}`))).items
    const option = options.find((item: { id: string; asset_id: string }) => withBatch ? item.asset_id === asset.id : item.id === asset.id)
    expect(option).toBeTruthy()
    const linkData = withBatch ? { scope_asset_id: option.id } : { asset_id: asset.id }
    for (const path of ['/api/v1/security-findings/import-candidates?status=pending', `/api/v1/security-findings/import-candidates/${candidate.id}/scope-assets`]) {
      expect((await body(await child.request.get(path), 403)).error.code).toBe('forbidden')
    }
    expect((await body(await child.request.post(`/api/v1/security-findings/import-candidates/${candidate.id}/link`, { data: { ...linkData, review_note: '子管理员不可处理全局候选' } }), 403)).error.code).toBe('forbidden')
    const before = (await body(await operator.request.get(candidatePath))).items[0]
    expect(before).toEqual(candidate)
    evidence.childCandidateDenied = true
    await page.goto('/security-work-orders?tab=candidates')
    await page.getByPlaceholder('资产值或漏洞标题').fill(title)
    await page.getByPlaceholder('资产值或漏洞标题').press('Enter')
    await clickRowAction(page, page.locator('.el-table__body-wrapper tr').filter({ hasText: title }), '关联资产')
    const dialog = page.getByRole('dialog', { name: '关联漏洞到资产', exact: true })
    await dialog.locator('.el-select').click()
    await dialog.locator('.el-select input').fill(asset.value)
    await page.locator('.el-select-dropdown:visible .el-select-dropdown__item').filter({ hasText: asset.value }).click()
    const note = '联'.repeat(2000)
    await dialog.locator('textarea').fill(note)
    const linkedResponse = page.waitForResponse(response => response.url().includes(`/security-findings/import-candidates/${candidate.id}/link`) && response.request().method() === 'POST')
    await dialog.getByRole('button', { name: '确认关联并生成安全发现', exact: true }).click()
    const linked = (await body(await linkedResponse)).item
    expect(linked).toMatchObject({ status: 'imported', matched_asset_id: asset.id, review_note: note })
    expect(linked.matched_scope_asset_id || '').toBe(withBatch ? option.id : '')
    await expect(dialog).toBeHidden()
    const finding = (await body(await operator.request.get(`/api/v1/security-findings/${linked.imported_finding_id}`))).item
    expect(finding).toMatchObject({ title, asset_id: asset.id, assignee_organization_id: org.id, status: 'pending' })
    expect(finding.test_batch_id || '').toBe(batchID)
    expect((await body(await operator.request.post(`/api/v1/security-findings/import-candidates/${candidate.id}/link`, { data: linkData }), 409)).error.code).toBe('invalid_candidate_state')
    const history = (await body(await operator.request.get('/api/v1/security-findings/imports?page=1&page_size=100'))).items.find((item: { id: string }) => item.id === imported.id)
    expect(history.pending_count).toBe(0)
    Object.assign(evidence, { linked, finding, history })
    expect(s.pageErrors).toEqual([])
    expect(s.serverErrors).toEqual([])
  })
}

test('真实安全运营员风险状态矩阵、活跃工单冲突与取消后浏览器接受风险；子管理员拒绝且不改数据', async ({ security: s }, testInfo) => {
  const { operator, child, page, org, asset, stamp, evidence } = s
  async function finding(status: string, suffix: string) {
    return (await body(await operator.request.post('/api/v1/security-findings', { data: { title: `安全运营风险_${suffix}_${stamp}`, severity: 'high', status, assignee_organization_id: org.id, asset_id: asset.id, description: '隔离环境验证权限与状态', source: 'manual', discovered_at: new Date().toISOString() } }), 201)).item
  }
  const riskData = (item: { updated_at: string }, reason = '真实安全运营员接受风险') => ({ reason, accepted_until: new Date(Date.now() + 86400000).toISOString(), expected_updated_at: item.updated_at })
  const detail = async (id: string) => (await body(await operator.request.get(`/api/v1/security-findings/${id}`))).item
  const matrix: unknown[] = []
  evidence.matrix = matrix
  for (const status of ['pending', 'in_progress', 'confirmed', 'resolved', 'fixed', 'false_positive', 'ignored']) {
    const original = await finding(status, status)
    const allowed = !['false_positive', 'ignored'].includes(status)
    const response = await body(await operator.request.post(`/api/v1/security-findings/${original.id}/accept-risk`, { data: riskData(original) }), allowed ? 200 : 409)
    if (allowed) expect(response.item).toMatchObject({ status: 'risk_accepted', risk_accepted_from_status: status, risk_acceptance_reason: '真实安全运营员接受风险' })
    else {
      expect(response.error.code).toBe('invalid_finding_state')
      expect(await detail(original.id)).toEqual(original)
    }
    matrix.push({ original, response, after: await detail(original.id), allowed })
  }
  const target = await finding('pending', 'browser')
  evidence.target = target
  expect((await body(await child.request.post(`/api/v1/security-findings/${target.id}/accept-risk`, { data: riskData(target) }), 403)).error.code).toBe('forbidden')
  const good = riskData(target)
  const invalidCases = [
    { data: { reason: good.reason, accepted_until: good.accepted_until }, code: 'finding_version_required' },
    { data: { ...good, expected_updated_at: new Date(0).toISOString() }, code: 'finding_edit_conflict', status: 409 },
    { data: { ...good, reason: '   ' }, code: 'invalid_risk_acceptance' },
    { data: { ...good, reason: '险'.repeat(5001) }, code: 'invalid_risk_acceptance' },
    { data: { ...good, accepted_until: new Date(Date.now() - 60000).toISOString() }, code: 'invalid_risk_acceptance' },
  ]
  for (const invalid of invalidCases) {
    expect((await body(await operator.request.post(`/api/v1/security-findings/${target.id}/accept-risk`, { data: invalid.data }), invalid.status ?? 400)).error.code).toBe(invalid.code)
    expect(await detail(target.id)).toEqual(target)
  }
  evidence.invalidAndChildDeniedUnchanged = true
  // A global security operator can already read these details and the unfiltered list.
  // Adding an organization filter must narrow that access, not revoke it.
  const filtered = await body(await operator.request.get(`/api/v1/security-findings?organization_id=${org.id}&page=1&page_size=100`))
  expect(filtered.total).toBe(8)
  expect(filtered.items.every((item: { assignee_organization_id: string }) => item.assignee_organization_id === org.id)).toBe(true)
  const summary = (await body(await operator.request.get(`/api/v1/security-findings/summary?organization_id=${org.id}`))).summary
  expect(summary.total).toBe(filtered.total)
  const ownChild = await body(await child.request.get(`/api/v1/security-findings?organization_id=${org.id}&page=1&page_size=100`))
  expect(ownChild.total).toBe(filtered.total)
  const rootID = flatten((await body(await s.group.request.get('/api/v1/organizations/tree'))).items).find(item => item.type === 'group')!.id
  for (const path of ['/api/v1/security-findings', '/api/v1/security-findings/summary']) {
    expect((await body(await child.request.get(`${path}?organization_id=${rootID}`), 403)).error.code).toBe('forbidden')
  }
  Object.assign(evidence, { filtered, summary, childOwnTotal: ownChild.total, childForeignOrganizationDenied: true })
  const order = (await body(await operator.request.post('/api/v1/security-work-orders', { data: { title: `阻止风险接受_${stamp}`, assignee_organization_id: org.id, description: '活跃工单应阻止风险接受', remediation_requirement: '修复后核验', due_at: new Date(Date.now() + 86400000).toISOString(), finding_ids: [target.id] } }), 201)).item
  evidence.order = order
  expect(order.status).toBe('draft')
  const draftVersion = await detail(target.id)
  expect((await body(await operator.request.post(`/api/v1/security-findings/${target.id}/accept-risk`, { data: riskData(draftVersion) }), 409)).error.code).toBe('invalid_finding_state')
  expect(await detail(target.id)).toEqual(draftVersion)
  evidence.draftConflictUnchanged = true
  const issued = (await body(await operator.request.post(`/api/v1/security-work-orders/${order.id}/issue`, { data: { comment: '隔离环境下发以验证冲突' } }))).item
  expect(issued.status).toBe('issued')
  const issuedFinding = await detail(target.id)
  evidence.issuedFinding = issuedFinding
  await page.goto(`/security-work-orders?tab=findings&organization_id=${org.id}`)
  const row = page.locator('.el-table__body-wrapper tr').filter({ hasText: target.title })
  await clickRowAction(page, row, '接受风险')
  let dialog = page.getByRole('dialog', { name: '接受风险', exact: true })
  const reason = '险'.repeat(5000)
  await dialog.locator('textarea').fill(reason)
  const blockedResponse = page.waitForResponse(response => response.url().includes(`/security-findings/${target.id}/accept-risk`) && response.request().method() === 'POST')
  await dialog.getByRole('button', { name: '确认接受风险', exact: true }).click()
  const blocked = await body(await blockedResponse, 409)
  expect(blocked.error.code).toBe('invalid_finding_state')
  await expect(page.getByText('安全发现当前状态不允许执行风险操作', { exact: true })).toBeVisible()
  await expect(dialog).toBeVisible()
  await expect(dialog.locator('textarea')).toHaveValue(reason)
  expect(await detail(target.id)).toEqual(issuedFinding)
  evidence.issuedBrowserConflictUnchanged = true
  await page.screenshot({ path: testInfo.outputPath('active-order-risk-conflict.png'), fullPage: true })
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
  const cancelled = (await body(await operator.request.post(`/api/v1/security-work-orders/${order.id}/cancel`, { data: { comment: '取消独立测试工单，重新验证风险接受' } }))).item
  expect(cancelled.status).toBe('cancelled')
  const afterCancel = await detail(target.id)
  Object.assign(evidence, { cancelled, afterCancel })
  await page.reload()
  await clickRowAction(page, row, '接受风险')
  dialog = page.getByRole('dialog', { name: '接受风险', exact: true })
  await dialog.locator('textarea').fill(reason + '险')
  await expect(dialog.locator('textarea')).toHaveValue(reason)
  const acceptedResponse = page.waitForResponse(response => response.url().includes(`/security-findings/${target.id}/accept-risk`) && response.request().method() === 'POST')
  await dialog.getByRole('button', { name: '确认接受风险', exact: true }).click()
  const accepted = (await body(await acceptedResponse)).item
  expect(accepted).toMatchObject({ status: 'risk_accepted', risk_accepted_from_status: afterCancel.status, risk_acceptance_reason: reason })
  await expect(dialog).toBeHidden()
  await expect(row).toContainText('风险接受')
  expect((await body(await operator.request.post(`/api/v1/security-findings/${target.id}/accept-risk`, { data: riskData(afterCancel, '旧页面不应覆盖原因') }), 409)).error.code).toBe('finding_edit_conflict')
  expect(await detail(target.id)).toEqual(accepted)
  evidence.accepted = accepted
  expect(s.pageErrors).toEqual([])
  expect(s.serverErrors).toEqual([])
})
