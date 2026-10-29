import { expect, test, type APIResponse, type Locator, type Page } from '@playwright/test'
import { writeFile } from 'node:fs/promises'
import { clickRowAction } from '../helpers/row-actions'
import { findRecordPage, showRecordPage } from '../helpers/pagination'

async function body(response: APIResponse, status = 200) {
  const text = await response.text()
  expect(response.status(), text).toBe(status)
  return text ? JSON.parse(text) : null
}

async function selectOption(page: Page, dialog: Locator, label: string, option: string) {
  await dialog.locator('.el-form-item').filter({ has: dialog.page().getByText(label, { exact: true }) }).locator('.el-select').click()
  await page.getByRole('option', { name: option, exact: true }).click()
}

test('质量规则新增编辑空白不请求、中英文128字保存、字段提示准确且拒绝不改配置和审计', async ({ page }, info) => {
  test.setTimeout(300_000)
  await body(await page.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }))
  const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
  const evidence: unknown[] = []
  const pageErrors: string[] = []
  page.on('pageerror', error => pageErrors.push(error.message))
  let saves = 0
  page.on('request', request => {
    if (['POST', 'PATCH'].includes(request.method()) && /\/api\/v1\/data-quality-rules(?:\/[^/]+)?$/.test(new URL(request.url()).pathname)) saves++
  })
  try {
    for (const resource of ['asset', 'business_system']) {
      for (const [index, char] of ['规', 'R'].entries()) {
        // Use a unique optional field so preserved rules (including disabled ones)
        // cannot conflict. The rule itself stays disabled throughout this test.
        const key = `qv_${resource}_${index}_${stamp}`
        const field = (await body(await page.request.post('/api/v1/custom-fields', { data: {
          resource_type: resource, field_key: key, field_name: key, field_type: 'text', options: [], is_required: false, is_enabled: true, sort_order: 0,
        } }), 201)).item
        await page.goto('/data-governance')
        await page.getByRole('tab', { name: '数据质量规则', exact: true }).click()
        await page.getByRole('button', { name: '新增质量规则', exact: true }).click()
        const dialog = page.getByRole('dialog', { name: '新增数据质量规则', exact: true })
        await dialog.getByLabel('规则名称', { exact: true }).fill('　 ')
        const before = saves
        await dialog.getByRole('button', { name: '保存', exact: true }).click()
        await expect(dialog.getByText('请输入规则名称', { exact: true })).toBeVisible()
        expect(saves).toBe(before)
        await dialog.getByLabel('规则名称', { exact: true }).fill(char.repeat(128))
        if (resource === 'business_system') await selectOption(page, dialog, '检查对象', '业务系统')
        await selectOption(page, dialog, '目标字段', `自定义字段：${key}`)
        const status = dialog.locator('.el-form-item').filter({ hasText: '状态' }).locator('.el-switch')
        await status.click()
        await expect(status.getByRole('switch')).not.toBeChecked()
        const saved = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/data-quality-rules')
        await dialog.getByRole('button', { name: '保存', exact: true }).click()
        const item = (await body(await saved, 201)).item
        expect(item).toMatchObject({ rule_name: char.repeat(128), resource_type: resource, target_field: `custom:${key}`, is_enabled: false })
        await expect(dialog).not.toBeVisible()
        const path = '/api/v1/data-quality-rules'
        const original = (await findRecordPage<{ id: string }>(page.request, path, rule => rule.id === item.id))!.item
        const auditPath = `/api/v1/audit-logs?q=${item.id}&page_size=100`
        const auditsBefore = (await body(await page.request.get(auditPath))).items
        const rejections: unknown[] = []
        for (const [fieldName, value, message] of [
          ['rule_name', '　 ', '规则名称不能为空'],
          ['rule_name', char.repeat(129), '规则名称最多 128 字'],
          ['resource_type', 'unknown', '请选择有效的检查对象'],
          ['rule_type', 'unknown', '请选择有效的规则类型'],
          ['severity', 'unknown', '请选择有效的问题等级'],
          ['sort_order', -1, '排序须为 0 至 100000 的整数'],
          ['target_field', 'custom:missing_' + stamp, '请选择有效的必填检查项'],
          ['conditions', [{ field: 'unknown', operator: 'empty' }], '第 1 条生效条件请选择有效字段'],
          ['conditions', [{ field: 'organization_id', operator: 'eq', values: ['not-a-uuid'] }], '第 1 条生效条件：条件值中的组织或网络区域标识格式不正确'],
        ] as [string, unknown, string][]) {
          for (const method of ['create', 'update']) {
            const data = { ...item, [fieldName]: value }
            const response = method === 'create' ? await page.request.post(path, { data }) : await page.request.patch(`${path}/${item.id}`, { data })
            expect((await body(response, 400)).error).toEqual({ code: 'invalid_data_quality_request', message })
            rejections.push({ fieldName, method, status: 400, message })
          }
        }
        expect((await findRecordPage<{ id: string }>(page.request, path, rule => rule.id === item.id))!.item).toEqual(original)
        expect((await body(await page.request.get(auditPath))).items).toEqual(auditsBefore)
        await showRecordPage(page, path, item.id, page.locator('.quality-rule-panel .el-pagination'))
        await clickRowAction(page, page.locator('.quality-rule-panel .el-table__row').filter({ hasText: key }), '编辑')
        const edit = page.getByRole('dialog', { name: '编辑数据质量规则', exact: true })
        await edit.getByLabel('规则名称', { exact: true }).fill('　 ')
        const beforeEdit = saves
        await edit.getByRole('button', { name: '保存', exact: true }).click()
        await expect(edit.getByText('请输入规则名称', { exact: true })).toBeVisible()
        expect(saves).toBe(beforeEdit)
        const changedName = char.repeat(127) + '改'
        await edit.getByLabel('规则名称', { exact: true }).fill(changedName)
        const edited = page.waitForResponse(response => response.request().method() === 'PATCH' && new URL(response.url()).pathname === `${path}/${item.id}`)
        await edit.getByRole('button', { name: '保存', exact: true }).click()
        expect((await body(await edited)).item).toMatchObject({ id: item.id, rule_name: changedName, is_enabled: false })
        await expect(edit).not.toBeVisible()
        expect((await findRecordPage<{ id: string; rule_name: string }>(page.request, path, rule => rule.id === item.id))!.item.rule_name).toBe(changedName)
        // This optional test-owned field is no longer needed by any enabled rule.
        await body(await page.request.patch(`/api/v1/custom-fields/${field.id}`, { data: { ...field, is_enabled: false } }))
        evidence.push({ resource, char, id: item.id, createdLength: [...item.rule_name].length, editedLength: [...changedName].length, rejectedUnchanged: true, rejections })
      }
    }
    expect(pageErrors).toEqual([])
    await page.screenshot({ path: info.outputPath('quality-rule-validated.png'), fullPage: true })
  } finally {
    await writeFile(info.outputPath('quality-rule-evidence.json'), JSON.stringify({ evidence, pageErrors }, null, 2))
  }
})
