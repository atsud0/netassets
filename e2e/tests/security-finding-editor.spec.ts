import { expect, test, type Page } from '@playwright/test'

const findingID = 'finding-editor-12'
const timestamp = '2026-11-03T00:00:00Z'
const organization = { id: 'org-one', code: 'ORG', name: '测试组织', type: 'company', status: 'enabled' }
const markdown = '# 漏洞复现\n\n1. 打开入口\n2. 检查响应\n\n```http\nGET /example HTTP/1.1\n```\n\n| 字段 | 结果 |\n| --- | --- |\n| 验证 | 成功 |'

async function fixture(page: Page, options: { forbidden?: boolean; missing?: boolean; conflict?: boolean } = {}) {
  const state = {
    item: { id: findingID, code: 'SF-012', title: '独立编辑页面验证', severity: 'high', status: 'pending', assignee_organization_id: organization.id,
      assignee_organization_name: organization.name, asset_id: 'asset-one', asset_value: '198.51.100.12', asset_link_state: 'live', business_system_id: '',
      test_batch_id: '', vulnerability_type: '配置错误', description: '原始说明\n保留换行', evidence: '原始证据', remediation_advice: '原始建议', source: 'manual',
      discovered_at: timestamp, created_at: timestamp, updated_at: timestamp },
    writes: [] as Record<string, unknown>[],
  }
  await page.route('**/api/v1/**', async route => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace('/api/v1', '')
    if (path === '/auth/me') return route.fulfill({ json: { user: {
      id: 'user-one', username: 'editor', display_name: '编辑测试', must_change_password: false, organization, organizations: [organization],
      roles: options.forbidden ? ['organization_admin'] : ['group_admin'], permissions: options.forbidden ? ['asset.manage'] : ['security_finding.manage', 'security_test.manage', 'security_workorder.manage'],
    } } })
    if (path === `/security-findings/${findingID}`) {
      if (request.method() === 'PATCH') {
        const payload = request.postDataJSON()
        state.writes.push(payload)
        if (options.conflict) return route.fulfill({ status: 409, json: { error: { code: 'version_conflict', message: '记录已被其他用户更新，请刷新后重试' } } })
        Object.assign(state.item, payload, { updated_at: '2026-11-03T01:00:00Z' })
        return route.fulfill({ json: { item: state.item } })
      }
      if (options.missing) return route.fulfill({ status: 404, json: { error: { code: 'not_found', message: '安全发现不存在' } } })
      return route.fulfill({ json: { item: state.item } })
    }
    let items: unknown[] = []
    let total = 0
    if (path === '/security-findings') { items = [state.item]; total = 60 }
    if (path.endsWith('/organizations')) items = [organization]
    if (path.endsWith('/assets')) items = [{ id: 'asset-one', organization_id: organization.id, value: '198.51.100.12', name: '测试资产', status: 'active', network_scope: 'internet' }]
    if (path === '/dashboard/security-overview') return route.fulfill({ json: { overview: { finding_severity: [], finding_status: [], finding_trend: [], organization_performance: [], test_coverage: {} } } })
    return route.fulfill({ json: { items, total: total || items.length, page: Number(url.searchParams.get('page') ?? 1), page_size: Number(url.searchParams.get('page_size') ?? 20),
      summary: { total: 60, pending: 60, critical: 0, high: 60, medium: 0, low: 0 }, counts: {}, by_status: {}, by_severity: {}, by_execution_blocked_reason: {} } })
  })
  return state
}

function field(page: Page, label: string) {
  return page.locator('.el-form-item').filter({ has: page.locator('.el-form-item__label', { hasText: label }) })
}

for (const width of [1440, 390]) {
  test(`独立编辑、刷新、Markdown 预览保存及返回来源（${width}px）`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1000 })
    const state = await fixture(page)
    await page.goto('/security-work-orders?tab=findings&severity=high&page=2&page_size=20')
    await page.getByRole('button', { name: '操作：独立编辑页面验证', exact: true }).click()
    await page.getByRole('dialog', { name: '操作：独立编辑页面验证', exact: true }).getByRole('button', { name: '编辑', exact: true }).click()
    await expect(page).toHaveURL(new RegExp(`/security-findings/${findingID}/edit`))
    await expect(page.getByRole('heading', { name: '编辑安全发现', exact: true })).toBeVisible()
    await expect(page.getByRole('dialog', { name: '编辑安全发现', exact: true })).toHaveCount(0)
    await page.reload()
    await expect(field(page, '漏洞描述').locator('textarea')).toHaveValue('原始说明\n保留换行')
    await field(page, '漏洞描述').locator('textarea').fill(markdown)
    await field(page, '验证证据').locator('textarea').fill('证据：`HTTP 200`')
    await field(page, '整改建议').locator('textarea').fill('## 整改\n\n- 限制权限')
    await field(page, '漏洞描述').getByRole('button', { name: '预览', exact: true }).click()
    await expect(field(page, '漏洞描述').getByRole('heading', { name: '漏洞复现' })).toBeVisible()
    await expect(field(page, '漏洞描述').getByRole('table')).toContainText('成功')
    await expect(field(page, '漏洞描述').locator('pre')).toContainText('GET /example HTTP/1.1')
    await field(page, '漏洞描述').scrollIntoViewIfNeeded()
    await page.screenshot({ path: test.info().outputPath(`finding-editor-${width}.png`), fullPage: true })
    await page.getByRole('button', { name: '保存修改', exact: true }).first().click()
    await expect(page).toHaveURL(url => url.pathname === '/security-work-orders')
    const returned = new URL(page.url())
    expect(returned.searchParams.get('tab')).toBe('findings')
    expect(returned.searchParams.get('severity')).toBe('high')
    expect(returned.searchParams.get('page')).toBe('2')
    expect(state.writes).toHaveLength(1)
    expect(state.writes[0]).toMatchObject({ description: markdown, evidence: '证据：`HTTP 200`', remediation_advice: '## 整改\n\n- 限制权限', expected_updated_at: timestamp, asset_id: 'asset-one' })
    await page.getByRole('button', { name: '独立编辑页面验证', exact: true }).click()
    const detail = page.getByRole('dialog', { name: '安全发现详情', exact: true })
    await expect(detail.getByRole('heading', { name: '漏洞复现' })).toBeVisible()
    await expect(detail.getByRole('table').filter({ hasText: '成功' })).toBeVisible()
    await detail.getByRole('button', { name: '编辑安全发现', exact: true }).click()
    await expect(page).toHaveURL(new RegExp(`/security-findings/${findingID}/edit`))
    await expect(field(page, '漏洞描述').locator('textarea')).toHaveValue(markdown)
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1)
    expect(overflow).toBe(false)
  })
}

test('未保存编辑离开时可继续编辑或放弃', async ({ page }) => {
  await fixture(page)
  await page.goto(`/security-findings/${findingID}/edit`)
  await field(page, '漏洞描述').locator('textarea').fill('尚未保存的新内容')
  await page.getByRole('button', { name: /返回|取消/ }).first().click()
  const prompt = page.getByRole('dialog').filter({ hasText: /未保存/ })
  await expect(prompt).toBeVisible()
  await prompt.getByRole('button', { name: '继续编辑', exact: true }).click()
  await expect(field(page, '漏洞描述').locator('textarea')).toHaveValue('尚未保存的新内容')
  await page.getByRole('button', { name: /返回|取消/ }).first().click()
  await prompt.getByRole('button', { name: /放弃/ }).click()
  await expect(page).toHaveURL(url => url.pathname === '/security-work-orders')
})

test('版本冲突保留未保存 Markdown 和原始版本', async ({ page }) => {
  const state = await fixture(page, { conflict: true })
  await page.goto(`/security-findings/${findingID}/edit`)
  await field(page, '漏洞描述').locator('textarea').fill(markdown)
  await page.getByRole('button', { name: '保存修改', exact: true }).first().click()
  await expect(page.getByRole('alert').filter({ hasText: '安全发现已被其他人更新' }).first()).toBeVisible()
  await expect(field(page, '漏洞描述').locator('textarea')).toHaveValue(markdown)
  expect(state.writes[0]?.expected_updated_at).toBe(timestamp)
  await expect(page).toHaveURL(new RegExp(`/security-findings/${findingID}/edit`))
})

test('不存在的安全发现给出反馈且不能提交', async ({ page }) => {
  const state = await fixture(page, { missing: true })
  await page.goto(`/security-findings/${findingID}/edit`)
  await expect(page.getByText('安全发现不存在', { exact: true }).first()).toBeVisible()
  await expect(page.locator('textarea')).toHaveCount(0)
  expect(state.writes).toHaveLength(0)
})

test('无编辑权限直接访问也不能编辑', async ({ page }) => {
  const state = await fixture(page, { forbidden: true })
  await page.goto(`/security-findings/${findingID}/edit`)
  await expect(page.getByText('当前账号没有编辑安全发现的权限', { exact: true })).toBeVisible()
  await expect(page.locator('textarea')).toHaveCount(0)
  await expect(page.getByRole('button', { name: '保存修改', exact: true })).toHaveCount(0)
  expect(state.writes).toHaveLength(0)
})
