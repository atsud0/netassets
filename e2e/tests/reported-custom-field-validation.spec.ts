import { expect, test, type APIResponse } from '@playwright/test'
import { writeFile } from 'node:fs/promises'
import { clickRowAction } from '../helpers/row-actions'
import { findRecordPage, showRecordPage } from '../helpers/pagination'

async function body(response: APIResponse, status = 200) {
  const text = await response.text()
  expect(response.status(), text).toBe(status)
  return text ? JSON.parse(text) : null
}

test('自定义字段新增编辑精确提示、空白不请求、中英文128字完整保存且拒绝不改配置和审计', async ({ page }, info) => {
  test.setTimeout(240_000)
  await body(await page.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }))
  const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
  const evidence: unknown[] = []
  const pageErrors: string[] = []
  page.on('pageerror', error => pageErrors.push(error.message))
  let saves = 0
  page.on('request', request => {
    if (['POST', 'PATCH'].includes(request.method()) && /\/api\/v1\/custom-fields(?:\/[^/]+)?$/.test(new URL(request.url()).pathname)) saves++
  })
  try {
    for (const resource of ['asset', 'business_system']) {
      for (const [index, char] of ['名', 'N'].entries()) {
        await page.goto('/data-governance')
        await page.getByRole('tab', { name: resource === 'asset' ? '网络资产字段' : '业务系统字段', exact: true }).click()
        await page.getByRole('button', { name: '新增自定义字段', exact: true }).click()
        const dialog = page.getByRole('dialog', { name: '新增集团自定义字段', exact: true })
        await dialog.getByLabel('字段编码', { exact: true }).fill('　 ')
        await dialog.getByLabel('字段名称', { exact: true }).fill('　 ')
        const before = saves
        await dialog.getByRole('button', { name: '保存', exact: true }).click()
        for (const message of ['请输入字段编码', '请输入字段名称']) await expect(dialog.getByText(message, { exact: true })).toBeVisible()
        expect(saves).toBe(before)
        const key = `cf_${resource}_${index}_${stamp}`
        await dialog.getByLabel('字段编码', { exact: true }).fill(key)
        await dialog.getByLabel('字段名称', { exact: true }).fill(char.repeat(128))
        // Keep these optional fixtures disabled so they cannot change data quality
        // rules or other preserved test records.
        const status = dialog.locator('.el-form-item').filter({ hasText: '状态' }).locator('.el-switch')
        await status.click()
        await expect(status.getByRole('switch')).not.toBeChecked()
        const saved = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/custom-fields')
        await dialog.getByRole('button', { name: '保存', exact: true }).click()
        const item = (await body(await saved, 201)).item
        expect(item).toMatchObject({ field_key: key, field_name: char.repeat(128), resource_type: resource, is_enabled: false, is_required: false })
        await expect(dialog).not.toBeVisible()
        const path = `/api/v1/custom-fields?resource_type=${resource}`
        const original = (await findRecordPage<{ id: string }>(page.request, path, field => field.id === item.id))!.item
        const auditPath = `/api/v1/audit-logs?q=${item.id}&page_size=100`
        const auditsBefore = (await body(await page.request.get(auditPath))).items
        const rejections: unknown[] = []
        for (const [field, value, message] of [
          ['field_name', '　 ', '字段名称不能为空'],
          ['field_name', char.repeat(129), '字段名称最多 128 字'],
          ['sort_order', -1, '排序须为 0 至 100000 的整数'],
          ['options', ['unexpected'], '当前字段类型不支持可选项'],
        ] as [string, unknown, string][]) {
          for (const method of ['create', 'update']) {
            const data = { ...item, field_key: method === 'create' ? `${key}_bad` : key, [field]: value }
            const response = method === 'create' ? await page.request.post('/api/v1/custom-fields', { data }) : await page.request.patch(`/api/v1/custom-fields/${item.id}`, { data })
            expect((await body(response, 400)).error).toEqual({ code: 'invalid_custom_field', message })
            rejections.push({ field, method, status: 400, message })
          }
        }
        for (const [field, value, message] of [
          ['resource_type', resource === 'asset' ? 'business_system' : 'asset', '对象类型创建后不可修改'],
          ['field_key', `${key}_changed`, '字段编码创建后不可修改'],
          ['field_type', 'number', '字段类型创建后不可修改'],
        ]) {
          expect((await body(await page.request.patch(`/api/v1/custom-fields/${item.id}`, { data: { ...item, [field]: value } }), 400)).error).toEqual({ code: 'invalid_custom_field', message })
        }
        expect((await findRecordPage<{ id: string }>(page.request, path, field => field.id === item.id))!.item).toEqual(original)
        expect((await body(await page.request.get(auditPath))).items).toEqual(auditsBefore)
        await showRecordPage(page, path, item.id, page.locator('.table-pagination .el-pagination'))
        await clickRowAction(page, page.locator('.el-table__row').filter({ hasText: key }), '编辑')
        const edit = page.getByRole('dialog', { name: '编辑集团自定义字段', exact: true })
        await edit.getByLabel('字段名称', { exact: true }).fill('　 ')
        const beforeEdit = saves
        await edit.getByRole('button', { name: '保存', exact: true }).click()
        await expect(edit.getByText('请输入字段名称', { exact: true })).toBeVisible()
        expect(saves).toBe(beforeEdit)
        const changedName = char.repeat(127) + '改'
        await edit.getByLabel('字段名称', { exact: true }).fill(changedName)
        const edited = page.waitForResponse(response => response.request().method() === 'PATCH' && new URL(response.url()).pathname === `/api/v1/custom-fields/${item.id}`)
        await edit.getByRole('button', { name: '保存', exact: true }).click()
        expect((await body(await edited)).item).toMatchObject({ id: item.id, field_name: changedName, is_enabled: false })
        await expect(edit).not.toBeVisible()
        expect((await findRecordPage<{ id: string; field_name: string }>(page.request, path, field => field.id === item.id))!.item.field_name).toBe(changedName)
        evidence.push({ resource, char, id: item.id, createdLength: [...item.field_name].length, editedLength: [...changedName].length, rejectedUnchanged: true, rejections })
      }
    }
    expect(pageErrors).toEqual([])
    await page.screenshot({ path: info.outputPath('custom-field-validated.png'), fullPage: true })
  } finally {
    await writeFile(info.outputPath('custom-field-evidence.json'), JSON.stringify({ evidence, pageErrors }, null, 2))
  }
})
