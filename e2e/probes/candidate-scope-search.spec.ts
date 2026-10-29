import { expect, test, type APIRequestContext, type BrowserContext } from '@playwright/test'
import { writeFile } from 'node:fs/promises'

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

test('真实安全运营员远程搜索超过200项后的资产并完成候选关联', async ({ browser, baseURL }, info) => {
  test.setTimeout(300_000)
  const contexts: BrowserContext[] = []
  try {
    const admin = await browser.newContext({ baseURL }); contexts.push(admin)
    await login(admin.request)
    const stamp = unique()
    const roots = await admin.request.get('/api/v1/organizations?page=1&page_size=100')
    expect(roots.status()).toBe(200)
    const root = (await roots.json()).items.find((item: { type: string }) => item.type === 'group')
    expect(root).toBeTruthy()
    const orgResponse = await admin.request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `SCOPE_${stamp}`, name: `搜索隔离_${stamp}`, type: 'subsidiary' } })
    expect(orgResponse.status(), await orgResponse.text()).toBe(201)
    const org = (await orgResponse.json()).item
    // Own 205 earlier-sorting assets. Never depend on a clean ledger or delete other fixtures.
    async function asset(value: string) {
      const response = await admin.request.post('/api/v1/assets', { data: { type: 'domain', value, organization_id: org.id, environment: 'testing', network_scope: 'public', status: 'active', importance: 'normal', direct_change_reason: '隔离环境验证搜索前200项截断' } })
      expect(response.status(), await response.text()).toBe(201)
      return (await response.json()).item
    }
    for (let i = 0; i < 205; i++) await asset(`a-${String(i).padStart(3, '0')}-${stamp}.example.com`)
    const target = await asset(`zz-target-${stamp}.example.com`)
    const roles = await admin.request.get('/api/v1/roles?page=1&page_size=100')
    expect(roles.status()).toBe(200)
    const role = (await roles.json()).items.find((item: { key: string }) => item.key === 'security_operator')
    expect(role).toBeTruthy()
    const username = `scope_op_${stamp}`, password = 'ScopeSearch123!'
    const userResponse = await admin.request.post('/api/v1/users', { data: { username, password, display_name: '资产搜索专项安全运营员', global_role_ids: [role.id], appointments: [] } })
    expect(userResponse.status(), await userResponse.text()).toBe(201)
    const operator = await browser.newContext({ baseURL }); contexts.push(operator)
    const loggedIn = await operator.request.post('/api/v1/auth/login', { data: { username, password } })
    expect(loggedIn.status()).toBe(200)
    const identity = (await loggedIn.json()).user
    expect(identity.roles).toEqual(['security_operator'])
    if (identity.must_change_password) {
      const changed = await operator.request.put('/api/v1/auth/profile/password', { data: { current_password: password, new_password: `${password}Changed` } })
      expect(changed.status()).toBe(204)
    }
    const title = `远程搜索200外资产_${stamp}`
    const { candidate } = await importCandidate(operator.request, title)
    const path = `/api/v1/security-findings/import-candidates/${candidate.id}/scope-assets`
    const initial = await operator.request.get(path)
    expect(initial.status()).toBe(200)
    const initialItems = (await initial.json()).items
    expect(initialItems).toHaveLength(200)
    expect(initialItems.some((item: { asset_id: string }) => item.asset_id === target.id)).toBe(false)
    // Soft assertion preserves browser evidence on the old image even when keyword is ignored.
    const searched = await operator.request.get(`${path}?keyword=${encodeURIComponent(target.value.toUpperCase())}`)
    expect(searched.status()).toBe(200)
    const searchItems = (await searched.json()).items
    expect.soft(searchItems.map((item: { asset_id: string }) => item.asset_id)).toEqual([target.id])
    const page = await operator.newPage()
    const pageErrors: string[] = []
    page.on('pageerror', error => pageErrors.push(error.message))
    await page.goto('/security-work-orders?tab=candidates')
    await page.getByPlaceholder('资产值或漏洞标题').fill(title)
    await page.getByPlaceholder('资产值或漏洞标题').press('Enter')
    const row = page.locator('.el-table__body-wrapper tr').filter({ hasText: title })
    await row.getByRole('button', { name: /^操作：/ }).click()
    await page.locator('.row-actions-popover:visible').getByRole('button', { name: '关联资产', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: '关联漏洞到资产', exact: true })
    await dialog.locator('.el-select').click()
    await dialog.locator('.el-select input').fill(target.value)
    const item = page.locator('.el-select-dropdown:visible .el-select-dropdown__item').filter({ hasText: target.value })
    await expect(item).toBeVisible({ timeout: 10_000 })
    await item.click()
    const linkedPromise = page.waitForResponse(response => new URL(response.url()).pathname === `/api/v1/security-findings/import-candidates/${candidate.id}/link` && response.request().method() === 'POST')
    await dialog.getByRole('button', { name: '确认关联并生成安全发现', exact: true }).click()
    const linked = await linkedPromise
    expect(linked.status()).toBe(200)
    const result = (await linked.json()).item
    expect(result).toMatchObject({ status: 'imported', matched_asset_id: target.id })
    const findingResponse = await operator.request.get(`/api/v1/security-findings/${result.imported_finding_id}`)
    expect(findingResponse.status()).toBe(200)
    expect((await findingResponse.json()).item).toMatchObject({ title, asset_id: target.id, assignee_organization_id: org.id })
    const duplicate = await operator.request.post(`/api/v1/security-findings/import-candidates/${candidate.id}/link`, { data: { asset_id: target.id } })
    expect(duplicate.status()).toBe(409)
    expect(pageErrors).toEqual([])
    // Small sanitized evidence, no credentials/cookies/network trace.
    await writeFile(info.outputPath('scope-search-evidence.json'), JSON.stringify({ organization_id: org.id, target_asset_id: target.id, candidate_id: candidate.id, finding_id: result.imported_finding_id, unfiltered_count: initialItems.length, searched_count: searchItems.length }, null, 2))
  } finally { for (const context of contexts.reverse()) await context.close() }
})
