import { expect, test, type APIResponse, type Response } from '@playwright/test'
import { clickRowAction } from '../helpers/row-actions'

async function body(response: APIResponse | Response, status = 200) {
  expect(response.status()).toBe(status)
  return response.json()
}

for (const target of ['reason', 'comment'] as const) {
  test(`填报退回${target === 'reason' ? '整体原因' : '逐条意见'}：Unicode满额不截断、超限不请求、原文落库`, async ({ page, browser, baseURL }) => {
    test.setTimeout(240_000)
    test.skip(!process.env.E2E_PASSWORD, 'E2E_PASSWORD 未配置')
    await body(await page.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }))
    const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 7)}`
    const roots = (await body(await page.request.get('/api/v1/organizations?page=1&page_size=100'))).items
    const root = roots.find((item: { type: string }) => item.type === 'group')
    expect(root).toBeTruthy()
    const org = (await body(await page.request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `REVIEW_UNI_${stamp}`, name: `审核原文${stamp}`, type: 'subsidiary' } }), 201)).item
    const system = (await body(await page.request.post('/api/v1/business-systems', { data: {
      code: `REVIEW_SYS_${stamp}`, name: `审核快照系统${stamp}`, organization_id: org.id, status: 'running', level: 'normal', internet_exposure: 'no',
      contains_personal_information: 'no', contains_sensitive_personal_information: 'no', data_volume_over_100k: 'no', contains_important_data: 'no', contains_core_data: 'no',
      owner_name: '审核回归', owner_contact: '13800000000', custom_values: [],
    } }), 201)).item
    const roles = (await body(await page.request.get('/api/v1/roles?page=1&page_size=100'))).items
    const role = roles.find((item: { key: string }) => item.key === 'organization_admin')
    expect(role).toBeTruthy()
    const username = `review_uni_${stamp}`, password = 'ReviewUnicode123!'
    await body(await page.request.post('/api/v1/users', { data: { username, password, display_name: '审核Unicode子管理员', global_role_ids: [], appointments: [{ organization_id: org.id, is_primary: true, assignments: [{ role_id: role.id, data_scope: 'organization' }] }] } }), 201)
    const child = await browser.newContext({ baseURL })
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    let writes = 0
    page.on('request', request => { if (new URL(request.url()).pathname.endsWith('/review') && request.method() === 'POST') writes++ })
    try {
      await body(await child.request.post('/api/v1/auth/login', { data: { username, password } }))
      expect((await child.request.put('/api/v1/auth/profile/password', { data: { current_password: password, new_password: `${password}Changed` } })).status()).toBe(204)
      for (const [index, unit] of ['🙂', 'a', '中'].entries()) {
        const task = (await body(await page.request.post('/api/v1/inventory-tasks', { data: {
          name: `审核边界${target}${stamp}_${index}`, description: '只操作自有隔离任务', start_at: new Date(Date.now() - 3600000).toISOString(), deadline_at: new Date(Date.now() + 86400000).toISOString(),
          status: 'active', asset_network_scope: 'all', required_fields: ['business_systems'], quality_rules: [], require_review: true, allow_late_submission: false, organization_ids: [org.id],
        } }), 201)).item
        const path = `/api/v1/inventory-tasks/${task.id}/organizations/${org.id}`
        await body(await child.request.post(`${path}/start`))
        const submitted = (await body(await child.request.post(`${path}/submit`, { data: { note: '真实子管理员提交，集团浏览器审核原文' } }))).item
        expect(submitted.status).toBe('submitted')
        expect(submitted.submitted_system_count).toBe(1)
        await page.goto(`/inventory-tasks?task_id=${task.id}&organization_id=${org.id}`)
        const detail = page.getByRole('dialog', { name: '填报任务详情', exact: true })
        const row = detail.locator('.task-organization-table .el-table__body-wrapper tr').filter({ hasText: org.name })
        await clickRowAction(page, row, '退回')
        const dialog = page.getByRole('dialog', { name: '退回填报结果', exact: true })
        const reason = dialog.getByLabel('整体退回原因', { exact: true })
        const comment = dialog.getByPlaceholder('针对该条数据填写整改要求（可选）', { exact: true })
        await expect(comment).toHaveCount(1)
        const input = target === 'reason' ? reason : comment
        const value = unit.repeat(2000)
        const before = writes
        await reason.fill('　 ')
        await dialog.getByRole('button', { name: '确认退回', exact: true }).click()
        await expect(page.getByText('请填写退回原因', { exact: true })).toBeVisible()
        await expect(dialog).toBeVisible()
        expect(writes).toBe(before)
        await reason.fill('审核整体说明')
        // Real browser editing must not silently turn 2000 code points into 1000.
        await input.fill(value)
        await expect(input).toHaveValue(value)
        await input.fill(unit.repeat(2001))
        await dialog.getByRole('button', { name: '确认退回', exact: true }).click()
        await expect(page.getByText(target === 'reason' ? '退回原因最多 2000 字' : '逐条审核意见最多 2000 字', { exact: true })).toBeVisible()
        await expect(dialog).toBeVisible()
        await expect(input).toHaveValue(unit.repeat(2001))
        expect(writes).toBe(before)
        const unchanged = (await body(await page.request.get(`/api/v1/inventory-tasks/${task.id}/organizations?page=1&page_size=20`))).items.find((item: { organization_id: string }) => item.organization_id === org.id)
        expect(unchanged.status).toBe('submitted')
        expect(unchanged.return_reason).toBeFalsy()
        expect((await body(await page.request.get(`${path}/review-comments?page=1&page_size=100`))).total).toBe(0)
        await input.fill(value)
        const response = page.waitForResponse(response => new URL(response.url()).pathname === `${path}/review` && response.request().method() === 'POST')
        await dialog.getByRole('button', { name: '确认退回', exact: true }).click()
        expect((await body(await response)).item.status).toBe('returned')
        await expect(dialog).toBeHidden()
        expect(writes).toBe(before + 1)
        const stored = (await body(await page.request.get(`/api/v1/inventory-tasks/${task.id}/organizations?page=1&page_size=20`))).items.find((item: { organization_id: string }) => item.organization_id === org.id)
        expect(stored.return_reason).toBe(target === 'reason' ? value : '审核整体说明')
        const comments = await body(await page.request.get(`${path}/review-comments?page=1&page_size=100`))
        expect(comments.total).toBe(target === 'comment' ? 1 : 0)
        if (target === 'comment') expect(comments.items[0]).toMatchObject({ resource_id: system.id, resource_type: 'business_system', comment: value })
      }
      expect(errors).toEqual([])
    } finally {
      await child.close()
    }
  })
}
