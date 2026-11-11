import { expect, test, type APIResponse, type Page } from '@playwright/test'

type Finding = { id: string; title: string; severity: string; status: string; assignee_organization_id: string }
type FindingList = { items: Finding[]; total: number; page: number; page_size: number }
type Overview = { closure_funnel: Record<string, number>; undispatched_findings: { finding_count: number } }

async function json<T>(response: APIResponse, status = 200): Promise<T> {
  expect(response.status(), await response.text()).toBe(status)
  return response.json() as Promise<T>
}

function waitForFindings(page: Page, filters: Record<string, string>) {
  return page.waitForResponse(response => {
    const url = new URL(response.url())
    return response.request().method() === 'GET' && url.pathname === '/api/v1/security-findings'
      && Object.entries(filters).every(([key, value]) => (url.searchParams.get(key) ?? '') === value)
  })
}

test('真实安全统计、五等级筛选与看板下钻保持同一数据口径', async ({ page }, testInfo) => {
  test.skip(!process.env.E2E_PASSWORD, 'E2E_PASSWORD 未配置')
  test.setTimeout(240_000)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await json(await page.request.post('/api/v1/auth/login', { data: {
    username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD,
  } }))
  const stamp = `${Date.now()}_${testInfo.workerIndex}`
  const organizations = await json<{ items: Array<{ id: string; type: string }> }>(
    await page.request.get('/api/v1/organizations?page_size=100'),
  )
  const group = organizations.items.find(item => item.type === 'group')
  expect(group).toBeTruthy()
  const { item: organization } = await json<{ item: { id: string } }>(
    await page.request.post('/api/v1/organizations', { data: {
      parent_id: group!.id, code: `OVERVIEW_${stamp}`, name: `安全看板验收_${stamp}`, type: 'subsidiary',
    } }), 201,
  )
  const { item: asset } = await json<{ item: { id: string } }>(
    await page.request.post('/api/v1/assets', { data: {
      type: 'domain', value: `overview-${stamp.replace('_', '-')}.example.com`, name: `看板资产_${stamp}`,
      organization_id: organization.id, environment: 'production', network_scope: 'public', status: 'active',
      importance: 'normal', owner_name: '隔离验收', direct_change_reason: '创建安全看板验收夹具',
    } }), 201,
  )
  const expectedBySeverity = { high: 21, critical: 1, medium: 1, low: 1, info: 1 }
  const findings: Finding[] = []
  for (const [severity, count] of Object.entries(expectedBySeverity)) {
    for (let index = 0; index < count; index++) {
      const result = await json<{ item: Finding }>(await page.request.post('/api/v1/security-findings', { data: {
        title: `看板_${stamp}_${severity}_${index}`, severity, status: 'pending',
        assignee_organization_id: organization.id, asset_id: asset.id,
        description: '安全看板和等级筛选真实 API 验收', source: 'manual', discovered_at: new Date().toISOString(),
      } }), 201)
      findings.push(result.item)
    }
  }

  const baseFilters = { organization_id: organization.id, status: 'pending', asset_link_state: 'live', page_size: '20' }
  const initial = waitForFindings(page, { ...baseFilters, page: '2' })
  await page.goto(`/security-work-orders?${new URLSearchParams({ tab: 'findings', ...baseFilters, page: '2' })}`)
  const initialResponse = await initial
  expect(initialResponse.status()).toBe(200)
  expect((await initialResponse.json() as FindingList).items).toHaveLength(5)
  await expect(page.getByRole('tab', { name: '漏洞与安全发现', exact: true })).toHaveAttribute('aria-selected', 'true')

  const { summary } = await json<{ summary: Record<string, number> }>(
    await page.request.get(`/api/v1/security-findings/summary?organization_id=${organization.id}`),
  )
  expect(summary).toMatchObject({ total: 25, pending: 25, critical: 1, high: 21, medium: 1, low: 1 })
  const cards = page.locator('.finding-summary-card')
  await expect(cards).toHaveCount(6)
  await expect(cards.locator('span')).toHaveText(['发现总数', '待处理', '严重', '高危', '中危', '低危'])
  for (const [index, key] of ['total', 'pending', 'critical', 'high', 'medium', 'low'].entries()) {
    await expect(cards.nth(index).locator('strong')).toHaveText(String(summary[key]))
  }
  await expect(page.locator('.finding-summary-dimensions')).toHaveCount(0)
  await expect(page.getByText('其他待办口径', { exact: false })).toHaveCount(0)
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 })
    await cards.first().scrollIntoViewIfNeeded()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`security-summary-${width}.png`), fullPage: true })
  }
  await page.setViewportSize({ width: 1440, height: 1000 })

  const severityInput = page.getByRole('combobox', { name: '漏洞级别', exact: true })
  const severitySelect = page.locator('.el-select').filter({ has: severityInput })
  const labels = { high: '高危', critical: '严重', medium: '中危', low: '低危', info: '提示' }
  for (const [severity, label] of Object.entries(labels)) {
    const filtered = waitForFindings(page, { ...baseFilters, severity, page: '1' })
    await severitySelect.locator('.el-select__wrapper').click()
    await page.getByRole('option', { name: label, exact: true }).click()
    const response = await filtered
    expect(response.status()).toBe(200)
    const result = await response.json() as FindingList
    expect(result.total).toBe(expectedBySeverity[severity as keyof typeof expectedBySeverity])
    expect(result.items.length).toBeGreaterThan(0)
    for (const item of result.items) {
      expect(item).toMatchObject({ severity, status: 'pending', assignee_organization_id: organization.id })
      expect(findings.map(finding => finding.id)).toContain(item.id)
    }
    await expect(page.locator('.security-panel .el-pager .is-active')).toHaveText('1')
    await expect(page.getByRole('button', { name: result.items[0]!.title, exact: true })).toBeVisible()
    if (severity === 'high') {
      const next = waitForFindings(page, { ...baseFilters, severity, page: '2' })
      await page.locator('.security-panel .el-pagination .btn-next').click()
      const secondPage = await (await next).json() as FindingList
      expect(secondPage.items).toHaveLength(1)
      await expect(page.getByRole('button', { name: secondPage.items[0]!.title, exact: true })).toBeVisible()
    }
  }

  const cleared = waitForFindings(page, { ...baseFilters, severity: '', page: '1' })
  await severitySelect.hover()
  await severitySelect.locator('.el-select__clear').click()
  const clearedResult = await (await cleared).json() as FindingList
  expect(clearedResult.total).toBe(25)
  await expect(severityInput).toHaveAttribute('aria-expanded', 'false')
  const reset = waitForFindings(page, { organization_id: '', severity: '', status: '', asset_link_state: '', page: '1', page_size: '20' })
  await page.getByRole('group', { name: '安全工单筛选', exact: true }).getByRole('button', { name: '重置', exact: true }).click()
  expect((await reset).status()).toBe(200)
  const invalid = await page.request.get('/api/v1/security-findings?severity=urgent')
  expect(invalid.status()).toBe(400)
  expect(await invalid.json()).toMatchObject({ error: { code: 'invalid_filter' } })

  const drilldowns: Array<{ key: string; selector: string; filters: Record<string, string>; countKey: string }> = [
    { key: 'discovered', selector: '[data-stage="discovered"]', filters: {}, countKey: 'discovered_finding_count' },
    { key: 'asset_linked', selector: '[data-stage="asset_linked"]', filters: { asset_link_state: 'linked' }, countKey: 'asset_linked_finding_count' },
    { key: 'dispatched', selector: '[data-stage="dispatched"]', filters: { dispatch_state: 'dispatched' }, countKey: 'dispatched_finding_count' },
    { key: 'remediating', selector: '[data-stage="remediating"]', filters: { dispatch_state: 'remediating' }, countKey: 'remediating_finding_count' },
    { key: 'pending_verification', selector: '[data-stage="pending_verification"]', filters: { dispatch_state: 'pending_verification' }, countKey: 'pending_verification_finding_count' },
    { key: 'closed', selector: '[data-stage="closed"]', filters: { dispatch_state: 'closed' }, countKey: 'closed_finding_count' },
    { key: 'undispatched', selector: '[data-testid="undispatched-gap"]', filters: { dispatch_state: 'undispatched' }, countKey: '' },
  ]
  const evidence: Array<{ stage: string; total: number; url: string }> = []
  for (const drilldown of drilldowns) {
    const overviewResponse = page.waitForResponse(response => new URL(response.url()).pathname === '/api/v1/dashboard/security-overview')
    await page.goto('/dashboard')
    const overviewResult = await overviewResponse
    expect(overviewResult.status()).toBe(200)
    const { overview } = await overviewResult.json() as { overview: Overview }
    const count = drilldown.countKey ? overview.closure_funnel[drilldown.countKey]! : overview.undispatched_findings.finding_count
    const button = page.locator(drilldown.selector)
    await expect(button.locator('strong')).toHaveText(new RegExp(`^${count}\\s*条`))
    const listed = waitForFindings(page, { dashboard_scope: '1', page: '1', page_size: '20', ...drilldown.filters })
    await button.click()
    const response = await listed
    expect(response.status()).toBe(200)
    const result = await response.json() as FindingList
    expect(result.total, `${drilldown.key} 下钻总数与看板不一致`).toBe(count)
    const current = new URL(page.url())
    expect(current.pathname).toBe('/security-work-orders')
    for (const [key, value] of Object.entries({ tab: 'findings', dashboard_scope: '1', ...drilldown.filters })) {
      expect(current.searchParams.get(key)).toBe(value)
    }
    await expect(page.getByText('工作台统计范围', { exact: true })).toBeVisible()
    if (result.items.length) await expect(page.getByRole('button', { name: result.items[0]!.title, exact: true })).toBeVisible()
    evidence.push({ stage: drilldown.key, total: result.total, url: current.pathname + current.search })
  }
  const ownUndispatched = await json<FindingList>(await page.request.get(`/api/v1/security-findings?${new URLSearchParams({
    organization_id: organization.id, dashboard_scope: '1', dispatch_state: 'undispatched', page_size: '100',
  })}`))
  expect(ownUndispatched.items.map(item => item.id).sort()).toEqual(findings.map(item => item.id).sort())
  await testInfo.attach('security-overview-live-evidence', { contentType: 'application/json', body: Buffer.from(JSON.stringify({
    organization_id: organization.id, asset_id: asset.id, finding_ids: findings.map(item => item.id), summary, drilldowns: evidence,
  }, null, 2)) })
})
