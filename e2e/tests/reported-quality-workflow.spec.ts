import { expect, test, type APIRequestContext, type Page } from '@playwright/test'

test.setTimeout(60_000)

interface ScannedQualityIssue {
  issue_id: string
  rule_id: string
  rule_name: string
  resource_id: string
  resource_name: string
}

// Keep issue/event evidence, but do not leave a globally enabled custom rule
// that would match every asset created by subsequent suites.
const cleanupRules = new WeakMap<APIRequestContext, { id: string; input: Record<string, unknown> }>()
test.afterEach(async ({ request }) => {
  const rule = cleanupRules.get(request)
  if (!rule) return
  const disabled = await request.patch(`/api/v1/data-quality-rules/${rule.id}`, { data: { ...rule.input, is_enabled: false } })
  expect(disabled.status(), await disabled.text()).toBe(200)
  cleanupRules.delete(request)
})

async function fixture(request: APIRequestContext, assetCount = 1) {
  const auth = await request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME, password: process.env.E2E_PASSWORD } })
  expect(auth.status(), await auth.text()).toBe(200)
  const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
  const roots = await request.get('/api/v1/organizations?page=1&page_size=100')
  const root = (await roots.json()).items.find((o: { type: string }) => o.type === 'group')
  const organization = await request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `Q_${stamp}`, name: `质量流程回归${stamp}`, type: 'subsidiary' } })
  expect(organization.status(), await organization.text()).toBe(201)
  const org = (await organization.json()).item
  const roles = (await (await request.get('/api/v1/roles?page=1&page_size=100')).json()).items
  const role = roles.find((r: { key: string }) => r.key === 'organization_admin')
  const username = `quality_${stamp}`, password = 'QualityRepro123!'
  const createdUser = await request.post('/api/v1/users', { data: { username, password, display_name: '质量回归负责人', global_role_ids: [], appointments: [{ organization_id: org.id, is_primary: true, assignments: [{ role_id: role.id, data_scope: 'organization' }] }] } })
  expect(createdUser.status(), await createdUser.text()).toBe(201)
  const user = (await createdUser.json()).item
  const key = `quality_${stamp}`
  const field = await request.post('/api/v1/custom-fields', { data: { resource_type: 'asset', field_key: key, field_name: '回归缺失字段', field_type: 'text', options: [], is_required: false, is_enabled: true, sort_order: 0 } })
  expect(field.status(), await field.text()).toBe(201)
  const ruleInput = { rule_name: `回归必填${stamp}`, resource_type: 'asset', rule_type: 'required', target_field: `custom:${key}`, severity: 'warning', is_enabled: true, sort_order: 0 }
  const rule = await request.post('/api/v1/data-quality-rules', { data: ruleInput })
  expect(rule.status(), await rule.text()).toBe(201)
  const ruleID = (await rule.json()).item.id
  cleanupRules.set(request, { id: ruleID, input: ruleInput })
  const assetIDs: string[] = []
  for (let index = 0; index < assetCount; index++) {
    const asset = await request.post('/api/v1/assets', { data: { type: 'domain', value: `q-${stamp.replaceAll('_', '-')}-${index}.example.com`, name: `质量流程测试资产${index + 1}`, organization_id: org.id, environment: 'production', network_scope: 'private', status: 'active', importance: 'normal', direct_change_reason: '隔离回归' } })
    expect(asset.status(), await asset.text()).toBe(201)
    assetIDs.push((await asset.json()).item.id)
  }
  const scan = await request.get(`/api/v1/data-quality-rules/scan?organization_id=${org.id}&resource_type=asset`)
  expect(scan.status(), await scan.text()).toBe(200)
  const scanned: ScannedQualityIssue[] = (await scan.json()).result.issues
  const issues = assetIDs.map(id => {
    const issue = scanned.find(item => item.rule_id === ruleID && item.resource_id === id)
    expect(issue, `scoped scan must find our rule for asset ${id}`).toBeTruthy()
    if (!issue) throw new Error(`scoped scan did not return issue for asset ${id}`)
    return issue
  })
  return { org, user, issue: issues[0], issues, username, password }
}

async function loginChild(page: Page, username: string, password: string) {
  const logged = await page.request.post('/api/v1/auth/login', { data: { username, password } })
  expect(logged.status(), await logged.text()).toBe(200)
  const changed = await page.request.put('/api/v1/auth/profile/password', { data: { current_password: password, new_password: `${password}Changed` } })
  expect(changed.status(), await changed.text()).toBe(204)
  await page.goto('/data-quality-issues')
}

test('质量问题调整负责人写入指派事件并保留完整中文处理说明', async ({ request, page }) => {
  const { user, issue, username, password } = await fixture(request)
  const note = '调'.repeat(5000)
  const started = await request.post(`/api/v1/data-quality-issues/${issue.issue_id}/start`, { data: { assignee_user_id: user.id } })
  expect(started.status(), await started.text()).toBe(200)
  await loginChild(page, username, password)
  const row = page.locator('.el-table__body-wrapper tr').filter({ hasText: issue.rule_name })
  await row.getByRole('button', { name: /^操作：/ }).click()
  await page.locator('.row-actions-popover:visible').getByRole('button', { name: '调整负责人', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '调整负责人', exact: true })
  await expect(dialog.locator('.el-select')).toContainText('质量回归负责人')
  await dialog.locator('textarea').fill(note)
  const [response] = await Promise.all([
    page.waitForResponse(r => r.url().includes(`/data-quality-issues/${issue.issue_id}/assign`) && r.request().method() === 'POST'),
    dialog.getByRole('button', { name: '确认调整负责人', exact: true }).click(),
  ])
  expect(response.status(), await response.text()).toBe(200)
  expect((await response.json()).item.assignee_user_id).toBe(user.id)
  const events = await request.get(`/api/v1/data-quality-issues/${issue.issue_id}/events?page=1&page_size=100`)
  expect(events.status(), await events.text()).toBe(200)
  expect((await events.json()).items).toEqual(expect.arrayContaining([expect.objectContaining({ event_type: 'assigned', note })]))
})

for (const sample of [{ label: '中文', char: '批' }, { label: '英文', char: 'B' }]) {
  test(`质量问题批量指派5000及5001字边界、成功弹窗与完整保存（${sample.label}）`, async ({ request, page }) => {
    const { user, issue, username, password } = await fixture(request)
    const note = sample.char.repeat(5000)
    const endpoint = `/api/v1/data-quality-issues/${issue.issue_id}`
    await loginChild(page, username, password)
    const before = await readQualityEvidence(page.request, endpoint)
    const oversized = await page.request.post('/api/v1/data-quality-issues/batch', { data: { ids: [issue.issue_id], action: 'assign', assignee_user_id: user.id, note: note + 'X' } })
    expect(oversized.status(), await oversized.text()).toBe(400)
    expect(await readQualityEvidence(page.request, endpoint)).toEqual(before)
    const row = page.locator('.el-table__body-wrapper tr').filter({ hasText: issue.rule_name })
    await row.locator('.el-checkbox').click()
    await expect(row.getByRole('checkbox')).toBeChecked()
    await page.getByRole('button', { name: '批量指派', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: '批量指派负责人', exact: true })
    await expect(dialog.locator('.el-select')).toContainText('质量回归负责人')
    await expect(dialog.locator('textarea')).toHaveAttribute('maxlength', '5000')
    await dialog.locator('textarea').fill(note + 'X')
    await expect(dialog.locator('textarea')).toHaveValue(note)
    await dialog.getByRole('button', { name: '确认执行', exact: true }).click()
    const [response] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith('/data-quality-issues/batch') && r.request().method() === 'POST'),
      page.locator('.el-message-box:visible').getByRole('button', { name: '确认执行', exact: true }).click(),
    ])
    expect(response.status(), await response.text()).toBe(200)
    const result = (await response.json()).result
    await test.info().attach('batch-result.json', { body: JSON.stringify(result, null, 2), contentType: 'application/json' })
    expect(result).toMatchObject({ total_count: 1, success_count: 1, failure_count: 0 })
    expect(result.items).toEqual([expect.objectContaining({ id: issue.issue_id, success: true })])
    const resultDialog = page.getByRole('dialog', { name: '批量指派负责人结果', exact: true })
    await expect(resultDialog).toContainText('共处理 1 个，成功 1 个，失败 0 个')
    const resultRow = resultDialog.locator('.el-table__body-wrapper tr')
    await expect(resultRow).toHaveCount(1)
    await expect(resultRow).toContainText(issue.resource_name)
    await expect(resultRow).toContainText(result.items[0].message)
    const saved = await readQualityEvidence(page.request, endpoint)
    expect(saved.issue).toMatchObject({ status: 'pending', assignee_user_id: user.id })
    expect(saved.events.filter((event: { event_type: string }) => event.event_type === 'assigned')).toEqual([expect.objectContaining({ note })])
    await resultDialog.getByRole('button', { name: '关闭', exact: true }).click()
    await expect(resultDialog).toBeHidden()
    await expect(page.getByRole('button', { name: '批量指派', exact: true })).toBeDisabled()
    await expect(row.getByRole('checkbox')).not.toBeChecked()
  })
}

async function readQualityEvidence(request: APIRequestContext, endpoint: string) {
  const detail = await request.get(endpoint)
  expect(detail.status(), await detail.text()).toBe(200)
  const events = await request.get(`${endpoint}/events?page=1&page_size=100`)
  expect(events.status(), await events.text()).toBe(200)
  return { issue: (await detail.json()).issue, events: (await events.json()).items }
}

for (const sample of [{ label: '中文', char: '批' }, { label: '英文', char: 'B' }]) {
  for (const failedCount of [1, 2]) {
    test(`子管理员批量指派真实状态冲突${failedCount === 1 ? '部分成功' : '全部失败'}结果弹窗（${sample.label}）`, async ({ request, page }) => {
      const { user, issues, username, password } = await fixture(request, 2)
      await loginChild(page, username, password)
      // Historical enabled rules also match these two assets. Use the real
      // pagination control rather than assuming our newest rule is on page 1.
      await page.locator('.el-pagination .el-select').click()
      const [listed] = await Promise.all([
        page.waitForResponse(r => r.url().includes('/api/v1/data-quality-issues?') && r.url().includes('page_size=100') && r.request().method() === 'GET'),
        page.getByRole('option', { name: '100条/页', exact: true }).click(),
      ])
      expect(listed.status(), await listed.text()).toBe(200)
      for (const issue of issues) {
        const row = page.locator('.quality-issue-table-scroll .el-table__body-wrapper tr').filter({ hasText: issue.rule_name }).filter({ hasText: issue.resource_name })
        await expect(row).toHaveCount(1)
        await row.locator('.el-checkbox').click()
        await expect(row.getByRole('checkbox')).toBeChecked()
      }
      await page.getByRole('button', { name: '批量指派', exact: true }).click()
      const dialog = page.getByRole('dialog', { name: '批量指派负责人', exact: true })
      await expect(dialog.locator('.el-select')).toContainText('质量回归负责人')
      const note = sample.char.repeat(5000)
      await dialog.locator('textarea').fill(note)
      await dialog.getByRole('button', { name: '确认执行', exact: true }).click()
      await expect(page.locator('.el-message-box:visible')).toContainText('选中的 2 个数据质量问题')
      // A real concurrent submission invalidates the stale UI selection. No
      // request interception, fabricated IDs, database edits or mocked results.
      const failedIssues = issues.slice(0, failedCount)
      const failedSnapshots = new Map<string, Awaited<ReturnType<typeof readQualityEvidence>>>()
      for (const issue of failedIssues) {
        const endpoint = `/api/v1/data-quality-issues/${issue.issue_id}`
        const submitted = await page.request.post(`${endpoint}/submit`, { data: { note: '另一会话已完成整改并提交复核' } })
        expect(submitted.status(), await submitted.text()).toBe(200)
        expect((await submitted.json()).item.status).toBe('pending_review')
        failedSnapshots.set(issue.issue_id, await readQualityEvidence(page.request, endpoint))
      }
      const [response] = await Promise.all([
        page.waitForResponse(r => r.url().endsWith('/data-quality-issues/batch') && r.request().method() === 'POST'),
        page.locator('.el-message-box:visible').getByRole('button', { name: '确认执行', exact: true }).click(),
      ])
      expect(response.status(), await response.text()).toBe(200)
      expect(response.request().postDataJSON()).toMatchObject({ action: 'assign', assignee_user_id: user.id, note })
      expect(response.request().postDataJSON().ids).toEqual(expect.arrayContaining(issues.map(issue => issue.issue_id)))
      const result = (await response.json()).result
      await test.info().attach('batch-result.json', { body: JSON.stringify(result, null, 2), contentType: 'application/json' })
      expect(result).toMatchObject({ total_count: 2, success_count: 2 - failedCount, failure_count: failedCount })
      expect(result.items).toHaveLength(2)
      const resultDialog = page.getByRole('dialog', { name: '批量指派负责人结果', exact: true })
      await expect(resultDialog).toContainText(`共处理 2 个，成功 ${2 - failedCount} 个，失败 ${failedCount} 个`)
      await expect(resultDialog.locator('.el-table__body-wrapper tr')).toHaveCount(2)
      for (const issue of issues) {
        const item = result.items.find((item: { id: string }) => item.id === issue.issue_id)
        const resultRow = resultDialog.locator('.el-table__body-wrapper tr').filter({ hasText: issue.resource_name })
        await expect(resultRow).toHaveCount(1)
        const saved = await readQualityEvidence(page.request, `/api/v1/data-quality-issues/${issue.issue_id}`)
        if (failedSnapshots.has(issue.issue_id)) {
          expect(item).toMatchObject({ success: false, code: 'invalid_status_transition', message: '当前状态不允许指派' })
          await expect(resultRow).toContainText('失败')
          await expect(resultRow).toContainText('当前状态不允许指派')
          expect(saved).toEqual(failedSnapshots.get(issue.issue_id))
        } else {
          expect(item).toMatchObject({ success: true })
          await expect(resultRow).toContainText('成功')
          await expect(resultRow).toContainText(item.message)
          expect(saved.issue).toMatchObject({ status: 'pending', assignee_user_id: user.id })
          expect(saved.events.filter((event: { event_type: string }) => event.event_type === 'assigned')).toEqual([expect.objectContaining({ note })])
        }
      }
      await resultDialog.getByRole('button', { name: '关闭', exact: true }).click()
      await expect(resultDialog).toBeHidden()
      await expect(page.getByRole('button', { name: '批量指派', exact: true })).toBeDisabled()
      for (const issue of issues) {
        const row = page.locator('.quality-issue-table-scroll .el-table__body-wrapper tr').filter({ hasText: issue.rule_name }).filter({ hasText: issue.resource_name })
        await expect(row.getByRole('checkbox')).not.toBeChecked()
        await expect(row).toContainText(failedSnapshots.has(issue.issue_id) ? '待复核' : '待处理')
      }
    })
  }
}

test('子系统管理员提交复核及集团豁免接受5000字中文', async ({ request, playwright }) => {
  const { issue, username, password } = await fixture(request)
  const child = await playwright.request.newContext({ baseURL: process.env.BASE_URL })
  try {
    const logged = await child.post('/api/v1/auth/login', { data: { username, password } })
    expect(logged.status(), await logged.text()).toBe(200)
    const changed = await child.put('/api/v1/auth/profile/password', { data: { current_password: password, new_password: `${password}Changed` } })
    expect(changed.status(), await changed.text()).toBe(204)
    const note = '复'.repeat(5000)
    const submitted = await child.post(`/api/v1/data-quality-issues/${issue.issue_id}/submit`, { data: { note } })
    expect(submitted.status(), await submitted.text()).toBe(200)
    expect((await submitted.json()).item.rectification_note).toBe(note)
    const reason = '豁'.repeat(5000)
    const exempted = await request.post(`/api/v1/data-quality-issues/${issue.issue_id}/exempt`, { data: { reason } })
    expect(exempted.status(), await exempted.text()).toBe(200)
    expect((await exempted.json()).item.exemption_reason).toBe(reason)
  } finally { await child.dispose() }
})

for (const sample of [{ label: '中文', noteChar: '复', reasonChar: '豁' }, { label: '英文', noteChar: 'R', reasonChar: 'E' }]) {
  test(`真实角色浏览器提交复核和豁免校验5000及5001字边界并保留完整历史（${sample.label}）`, async ({ request, page }) => {
    const { issue, username, password } = await fixture(request)
    const endpoint = `/api/v1/data-quality-issues/${issue.issue_id}`
    await loginChild(page, username, password)
    const invalidSubmit = await page.request.post(`${endpoint}/submit`, { data: { note: sample.noteChar.repeat(5001) } })
    expect(invalidSubmit.status(), await invalidSubmit.text()).toBe(400)
    const beforeSubmit = await page.request.get(endpoint)
    expect(beforeSubmit.status(), await beforeSubmit.text()).toBe(200)
    expect((await beforeSubmit.json()).issue).toMatchObject({ status: 'pending' })
    const childRow = page.locator('.el-table__body-wrapper tr').filter({ hasText: issue.rule_name })
    await childRow.getByRole('button', { name: /^操作：/ }).click()
    await page.locator('.row-actions-popover:visible').getByRole('button', { name: '提交复核', exact: true }).click()
    const submitDialog = page.getByRole('dialog', { name: '提交集团复核', exact: true })
    await submitDialog.getByRole('button', { name: '确认提交集团复核', exact: true }).click()
    await expect(page.getByText('请填写处理说明', { exact: true })).toBeVisible()
    await expect(submitDialog).toBeVisible()
    const note = sample.noteChar.repeat(5000)
    await expect(submitDialog.locator('textarea')).toHaveAttribute('maxlength', '5000')
    await submitDialog.locator('textarea').fill(note + '多')
    await expect(submitDialog.locator('textarea')).toHaveValue(note)
    const [submitted] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith(`${endpoint}/submit`) && r.request().method() === 'POST'),
      submitDialog.getByRole('button', { name: '确认提交集团复核', exact: true }).click(),
    ])
    expect(submitted.status(), await submitted.text()).toBe(200)
    expect((await submitted.json()).item).toMatchObject({ status: 'pending_review', rectification_note: note })
    await expect(submitDialog).toBeHidden()
    await expect(childRow).toContainText('待复核')
    const childExempt = await page.request.post(`${endpoint}/exempt`, { data: { reason: sample.reasonChar.repeat(5000) } })
    expect(childExempt.status(), await childExempt.text()).toBe(403)

    const logged = await page.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME, password: process.env.E2E_PASSWORD } })
    expect(logged.status(), await logged.text()).toBe(200)
    await page.reload()
    const invalidExempt = await request.post(`${endpoint}/exempt`, { data: { reason: sample.reasonChar.repeat(5001) } })
    expect(invalidExempt.status(), await invalidExempt.text()).toBe(400)
    const afterRejectedExempt = await request.get(endpoint)
    expect(afterRejectedExempt.status(), await afterRejectedExempt.text()).toBe(200)
    expect((await afterRejectedExempt.json()).issue).toMatchObject({ status: 'pending_review', rectification_note: note })
    const adminRow = page.locator('.el-table__body-wrapper tr').filter({ hasText: issue.rule_name })
    await adminRow.getByRole('button', { name: /^操作：/ }).click()
    await page.locator('.row-actions-popover:visible').getByRole('button', { name: '豁免', exact: true }).click()
    const exemptDialog = page.getByRole('dialog', { name: '问题豁免', exact: true })
    await exemptDialog.getByRole('button', { name: '确认问题豁免', exact: true }).click()
    await expect(page.getByText('请填写豁免原因', { exact: true })).toBeVisible()
    await expect(exemptDialog).toBeVisible()
    const reason = sample.reasonChar.repeat(5000)
    await expect(exemptDialog.locator('textarea')).toHaveAttribute('maxlength', '5000')
    await exemptDialog.locator('textarea').fill(reason + '多')
    await expect(exemptDialog.locator('textarea')).toHaveValue(reason)
    const [exempted] = await Promise.all([
      page.waitForResponse(r => r.url().endsWith(`${endpoint}/exempt`) && r.request().method() === 'POST'),
      exemptDialog.getByRole('button', { name: '确认问题豁免', exact: true }).click(),
    ])
    expect(exempted.status(), await exempted.text()).toBe(200)
    expect((await exempted.json()).item).toMatchObject({ status: 'exempted', exemption_reason: reason, rectification_note: note })
    await expect(exemptDialog).toBeHidden()
    const stored = await request.get(endpoint)
    expect(stored.status(), await stored.text()).toBe(200)
    expect((await stored.json()).issue).toMatchObject({ status: 'exempted', exemption_reason: reason, rectification_note: note })
    const events = await request.get(`${endpoint}/events?page=1&page_size=100`)
    expect(events.status(), await events.text()).toBe(200)
    expect((await events.json()).items).toEqual(expect.arrayContaining([
      expect.objectContaining({ event_type: 'submitted', note }),
      expect.objectContaining({ event_type: 'exempted', note: reason }),
    ]))
  })
}

test('豁免到期后不执行巡检也会自动恢复待处理并保留历史', async ({ request, page }) => {
  test.setTimeout(110_000)
  const { issue, org, username, password } = await fixture(request)
  const rule = cleanupRules.get(request)!
  const disabled = await request.patch(`/api/v1/data-quality-rules/${rule.id}`, { data: { ...rule.input, is_enabled: false } })
  expect(disabled.status(), await disabled.text()).toBe(200)
  const reason = '到期回归：即使规则已停用也不能无限期豁免'
  const expiresAt = new Date(Date.now() + 3_000).toISOString()
  const exempted = await request.post(`/api/v1/data-quality-issues/${issue.issue_id}/exempt`, { data: { reason, expires_at: expiresAt } })
  expect(exempted.status(), await exempted.text()).toBe(200)
  expect((await exempted.json()).item.status).toBe('exempted')
  // No scan endpoint is called after exemption. Poll the actual production
  // minute-level worker rather than changing the app clock or writing via SQL.
  await expect.poll(async () => {
    const detail = await request.get(`/api/v1/data-quality-issues/${issue.issue_id}`)
    expect(detail.status(), await detail.text()).toBe(200)
    return (await detail.json()).issue.status
  }, { timeout: 80_000, intervals: [1_000, 2_000] }).toBe('pending')
  const reopenedDetail = await request.get(`/api/v1/data-quality-issues/${issue.issue_id}`)
  expect(reopenedDetail.status(), await reopenedDetail.text()).toBe(200)
  const item = (await reopenedDetail.json()).issue
  expect(item.exemption_reason ?? '').toBe('')
  expect(item.resolution_type ?? '').toBe('')
  expect(item.closed_at ?? null).toBeNull()
  expect(item.exemption_expires_at ?? null).toBeNull()
  const pending = await request.get(`/api/v1/data-quality-issues?organization_id=${org.id}&status=pending&page=1&page_size=100`)
  expect(pending.status(), await pending.text()).toBe(200)
  expect((await pending.json()).items).toEqual(expect.arrayContaining([expect.objectContaining({ id: issue.issue_id, status: 'pending' })]))
  const events = await request.get(`/api/v1/data-quality-issues/${issue.issue_id}/events?page=1&page_size=100`)
  expect(events.status(), await events.text()).toBe(200)
  const reopened = (await events.json()).items.filter((event: { event_type: string }) => event.event_type === 'reopened')
  expect(reopened).toHaveLength(1)
  expect(reopened[0]).toMatchObject({ from_status: 'exempted', to_status: 'pending', details: { reason: 'exemption_expired', exemption_reason: reason } })
  expect(reopened[0].actor_user_id ?? '').toBe('')
  expect(new Date(reopened[0].details.exemption_expires_at).getTime()).toBe(new Date(expiresAt).getTime())
  await loginChild(page, username, password)
  const row = page.locator('.el-table__body-wrapper tr').filter({ hasText: issue.rule_name })
  await expect(row).toContainText('待处理')
  await expect(row).not.toContainText('已豁免')
})

test('子管理员批量指派逐项隔离越权和不存在的问题并保留5000字说明', async ({ request, page }) => {
  const { org, user, issue, username, password } = await fixture(request)
  const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
  const roots = await request.get('/api/v1/organizations?page=1&page_size=100')
  expect(roots.status(), await roots.text()).toBe(200)
  const root = (await roots.json()).items.find((item: { type: string }) => item.type === 'group')
  const foreignOrgResponse = await request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `Q_OTHER_${stamp}`, name: `越权隔离组织${stamp}`, type: 'subsidiary' } })
  expect(foreignOrgResponse.status(), await foreignOrgResponse.text()).toBe(201)
  const foreignOrg = (await foreignOrgResponse.json()).item
  expect(foreignOrg.id).not.toBe(org.id)
  const asset = await request.post('/api/v1/assets', { data: { type: 'domain', value: `foreign-${stamp.replaceAll('_', '-')}.example.com`, name: '跨组织质量权限资产', organization_id: foreignOrg.id, environment: 'production', network_scope: 'private', status: 'active', importance: 'normal', direct_change_reason: '隔离越权回归' } })
  expect(asset.status(), await asset.text()).toBe(201)
  const assetID = (await asset.json()).item.id
  const scan = await request.get(`/api/v1/data-quality-rules/scan?organization_id=${foreignOrg.id}&resource_type=asset`)
  expect(scan.status(), await scan.text()).toBe(200)
  const foreignIssue = (await scan.json()).result.issues.find((item: { rule_id: string; resource_id: string }) => item.rule_id === issue.rule_id && item.resource_id === assetID)
  expect(foreignIssue).toBeTruthy()
  const foreignEndpoint = `/api/v1/data-quality-issues/${foreignIssue.issue_id}`
  const foreignBefore = await request.get(foreignEndpoint)
  expect(foreignBefore.status(), await foreignBefore.text()).toBe(200)
  const before = (await foreignBefore.json()).issue
  const eventsBefore = await request.get(`${foreignEndpoint}/events?page=1&page_size=100`)
  expect(eventsBefore.status(), await eventsBefore.text()).toBe(200)
  const originalEvents = (await eventsBefore.json()).items
  await loginChild(page, username, password)
  const note = '批'.repeat(5000)
  const payload = { ids: [issue.issue_id, foreignIssue.issue_id, '00000000-0000-4000-8000-000000000000'], action: 'assign', assignee_user_id: user.id, note }
  const oversized = await page.request.post('/api/v1/data-quality-issues/batch', { data: { ...payload, note: note + '多' } })
  expect(oversized.status(), await oversized.text()).toBe(400)
  const ownEventsAfterReject = await request.get(`/api/v1/data-quality-issues/${issue.issue_id}/events?page=1&page_size=100`)
  expect(ownEventsAfterReject.status(), await ownEventsAfterReject.text()).toBe(200)
  expect((await ownEventsAfterReject.json()).items.filter((event: { event_type: string }) => event.event_type === 'assigned')).toHaveLength(0)
  const singleDenied = await page.request.post(`${foreignEndpoint}/assign`, { data: { assignee_user_id: user.id, note } })
  expect(singleDenied.status(), await singleDenied.text()).toBe(403)
  const response = await page.request.post('/api/v1/data-quality-issues/batch', { data: payload })
  expect(response.status(), await response.text()).toBe(200)
  const result = (await response.json()).result
  expect(result).toMatchObject({ total_count: 3, success_count: 1, failure_count: 2 })
  expect(result.items).toEqual([
    expect.objectContaining({ id: issue.issue_id, success: true, item: expect.objectContaining({ assignee_user_id: user.id, status: 'pending' }) }),
    expect.objectContaining({ id: foreignIssue.issue_id, success: false, code: 'forbidden' }),
    expect.objectContaining({ id: payload.ids[2], success: false, code: 'not_found' }),
  ])
  const ownDetail = await request.get(`/api/v1/data-quality-issues/${issue.issue_id}`)
  expect(ownDetail.status(), await ownDetail.text()).toBe(200)
  expect((await ownDetail.json()).issue.assignee_user_id).toBe(user.id)
  const ownEvents = await request.get(`/api/v1/data-quality-issues/${issue.issue_id}/events?page=1&page_size=100`)
  expect(ownEvents.status(), await ownEvents.text()).toBe(200)
  expect((await ownEvents.json()).items.filter((event: { event_type: string }) => event.event_type === 'assigned')).toEqual([expect.objectContaining({ note })])
  const foreignAfter = await request.get(foreignEndpoint)
  expect(foreignAfter.status(), await foreignAfter.text()).toBe(200)
  expect((await foreignAfter.json()).issue).toEqual(before)
  const eventsAfter = await request.get(`${foreignEndpoint}/events?page=1&page_size=100`)
  expect(eventsAfter.status(), await eventsAfter.text()).toBe(200)
  expect((await eventsAfter.json()).items).toEqual(originalEvents)
})

test('质量问题字段错误精确、子管理员越权先拒绝、非法操作不改问题事件及审计', async ({ request, page }, info) => {
  test.setTimeout(180_000)
  const { user, issue, username, password } = await fixture(request)
  const endpoint = `/api/v1/data-quality-issues/${issue.issue_id}`
  await loginChild(page, username, password)
  const audit = async () => {
    const response = await request.get(`/api/v1/audit-logs?q=${issue.issue_id}&page_size=100`)
    expect(response.status(), await response.text()).toBe(200)
    return (await response.json()).items
  }
  const before = await readQualityEvidence(request, endpoint)
  const beforeAudit = await audit()
  const evidence: unknown[] = []
  const invalid: Array<[string, Record<string, unknown>, string, boolean]> = [
    ['assign', {}, '请选择有效的整改负责人', false],
    ['start', { assignee_user_id: 'bad' }, '请选择有效的整改负责人', false],
    ['assign', { assignee_user_id: user.id, note: '调'.repeat(5001) }, '处理说明最多 5000 字', false],
    ['start', { note: 'S'.repeat(5001) }, '处理说明最多 5000 字', false],
    ['submit', { note: '　 ' }, '处理说明不能为空', false],
    ['submit', { note: '整'.repeat(5001) }, '处理说明最多 5000 字', false],
    ['review', { approved: false, note: '　 ' }, '退回整改时处理说明不能为空', true],
    ['review', { approved: true, note: 'R'.repeat(5001) }, '处理说明最多 5000 字', true],
    ['exempt', { reason: '　 ' }, '豁免原因不能为空', true],
    ['exempt', { reason: '豁'.repeat(5001) }, '豁免原因最多 5000 字', true],
    ['exempt', { reason: '合理原因', expires_at: 'bad' }, '豁免有效期格式不正确', true],
    ['exempt', { reason: '合理原因', expires_at: '2020-01-01T00:00:00Z' }, '豁免有效期必须晚于当前时间', true],
    ['reopen', { note: '重'.repeat(5001) }, '处理说明最多 5000 字', true],
  ]
  for (const [action, data, message, groupOnly] of invalid) {
    for (const [actor, context] of [['group', request], ['child', page.request]] as const) {
      const response = await context.post(`${endpoint}/${action}`, { data })
      const forbidden = actor === 'child' && groupOnly
      expect(response.status(), await response.text()).toBe(forbidden ? 403 : 400)
      expect((await response.json()).error).toEqual(forbidden
        ? { code: 'forbidden', message: '当前账号无权处理该组织的数据质量问题' }
        : { code: 'invalid_data_quality_issue_request', message })
      evidence.push({ action, actor, status: response.status(), message: forbidden ? 'forbidden' : message })
    }
  }
  // Batch entry validation and read-side filters preserve their existing codes.
  // Both actors are real DB users with quality workflow permission.
  const collectionInvalid: Array<[string, string, Record<string, unknown> | null, string, string]> = [
    ['empty_selection', '/batch', { action: 'submit', note: '说明' }, 'invalid_batch', '请至少选择 1 个数据质量问题'],
    ['too_many', '/batch', { ids: Array(201).fill(issue.issue_id), action: 'submit', note: '说明' }, 'invalid_batch', '单次最多选择 200 个数据质量问题'],
    ['bad_id', '/batch', { ids: [issue.issue_id, 'bad'], action: 'submit', note: '说明' }, 'invalid_batch', '数据质量问题标识格式不正确'],
    ['bad_action', '/batch', { ids: [issue.issue_id], action: 'review' }, 'invalid_batch', '请选择有效的批量操作'],
    ['missing_assignee', '/batch', { ids: [issue.issue_id], action: 'assign' }, 'invalid_batch', '请选择有效的整改负责人'],
    ['bad_assignee', '/batch', { ids: [issue.issue_id], action: 'start', assignee_user_id: 'bad' }, 'invalid_batch', '请选择有效的整改负责人'],
    ['blank_note', '/batch', { ids: [issue.issue_id], action: 'submit', note: '　 ' }, 'invalid_batch', '处理说明不能为空'],
    ['long_note', '/batch', { ids: [issue.issue_id], action: 'submit', note: '中'.repeat(5001) }, 'invalid_batch', '处理说明最多 5000 字'],
    ['organization_filter', '?organization_id=bad', null, 'invalid_data_quality_issue_request', '组织筛选标识格式不正确'],
    ['status_filter', '?status=bad', null, 'invalid_data_quality_issue_request', '请选择有效的问题状态'],
    ['severity_filter', '?severity=bad', null, 'invalid_data_quality_issue_request', '请选择有效的严重程度'],
    ['resource_filter', '?resource_type=bad', null, 'invalid_data_quality_issue_request', '请选择有效的检查对象'],
    ['assignee_filter', '?assignee=bad', null, 'invalid_data_quality_issue_request', '整改负责人筛选条件格式不正确'],
    ['detail_id', '/bad', null, 'invalid_data_quality_issue_request', '数据质量问题标识格式不正确'],
    ['events_id', '/bad/events', null, 'invalid_data_quality_issue_request', '数据质量问题标识格式不正确'],
    ['assignees_id', '/bad/assignees', null, 'invalid_data_quality_issue_request', '数据质量问题标识格式不正确'],
  ]
  for (const [action, path, data, code, message] of collectionInvalid) {
    for (const [actor, context] of [['group', request], ['child', page.request]] as const) {
      const url = `/api/v1/data-quality-issues${path}`
      const response = data === null ? await context.get(url) : await context.post(url, { data })
      expect(response.status(), await response.text()).toBe(400)
      expect((await response.json()).error).toEqual({ code, message })
      evidence.push({ action, actor, status: response.status(), message })
    }
  }
  expect(await readQualityEvidence(request, endpoint)).toEqual(before)
  expect(await audit()).toEqual(beforeAudit)
  // Negative form validation uses native user actions and must not submit.
  const pageErrors: string[] = []
  page.on('pageerror', error => pageErrors.push(error.message))
  let submits = 0
  page.on('request', request => { if (request.method() === 'POST' && new URL(request.url()).pathname === `${endpoint}/submit`) submits++ })
  await page.locator('.el-pagination .el-select').click()
  await page.getByRole('option', { name: '100条/页', exact: true }).click()
  const row = page.locator('.quality-issue-table-scroll .el-table__body-wrapper tr').filter({ hasText: issue.rule_name }).filter({ hasText: issue.resource_name })
  await row.getByRole('button', { name: /^操作：/ }).click()
  await page.getByRole('button', { name: '提交复核', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '提交集团复核', exact: true })
  await dialog.locator('textarea').fill('　 ')
  await dialog.getByRole('button', { name: '确认提交集团复核', exact: true }).click()
  await expect(page.getByText('请填写处理说明', { exact: true })).toBeVisible()
  expect(submits).toBe(0)
  expect(pageErrors).toEqual([])
  expect(await readQualityEvidence(request, endpoint)).toEqual(before)
  expect(await audit()).toEqual(beforeAudit)
  await info.attach('quality-issue-validation.json', { body: JSON.stringify({ evidence, rejectedUnchanged: true, blankFormRequested: false, pageErrors }, null, 2), contentType: 'application/json' })
})
