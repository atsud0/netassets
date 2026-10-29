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
      const org = (await body(await group.request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `RISK_TASK_${stamp}`, name: `个人任务风险_${stamp}`, type: 'subsidiary' } }), 201)).item
      const asset = (await body(await group.request.post('/api/v1/assets', { data: { type: 'domain', value: `risk-task-${stamp.replaceAll('_', '-')}.example.com`, organization_id: org.id, environment: 'production', network_scope: 'public', status: 'active', importance: 'normal', direct_change_reason: '仅隔离环境角色状态回归' } }), 201)).item
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
      if (page && !page.isClosed()) await page.screenshot({ path: testInfo.outputPath('risk-personal-task.png'), fullPage: true }).catch(() => {})
      await writeFile(testInfo.outputPath('risk-personal-task-evidence.json'), JSON.stringify(evidence, null, 2))
      for (const context of contexts.reverse()) await context.close()
    }
  },
})
test.setTimeout(240_000)

test('个人任务阻止风险操作且权限优先；取消任务仍受工单约束，取消工单后浏览器接受风险', async ({ security: s }, testInfo) => {
  const { group, operator, child, page, org, stamp, evidence } = s
  const due = new Date(Date.now() + 3 * 86400000).toISOString()
  const system = (await body(await group.request.post('/api/v1/business-systems', { data: {
    code: `RISK_TASK_SYS_${stamp}`, name: `个人风险系统_${stamp}`, organization_id: org.id,
    owner_name: '回归负责人', owner_contact: '13800000018', status: 'running', level: 'normal', internet_exposure: 'yes',
    contains_personal_information: 'no', contains_sensitive_personal_information: 'no', contains_important_data: 'no', contains_core_data: 'no', data_volume_over_100k: 'no', custom_values: [],
  } }), 201)).item
  const grant = await body(await child.request.post('/api/v1/remediation-grants', { data: {
    organization_id: org.id, business_system_id: system.id, participant_type: 'internal',
    username: `risk_worker_${stamp}`, display_name: '个人风险修复人员', password: 'RiskWorker123!', valid_until: new Date(Date.now() + 30 * 86400000).toISOString(),
  } }))
  const finding = (await body(await operator.request.post('/api/v1/security-findings', { data: {
    title: `个人任务风险_${stamp}`, severity: 'high', status: 'pending', assignee_organization_id: org.id,
    business_system_id: system.id, description: '隔离环境验证个人任务与风险权限顺序', evidence: '仅隔离环境', remediation_advice: '完善输入校验', source: 'manual', discovered_at: new Date().toISOString(),
  } }), 201)).item
  const order = (await body(await operator.request.post('/api/v1/security-work-orders', { data: {
    execution_mode: 'individual', title: `个人风险工单_${stamp}`, assignee_organization_id: org.id,
    remediation_requirement: '修复并验证', due_at: due, finding_ids: [finding.id],
  } }), 201)).item
  await body(await operator.request.post(`/api/v1/security-work-orders/${order.id}/issue`, { data: { comment: '个人任务风险回归' } }))
  Object.assign(evidence, { system, grant: { id: grant.id, user_id: grant.user_id }, finding, order })
  const detail = async () => (await body(await operator.request.get(`/api/v1/security-findings/${finding.id}`))).item
  const taskDetail = async (id: string) => (await body(await child.request.get(`/api/v1/remediation-tasks/${id}`))).item
  async function history(path: string) {
    const result = await body(await child.request.get(`${path}?page=1&page_size=100`))
    expect(result.items.length).toBe(result.total)
    return result.items
  }
  async function assign(key: string) {
    const result = await body(await child.request.post(`/api/v1/security-work-orders/${order.id}/remediation-tasks/batch-assign`, {
      headers: { 'Idempotency-Key': `risk-personal-${stamp}-${key}` }, data: { items: [{ finding_id: finding.id, grant_id: grant.id, due_at: due, target_environment: '生产环境', deployment_required: true,
        material: { title: finding.title, severity: 'high', description: '已批准共享描述', requirement: '修复并验证', evidence: '已批准证据', remediation_advice: '完善校验' } }] },
    }))
    expect(result.items).toHaveLength(1)
    expect(result.items[0].error).toBeUndefined()
    const task = await taskDetail(result.items[0].id)
    expect(task.status).toBe('pending_acceptance')
    return task
  }
  let task = await assign('first')
  evidence.firstTask = task
  async function snapshot() {
    return { finding: await detail(), task: await taskDetail(task.id),
      taskEvents: await history(`/api/v1/remediation-tasks/${task.id}/events`),
      order: (await body(await operator.request.get(`/api/v1/security-work-orders/${order.id}`))).item,
      orderEvents: await history(`/api/v1/security-work-orders/${order.id}/events`) }
  }
  const checks: unknown[] = []; evidence.checks = checks
  async function deniedActions(stage: string, active: boolean) {
    const before = await snapshot()
    const data = { reason: '个人任务风险接受', comment: '个人任务风险复核', accepted_until: due, expected_updated_at: before.finding.updated_at }
    for (const action of ['accept-risk', 'revoke-risk', 'review-risk']) {
      const response = await child.request.post(`/api/v1/security-findings/${finding.id}/${action}`, { data })
      const result = await response.json()
      checks.push({ stage, actor: 'organization_admin', action, status: response.status(), result })
      // Soft assertion retains the complete baseline lifecycle even when permission ordering is broken.
      expect.soft(response.status(), `${stage}/${action}: ${JSON.stringify(result)}`).toBe(403)
      expect.soft(result.error.code).toBe('forbidden')
      if (active) {
        const blocked = await body(await operator.request.post(`/api/v1/security-findings/${finding.id}/${action}`, { data }), 409)
        expect(blocked.error.code).toBe('invalid_finding_state')
        checks.push({ stage, actor: 'security_operator', action, status: 409, result: blocked })
      }
    }
    const edit = { ...before.finding, expected_updated_at: before.finding.updated_at, title: '无权限不得修改标题' }
    const response = await child.request.patch(`/api/v1/security-findings/${finding.id}`, { data: edit })
    const result = await response.json()
    checks.push({ stage, actor: 'organization_admin', action: 'update', status: response.status(), result })
    expect.soft(response.status(), `${stage}/update: ${JSON.stringify(result)}`).toBe(403)
    expect.soft(result.error.code).toBe('forbidden')
    if (active) {
      const blocked = await body(await operator.request.patch(`/api/v1/security-findings/${finding.id}`, { data: edit }), 409)
      expect(blocked.error.code).toBe('finding_conflict')
      checks.push({ stage, actor: 'security_operator', action: 'update', status: 409, result: blocked })
    }
    const after = await snapshot()
    expect(after).toEqual(before)
    checks.push({ stage, rejectedActionsUnchanged: true, snapshot: after })
  }
  await deniedActions('active-personal-task', true)
  await page.goto(`/security-work-orders?tab=findings&organization_id=${org.id}`)
  const row = page.locator('.el-table__body-wrapper tr').filter({ hasText: finding.title })
  await clickRowAction(page, row, '接受风险')
  let dialog = page.getByRole('dialog', { name: '接受风险', exact: true })
  const reason = '个'.repeat(5000)
  await dialog.locator('textarea').fill(reason)
  const beforeBrowser = await snapshot()
  const blockedResponse = page.waitForResponse(response => response.url().includes(`/security-findings/${finding.id}/accept-risk`) && response.request().method() === 'POST')
  await dialog.getByRole('button', { name: '确认接受风险', exact: true }).click()
  expect((await body(await blockedResponse, 409)).error.code).toBe('invalid_finding_state')
  await expect(page.getByText('安全发现当前状态不允许执行风险操作', { exact: true })).toBeVisible()
  await expect(dialog).toBeVisible()
  await expect(dialog.locator('textarea')).toHaveValue(reason)
  expect(await snapshot()).toEqual(beforeBrowser)
  evidence.browserConflictUnchanged = true
  await page.screenshot({ path: testInfo.outputPath('active-personal-task-conflict.png'), fullPage: true })
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
  await body(await child.request.post(`/api/v1/remediation-tasks/${task.id}/actions/cancel`, { headers: { 'Idempotency-Key': `risk-cancel-task-${stamp}` }, data: { expected_version: task.version, reason: '验证取消任务不绕过活跃工单约束' } }))
  const cancelledFirst = await taskDetail(task.id)
  expect(cancelledFirst.status).toBe('cancelled')
  expect(cancelledFirst.version).toBe(task.version + 1)
  evidence.cancelledFirstTask = cancelledFirst
  evidence.firstTaskEvents = await history(`/api/v1/remediation-tasks/${task.id}/events`)
  // A cancelled task alone does not release an active work-order link.
  const afterTaskCancel = await detail()
  expect((await body(await operator.request.post(`/api/v1/security-findings/${finding.id}/accept-risk`, { data: { reason, accepted_until: due, expected_updated_at: afterTaskCancel.updated_at } }), 409)).error.code).toBe('invalid_finding_state')
  expect(await detail()).toEqual(afterTaskCancel)
  evidence.cancelledTaskActiveOrderStillBlocked = true
  task = await assign('second')
  evidence.secondTask = task
  const cancelledOrder = (await body(await operator.request.post(`/api/v1/security-work-orders/${order.id}/cancel`, { data: { comment: '验证取消个人工单连带取消任务后恢复风险操作' } }))).item
  expect(cancelledOrder.status).toBe('cancelled')
  const cancelledSecond = await taskDetail(task.id)
  expect(cancelledSecond.status).toBe('cancelled')
  expect(cancelledSecond.version).toBe(task.version + 1)
  const afterOrderCancel = await detail()
  Object.assign(evidence, { cancelledOrder, cancelledSecondTask: cancelledSecond, afterOrderCancel })
  await deniedActions('cancelled-order', false)
  await page.reload()
  await clickRowAction(page, row, '接受风险')
  dialog = page.getByRole('dialog', { name: '接受风险', exact: true })
  await dialog.locator('textarea').fill(reason)
  const acceptedResponse = page.waitForResponse(response => response.url().includes(`/security-findings/${finding.id}/accept-risk`) && response.request().method() === 'POST')
  await dialog.getByRole('button', { name: '确认接受风险', exact: true }).click()
  const accepted = (await body(await acceptedResponse)).item
  expect(accepted).toMatchObject({ status: 'risk_accepted', risk_accepted_from_status: afterOrderCancel.status, risk_acceptance_reason: reason })
  await expect(dialog).toBeHidden()
  await expect(row).toContainText('风险接受')
  evidence.accepted = accepted
  await deniedActions('risk-accepted', false)
  const beforeStale = await snapshot()
  for (const action of ['accept-risk', 'revoke-risk', 'review-risk']) {
    const response = await body(await operator.request.post(`/api/v1/security-findings/${finding.id}/${action}`, { data: { reason: '旧版本不得覆盖', comment: '旧版本不得复核', accepted_until: due, expected_updated_at: afterOrderCancel.updated_at } }), 409)
    expect(response.error.code).toBe('finding_edit_conflict')
  }
  expect(await snapshot()).toEqual(beforeStale)
  evidence.staleRiskActionsUnchanged = true
  evidence.final = await snapshot()
  expect(s.pageErrors).toEqual([])
  expect(s.serverErrors).toEqual([])
})
