import { expect, test, type Page } from '@playwright/test'

import { showRecordPage } from '../helpers/pagination'

const adminUsername = process.env.E2E_USERNAME ?? 'admin'
const adminPassword = process.env.E2E_PASSWORD

const cleanupRules: Array<{ id: string; body: Record<string, unknown> }> = []
const cleanupAssets = new Set<string>()
test.afterEach(async ({ request }, info) => {
  if (!cleanupRules.length && !cleanupAssets.size) return
  info.setTimeout(60_000)
  expect((await request.post('/api/v1/auth/login', { data: { username: adminUsername, password: adminPassword } })).status()).toBe(200)
  for (const { id, body } of cleanupRules) expect((await request.patch(`/api/v1/data-quality-rules/${id}`, { data: body })).status()).toBe(200)
  cleanupRules.length = 0
  for (const id of cleanupAssets) {
    const current = await request.get(`/api/v1/assets/${id}`)
    if (current.status() === 404) { cleanupAssets.delete(id); continue }
    expect(current.status()).toBe(200)
    const { item } = await current.json()
    const deleted = await request.delete(`/api/v1/assets/${id}?expected_version=${item.version}&direct_change_reason=${encodeURIComponent('清理有效期 E2E 测试资产')}`)
    expect(deleted.status()).toBe(204)
    cleanupAssets.delete(id)
  }
})

type APIResult<T> = { status: number; body: T }
type AssetItem = {
  id: string
  version: number
  type: string
  value: string
  name?: string
  organization_id: string
  business_system_id?: string
  environment: string
  network_scope: string
  network_area_id?: string
  status: string
  importance: string
  owner_name?: string
  notes?: string
  expires_at?: string
}

async function login(page: Page) {
  await page.goto('/login')
  await page.getByPlaceholder('请输入用户名').fill(adminUsername)
  await page.getByPlaceholder('请输入密码').fill(adminPassword!)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page).not.toHaveURL(/\/login(?:\?|$)/)
}

async function api<T>(page: Page, path: string, requestID: string, method = 'GET', body?: unknown): Promise<APIResult<T>> {
  return page.evaluate(async ({ path, requestID, method, body }) => {
    const response = await fetch(`/api/v1${path}`, {
      method,
      credentials: 'include',
      headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestID },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    const text = await response.text()
    let payload: unknown = null
    if (text) {
      try { payload = JSON.parse(text) } catch { payload = text }
    }
    return { status: response.status, body: payload }
  }, { path, requestID, method, body }) as Promise<APIResult<T>>
}

async function uploadCSV<T>(page: Page, path: string, requestID: string, organizationID: string, filename: string, csv: string): Promise<APIResult<T>> {
  return page.evaluate(async ({ path, requestID, organizationID, filename, csv }) => {
    const form = new FormData()
    form.append('organization_id', organizationID)
    form.append('direct_change_reason', '验证有效期资产导入')
    form.append('file', new File([`\uFEFF${csv}`], filename, { type: 'text/csv;charset=utf-8' }))
    const response = await fetch(`/api/v1${path}`, {
      method: 'POST',
      credentials: 'include',
      headers: { 'X-Request-ID': requestID },
      body: form,
    })
    const text = await response.text()
    let payload: unknown = null
    if (text) {
      try { payload = JSON.parse(text) } catch { payload = text }
    }
    return { status: response.status, body: payload }
  }, { path, requestID, organizationID, filename, csv }) as Promise<APIResult<T>>
}

function requireStatus<T>(result: APIResult<T>, expected: number, step: string): T {
  expect(result.status, `${step}: ${JSON.stringify(result.body)}`).toBe(expected)
  return result.body
}

function assetPayload(item: AssetItem, expiresAt: string | null) {
  return {
    expected_version: item.version,
    type: item.type,
    value: item.value,
    name: item.name ?? '',
    organization_id: item.organization_id,
    business_system_id: item.business_system_id ?? '',
    environment: item.environment,
    network_scope: item.network_scope,
    network_area_id: item.network_area_id ?? '',
    status: item.status,
    importance: item.importance,
    owner_name: item.owner_name ?? '',
    notes: item.notes ?? '',
    expires_at: expiresAt,
    direct_change_reason: '验证有效期全链路',
  }
}

test('网络资产有效期贯通台账、导入导出、质量巡检和自动关闭', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  test.setTimeout(240_000)

  const stamp = Date.now()
  const requestPrefix = `e2e-asset-expiry-${stamp}`
  const lifecycleValue = `lifecycle-${stamp}.example.com`
  const expiringValue = `expiring-${stamp}.example.com`
  const importedValue = `expiry-import-${stamp}.example.com`
  const invalidValue = `expiry-invalid-${stamp}.example.com`
  const ruleName = `ASSET_EXPIRY_E2E_${stamp}`
  const originalExpiry = '2099-12-31T15:59:59Z'
  const updatedExpiry = '2099-06-30T15:59:59Z'
  const scanExpiry = '2026-08-01T15:59:59Z'

  await login(page)

  const organizations = requireStatus(
    await api<{ items: Array<{ id: string; type: string }> }>(page, '/organizations/tree', `${requestPrefix}-organizations`),
    200,
    '读取组织',
  ).items
  const group = organizations.find((item) => item.type === 'group')
  expect(group, '未找到集团根组织').toBeTruthy()

  // A dedicated organization keeps the scan independent of accumulated group assets.
  const scopeName = `有效期回归组织_${stamp}`
  const scope = requireStatus(await api<{ item: { id: string } }>(page, '/organizations', `${requestPrefix}-scope`, 'POST', {
    parent_id: group!.id, code: `EXPIRY_${stamp}`, name: scopeName, type: 'subsidiary',
    short_name: '', manager: '', contact: '', notes: '有效期 E2E 专属范围',
  }), 201, '创建有效期测试范围').item
  {
    const lifecycleAsset = requireStatus(
      await api<{ item: AssetItem }>(page, '/assets', `${requestPrefix}-create-lifecycle`, 'POST', {
        type: 'domain', value: lifecycleValue, name: `生命周期资产_${stamp}`, organization_id: scope.id,
        business_system_id: '', environment: 'production', network_scope: 'public', network_area_id: '',
        status: 'active', importance: 'important', owner_name: '有效期测试责任人', notes: '验证有效期全链路', expires_at: originalExpiry,
        direct_change_reason: '验证有效期全链路',
      }),
      201,
      '创建带有效期资产',
    ).item
    cleanupAssets.add(lifecycleAsset.id)
    expect(lifecycleAsset.expires_at).toBe(originalExpiry)

    const detail = requireStatus(
      await api<{ item: AssetItem }>(page, `/assets/${lifecycleAsset.id}`, `${requestPrefix}-detail`),
      200,
      '读取资产详情',
    ).item
    expect(detail.expires_at).toBe(originalExpiry)

    const updatedAsset = requireStatus(
      await api<{ item: AssetItem }>(page, `/assets/${lifecycleAsset.id}`, `${requestPrefix}-update-expiry`, 'PATCH', assetPayload(lifecycleAsset, updatedExpiry)),
      200,
      '修改资产有效期',
    ).item
    expect(updatedAsset.expires_at).toBe(updatedExpiry)

    const history = requireStatus(
      await api<{ items: Array<{ action: string; details: { changes?: Array<{ field: string; label: string; before: string; after: string }> } }> }>(page, `/assets/${lifecycleAsset.id}/change-history?page=1&page_size=100`, `${requestPrefix}-history`),
      200,
      '读取资产变更历史',
    ).items
    const expiryChange = history
      .find((item) => item.action === 'asset.update')
      ?.details.changes?.find((change) => change.field === 'expires_at')
    expect(expiryChange).toMatchObject({ label: '有效期至' })
    expect(expiryChange?.before).toContain('2099-12-31')
    expect(expiryChange?.after).toContain('2099-06-30')

    const exported = await page.evaluate(async (value) => {
      const response = await fetch(`/api/v1/assets/export?format=csv&q=${encodeURIComponent(value)}`)
      return {
        status: response.status,
        count: response.headers.get('x-export-count'),
        contentType: response.headers.get('content-type'),
        text: await response.text(),
      }
    }, lifecycleValue)
    expect(exported.status).toBe(200)
    expect(exported.count).toBe('1')
    expect(exported.contentType).toContain('text/csv')
    expect(exported.text).toContain('有效期至')
    expect(exported.text).toContain('2099-06-30')

    const validCSV = [
      '资产类型,资产值,资产名称,环境,网络范围,重要程度,有效期至',
      `domain,${importedValue},有效期导入资产,production,public,normal,2099-08-08 12:34:56`,
    ].join('\n')
    const preview = requireStatus(
      await uploadCSV<{ result: { status: string; total_count: number; success_count: number; failure_count: number; errors: Array<{ code: string }> } }>(
        page, '/assets/import/preview', `${requestPrefix}-preview-valid`, scope.id, 'asset-expiry-valid.csv', validCSV,
      ),
      200,
      '预检合法有效期文件',
    ).result
    expect(preview).toMatchObject({ status: 'ready', total_count: 1, success_count: 1, failure_count: 0 })

    const imported = requireStatus(
      await uploadCSV<{ result: { status: string; total_count: number; success_count: number; failure_count: number } }>(
        page, '/assets/import', `${requestPrefix}-import-valid`, scope.id, 'asset-expiry-valid.csv', validCSV,
      ),
      200,
      '正式导入合法有效期文件',
    ).result
    expect(imported).toMatchObject({ status: 'completed', total_count: 1, success_count: 1, failure_count: 0 })

    const importedItems = requireStatus(
      await api<{ items: AssetItem[] }>(page, `/assets?q=${encodeURIComponent(importedValue)}&page=1&page_size=20`, `${requestPrefix}-find-imported`),
      200,
      '读取导入资产',
    ).items
    for (const item of importedItems) cleanupAssets.add(item.id)
    expect(importedItems).toHaveLength(1)
    expect(importedItems[0].expires_at).toBe('2099-08-08T04:34:56Z')

    const invalidCSV = [
      '资产类型,资产值,资产名称,环境,网络范围,重要程度,有效期至',
      `domain,${invalidValue},非法有效期资产,production,public,normal,not-a-date`,
    ].join('\n')
    const invalidPreview = requireStatus(
      await uploadCSV<{ result: { status: string; success_count: number; failure_count: number; errors: Array<{ row: number; code: string; message: string }> } }>(
        page, '/assets/import/preview', `${requestPrefix}-preview-invalid`, scope.id, 'asset-expiry-invalid.csv', invalidCSV,
      ),
      200,
      '预检非法有效期文件',
    ).result
    expect(invalidPreview.status).toBe('failed')
    expect(invalidPreview.success_count).toBe(0)
    expect(invalidPreview.failure_count).toBe(1)
    expect(invalidPreview.errors).toContainEqual(expect.objectContaining({ row: 2, code: 'invalid_expiry' }))
    expect(invalidPreview.errors[0].message).toContain('YYYY-MM-DD HH:mm:ss')

    const expiringAsset = requireStatus(
      await api<{ item: AssetItem }>(page, '/assets', `${requestPrefix}-create-expiring`, 'POST', {
        type: 'domain', value: expiringValue, name: `临近到期资产_${stamp}`, organization_id: scope.id,
        business_system_id: '', environment: 'testing', network_scope: 'public', network_area_id: '',
        status: 'active', importance: 'normal', owner_name: '到期整改责任人', notes: '用于验证到期巡检', expires_at: scanExpiry,
        direct_change_reason: '验证到期巡检',
      }),
      201,
      '创建临近到期资产',
    ).item
    cleanupAssets.add(expiringAsset.id)

    const expiryRule = requireStatus(
      await api<{ item: { id: string } }>(page, '/data-quality-rules', `${requestPrefix}-rule-create`, 'POST', {
        rule_name: ruleName,
        description: 'E2E 专用资产到期规则',
        resource_type: 'asset',
        rule_type: 'expiry',
        target_field: 'expires_at',
        severity: 'warning',
        stale_after_days: 0,
        expiry_warning_days: 30,
        conditions: [{ field: 'organization_id', operator: 'eq', values: [scope.id] }],
        is_enabled: true,
        sort_order: 91000,
      }),
      201,
      '创建资产到期规则',
    ).item
    cleanupRules.push({ id: expiryRule.id, body: { rule_name: ruleName, description: 'E2E 专用资产到期规则', resource_type: 'asset', rule_type: 'expiry', target_field: 'expires_at', severity: 'warning', stale_after_days: 0, expiry_warning_days: 30, conditions: [{ field: 'organization_id', operator: 'eq', values: [scope.id] }], is_enabled: false, sort_order: 91000 } })

    await page.goto('/data-governance')
    await page.getByRole('tab', { name: '数据质量规则' }).click()
    await showRecordPage(page, '/api/v1/data-quality-rules', expiryRule.id, page.locator('.el-pagination:visible'))
    const ruleRow = page.locator('.el-table__row').filter({ hasText: ruleName }).first()
    await expect(ruleRow).toBeVisible()
    await expect(ruleRow).toContainText('资产到期')
    await expect(ruleRow).toContainText('有效期至（提前 30 天提醒）')

    const scan = requireStatus(
      await api<{ result: { batch_id: string; summary: { auto_closed_count: number }; issues: Array<{ issue_id: string; rule_name: string; rule_type: string; expiry_warning_days: number; resource_id: string; target_field: string }> } }>(
        page,
        `/data-quality-rules/scan?organization_id=${scope.id}&resource_type=asset`,
        `${requestPrefix}-scan-open`,
      ),
      200,
      '执行资产到期巡检',
    ).result
    const issue = scan.issues.find((item) => item.rule_name === ruleName && item.resource_id === expiringAsset.id)
    expect(issue).toMatchObject({ rule_type: 'expiry', expiry_warning_days: 30, target_field: 'expires_at' })

    await page.goto('/data-quality-issues')
    await page.locator('.quality-workflow-filters .el-select').filter({ hasText: '全部组织' }).getByRole('combobox').fill(scopeName)
    await page.getByRole('option', { name: scopeName, exact: true }).click()
    const issuesLoaded = page.waitForResponse(response => response.url().includes('/api/v1/data-quality-issues?') && new URL(response.url()).searchParams.get('organization_id') === scope.id)
    await page.getByRole('button', { name: '查询', exact: true }).click()
    expect((await issuesLoaded).status()).toBe(200)
    await showRecordPage(page, `/api/v1/data-quality-issues?organization_id=${scope.id}`, issue!.issue_id, page.locator('.quality-issue-panel .el-pagination'))
    const issueRows = page.locator('.quality-issue-panel .el-table__row').filter({ hasText: ruleName })
    const expiringIssueRow = issueRows.filter({ hasText: `临近到期资产_${stamp}` })
    await expect(expiringIssueRow).toBeVisible()
    await expect(expiringIssueRow).toContainText('有效期至（提前 30 天提醒）')

    requireStatus(
      await api<{ item: AssetItem }>(page, `/assets/${expiringAsset.id}`, `${requestPrefix}-clear-expiry`, 'PATCH', assetPayload(expiringAsset, null)),
      200,
      '清空资产有效期',
    )
    const rescan = requireStatus(
      await api<{ result: { summary: { auto_closed_count: number }; issues: Array<{ rule_name: string; resource_id: string }> } }>(
        page,
        `/data-quality-rules/scan?organization_id=${scope.id}&resource_type=asset`,
        `${requestPrefix}-scan-close`,
      ),
      200,
      '重新巡检并自动关闭问题',
    ).result
    expect(rescan.issues.some((item) => item.rule_name === ruleName && item.resource_id === expiringAsset.id)).toBeFalsy()
    expect(rescan.summary.auto_closed_count).toBeGreaterThanOrEqual(1)

    const closedIssue = requireStatus(
      await api<{ issue: { status: string; resolution_type: string } }>(page, `/data-quality-issues/${issue!.issue_id}`, `${requestPrefix}-issue-detail`),
      200,
      '读取自动关闭结果',
    ).issue
    expect(closedIssue).toMatchObject({ status: 'closed', resolution_type: 'auto_resolved' })

  }
  // Cleanup is performed by the independent API fixture even after browser timeout.
})
