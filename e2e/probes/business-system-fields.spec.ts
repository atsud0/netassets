import { expect, test, type APIResponse, type Response } from '@playwright/test'
import { clickRowAction } from '../helpers/row-actions'

async function body(response: APIResponse | Response, status = 200) {
  expect(response.status()).toBe(status)
  return response.json()
}

test('业务系统新增及编辑：空白/超限不请求，Unicode满额完整显示、保存和读回', async ({ page }) => {
  test.setTimeout(240_000)
  test.skip(!process.env.E2E_PASSWORD, 'E2E_PASSWORD 未配置')
  await body(await page.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }))
  const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 7)}`
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  let writes = 0
  page.on('request', request => { if (/^\/api\/v1\/business-systems(?:\/[^/]+)?$/.test(new URL(request.url()).pathname) && ['POST', 'PATCH'].includes(request.method())) writes++ })
  const loaded = page.waitForResponse(response => new URL(response.url()).pathname === '/api/v1/business-systems' && response.request().method() === 'GET' && response.ok())
  await page.goto('/systems')
  await loaded
  const fields = [['系统名称', 'name', 255, '系统名称最多 255 字'], ['系统简称', 'short_name', 128, '系统简称最多 128 字'], ['负责人', 'owner_name', 128, '负责人最多 128 字'], ['部署方式', 'deployment_type', 32, '部署方式最多 32 字']] as const
  for (const [i, unit] of ['a', '中', '🙂'].entries()) {
    const code = `SYS_FIELDS_${stamp}_${i}`
    await page.getByRole('button', { name: '新增业务系统', exact: true }).click()
    let dialog = page.getByRole('dialog', { name: '新增业务系统', exact: true })
    await dialog.getByLabel('系统编码', { exact: true }).fill(code)
    let before = writes
    await dialog.getByLabel('系统名称', { exact: true }).fill('　 ')
    await dialog.getByRole('button', { name: '确认新增', exact: true }).click()
    await expect(dialog.getByText('请输入系统名称', { exact: true })).toBeVisible()
    expect(writes).toBe(before)
    for (const [label, , limit, message] of fields) {
      const input = dialog.getByLabel(label, { exact: true })
      await input.fill(unit.repeat(limit + 1))
      await dialog.getByRole('button', { name: '确认新增', exact: true }).click()
      await expect(dialog.getByText(message, { exact: true })).toBeVisible()
      await expect(input).toHaveValue(unit.repeat(limit + 1))
      expect(writes).toBe(before)
      await input.fill(unit.repeat(limit))
    }
    const createdResponse = page.waitForResponse(response => new URL(response.url()).pathname === '/api/v1/business-systems' && response.request().method() === 'POST')
    await dialog.getByRole('button', { name: '确认新增', exact: true }).click()
    const created = (await body(await createdResponse, 201)).item
    await expect(dialog).toBeHidden()
    expect(writes).toBe(before + 1)
    for (const [, key, limit] of fields) expect(created[key]).toBe(unit.repeat(limit))
    let stored = (await body(await page.request.get(`/api/v1/business-systems/${created.id}`))).item
    for (const [, key, limit] of fields) expect(stored[key]).toBe(unit.repeat(limit))
    const searchResponse = page.waitForResponse(response => {
      const url = new URL(response.url())
      return url.pathname === '/api/v1/business-systems' && url.searchParams.get('q') === code && response.request().method() === 'GET'
    })
    const search = page.getByPlaceholder('搜索系统名称、组织或负责人，也可输入编码', { exact: true })
    await search.fill(code)
    await search.press('Enter')
    expect((await body(await searchResponse)).items.map((item: { id: string }) => item.id)).toEqual([created.id])
    const row = page.locator('.el-table__body-wrapper tbody tr').filter({ hasText: unit.repeat(255) })
    await expect(row).toHaveCount(1)
    await clickRowAction(page, row, '编辑')
    dialog = page.getByRole('dialog', { name: '编辑业务系统', exact: true })
    before = writes
    const editingName = dialog.getByLabel('系统名称', { exact: true })
    await expect(editingName).toHaveValue(unit.repeat(255))
    await editingName.fill('　 ')
    await dialog.getByRole('button', { name: '保存修改', exact: true }).click()
    await expect(dialog.getByText('请输入系统名称', { exact: true })).toBeVisible()
    expect(writes).toBe(before)
    await editingName.fill(unit.repeat(255))
    for (const [label, , limit, message] of fields) {
      const input = dialog.getByLabel(label, { exact: true })
      await expect(input).toHaveValue(unit.repeat(limit))
      await input.fill(unit.repeat(limit + 1))
      await dialog.getByRole('button', { name: '保存修改', exact: true }).click()
      await expect(dialog.getByText(message, { exact: true })).toBeVisible()
      expect(writes).toBe(before)
      await expect(input).toHaveValue(unit.repeat(limit + 1))
      await input.fill(unit.repeat(limit))
    }
    const updatedResponse = page.waitForResponse(response => new URL(response.url()).pathname === `/api/v1/business-systems/${created.id}` && response.request().method() === 'PATCH')
    await dialog.getByRole('button', { name: '保存修改', exact: true }).click()
    const updated = (await body(await updatedResponse)).item
    await expect(dialog).toBeHidden()
    expect(writes).toBe(before + 1)
    expect(updated.version).toBe(created.version + 1)
    stored = (await body(await page.request.get(`/api/v1/business-systems/${created.id}`))).item
    for (const [, key, limit] of fields) expect(stored[key]).toBe(unit.repeat(limit))
  }
  expect(errors).toEqual([])
})
