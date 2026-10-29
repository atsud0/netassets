import { expect, test } from '@playwright/test'

test('草稿不算逾期，工作台数量与逾期筛选总数一致', async ({ request, page }) => {
  test.setTimeout(90_000)
  const login = await request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME, password: process.env.E2E_PASSWORD } })
  expect(login.status(), await login.text()).toBe(200)
  const stamp = Date.now()
  const roots = await request.get('/api/v1/organizations?page=1&page_size=100')
  expect(roots.status()).toBe(200)
  const root = (await roots.json()).items.find((o: { type: string }) => o.type === 'group')
  const createdOrg = await request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `OVERDUE_${stamp}`, name: `逾期口径回归${stamp}`, type: 'subsidiary' } })
  expect(createdOrg.status(), await createdOrg.text()).toBe(201)
  const org = (await createdOrg.json()).item
  const createdAsset = await request.post('/api/v1/assets', { data: { type: 'domain', value: `overdue-${stamp}.example.com`, organization_id: org.id, environment: 'production', network_scope: 'private', status: 'active', importance: 'normal', direct_change_reason: '隔离回归' } })
  expect(createdAsset.status(), await createdAsset.text()).toBe(201)
  const asset = (await createdAsset.json()).item
  const orders: { id: string; status: string; overdue: boolean }[] = []
  for (let index = 0; index < 22; index++) {
    const title = `${index === 0 ? '保留草稿' : '正式下发'}${index}`
    const finding = await request.post('/api/v1/security-findings', { data: { title: `${title}${stamp}`, severity: 'high', status: 'pending', assignee_organization_id: org.id, asset_id: asset.id, description: '隔离环境逾期统计回归', source: 'manual', discovered_at: new Date().toISOString() } })
    expect(finding.status(), await finding.text()).toBe(201)
    const findingID = (await finding.json()).item.id
    // The API allows a one-minute tolerance; use an already elapsed deadline
    // within that tolerance to test the time boundary without sleeping.
    const order = await request.post('/api/v1/security-work-orders', { data: { title: `${title}${stamp}`, assignee_organization_id: org.id, remediation_requirement: '仅验证逾期统计', due_at: new Date(Date.now() - 5000).toISOString(), finding_ids: [findingID] } })
    expect(order.status(), await order.text()).toBe(201)
    const item = (await order.json()).item
    orders.push(item)
    if (index > 0) {
      const issued = await request.post(`/api/v1/security-work-orders/${item.id}/issue`, { data: { comment: '验证已下发逾期工单' } })
      expect(issued.status(), await issued.text()).toBe(200)
      expect((await issued.json()).item.overdue).toBe(true)
    }
  }
  expect.soft(orders[0]!.overdue, '未下发的草稿不应标为逾期').toBe(false)
  const scoped = await request.get(`/api/v1/security-work-orders?organization_id=${org.id}&overdue=true&page=1&page_size=20`)
  expect(scoped.status(), await scoped.text()).toBe(200)
  const scopedBody = await scoped.json()
  expect.soft(scopedBody.total, '仅应统计已下发工单').toBe(21)
  expect(scopedBody.items).toHaveLength(20)
  expect(scopedBody.items.every((o: { status: string; overdue: boolean }) => o.status === 'issued' && o.overdue)).toBe(true)
  const summary = await request.get('/api/v1/dashboard/summary')
  expect(summary.status(), await summary.text()).toBe(200)
  const expectedCount = (await summary.json()).summary.overdue_work_order_count
  const filtered = await request.get('/api/v1/security-work-orders?overdue=true&page=1&page_size=1')
  expect(filtered.status(), await filtered.text()).toBe(200)
  expect.soft((await filtered.json()).total).toBe(expectedCount)
  const beyondLastPage = await request.get('/api/v1/security-work-orders?overdue=true&page=999999&page_size=1')
  expect(beyondLastPage.status(), await beyondLastPage.text()).toBe(200)
  expect.soft((await beyondLastPage.json()).total, '空分页的计数回退应采用同一口径').toBe(expectedCount)

  const browserLogin = await page.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME, password: process.env.E2E_PASSWORD } })
  expect(browserLogin.status()).toBe(200)
  await page.goto('/')
  const dashboardCard = page.locator('.stat-card').filter({ hasText: '逾期整改工单' })
  await expect(dashboardCard.locator('strong')).toHaveText(String(expectedCount))
  await dashboardCard.click()
  await expect(page).toHaveURL(/\/security-work-orders\?.*overdue=1/)
  const stats = page.locator('.work-order-stat-list')
  await expect(stats.locator('.filter-stat').filter({ hasText: '符合筛选工单' }).locator('strong')).toHaveText(String(expectedCount))
  await expect(stats.locator('.filter-stat.danger strong')).toHaveText(String(expectedCount))
  await expect(stats.locator('.filter-stat.danger')).toContainText('逾期总数')
  await expect(page.locator('.el-table__body-wrapper tr')).toHaveCount(20)
  const [secondPage] = await Promise.all([
    page.waitForResponse(r => r.url().includes('/api/v1/security-work-orders?') && new URL(r.url()).searchParams.get('page') === '2'),
    page.locator('.el-pagination:visible .btn-next').click(),
  ])
  expect(secondPage.status(), await secondPage.text()).toBe(200)
  await expect(stats.locator('.filter-stat.danger strong')).toHaveText(String(expectedCount))
})
