import { expect, test, type APIRequestContext, type Page } from '@playwright/test'

const credentials = () => ({ username: process.env.E2E_USERNAME, password: process.env.E2E_PASSWORD })
const unique = () => `${Date.now()}-${Math.random().toString(36).slice(2, 7)}`

async function login(request: APIRequestContext) {
  const response = await request.post('/api/v1/auth/login', { data: credentials() })
  expect(response.status(), await response.text()).toBe(200)
}

async function importCandidate(request: APIRequestContext, title: string, batchID = '') {
  const template = await request.get('/api/v1/security-findings/import-template?format=csv')
  expect(template.status(), await template.text()).toBe(200)
  // Keep the real template header, not its demonstration row.
  const header = (await template.text()).replace(/^\uFEFF/, '').split(/\r?\n/)[0]
  const csv = `${header}\n${['domain', `unmatched-${unique()}.invalid`, title, 'high', '人工关联回归', '仅在隔离环境复现', '', '', new Date().toISOString(), 'pending'].join(',')}\n`
  const response = await request.post('/api/v1/security-findings/import', {
    multipart: { file: { name: 'candidate-regression.csv', mimeType: 'text/csv', buffer: Buffer.from(csv) }, ...(batchID ? { test_batch_id: batchID } : {}) },
  })
  expect(response.status(), await response.text()).toBe(201)
  const imported = (await response.json()).item
  expect(imported).toMatchObject({ total_count: 1, pending_count: 1, success_count: 0, failure_count: 0 })
  const responseList = await request.get(`/api/v1/security-findings/import-candidates?status=pending&keyword=${encodeURIComponent(title)}&page=1&page_size=20`)
  expect(responseList.status(), await responseList.text()).toBe(200)
  const candidates = (await responseList.json()).items
  expect(candidates).toHaveLength(1)
  return { candidate: candidates[0], imported }
}

async function openCandidateAction(page: Page, title: string, action: string) {
  await login(page.request)
  await page.goto('/security-work-orders?tab=candidates')
  await page.getByPlaceholder('资产值或漏洞标题').fill(title)
  await page.getByPlaceholder('资产值或漏洞标题').press('Enter')
  const row = page.locator('.el-table__body-wrapper tr').filter({ hasText: title })
  await row.getByRole('button', { name: /^操作：/ }).click()
  await page.locator('.row-actions-popover:visible').getByRole('button', { name: action, exact: true }).click()
}

for (const withBatch of [false, true]) {
  test(`待关联漏洞${withBatch ? '授权批次' : '无批次'}浏览器关联资产并阻止重复处理`, async ({ request, page }) => {
    test.setTimeout(60_000)
    await login(request)
    const stamp = unique()
    const roots = await request.get('/api/v1/organizations?page=1&page_size=100')
    expect(roots.status()).toBe(200)
    const group = (await roots.json()).items.find((o: { type: string }) => o.type === 'group')
    const createdOrg = await request.post('/api/v1/organizations', { data: { parent_id: group.id, code: `LINK_${stamp}`, name: `漏洞关联${stamp}`, type: 'subsidiary' } })
    expect(createdOrg.status(), await createdOrg.text()).toBe(201)
    const org = (await createdOrg.json()).item
    const createdAsset = await request.post('/api/v1/assets', { data: { type: 'domain', value: `link-${stamp}.example.com`, organization_id: org.id, environment: 'production', network_scope: 'public', status: 'active', importance: 'normal', direct_change_reason: '隔离关联回归' } })
    expect(createdAsset.status(), await createdAsset.text()).toBe(201)
    const asset = (await createdAsset.json()).item
    let batchID = ''
    if (withBatch) {
      const batch = await request.post('/api/v1/security-test-batches', { data: { batch_name: `候选关联${stamp}`, test_type: 'penetration', provider: '隔离回归', authorization_note: '只测隔离环境', start_at: new Date(Date.now() - 3600000).toISOString(), end_at: new Date(Date.now() + 86400000).toISOString(), asset_ids: [asset.id] } })
      expect(batch.status(), await batch.text()).toBe(201)
      batchID = (await batch.json()).item.id
      const authorized = await request.post(`/api/v1/security-test-batches/${batchID}/authorize`, { data: { comment: '隔离环境授权' } })
      expect(authorized.status(), await authorized.text()).toBe(200)
    }
    const title = `候选关联${withBatch ? '批次' : '全局'}${stamp}`
    const { candidate, imported } = await importCandidate(request, title, batchID)
    const optionsResponse = await request.get(`/api/v1/security-findings/import-candidates/${candidate.id}/scope-assets?keyword=${encodeURIComponent(asset.value)}`)
    expect(optionsResponse.status(), await optionsResponse.text()).toBe(200)
    const options = (await optionsResponse.json()).items
    const option = options.find((o: { asset_id: string }) => o.asset_id === asset.id)
    expect(option).toBeTruthy()
    if (withBatch) {
      expect(options).toHaveLength(1)
      // Formal asset UUIDs cannot be used as an authorized-scope UUID.
      const outsideScope = await request.post(`/api/v1/security-findings/import-candidates/${candidate.id}/link`, { data: { scope_asset_id: asset.id } })
      expect(outsideScope.status(), await outsideScope.text()).toBe(404)
    }
    const tooLong = await request.post(`/api/v1/security-findings/import-candidates/${candidate.id}/link`, { data: { ...(withBatch ? { scope_asset_id: option.id } : { asset_id: asset.id }), review_note: '联'.repeat(2001) } })
    expect(tooLong.status(), await tooLong.text()).toBe(400)
    await openCandidateAction(page, title, '关联资产')
    const dialog = page.getByRole('dialog', { name: '关联漏洞到资产', exact: true })
    await dialog.locator('.el-select').click()
    await dialog.locator('.el-select input').fill(asset.value)
    await page.locator('.el-select-dropdown:visible .el-select-dropdown__item').filter({ hasText: asset.value }).click()
    const note = '联'.repeat(2000)
    await dialog.locator('textarea').fill(note)
    const [linked] = await Promise.all([
      page.waitForResponse(r => r.url().includes(`/security-findings/import-candidates/${candidate.id}/link`) && r.request().method() === 'POST'),
      dialog.getByRole('button', { name: '确认关联并生成安全发现', exact: true }).click(),
    ])
    expect(linked.status(), await linked.text()).toBe(200)
    const result = (await linked.json()).item
    expect(result).toMatchObject({ status: 'imported', matched_asset_id: asset.id, review_note: note })
    expect(result.matched_scope_asset_id || '').toBe(withBatch ? option.id : '')
    expect(result.imported_finding_id).toBeTruthy()
    await expect(dialog).not.toBeVisible()
    await expect(page.locator('.el-table__body-wrapper tr').filter({ hasText: title })).toHaveCount(0)
    const findingResponse = await request.get(`/api/v1/security-findings/${result.imported_finding_id}`)
    expect(findingResponse.status(), await findingResponse.text()).toBe(200)
    const finding = (await findingResponse.json()).item
    expect(finding).toMatchObject({ title, asset_id: asset.id, assignee_organization_id: org.id, status: 'pending' })
    expect(finding.test_batch_id || '').toBe(batchID)
    const repeated = await request.post(`/api/v1/security-findings/import-candidates/${candidate.id}/link`, { data: { ...(withBatch ? { scope_asset_id: option.id } : { asset_id: asset.id }), review_note: '不能重复生成' } })
    expect(repeated.status(), await repeated.text()).toBe(409)
    const history = await request.get('/api/v1/security-findings/imports?page=1&page_size=100')
    expect(history.status(), await history.text()).toBe(200)
    expect((await history.json()).items.find((i: { id: string }) => i.id === imported.id).pending_count).toBe(0)
  })
}

test('忽略待关联漏洞在浏览器拦截空白及2001字并完整保存2000字原因', async ({ request, page }) => {
  await login(request)
  const title = `忽略边界${unique()}`
  const { candidate } = await importCandidate(request, title)
  const reason = '略'.repeat(2000)
  for (const invalidReason of ['   ', reason + '略']) {
    const invalid = await request.post(`/api/v1/security-findings/import-candidates/${candidate.id}/ignore`, { data: { review_note: invalidReason } })
    expect(invalid.status(), await invalid.text()).toBe(400)
  }
  await openCandidateAction(page, title, '忽略')
  const prompt = page.locator('.el-message-box:visible')
  await prompt.locator('textarea').fill('   ')
  await prompt.getByRole('button', { name: '确认忽略', exact: true }).click()
  await expect(prompt).toContainText('请填写忽略原因')
  const submitted: string[] = []
  page.on('request', req => {
    if (req.url().includes(`/security-findings/import-candidates/${candidate.id}/ignore`) && req.method() === 'POST') submitted.push(req.postData() ?? '')
  })
  await prompt.locator('textarea').fill(reason + '略')
  await prompt.getByRole('button', { name: '确认忽略', exact: true }).click()
  await expect(prompt).toContainText('忽略原因最多 2000 字')
  expect(submitted).toHaveLength(0)
  await prompt.locator('textarea').fill(reason)
  const [ignored] = await Promise.all([
    page.waitForResponse(r => r.url().includes(`/security-findings/import-candidates/${candidate.id}/ignore`) && r.request().method() === 'POST'),
    prompt.getByRole('button', { name: '确认忽略', exact: true }).click(),
  ])
  expect(ignored.status(), await ignored.text()).toBe(200)
  expect((await ignored.json()).item).toMatchObject({ status: 'ignored', review_note: reason })
  await expect(prompt).not.toBeVisible()
  const repeated = await request.post(`/api/v1/security-findings/import-candidates/${candidate.id}/ignore`, { data: { review_note: '不应覆盖原原因' } })
  expect(repeated.status(), await repeated.text()).toBe(409)
  const list = await request.get(`/api/v1/security-findings/import-candidates?status=ignored&keyword=${encodeURIComponent(title)}`)
  expect(list.status(), await list.text()).toBe(200)
  expect((await list.json()).items).toEqual([expect.objectContaining({ id: candidate.id, status: 'ignored', review_note: reason })])
})
