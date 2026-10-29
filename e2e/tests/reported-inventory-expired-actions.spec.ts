import { expect, test, type APIRequestContext, type Locator, type Page } from '@playwright/test'

test.setTimeout(120_000)

async function fixture(request: APIRequestContext, page: Page) {
  const login = await request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME, password: process.env.E2E_PASSWORD } })
  expect(login.status(), await login.text()).toBe(200)
  const roots = await request.get('/api/v1/organizations?page=1&page_size=100')
  expect(roots.status(), await roots.text()).toBe(200)
  const root = (await roots.json()).items.find((item: { type: string }) => item.type === 'group')
  const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
  const orgResponse = await request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `EXPIRED_${stamp}`, name: `过期操作回归${stamp}`, type: 'subsidiary' } })
  expect(orgResponse.status(), await orgResponse.text()).toBe(201)
  const org = (await orgResponse.json()).item
  const roles = await request.get('/api/v1/roles?page=1&page_size=100')
  expect(roles.status(), await roles.text()).toBe(200)
  const role = (await roles.json()).items.find((item: { key: string }) => item.key === 'organization_admin')
  const username = `expired_${stamp}`, password = 'ExpiredAction123!'
  const user = await request.post('/api/v1/users', { data: { username, password, display_name: '过期任务子管理员', global_role_ids: [], appointments: [{ organization_id: org.id, is_primary: true, assignments: [{ role_id: role.id, data_scope: 'organization' }] }] } })
  expect(user.status(), await user.text()).toBe(201)
  const childLogin = await page.request.post('/api/v1/auth/login', { data: { username, password } })
  expect(childLogin.status(), await childLogin.text()).toBe(200)
  const changed = await page.request.put('/api/v1/auth/profile/password', { data: { current_password: password, new_password: `${password}Changed` } })
  expect(changed.status(), await changed.text()).toBe(204)
  return { org, stamp }
}

async function createTask(request: APIRequestContext, organizationID: string, name: string, allowLate: boolean, deadlineAt?: number) {
  const now = Date.now()
  const response = await request.post('/api/v1/inventory-tasks', { data: {
    name, description: '过期操作显示及权限回归', start_at: new Date(now - 172_800_000).toISOString(), deadline_at: new Date(deadlineAt ?? now - 86_400_000).toISOString(),
    status: 'active', asset_network_scope: 'all', required_fields: ['business_systems'], quality_rules: [], require_review: true, allow_late_submission: allowLate, organization_ids: [organizationID],
  } })
  expect(response.status(), await response.text()).toBe(201)
  return (await response.json()).item
}

async function openTask(page: Page, name: string, status = '已过期') {
  await page.goto('/inventory-tasks')
  const row = page.locator('.el-table__body-wrapper tr').filter({ hasText: name })
  await expect(row.locator('.el-tag')).toHaveText(status)
  await row.getByRole('button', { name, exact: true }).click()
  const detail = page.getByRole('dialog', { name: '填报任务详情', exact: true })
  await expect(detail).toBeVisible()
  return detail
}

async function expectTextUnclipped(text: Locator) {
  await expect(text).toBeVisible()
  // Visibility alone passes for text hidden by an overflow:hidden table cell.
  // Measure every wrapped line, not just the element's (potentially clipped) box.
  const dimensions = await text.evaluate(element => {
    const cell = element.closest('.cell, .row-actions-popover')!
    const range = document.createRange()
    range.selectNodeContents(element)
    const bounds = cell.getBoundingClientRect()
    return { left: bounds.left, right: bounds.right, top: bounds.top, bottom: bounds.bottom, lines: Array.from(range.getClientRects()).map(rect => ({ left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom })) }
  })
  expect(dimensions.lines.length).toBeGreaterThan(0)
  for (const line of dimensions.lines) {
    expect(line.left, JSON.stringify(dimensions)).toBeGreaterThanOrEqual(dimensions.left - 1)
    expect(line.right, JSON.stringify(dimensions)).toBeLessThanOrEqual(dimensions.right + 1)
    expect(line.top, JSON.stringify(dimensions)).toBeGreaterThanOrEqual(dimensions.top - 1)
    expect(line.bottom, JSON.stringify(dimensions)).toBeLessThanOrEqual(dimensions.bottom + 1)
  }
}

test('不允许补报的过期任务完整显示截止说明且无法开始或提交', async ({ request, page }, testInfo) => {
  const { org, stamp } = await fixture(request, page)
  const task = await createTask(request, org.id, `禁止补报${stamp}`, false)
  const detail = await openTask(page, task.name)
  const progressRow = detail.locator('.task-organization-table .el-table__body-wrapper tr').filter({ hasText: org.name })
  await expect(progressRow.getByRole('button')).toHaveCount(0)
  const reason = progressRow.getByText('已截止，不允许逾期提交', { exact: true })
  for (const width of [1280, 960]) {
    await page.setViewportSize({ width, height: 900 })
    await expectTextUnclipped(reason)
    await page.screenshot({ path: testInfo.outputPath(`expired-no-late-${width}.png`), fullPage: true })
  }
  const started = await page.request.post(`/api/v1/inventory-tasks/${task.id}/organizations/${org.id}/start`)
  expect(started.status(), await started.text()).toBe(409)
})

async function createSystem(request: APIRequestContext, organizationID: string, stamp: string) {
  const response = await request.post('/api/v1/business-systems', { data: {
    code: `EXPIRED_SYS_${stamp}`, name: `过期填报有效系统${stamp}`, organization_id: organizationID,
    status: 'running', level: 'normal', internet_exposure: 'no', contains_personal_information: 'no',
    contains_sensitive_personal_information: 'no', data_volume_over_100k: 'no', contains_important_data: 'no',
    contains_core_data: 'no', owner_name: '填报负责人', owner_contact: '13800000000', custom_values: [],
  } })
  expect(response.status(), await response.text()).toBe(201)
}

function progressRow(detail: Locator, organizationName: string) {
  return detail.locator('.task-organization-table .el-table__body-wrapper tr').filter({ hasText: organizationName })
}

async function actions(page: Page, row: Locator, expected: string[]) {
  await row.getByRole('button', { name: /^操作：/ }).click()
  const menu = page.locator('.row-actions-popover:visible')
  await expect(menu).toBeVisible()
  await expect(menu.getByRole('button')).toHaveText(expected)
  for (const name of expected) await expect(menu.getByRole('button', { name, exact: true })).toBeEnabled()
  return menu
}

async function submitInBrowser(page: Page, row: Locator, note: string) {
  const menu = await actions(page, row, ['维护业务系统', '维护网络资产', '提交'])
  await menu.getByRole('button', { name: '提交', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '提交填报结果', exact: true })
  await expect(dialog.getByText('检查通过，可以提交', { exact: true })).toBeVisible()
  await dialog.getByRole('textbox', { name: '填报说明', exact: true }).fill(note)
  const saved = page.waitForResponse(response => response.url().endsWith('/submit') && response.request().method() === 'POST')
  await dialog.getByRole('button', { name: '确认提交', exact: true }).click()
  const response = await saved
  expect(response.status(), await response.text()).toBe(200)
  const item = (await response.json()).item
  expect(item.status).toBe('submitted')
  expect(item.submitted_system_count).toBe(1)
  expect(item.submission_note).toBe(note)
  await expect(dialog).toBeHidden()
  return item
}

test('进行中任务真实到期后禁止补报，质检通过也无法绕过截止限制', async ({ request, page }, testInfo) => {
  const { org, stamp } = await fixture(request, page)
  await createSystem(request, org.id, stamp)
  const deadlineAt = Date.now() + 12_000
  const task = await createTask(request, org.id, `进行中到期${stamp}`, false, deadlineAt)
  const path = `/api/v1/inventory-tasks/${task.id}/organizations/${org.id}`
  const started = await page.request.post(`${path}/start`)
  expect(started.status(), await started.text()).toBe(200)
  expect((await started.json()).item.status).toBe('in_progress')
  // Wait for the actual deadline; no database manipulation or browser/server clock mocks.
  await expect.poll(() => Date.now(), { timeout: 20_000 }).toBeGreaterThan(deadlineAt + 200)
  const detail = await openTask(page, task.name)
  const row = progressRow(detail, org.name)
  await expect(row.getByRole('button')).toHaveCount(0)
  for (const width of [1280, 960]) {
    await page.setViewportSize({ width, height: 900 })
    await expectTextUnclipped(row.getByText('已截止，不允许逾期提交', { exact: true }))
  }
  await page.screenshot({ path: testInfo.outputPath('expired-in-progress.png'), fullPage: true })
  const quality = await page.request.get(`${path}/quality-check`)
  expect(quality.status(), await quality.text()).toBe(200)
  expect((await quality.json()).quality_check.passed).toBe(true)
  const submitted = await page.request.post(`${path}/submit`, { data: { note: '禁止绕过截止' } })
  expect(submitted.status(), await submitted.text()).toBe(409)
  const persisted = await request.get(`/api/v1/inventory-tasks/${task.id}/organizations?page=1&page_size=20`)
  expect(persisted.status(), await persisted.text()).toBe(200)
  const item = (await persisted.json()).items.find((item: { organization_id: string }) => item.organization_id === org.id)
  expect(item.status).toBe('in_progress')
  expect(item.submitted_at).toBeFalsy()
})

test('允许补报的过期任务可开始、提交、退回重提及审核，操作菜单与真实权限一致', async ({ request, page, browser }, testInfo) => {
  const { org, stamp } = await fixture(request, page)
  await createSystem(request, org.id, stamp)
  const task = await createTask(request, org.id, `允许补报${stamp}`, true)
  let detail = await openTask(page, task.name)
  let row = progressRow(detail, org.name)
  let menu = await actions(page, row, ['维护业务系统', '维护网络资产', '开始填报'])
  const started = page.waitForResponse(response => response.url().endsWith('/start') && response.request().method() === 'POST')
  await menu.getByRole('button', { name: '开始填报', exact: true }).click()
  const startResponse = await started
  expect(startResponse.status(), await startResponse.text()).toBe(200)
  expect((await startResponse.json()).item.status).toBe('in_progress')
  // Reopen after the mutation; clicking while the table is being replaced closes
  // the old teleported popover before its buttons can be read.
  detail = await openTask(page, task.name)
  row = progressRow(detail, org.name)
  await submitInBrowser(page, row, `过期首次提交${stamp}`)
  detail = await openTask(page, task.name)
  row = progressRow(detail, org.name)
  menu = await actions(page, row, ['查看差异', '审核意见'])
  await page.screenshot({ path: testInfo.outputPath('expired-submitted-child-actions.png'), fullPage: true })
  await page.keyboard.press('Escape')
  // A real child role must not acquire the group's review operation through a direct request.
  const reviewDenied = await page.request.post(`/api/v1/inventory-tasks/${task.id}/organizations/${org.id}/review`, { data: { decision: 'approved', return_reason: '' } })
  expect(reviewDenied.status(), await reviewDenied.text()).toBe(403)

  const groupContext = await browser.newContext({ baseURL: String(testInfo.project.use.baseURL), storageState: await request.storageState() })
  try {
    const groupPage = await groupContext.newPage()
    const groupDetail = await openTask(groupPage, task.name)
    const groupRow = progressRow(groupDetail, org.name)
    menu = await actions(groupPage, groupRow, ['查看差异', '审核意见', '通过', '退回'])
    await menu.getByRole('button', { name: '退回', exact: true }).click()
    const returnDialog = groupPage.getByRole('dialog', { name: '退回填报结果', exact: true })
    await returnDialog.getByLabel('整体退回原因').fill(`过期补报退回原因${stamp}`)
    const returned = groupPage.waitForResponse(response => response.url().endsWith('/review') && response.request().method() === 'POST')
    await returnDialog.getByRole('button', { name: '确认退回', exact: true }).click()
    const returnResponse = await returned
    expect(returnResponse.status(), await returnResponse.text()).toBe(200)
    expect((await returnResponse.json()).item.status).toBe('returned')
    await expect(returnDialog).toBeHidden()

    detail = await openTask(page, task.name)
    row = progressRow(detail, org.name)
    menu = await actions(page, row, ['查看差异', '审核意见', '维护业务系统', '维护网络资产', '开始填报', '提交'])
    await menu.getByRole('button', { name: '提交', exact: true }).click()
    const submitDialog = page.getByRole('dialog', { name: '提交填报结果', exact: true })
    await expect(submitDialog.getByText('检查通过，可以提交', { exact: true })).toBeVisible()
    const note = `退回后补报${stamp}`
    await submitDialog.getByRole('textbox', { name: '填报说明', exact: true }).fill(note)
    const resubmitted = page.waitForResponse(response => response.url().endsWith('/submit') && response.request().method() === 'POST')
    await submitDialog.getByRole('button', { name: '确认提交', exact: true }).click()
    const resubmitResponse = await resubmitted
    expect(resubmitResponse.status(), await resubmitResponse.text()).toBe(200)
    const resubmitItem = (await resubmitResponse.json()).item
    expect(resubmitItem.status).toBe('submitted')
    expect(resubmitItem.submission_note).toBe(note)
    expect(resubmitItem.submitted_system_count).toBe(1)
    await expect(submitDialog).toBeHidden()

    await openTask(groupPage, task.name)
    menu = await actions(groupPage, groupRow, ['查看差异', '审核意见', '通过', '退回'])
    await menu.getByRole('button', { name: '通过', exact: true }).click()
    const approveDialog = groupPage.getByRole('dialog', { name: '审核通过', exact: true })
    const approved = groupPage.waitForResponse(response => response.url().endsWith('/review') && response.request().method() === 'POST')
    await approveDialog.getByRole('button', { name: '通过', exact: true }).click()
    const approveResponse = await approved
    expect(approveResponse.status(), await approveResponse.text()).toBe(200)
    expect((await approveResponse.json()).item.status).toBe('approved')
    await expect(approveDialog).toBeHidden()
    await openTask(groupPage, task.name, '已完成')
    menu = await actions(groupPage, groupRow, ['查看差异', '审核意见'])
    await expectTextUnclipped(menu.locator('.table-secondary'))
    detail = await openTask(page, task.name, '已完成')
    row = progressRow(detail, org.name)
    await expect(row.locator('.el-tag')).toHaveText('已通过')
    await actions(page, row, ['查看差异', '审核意见'])
    await page.screenshot({ path: testInfo.outputPath('expired-approved-child.png'), fullPage: true })
    await groupPage.screenshot({ path: testInfo.outputPath('expired-approved-group.png'), fullPage: true })
    const persisted = await request.get(`/api/v1/inventory-tasks/${task.id}/organizations?page=1&page_size=20`)
    expect(persisted.status(), await persisted.text()).toBe(200)
    const item = (await persisted.json()).items.find((item: { organization_id: string }) => item.organization_id === org.id)
    expect(item.status).toBe('approved')
    expect(item.submitted_system_count).toBe(1)
    expect(item.submission_note).toBe(note)
    expect(item.snapshot_available).toBe(true)
    expect(item.reviewed_at).toBeTruthy()
  } finally {
    await groupContext.close()
  }
})


test('禁止补报时已提交仍可审核，已退回只能查看快照和完整截止说明', async ({ request, page, browser }, testInfo) => {
  const { org, stamp } = await fixture(request, page)
  await createSystem(request, org.id, stamp)
  const deadlineAt = Date.now() + 20_000
  const submittedTask = await createTask(request, org.id, `到期待审核${stamp}`, false, deadlineAt)
  const returnedTask = await createTask(request, org.id, `到期已退回${stamp}`, false, deadlineAt)
  for (const task of [submittedTask, returnedTask]) {
    const path = `/api/v1/inventory-tasks/${task.id}/organizations/${org.id}`
    const start = await page.request.post(`${path}/start`)
    expect(start.status(), await start.text()).toBe(200)
    const submit = await page.request.post(`${path}/submit`, { data: { note: `截止前提交${stamp}` } })
    expect(submit.status(), await submit.text()).toBe(200)
    expect((await submit.json()).item.submitted_system_count).toBe(1)
  }
  const returnReason = `截止前集团退回${stamp}`
  const returned = await request.post(`/api/v1/inventory-tasks/${returnedTask.id}/organizations/${org.id}/review`, { data: { decision: 'returned', return_reason: returnReason, comments: [] } })
  expect(returned.status(), await returned.text()).toBe(200)
  expect((await returned.json()).item.status).toBe('returned')
  await expect.poll(() => Date.now(), { timeout: 30_000 }).toBeGreaterThan(deadlineAt + 200)

  let detail = await openTask(page, submittedTask.name)
  let row = progressRow(detail, org.name)
  await expect(row.locator('.el-tag')).toHaveText('待审核')
  let menu = await actions(page, row, ['查看差异', '审核意见'])
  await expect(menu.getByText('已截止，不允许逾期提交', { exact: true })).toHaveCount(0)

  detail = await openTask(page, returnedTask.name)
  row = progressRow(detail, org.name)
  await expect(row.locator('.el-tag')).toHaveText('已逾期')
  await expect(row.getByText(`退回：${returnReason}`, { exact: true })).toBeVisible()
  menu = await actions(page, row, ['查看差异', '审核意见'])
  await expectTextUnclipped(menu.getByText('已截止，不允许逾期提交', { exact: true }))
  await page.screenshot({ path: testInfo.outputPath('expired-returned-no-late.png'), fullPage: true })
  const resubmit = await page.request.post(`/api/v1/inventory-tasks/${returnedTask.id}/organizations/${org.id}/submit`, { data: { note: '截止后不能再次提交' } })
  expect(resubmit.status(), await resubmit.text()).toBe(409)

  const groupContext = await browser.newContext({ baseURL: String(testInfo.project.use.baseURL), storageState: await request.storageState() })
  try {
    const groupPage = await groupContext.newPage()
    const groupDetail = await openTask(groupPage, submittedTask.name)
    const groupRow = progressRow(groupDetail, org.name)
    menu = await actions(groupPage, groupRow, ['查看差异', '审核意见', '通过', '退回'])
    await menu.getByRole('button', { name: '通过', exact: true }).click()
    const confirm = groupPage.getByRole('dialog', { name: '审核通过', exact: true })
    const responsePromise = groupPage.waitForResponse(response => response.url().endsWith('/review') && response.request().method() === 'POST')
    await confirm.getByRole('button', { name: '通过', exact: true }).click()
    const response = await responsePromise
    expect(response.status(), await response.text()).toBe(200)
    expect((await response.json()).item.status).toBe('approved')
    // Rejected late resubmission must preserve the original return and snapshot.
    const progress = await request.get(`/api/v1/inventory-tasks/${returnedTask.id}/organizations?page=1&page_size=20`)
    expect(progress.status(), await progress.text()).toBe(200)
    const item = (await progress.json()).items.find((item: { organization_id: string }) => item.organization_id === org.id)
    expect(item.status).toBe('returned')
    expect(item.return_reason).toBe(returnReason)
    expect(item.submission_note).toBe(`截止前提交${stamp}`)
    expect(item.snapshot_available).toBe(true)
  } finally {
    await groupContext.close()
  }
})
