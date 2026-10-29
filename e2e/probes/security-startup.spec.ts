import { expect, test } from '@playwright/test'
import { writeFile } from 'node:fs/promises'

for (const tab of ['orders', 'findings']) {
  test(`安全${tab === 'orders' ? '工单' : '发现'}首屏不等待编辑用资产下拉请求`, async ({ page }, info) => {
    test.skip(!process.env.E2E_PASSWORD, 'E2E_PASSWORD 未配置')
    test.setTimeout(60_000)
    const request = page.context().request
    expect((await request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } })).status()).toBe(200)
    const roots = await request.get('/api/v1/organizations?page=1&page_size=100')
    expect(roots.status()).toBe(200)
    const root = (await roots.json()).items.find((item: { type: string }) => item.type === 'group')
    expect(root).toBeTruthy()
    let release!: () => void
    const pending = new Promise<void>(resolve => { release = resolve })
    let held = false, released = false, listReceived = false
    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    const endpoint = `/api/v1/${tab === 'orders' ? 'security-work-orders' : 'security-findings'}`
    page.on('response', response => {
      const url = new URL(response.url())
      if (url.pathname === endpoint && url.searchParams.get('organization_id') === root.id && response.status() === 200) listReceived = true
    })
    // Only hold an editing selector, never mock the list or its database result.
    // This deterministically isolates the dependency from CPU/load/timing noise.
    await page.route(/\/api\/v1\/security-reference\/assets(?:\?|$)/, async route => {
      held = true
      await pending
      await route.continue().catch(() => {})
    })
    try {
      await page.goto(`/security-work-orders?tab=${tab}&organization_id=${root.id}`)
      await expect.poll(() => held).toBe(true)
      await expect.poll(() => listReceived, { message: '当前组织列表不应等待编辑资产选项' }).toBe(true)
      await expect(page.locator('.el-loading-mask:visible')).toHaveCount(0)
      expect(released).toBe(false)
      expect(errors).toEqual([])
    } finally {
      await writeFile(info.outputPath('startup-dependency-evidence.json'), JSON.stringify({ tab, held, list_received_before_selector_release: listReceived && !released, page_errors: errors }, null, 2))
      released = true
      release()
      await page.unrouteAll({ behavior: 'ignoreErrors' })
    }
  })
}

test('真实漏洞导入成功后关闭弹窗，基础选项刷新完成不重新打开', async ({ page }, info) => {
  test.skip(!process.env.E2E_PASSWORD, 'E2E_PASSWORD 未配置')
  test.setTimeout(120_000)
  const request = page.context().request
  expect((await request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } })).status()).toBe(200)
  const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 7)}`
  const roots = await request.get('/api/v1/organizations?page=1&page_size=100')
  expect(roots.status()).toBe(200)
  const root = (await roots.json()).items.find((item: { type: string }) => item.type === 'group')
  expect(root).toBeTruthy()
  const orgResponse = await request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `IMPORT_CLOSE_${stamp}`, name: `导入关闭意图_${stamp}`, type: 'subsidiary' } })
  expect(orgResponse.status()).toBe(201)
  const org = (await orgResponse.json()).item
  const value = `import-close-${stamp.replaceAll('_', '-')}.example.com`
  const assetResponse = await request.post('/api/v1/assets', { data: { type: 'domain', value, organization_id: org.id, environment: 'testing', network_scope: 'public', status: 'active', importance: 'normal', direct_change_reason: '导入窗口异步关闭回归' } })
  expect(assetResponse.status()).toBe(201)
  const asset = (await assetResponse.json()).item
  await page.goto(`/security-work-orders?tab=findings&organization_id=${org.id}`)
  // The list now loads independently: a hidden list mask no longer proves that
  // editing options finished. Verify our real asset is usable before holding
  // only the post-import refresh, without adding a fixed sleep or mock response.
  await page.getByRole('button', { name: '登记安全发现', exact: true }).click()
  const create = page.getByRole('dialog', { name: '登记安全发现', exact: true })
  const orgField = create.locator('.el-form-item').filter({ has: page.getByText('责任组织', { exact: true }) })
  await orgField.locator('.el-select').click()
  await page.getByRole('option', { name: org.name, exact: true }).click()
  await create.getByLabel('漏洞标题', { exact: true }).click()
  const assetField = create.locator('.el-form-item').filter({ has: page.getByText('关联网络资产', { exact: true }) })
  await assetField.locator('.el-select').click()
  await expect(page.getByRole('option').filter({ hasText: value })).toBeVisible()
  await page.getByRole('option').filter({ hasText: value }).click()
  await create.getByRole('button', { name: '取消', exact: true }).click()
  await expect(create).toBeHidden()
  await page.getByRole('button', { name: '批量导入漏洞', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '批量导入漏洞与安全发现', exact: true })
  await expect(dialog).toBeVisible()
  let release!: () => void
  const pending = new Promise<void>(resolve => { release = resolve })
  let held = false, continued = false
  await page.route(/\/api\/v1\/security-reference\/assets(?:\?|$)/, async route => {
    held = true
    await pending
    await route.continue().catch(() => {})
    continued = true
  })
  let imported: Record<string, unknown> | undefined
  try {
    const csv = `资产类型,资产值,漏洞标题,严重等级,漏洞描述,发现时间\ndomain,${value},导入关闭_${stamp},high,实际导入结果关闭回归,2026-10-29T00:00:00Z`
    await dialog.locator('input[type=file]').setInputFiles({ name: `import-close-${stamp}.csv`, mimeType: 'text/csv', buffer: Buffer.from(csv) })
    const done = page.waitForResponse(response => new URL(response.url()).pathname === '/api/v1/security-findings/import' && response.request().method() === 'POST')
    await dialog.getByRole('button', { name: '开始导入', exact: true }).click()
    const response = await done
    expect(response.status()).toBe(201)
    imported = (await response.json()).item
    expect(imported).toMatchObject({ total_count: 1, success_count: 1, failure_count: 0, pending_count: 0 })
    await expect.poll(() => held).toBe(true)
    await expect(dialog.locator('.import-result')).toBeVisible()
    await dialog.getByRole('button', { name: '关闭', exact: true }).click()
    await expect(dialog).toBeHidden()
    const refreshed = page.waitForResponse(response => {
      const url = new URL(response.url())
      return url.pathname === '/api/v1/security-findings/summary' && url.searchParams.get('organization_id') === org.id && response.status() === 200
    })
    release()
    await refreshed
    await expect.poll(() => continued).toBe(true)
    await expect(page.locator('.el-loading-mask:visible')).toHaveCount(0)
    await expect(dialog).toBeHidden()
    const findings = await request.get(`/api/v1/security-findings?organization_id=${org.id}&page=1&page_size=100`)
    expect(findings.status()).toBe(200)
    expect((await findings.json()).items).toEqual([expect.objectContaining({ title: `导入关闭_${stamp}`, asset_id: asset.id })])
  } finally {
    release()
    await page.unrouteAll({ behavior: 'ignoreErrors' })
    await writeFile(info.outputPath('import-close-evidence.json'), JSON.stringify({ organization_id: org.id, asset_id: asset.id, import_id: imported?.id, held, continued, dialog_visible: await dialog.isVisible() }, null, 2))
  }
})
