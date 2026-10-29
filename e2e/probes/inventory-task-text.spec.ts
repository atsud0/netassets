import { expect, test, type APIResponse, type Response } from '@playwright/test'
import { writeFile } from 'node:fs/promises'
import { clickRowAction } from '../helpers/row-actions'
import { findRecordPage } from '../helpers/pagination'

async function body(response: APIResponse | Response, status = 200) {
  expect(response.status()).toBe(status)
  return response.json()
}
async function login(request: import('@playwright/test').APIRequestContext) {
  await body(await request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }))
}
async function organization(request: import('@playwright/test').APIRequestContext, stamp: string) {
  const roots = (await body(await request.get('/api/v1/organizations?page=1&page_size=100'))).items
  const root = roots.find((item: { type: string }) => item.type === 'group')
  expect(root).toBeTruthy()
  return (await body(await request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `TEXT_${stamp}`, name: `任务文字边界_${stamp}`, type: 'subsidiary' } }), 201)).item
}

for (const target of ['name', 'description'] as const) {
  test(`任务${target === 'name' ? '名称' : '说明'}：Unicode满额不截断、超限不创建、草稿原文读回`, async ({ page }, info) => {
    test.setTimeout(240_000)
    test.skip(!process.env.E2E_PASSWORD, 'E2E_PASSWORD 未配置')
    await login(page.request)
    const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
    const org = await organization(page.request, stamp)
    const records: Array<{ id: string; unit: string }> = []
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    const path = '/api/v1/inventory-tasks'
    let writes = 0
    page.on('request', request => { if (new URL(request.url()).pathname === path && request.method() === 'POST') writes++ })
    for (const unit of ['🙂', 'a', '中']) {
      await page.goto('/inventory-tasks')
      await page.getByRole('button', { name: '创建任务', exact: true }).click()
      const dialog = page.getByRole('dialog', { name: '创建填报任务', exact: true })
      const name = dialog.getByLabel('任务名称', { exact: true })
      const description = dialog.getByLabel('任务说明', { exact: true })
      await dialog.locator('.el-form-item').filter({ has: page.getByText('填报组织', { exact: true }) }).locator('.el-select').click()
      await page.getByRole('option', { name: org.name, exact: true }).click()
      await name.click()
      await dialog.locator('.el-form-item').filter({ has: page.getByText('任务状态', { exact: true }) }).locator('.el-select').click()
      await page.getByRole('option', { name: '保存草稿', exact: true }).click()
      await expect(dialog.getByTestId('scope-preview-summary')).toContainText('1 家组织')
      const save = dialog.getByRole('button', { name: '保存草稿', exact: true })
      const before = writes
      await name.fill('　 ')
      await save.click()
      await expect(dialog.getByText('请输入任务名称', { exact: true })).toBeVisible()
      expect(writes).toBe(before)
      const baseName = `文字任务_${stamp}_${unit}`
      await name.fill(baseName)
      await description.fill('合法任务说明')
      const input = target === 'name' ? name : description
      const limit = target === 'name' ? 255 : 5000
      const value = unit.repeat(limit)
      await input.fill(value)
      await expect(input).toHaveValue(value)
      const totalBefore = (await body(await page.request.get(`${path}?page_size=1`))).total
      await input.fill(unit.repeat(limit + 1))
      await save.click()
      await expect(dialog.getByText(target === 'name' ? '任务名称最多 255 字' : '任务说明最多 5000 字', { exact: true })).toBeVisible()
      await expect(input).toHaveValue(unit.repeat(limit + 1))
      await expect(dialog).toBeVisible()
      expect(writes).toBe(before)
      expect((await body(await page.request.get(`${path}?page_size=1`))).total).toBe(totalBefore)
      await input.fill(value)
      const saved = page.waitForResponse(response => new URL(response.url()).pathname === path && response.request().method() === 'POST')
      await save.click()
      const item = (await body(await saved, 201)).item
      const expected = { name: target === 'name' ? value : baseName, description: target === 'description' ? value : '合法任务说明', status: 'draft', target_count: 1 }
      expect(item).toMatchObject(expected)
      expect(item.organizations.map((item: { organization_id: string }) => item.organization_id)).toEqual([org.id])
      await expect(dialog).toBeHidden()
      expect(writes).toBe(before + 1)
      const stored = await findRecordPage<{ id: string }>(page.request, path, task => task.id === item.id)
      expect(stored?.item).toMatchObject(expected)
      records.push({ id: item.id, unit })
    }
    expect(errors).toEqual([])
    await writeFile(info.outputPath('inventory-text-evidence.json'), JSON.stringify({ target, organization_id: org.id, records, page_errors: errors }, null, 2))
  })
}

test('真实子管理员提交说明：Unicode满额不截断、超限不请求、提交原文落库', async ({ page, browser, baseURL }, info) => {
  test.setTimeout(240_000)
  test.skip(!process.env.E2E_PASSWORD, 'E2E_PASSWORD 未配置')
  await login(page.request)
  const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
  const org = await organization(page.request, stamp)
  await body(await page.request.post('/api/v1/business-systems', { data: {
    code: `TEXT_SYS_${stamp}`, name: `文字任务系统_${stamp}`, organization_id: org.id, status: 'running', level: 'normal', internet_exposure: 'no',
    contains_personal_information: 'no', contains_sensitive_personal_information: 'no', data_volume_over_100k: 'no', contains_important_data: 'no', contains_core_data: 'no',
    owner_name: '填报回归', owner_contact: '13800000000', custom_values: [],
  } }), 201)
  const roles = (await body(await page.request.get('/api/v1/roles?page=1&page_size=100'))).items
  const role = roles.find((item: { key: string }) => item.key === 'organization_admin')
  expect(role).toBeTruthy()
  const username = `text_child_${stamp}`, password = 'TaskTextUnicode123!'
  await body(await page.request.post('/api/v1/users', { data: { username, password, display_name: '填报文字子管理员', global_role_ids: [], appointments: [{ organization_id: org.id, is_primary: true, assignments: [{ role_id: role.id, data_scope: 'organization' }] }] } }), 201)
  const child = await browser.newContext({ baseURL })
  try {
    const user = (await body(await child.request.post('/api/v1/auth/login', { data: { username, password } }))).user
    expect(user.roles).toEqual(['organization_admin'])
    expect((await child.request.put('/api/v1/auth/profile/password', { data: { current_password: password, new_password: `${password}Changed` } })).status()).toBe(204)
    const records: Array<{ id: string; unit: string }> = []
    const childPage = await child.newPage()
    const errors: string[] = []
    childPage.on('pageerror', error => errors.push(error.message))
    let writes = 0
    childPage.on('request', request => { if (new URL(request.url()).pathname.endsWith('/submit') && request.method() === 'POST') writes++ })
    for (const [index, unit] of ['🙂', 'a', '中'].entries()) {
      const task = (await body(await page.request.post('/api/v1/inventory-tasks', { data: {
        name: `文字提交_${stamp}_${index}`, description: '真实子管理员提交说明边界', start_at: new Date(Date.now() - 3600000).toISOString(), deadline_at: new Date(Date.now() + 86400000).toISOString(),
        status: 'active', asset_network_scope: 'all', required_fields: ['business_systems'], quality_rules: [], require_review: true, allow_late_submission: false, organization_ids: [org.id],
      } }), 201)).item
      const path = `/api/v1/inventory-tasks/${task.id}/organizations/${org.id}`
      await body(await child.request.post(`${path}/start`))
      await childPage.goto(`/inventory-tasks?task_id=${task.id}&organization_id=${org.id}`)
      const detail = childPage.getByRole('dialog', { name: '填报任务详情', exact: true })
      const row = detail.locator('.task-organization-table .el-table__body-wrapper tr').filter({ hasText: org.name })
      await clickRowAction(childPage, row, '提交')
      const dialog = childPage.getByRole('dialog', { name: '提交填报结果', exact: true })
      await expect(dialog.getByText('检查通过，可以提交', { exact: true })).toBeVisible()
      const note = dialog.getByLabel('填报说明', { exact: true })
      const value = unit.repeat(5000)
      const before = writes
      await note.fill(value)
      await expect(note).toHaveValue(value)
      await note.fill(unit.repeat(5001))
      await dialog.getByRole('button', { name: '确认提交', exact: true }).click()
      await expect(childPage.getByText('提交说明最多 5000 字', { exact: true })).toBeVisible()
      await expect(note).toHaveValue(unit.repeat(5001))
      await expect(dialog).toBeVisible()
      expect(writes).toBe(before)
      const unchanged = (await body(await page.request.get(`/api/v1/inventory-tasks/${task.id}/organizations?page=1&page_size=20`))).items.find((item: { organization_id: string }) => item.organization_id === org.id)
      expect(unchanged.status).toBe('in_progress')
      expect(unchanged.submission_note).toBeFalsy()
      await note.fill(value)
      const submitted = childPage.waitForResponse(response => new URL(response.url()).pathname === `${path}/submit` && response.request().method() === 'POST')
      await dialog.getByRole('button', { name: '确认提交', exact: true }).click()
      expect((await body(await submitted)).item).toMatchObject({ status: 'submitted', submission_note: value, submitted_system_count: 1 })
      await expect(dialog).toBeHidden()
      expect(writes).toBe(before + 1)
      const stored = (await body(await page.request.get(`/api/v1/inventory-tasks/${task.id}/organizations?page=1&page_size=20`))).items.find((item: { organization_id: string }) => item.organization_id === org.id)
      expect(stored).toMatchObject({ status: 'submitted', submission_note: value, submitted_system_count: 1 })
      records.push({ id: task.id, unit })
    }
    expect(errors).toEqual([])
    await writeFile(info.outputPath('inventory-text-evidence.json'), JSON.stringify({ target: 'submission_note', organization_id: org.id, submitted_by: user.id, records, page_errors: errors }, null, 2))
  } finally {
    await child.close()
  }
})
