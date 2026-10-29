import { expect, test, type Page } from '@playwright/test'
import { writeFile } from 'node:fs/promises'

test.use({ actionTimeout: 15_000 })
const password = process.env.E2E_PASSWORD
async function login(page: Page, username: string, secret: string) {
  await page.goto('/login')
  await page.getByPlaceholder('请输入用户名').fill(username)
  await page.getByPlaceholder('请输入密码').fill(secret)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page).not.toHaveURL(/\/login/)
}
async function api(page: Page, path: string, data?: unknown, method?: string) {
  return page.evaluate(async ({ path, data, method }) => {
    const response = await fetch(`/api/v1${path}`, { method: method ?? (data === undefined ? 'GET' : 'POST'), headers: { 'Content-Type': 'application/json' }, body: data === undefined ? undefined : JSON.stringify(data) })
    const text = await response.text()
    if (!response.ok) throw new Error(`${path}: ${response.status} ${text}`)
    return text ? JSON.parse(text) : null
  }, { path, data, method })
}
async function changePassword(page: Page, before: string, after: string) {
  await expect(page).toHaveURL(/\/profile/)
  await page.locator('input[autocomplete="current-password"]').fill(before)
  await page.locator('input[autocomplete="new-password"]').nth(0).fill(after)
  await page.locator('input[autocomplete="new-password"]').nth(1).fill(after)
  await page.getByRole('button', { name: '修改密码', exact: true }).click()
  await expect(page).not.toHaveURL(/\/profile/)
}

test('组织管理员直接派给外包人员，个人提交后逐项组织确认和集团复测', async ({ page, browser }, testInfo) => {
  test.skip(!password, 'requires isolated test credentials')
  test.setTimeout(240_000)
  page.setDefaultTimeout(15_000)
  const suffix = Date.now().toString()
  const temporary = 'Repair-Temporary-2026!'
  const formal = 'Repair-Formal-2026!'
  await login(page, process.env.E2E_USERNAME ?? 'admin', password!)
  const group = (await api(page, '/organizations?page_size=100')).items.find((o: { type: string }) => o.type === 'group')
  const org = (await api(page, '/organizations', { parent_id: group.id, code: `RP_${suffix}`, name: `修复验收组织_${suffix}`, short_name: '修复验收', type: 'subsidiary', manager: '王工', contact: '13800000018', notes: '' })).item
  const system = (await api(page, '/business-systems', { code: `RP_SYS_${suffix}`, name: `修复验收系统_${suffix}`, organization_id: org.id, owner_name: '王工', owner_contact: '13800000018', status: 'running', level: 'normal', internet_exposure: 'yes', contains_personal_information: 'no', contains_sensitive_personal_information: 'no', contains_important_data: 'no', contains_core_data: 'no', data_volume_over_100k: 'no', custom_values: [] })).item
  const roles = (await api(page, '/roles/assignable?page_size=100')).items
  const orgRole = roles.find((r: { key: string }) => r.key === 'organization_admin')
  await api(page, '/users', { username: `rp_manager_${suffix}`, password: temporary, display_name: '修复组织管理员', appointments: [{ organization_id: org.id, is_primary: true, assignments: [{ role_id: orgRole.id, data_scope: 'organization' }] }], global_role_ids: [] })
  const vendor = await api(page, '/vendors', { organization_id: org.id, name: `外包厂商_${suffix}`, contact: '13800000018', status: 'enabled' })
  const worker = await api(page, '/remediation-grants', { organization_id: org.id, business_system_id: system.id, vendor_id: vendor.id, participant_type: 'external', username: `rp_worker_${suffix}`, display_name: '外包开发张工', password: temporary, valid_until: new Date(Date.now() + 30 * 86400000).toISOString() })
  const findingIDs: string[] = []
  for (const index of [1, 2]) findingIDs.push((await api(page, '/security-findings', { title: `修复验证漏洞${index}_${suffix}`, severity: 'high', status: 'pending', assignee_organization_id: org.id, business_system_id: system.id, vulnerability_type: '输入验证', description: `需要修复的公开描述${index}`, evidence: '按说明验证输入校验', remediation_advice: '完善输入校验并自测', source: 'manual', discovered_at: new Date().toISOString() })).item.id)
  const order = (await api(page, '/security-work-orders', { execution_mode: 'individual', title: `修复工作台验收_${suffix}`, assignee_organization_id: org.id, description: '组织直接派给具体人员', remediation_requirement: '部署到生产并验证', due_at: new Date(Date.now() + 7 * 86400000).toISOString(), finding_ids: findingIDs })).item
  await api(page, `/security-work-orders/${order.id}/issue`, { comment: '下发个人修复工单' })

  const managerContext = await browser.newContext()
  const manager = await managerContext.newPage()
  manager.setDefaultTimeout(15_000)
  const workerContext = await browser.newContext()
  const workerPage = await workerContext.newPage()
  workerPage.setDefaultTimeout(15_000)
  try {
    await login(manager, `rp_manager_${suffix}`, temporary)
    await changePassword(manager, temporary, formal)
    await manager.goto(`/remediation?work_order_id=${order.id}`)
    await manager.getByRole('button', { name: '派发修复任务', exact: true }).click()
    const dialog = manager.getByRole('dialog', { name: '派发修复任务' })
    await dialog.locator('thead .el-checkbox').click()
    await dialog.getByLabel('具体负责人').click()
    await manager.getByRole('option', { name: /外包开发张工/ }).click()
    await dialog.getByRole('button', { name: '确认共享并派发 2 项' }).click()
    await expect(dialog).not.toBeVisible()
    const tasks = (await api(manager, `/remediation-tasks?work_order_id=${order.id}`)).items
    expect(tasks).toHaveLength(2)
    await manager.screenshot({ path: testInfo.outputPath('remediation-manager.png'), fullPage: true })

    await login(workerPage, `rp_worker_${suffix}`, temporary)
    await changePassword(workerPage, temporary, formal)
    await expect(workerPage).toHaveURL(/\/repair\/tasks$/)
    await expect(workerPage.getByRole('button', { name: '业务系统', exact: true })).toHaveCount(0)
    await expect(workerPage.getByRole('button', { name: '安全工单', exact: true })).toHaveCount(0)
    const denied = await workerPage.evaluate(async () => (await fetch('/api/v1/assets')).status)
    expect(denied).toBe(403)
    await workerPage.screenshot({path:testInfo.outputPath('ui-executor.png')})
    const proofs: { task_id: string; attachment_id: string; content: string }[] = []
    for (const task of tasks) {
      await workerPage.goto(`/repair/tasks?task_id=${task.id}`)
      const detail = workerPage.getByRole('dialog', { name: '修复任务详情' })
      await detail.getByRole('button', { name: '接收任务', exact: true }).click()
      await detail.getByRole('tab', { name: '修复进展', exact: true }).click()
      const proofName = `proof-${task.id}.txt`
      const content = `修复证据 ${task.id}`
      const uploaded = workerPage.waitForResponse(r => r.url().endsWith(`/repair/tasks/${task.id}/attachments`) && r.request().method() === 'POST')
      await detail.getByLabel('上传修复附件').setInputFiles({ name: proofName, mimeType: 'text/plain', buffer: Buffer.from(content) })
      const uploadResponse = await uploaded
      expect(uploadResponse.status()).toBe(200)
      const proof = await uploadResponse.json()
      proofs.push({ task_id: task.id, attachment_id: proof.id, content })
      await detail.getByLabel('本次提交引用的证明附件').press('ArrowDown')
      await workerPage.getByRole('option', { name: proofName, exact: true }).click()
      await detail.getByLabel('修复说明（必填）').click()

      await detail.getByLabel('修复说明（必填）').fill('已修复输入校验并部署 release-2026')
      await detail.getByLabel('版本 / 变更编号').fill('release-2026')
      await detail.getByLabel('部署时间').fill('2026-10-02 10:00:00')
      await detail.getByLabel('自测步骤与结果（必填）').fill('在生产环境按复现步骤验证，未发现原问题')
      await detail.getByRole('button', { name: '提交修复，交组织确认' }).click()
      await expect(detail.getByText('待组织确认', { exact: true }).first()).toBeVisible()
      await detail.getByRole('tab', { name: '提交与审核', exact: true }).click()
      await expect(detail.getByRole('tabpanel',{name:'提交与审核',exact:true}).getByText('已修复输入校验并部署 release-2026', { exact: true })).toBeVisible()
    }
    await workerPage.screenshot({ path: testInfo.outputPath('remediation-worker.png'), fullPage: true })
    await writeFile(testInfo.outputPath('remediation-restore-fixture.json'), JSON.stringify({order_id: order.id, tasks: proofs}))
    await manager.goto(`/remediation?work_order_id=${order.id}`)
    await manager.locator('.repair-list-panel thead .el-checkbox').click()
    await manager.getByRole('button', {name:'进入逐项审核',exact:true}).click()
    for (const [index, task] of tasks.entries()) {
      const detail = manager.getByRole('dialog', { name: '修复任务详情' })
      await expect(detail.getByText(`审核队列 ${index+1} / 2`,{exact:true})).toBeVisible()
      await expect(detail.getByLabel('审核 / 复测结果（执行人员可见，必填）')).toHaveValue('')
      await expect(detail.getByLabel('我已核对目标环境、提交版本及证明材料')).not.toBeChecked()
      await detail.getByLabel('审核 / 复测结果（执行人员可见，必填）').fill('组织确认版本已部署且材料齐全')
      await detail.getByLabel('内部备注（不向执行人员公开）').fill('集团内部验证窗口：不可对外共享')
      await detail.getByRole('button', { name: '确认并送集团复测', exact: true }).click()
      await expect(manager.getByText('请先确认已核对提交版本、目标环境与证明材料',{exact:true})).toBeVisible()
      await detail.getByText('我已核对目标环境、提交版本及证明材料',{exact:true}).click()
      if(index===0)await manager.screenshot({path:testInfo.outputPath('ui-organization-review.png')})
      await detail.getByRole('button', { name: '确认并送集团复测', exact: true }).click()
      // Queue order follows the displayed table, not the fixture creation order.
      await expect.poll(async () => (await api(manager, `/remediation-tasks?work_order_id=${order.id}`)).items.filter((item:{status:string})=>item.status==='pending_security_review').length).toBe(index+1)
    }
    await expect(manager.getByRole('dialog',{name:'修复任务详情'})).not.toBeVisible()
    for (const [index, task] of tasks.entries()) {
      await page.goto(`/remediation?task_id=${task.id}`)
      const detail = page.getByRole('dialog', { name: '修复任务详情' })
      await detail.getByRole('tab', { name: '提交与审核', exact: true }).click()
      await detail.getByLabel('审核 / 复测结果（执行人员可见，必填）').fill(index === 0 ? '生产版本 release-2026 复测通过' : '另一条输入路径仍存在问题，请补充修复')
      if (index === 0) await detail.getByText('我已核对目标环境、提交版本及证明材料',{exact:true}).click()
      await detail.getByRole('button', { name: index === 0 ? '复测通过' : '退回修复', exact: true }).click()
      await expect(detail.getByText(index === 0 ? '复测通过' : '修复中', { exact: true }).first()).toBeVisible()
    }
    await workerPage.goto(`/repair/tasks?task_id=${tasks[1].id}`)
    await expect(workerPage.getByRole('dialog',{name:'修复任务详情'}).getByText('退回原因：另一条输入路径仍存在问题，请补充修复')).toBeVisible()
    await workerPage.getByRole('tab', { name: '提交与审核', exact: true }).click()
    await expect(workerPage.getByText('集团内部验证窗口：不可对外共享')).toHaveCount(0)
    const results = await api(page, `/remediation-tasks?work_order_id=${order.id}`)
    expect(results.items.filter((t: { status: string }) => t.status === 'completed')).toHaveLength(1)
    expect(results.items.filter((t: { status: string }) => t.status === 'in_progress')).toHaveLength(1)
    await api(manager, `/remediation-grants/${worker.id}`, { status: 'revoked', reason: '合作结束，撤销授权' }, 'PATCH')
    const revoked = await workerPage.evaluate(async id => (await fetch(`/api/v1/repair/tasks/${id}`)).status, tasks[1].id)
    expect(revoked).toBe(404)
    await workerPage.goto('/repair/tasks')
    await expect(workerPage.getByText('当前视图没有修复任务')).toBeVisible()
  } finally { await workerContext.close(); await managerContext.close() }
})

test('一次性邀请激活独立执行账号', async ({ page, browser }) => {
  test.skip(!password, 'requires isolated test credentials')
  await login(page, process.env.E2E_USERNAME ?? 'admin', password!)
  const options = await api(page, '/remediation-options?page_size=100')
  test.skip(!options.items.length, 'requires a business system')
  const system = options.items[0]
  const username = `invited_${Date.now()}`
  const invite = await api(page, '/remediation-invitations', { organization_id: system.organization_id, business_system_id: system.id, participant_type: 'internal', username, display_name: '邀请开发人员', valid_until: new Date(Date.now() + 86400000 * 30).toISOString() })
  const context = await browser.newContext()
  try {
    const worker = await context.newPage()
    await worker.goto(`/repair/invite#${invite.token}`)
    await worker.getByLabel('设置登录密码').fill('Repair-Invite-2026!')
    await worker.getByRole('button', { name: '接受邀请', exact: true }).click()
    await expect(worker.getByText('授权已激活，请使用管理员为你指定的用户名和刚设置的密码登录。')).toBeVisible()
    await login(worker, username, 'Repair-Invite-2026!')
    await expect(worker).toHaveURL(/\/repair\/tasks$/)
    const retry = await worker.evaluate(async token => (await fetch('/api/v1/repair-invitations/accept-existing', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ token }) })).status, invite.token)
    expect(retry).toBe(404)
  } finally { await context.close() }
})

test('材料复核、版本差异、执行计划和批量派单部分失败', async ({ page, browser }) => {
  test.skip(!password, 'requires isolated credentials')
  test.setTimeout(240_000)
  await login(page, process.env.E2E_USERNAME ?? 'admin', password!)
  const system = (await api(page, '/remediation-options?page_size=100')).items[0]
  const suffix = Date.now().toString()
  const username = `material_worker_${suffix}`
  const temporary = 'Material-Temporary-2026!'
  const formal = 'Material-Formal-2026!'
  const due = new Date(Date.now() + 2 * 86400000).toISOString()
  const grant = await api(page, '/remediation-grants', { organization_id: system.organization_id, business_system_id: system.id, participant_type: 'internal', username, display_name: '材料复核开发', password: temporary, valid_until: new Date(Date.now() + 30 * 86400000).toISOString() })
  const findingIDs: string[] = []
  for (const n of [1, 2]) findingIDs.push((await api(page, '/security-findings', { title: `材料变更漏洞${n}_${suffix}`, severity: 'high', status: 'pending', assignee_organization_id: system.organization_id, business_system_id: system.id, description: '内部漏洞原始描述', evidence: '内部原始证据', source: 'manual', discovered_at: new Date().toISOString() })).item.id)
  const order = (await api(page, '/security-work-orders', { execution_mode: 'individual', title: `材料复核工单_${suffix}`, assignee_organization_id: system.organization_id, remediation_requirement: '部署生产后验证', due_at: due, finding_ids: findingIDs })).item
  await api(page, `/security-work-orders/${order.id}/issue`, { comment: '下发' })
  const assignment = (id: string, grantID: string) => ({ finding_id: id, grant_id: grantID, due_at: due, target_environment: '生产环境', deployment_required: true, material: { title: '已批准公开漏洞', severity: 'high', description: '第一版公开描述', requirement: '生产验证', evidence: '公开复现步骤', remediation_advice: '修复输入校验' } })
  const partial = await page.evaluate(async ({ orderID, items, key }) => {
    const send = async () => {
      const response = await fetch(`/api/v1/security-work-orders/${orderID}/remediation-tasks/batch-assign`, { method: 'POST', headers: { 'Content-Type': 'application/json', 'Idempotency-Key': key }, body: JSON.stringify({ items }) })
      if (!response.ok) throw new Error(`batch failed: ${response.status}`)
      return response.json()
    }
    return [await send(), await send()]
  }, { orderID: order.id, items: [assignment(findingIDs[0]!, grant.id), assignment(findingIDs[1]!, grant.user_id)], key: `partial-${suffix}` })
  expect(partial[0].items[0].id).toBeTruthy()
  expect(partial[0].items[1].error).toBeTruthy()
  expect(partial[1].items[0].id).toBe(partial[0].items[0].id)
  const taskID = partial[0].items[0].id
  const workerContext = await browser.newContext()
  try {
    const worker = await workerContext.newPage()
    await login(worker, username, temporary)
    await changePassword(worker, temporary, formal)
    await worker.goto(`/repair/tasks?task_id=${taskID}`)
    let detail = worker.getByRole('dialog', { name: '修复任务详情' })
    await expect(detail.getByLabel('预计完成时间（接收时必填）')).toBeVisible()
    await detail.getByRole('button', { name: '接收任务', exact: true }).click()
    async function submit(summary: string) {
      detail = worker.getByRole('dialog', { name: '修复任务详情' })
      await detail.getByRole('tab', { name: '修复进展', exact: true }).click()
      await detail.getByLabel('修复说明（必填）').fill(summary)
      await detail.getByLabel('版本 / 变更编号').fill('release-material')
      await detail.getByLabel('部署时间').fill('2026-10-02 10:00:00')
      await detail.getByLabel('自测步骤与结果（必填）').fill('按批准材料验证')
      await detail.getByRole('button', { name: '提交修复，交组织确认' }).click()
      await expect(detail.getByText('待组织确认', { exact: true }).first()).toBeVisible()
    }
    await submit('第一轮修复结果')
    await page.goto(`/remediation?task_id=${taskID}`)
    const adminDetail = page.getByRole('dialog', { name: '修复任务详情' })
    await adminDetail.getByRole('button', { name: '更新原始漏洞材料', exact: true }).click()
    const source = page.getByRole('dialog', { name: '更新原始漏洞材料' })
    await source.getByLabel('原始描述').fill('新增内部路径描述')
    await source.getByLabel('内部原始证据').fill('SOURCE-PRIVATE-DO-NOT-SHARE')
    await source.getByLabel('变更原因').fill('发现需要补充验证的路径')
    await source.getByRole('button', { name: '更新并通知材料复核' }).click()
    await expect(source).not.toBeVisible()
    await worker.reload()
    await expect(worker.getByText('共享材料待管理员复核，暂不能提交或通过审核。原有材料保留供查阅。')).toBeVisible()
    const stale = await api(worker, `/repair/tasks/${taskID}`)
    expect(stale.item.material_needs_review).toBe(true)
    expect(JSON.stringify(stale)).not.toContain('SOURCE-PRIVATE-DO-NOT-SHARE')
    await adminDetail.getByRole('button', { name: '更新共享材料', exact: true }).click()
    const material = page.getByRole('dialog', { name: '更新共享材料' })
    await material.getByLabel('漏洞描述').fill('第二版脱敏公开描述')
    await material.getByLabel('复现步骤与证据').fill('可公开的新路径验证步骤')
    await material.getByLabel('更新原因').fill('已复核并去除内部信息')
    await material.getByRole('button', { name: '确认共享并保存新版本' }).click()
    await expect(material).not.toBeVisible()
    await worker.reload()
    await submit('第二轮按新材料完成修复')
    await page.reload()
    await adminDetail.getByRole('tab', { name: '提交与审核', exact: true }).click()
    await adminDetail.getByText('比较两次提交的差异', { exact: true }).click()
    await expect(adminDetail.getByRole('cell', { name: '第一轮修复结果', exact: true })).toBeVisible()
    await expect(adminDetail.getByRole('cell', { name: /第二轮按新材料完成修复/ })).toBeVisible()
    await adminDetail.getByLabel('审核 / 复测结果（执行人员可见，必填）').fill('组织核验通过')
    await adminDetail.getByText('我已核对目标环境、提交版本及证明材料',{exact:true}).click()
    await adminDetail.getByRole('button', { name: '确认并送集团复测' }).click()
    await expect(adminDetail.getByLabel('实际验证环境（必填）')).toBeVisible()
    await adminDetail.getByLabel('审核 / 复测结果（执行人员可见，必填）').fill('新路径已部署验证通过')
    await adminDetail.getByText('我已核对目标环境、提交版本及证明材料',{exact:true}).click()
    await adminDetail.getByRole('button', { name: '复测通过', exact: true }).click()
    await expect.poll(async () => (await api(page, `/remediation-tasks/${taskID}`)).item.status).toBe('completed')
    const stats = await api(page, `/remediation-tasks/statistics?work_order_id=${order.id}&status=all`)
    expect(stats.verified_fixed).toBe(1)
    await worker.goto('/repair/tasks')
    await expect(worker.getByText('当前视图没有修复任务')).toBeVisible()
  } finally { await workerContext.close() }
})
