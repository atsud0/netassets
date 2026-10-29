import { expect, test } from '@playwright/test'

test('安全测试授权开始取消均接受2000字中文且拒绝超限说明', async ({ request, page }) => {
  const logged = await request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME, password: process.env.E2E_PASSWORD } })
  expect(logged.status()).toBe(200)
  const stamp = Date.now()
  const roots = (await (await request.get('/api/v1/organizations?page=1&page_size=100')).json()).items
  const group = roots.find((o: { type: string }) => o.type === 'group')
  const orgResponse = await request.post('/api/v1/organizations', { data: { parent_id: group.id, code: `ST_NOTE_${stamp}`, name: '安全测试说明回归', type: 'subsidiary' } })
  expect(orgResponse.status(), await orgResponse.text()).toBe(201)
  const org = (await orgResponse.json()).item
  const assetResponse = await request.post('/api/v1/assets', { data: { type: 'domain', value: `st-note-${stamp}.example.com`, organization_id: org.id, environment: 'production', network_scope: 'public', status: 'active', importance: 'normal', direct_change_reason: '安全测试回归' } })
  expect(assetResponse.status(), await assetResponse.text()).toBe(201)
  const asset = (await assetResponse.json()).item
  const batchResponse = await request.post('/api/v1/security-test-batches', { data: { batch_name: `长说明测试${stamp}`, test_type: 'penetration', provider: '隔离回归', authorization_note: '仅在隔离环境测试', start_at: new Date(stamp - 3600000).toISOString(), end_at: new Date(stamp + 86400000).toISOString(), asset_ids: [asset.id] } })
  expect(batchResponse.status(), await batchResponse.text()).toBe(201)
  const batch = (await batchResponse.json()).item
  const browserLogin = await page.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME, password: process.env.E2E_PASSWORD } })
  expect(browserLogin.status()).toBe(200)
  await page.goto('/security-test-batches')
  const row = page.locator('.el-table__body-wrapper tr').filter({ hasText: batch.name })
  const comment = '测'.repeat(2000)
  for (const [action, status] of [['authorize', 'authorized'], ['start', 'in_progress'], ['cancel', 'cancelled']] as const) {
    const oversized = await request.post(`/api/v1/security-test-batches/${batch.id}/${action}`, { data: { comment: comment + '测' } })
    expect(oversized.status(), await oversized.text()).toBe(400)
    await row.getByRole('button', { name: /^操作：/ }).click()
    const label = { authorize: '授权', start: '开始', cancel: '取消' }[action]!
    await page.locator('.row-actions-popover:visible').getByRole('button', { name: label, exact: true }).click()
    const prompt = page.locator('.el-message-box:visible')
    await prompt.locator('input').fill(comment + '测')
    await prompt.getByRole('button', { name: '确认', exact: true }).click()
    await expect(prompt).toContainText(/最多 2000 字/)
    await prompt.locator('input').fill(comment)
    const [response] = await Promise.all([
      page.waitForResponse(r => r.url().includes(`/security-test-batches/${batch.id}/${action}`) && r.request().method() === 'POST'),
      prompt.getByRole('button', { name: '确认', exact: true }).click(),
    ])
    expect(response.status(), await response.text()).toBe(200)
    expect((await response.json()).item.status).toBe(status)
  }
  const events = await request.get(`/api/v1/security-test-batches/${batch.id}/events?page=1&page_size=100`)
  expect(events.status(), await events.text()).toBe(200)
  const recorded = (await events.json()).items.filter((e: { action: string }) => ['authorize', 'start', 'cancel'].includes(e.action))
  expect(recorded).toHaveLength(3)
  for (const event of recorded) expect(event.comment).toBe(comment)
})

test('安全发现接受风险保存5000字中文并保留版本保护', async ({ request, page }) => {
  test.setTimeout(60_000)
  const logged = await request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME, password: process.env.E2E_PASSWORD } })
  expect(logged.status(), await logged.text()).toBe(200)
  const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
  const roots = await request.get('/api/v1/organizations?page=1&page_size=100')
  expect(roots.status()).toBe(200)
  const group = (await roots.json()).items.find((o: { type: string }) => o.type === 'group')
  const createdOrg = await request.post('/api/v1/organizations', { data: { parent_id: group.id, code: `RISK_${stamp}`, name: `风险接受回归${stamp}`, type: 'subsidiary' } })
  expect(createdOrg.status(), await createdOrg.text()).toBe(201)
  const org = (await createdOrg.json()).item
  const createdAsset = await request.post('/api/v1/assets', { data: { type: 'domain', value: `risk-${stamp.replaceAll('_', '-')}.example.com`, organization_id: org.id, environment: 'production', network_scope: 'private', status: 'active', importance: 'normal', direct_change_reason: '隔离回归' } })
  expect(createdAsset.status(), await createdAsset.text()).toBe(201)
  const asset = (await createdAsset.json()).item
  const createdFinding = await request.post('/api/v1/security-findings', { data: { title: `风险接受${stamp}`, severity: 'high', status: 'pending', assignee_organization_id: org.id, asset_id: asset.id, description: '隔离回归，未关联活跃工单', source: 'manual', discovered_at: new Date().toISOString() } })
  expect(createdFinding.status(), await createdFinding.text()).toBe(201)
  const finding = (await createdFinding.json()).item
  const reason = '险'.repeat(5000)
  const invalid = await request.post(`/api/v1/security-findings/${finding.id}/accept-risk`, { data: { reason: reason + '险', accepted_until: new Date(Date.now() + 86400000).toISOString(), expected_updated_at: finding.updated_at } })
  expect(invalid.status(), await invalid.text()).toBe(400)
  const login = await page.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME, password: process.env.E2E_PASSWORD } })
  expect(login.status()).toBe(200)
  await page.goto(`/security-work-orders?tab=findings&organization_id=${org.id}`)
  const row = page.locator('.el-table__body-wrapper tr').filter({ hasText: finding.title })
  await row.getByRole('button', { name: /^操作：/ }).click()
  await page.locator('.row-actions-popover:visible').getByRole('button', { name: '接受风险', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: '接受风险', exact: true })
  await dialog.locator('textarea').fill(reason)
  const [accepted] = await Promise.all([
    page.waitForResponse(r => r.url().includes(`/security-findings/${finding.id}/accept-risk`) && r.request().method() === 'POST'),
    dialog.getByRole('button', { name: '确认接受风险', exact: true }).click(),
  ])
  expect(accepted.status(), await accepted.text()).toBe(200)
  expect((await accepted.json()).item).toMatchObject({ status: 'risk_accepted', risk_acceptance_reason: reason })
  await expect(dialog).not.toBeVisible()
  await expect(row).toContainText('风险接受')
  const stale = await request.post(`/api/v1/security-findings/${finding.id}/accept-risk`, { data: { reason: '旧页面不应覆盖已接受的原因', accepted_until: new Date(Date.now() + 86400000).toISOString(), expected_updated_at: finding.updated_at } })
  expect(stale.status(), await stale.text()).toBe(409)
  const detail = await request.get(`/api/v1/security-findings/${finding.id}`)
  expect(detail.status(), await detail.text()).toBe(200)
  expect((await detail.json()).item.risk_acceptance_reason).toBe(reason)
})
