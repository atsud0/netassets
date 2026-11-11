import { expect, test as base, type APIResponse, type BrowserContext, type Locator, type Page } from '@playwright/test'
import { clickRowAction } from '../helpers/row-actions'

type Finding = {
  id: string; title: string; severity: string; status: string; assignee_organization_id: string
  asset_id: string; description: string; evidence: string; remediation_advice: string
  source: string; discovered_at: string; updated_at: string
}
type Organization = { id: string; name: string; type: string; children?: Organization[] }
type Fixture = {
  operator: BrowserContext; editor: BrowserContext; page: Page; org: Organization
  finding: Finding; createFinding: (number: number, severity?: string) => Promise<Finding>
  evidence: Record<string, unknown>
}

async function body(response: APIResponse, status = 200) {
  const text = await response.text()
  expect(response.status(), text).toBe(status)
  return text ? JSON.parse(text) : undefined
}

function flatten(items: Organization[]): Organization[] {
  return items.flatMap(item => [item, ...flatten(item.children ?? [])])
}

// Every case owns its organizations, users and findings in the isolated E2E database.
// No mocked API responses or shared business records are used or removed.
const test = base.extend<{ liveEditor: Fixture }>({
  liveEditor: async ({ browser, baseURL }, use, testInfo) => {
    const contexts: BrowserContext[] = []
    const evidence: Record<string, unknown> = {}
    try {
      const group = await browser.newContext({ baseURL }); contexts.push(group)
      await body(await group.request.post('/api/v1/auth/login', { data: {
        username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD,
      } }))
      const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
      const root = flatten((await body(await group.request.get('/api/v1/organizations/tree'))).items).find(item => item.type === 'group')
      expect(root).toBeTruthy()
      const org = (await body(await group.request.post('/api/v1/organizations', { data: {
        parent_id: root!.id, code: `EDIT12_${stamp}`, name: `编辑页真实验收_${stamp}`, type: 'subsidiary',
      } }), 201)).item as Organization
      const asset = (await body(await group.request.post('/api/v1/assets', { data: {
        type: 'domain', value: `edit12-${stamp.replaceAll('_', '-')}.example.com`, organization_id: org.id,
        environment: 'production', network_scope: 'public', status: 'active', importance: 'normal',
        direct_change_reason: '隔离环境安全发现编辑页验收',
      } }), 201)).item
      const roles = (await body(await group.request.get('/api/v1/roles?page=1&page_size=100'))).items as Array<{ id: string; key: string }>
      async function actor(key: string, global: boolean) {
        const role = roles.find(item => item.key === key)
        expect(role).toBeTruthy()
        const username = `${global ? 'edit12_sec' : 'edit12_org'}_${stamp}`
        const password = 'EditorLive123!'
        const user = (await body(await group.request.post('/api/v1/users', { data: {
          username, password, display_name: `编辑验收_${key}`, global_role_ids: global ? [role!.id] : [],
          appointments: global ? [] : [{ organization_id: org.id, is_primary: true,
            assignments: [{ role_id: role!.id, data_scope: 'organization' }] }],
        } }), 201)).item
        const context = await browser.newContext({ baseURL }); contexts.push(context)
        const login = await body(await context.request.post('/api/v1/auth/login', { data: { username, password } }))
        expect(login.user.roles).toEqual([key])
        if (login.user.must_change_password) await body(await context.request.put('/api/v1/auth/profile/password', {
          data: { current_password: password, new_password: `${password}Changed` },
        }), 204)
        evidence[key] = { id: user.id, roles: login.user.roles }
        return context
      }
      const operator = await actor('security_operator', true)
      const editor = await actor('organization_editor', false)
      const discovered = Date.now() - 3_600_000
      const createFinding = async (number: number, severity = 'high') => (await body(await operator.request.post('/api/v1/security-findings', { data: {
        title: `真实编辑_${stamp}_${number}`, severity, status: 'pending', assignee_organization_id: org.id,
        asset_id: asset.id, vulnerability_type: '配置错误', description: '原始纯文本第一行\n原始纯文本第二行',
        evidence: '原始验证证据', remediation_advice: '原始整改建议', source: 'manual',
        discovered_at: new Date(discovered + number * 1000).toISOString(),
      } }), 201)).item as Finding
      const finding = await createFinding(0)
      const page = await operator.newPage()
      const errors: string[] = []
      page.on('pageerror', error => errors.push(error.message))
      Object.assign(evidence, { organization: org, asset_id: asset.id, finding_id: finding.id, pageErrors: errors })
      await use({ operator, editor, page, org, finding, createFinding, evidence })
      expect(errors).toEqual([])
    } finally {
      await testInfo.attach('finding-editor-live-evidence', { body: JSON.stringify(evidence, null, 2), contentType: 'application/json' })
      for (const context of contexts.reverse()) await context.close()
    }
  },
})
test.setTimeout(240_000)

function field(page: Page, label: string) {
  return page.locator('.el-form-item').filter({ has: page.locator('.el-form-item__label', { hasText: label }) })
}

const markdown = [
  '# 真实漏洞复现', '', '1. 打开隔离验收入口', '2. 检查返回结果', '', '> 仅包含脱敏测试证据', '',
  '```http', `GET /${'long-path-'.repeat(24)} HTTP/1.1`, 'X-Evidence: <safe-text>', '```', '',
  '| 位置 | 结果 |', '| --- | --- |', '| /example | 验证成功 |', '',
  '[安全链接](https://example.com/evidence)', '',
  '<script>window.__findingEditorExecuted = true</script>',
  '<img src=x onerror="window.__findingEditorExecuted=true">', '',
  '[危险链接](javascript:alert%281%29)', '[数据链接](data:text/html;base64,PHNjcmlwdD4=)',
  '[旧脚本链接](vbscript:msgbox%281%29)', '',
  '![远程图片](https://finding-editor-probe.invalid/tracker.png)',
].join('\n')
const evidenceText = '## 验证证据\n\n响应包含 `HTTP 200`。\n第二行原文保留。'
const advice = '## 整改建议\n\n- 限制访问权限\n- 补充回归验证'

async function assertRendered(region: Locator) {
  await expect(region.getByRole('heading', { name: '真实漏洞复现', exact: true })).toBeVisible()
  await expect(region.locator('ol li')).toHaveCount(2)
  await expect(region.locator('pre')).toContainText('X-Evidence: <safe-text>')
  await expect(region.getByRole('table')).toContainText('验证成功')
  await expect(region.locator('script, img, iframe, [onerror], a[href^="javascript:"], a[href^="data:"], a[href^="vbscript:"]')).toHaveCount(0)
  await expect(region.getByRole('link', { name: '安全链接', exact: true })).toHaveAttribute('rel', 'noopener noreferrer')
}

for (const width of [1440, 390]) {
  test(`真实安全运营员独立编辑、原文保存和第 2 页筛选恢复（${width}px）`, async ({ liveEditor: s }, testInfo) => {
    const { page, org, finding, operator, evidence } = s
    await page.setViewportSize({ width, height: 1000 })
    for (let number = 1; number <= 20; number++) await s.createFinding(number)
    const lowFinding = await s.createFinding(21, 'low')
    const query = new URLSearchParams({ tab: 'findings', organization_id: org.id, severity: 'high', status: 'pending', page: '2', page_size: '20' })
    const listURL = `/security-work-orders?${query}`
    const apiListURL = `/api/v1/security-findings?${query}`
    const listed = await body(await operator.request.get(apiListURL))
    expect(listed.total).toBe(21)
    expect(listed.items.map((item: Finding) => item.id)).toEqual([finding.id])
    await page.goto(listURL)
    await expect(page.getByRole('tab', { name: '漏洞与安全发现', exact: true })).toHaveAttribute('aria-selected', 'true')
    const row = page.locator('.security-panel .el-table__body-wrapper tr').filter({ hasText: finding.title })
    await expect(row).toBeVisible()
    await expect(page.locator('.security-panel .el-table__body-wrapper tr')).toHaveCount(1)
    await expect(page.getByRole('button', { name: lowFinding.title, exact: true })).toHaveCount(0)
    if (width === 1440) await clickRowAction(page, row, '编辑')
    else {
      await row.getByRole('button', { name: finding.title, exact: true }).click()
      await page.getByRole('dialog', { name: '安全发现详情', exact: true }).getByRole('button', { name: '编辑安全发现', exact: true }).click()
    }
    await expect(page).toHaveURL(new RegExp(`/security-findings/${finding.id}/edit`))
    await expect(page.getByRole('dialog', { name: '编辑安全发现', exact: true })).toHaveCount(0)
    await page.reload()
    await expect(field(page, '漏洞描述').locator('textarea')).toHaveValue(finding.description)
    const imageRequests: string[] = []
    page.on('request', request => { if (request.url().includes('finding-editor-probe.invalid')) imageRequests.push(request.url()) })
    for (const [label, value] of [['漏洞描述', markdown], ['验证证据', evidenceText], ['整改建议', advice]]) {
      await field(page, label).locator('textarea').fill(value)
      await field(page, label).getByRole('button', { name: '分栏', exact: true }).click()
      await expect(field(page, label).locator('textarea')).toBeVisible()
      await expect(field(page, label).getByRole('region')).toBeVisible()
    }
    await assertRendered(field(page, '漏洞描述').getByRole('region'))
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true)
    expect(imageRequests).toEqual([])
    await field(page, '漏洞描述').scrollIntoViewIfNeeded()
    await page.screenshot({ path: testInfo.outputPath(`live-finding-editor-${width}.png`), fullPage: true })
    const savedResponse = page.waitForResponse(response => response.request().method() === 'PATCH' && new URL(response.url()).pathname === `/api/v1/security-findings/${finding.id}`)
    const returnedList = page.waitForResponse(response => {
      const url = new URL(response.url())
      return url.pathname === '/api/v1/security-findings' && url.searchParams.get('page') === '2'
        && url.searchParams.get('organization_id') === org.id && url.searchParams.get('severity') === 'high'
        && url.searchParams.get('status') === 'pending' && url.searchParams.get('page_size') === '20'
    })
    await page.getByRole('button', { name: '保存修改', exact: true }).last().click()
    expect((await savedResponse).status()).toBe(200)
    const returnedResponse = await returnedList
    expect(returnedResponse.status()).toBe(200)
    expect((await returnedResponse.json()).items.map((item: Finding) => item.id)).toEqual([finding.id])
    await expect(page).toHaveURL(url => url.pathname === '/security-work-orders' && url.searchParams.get('page') === '2')
    const returned = new URL(page.url())
    for (const [key, value] of query) expect(returned.searchParams.get(key)).toBe(value)
    await expect(page.getByRole('tab', { name: '漏洞与安全发现', exact: true })).toHaveAttribute('aria-selected', 'true')
    await expect(page.locator('.security-panel .el-pagination .number.is-active')).toHaveText('2')
    const stored = (await body(await operator.request.get(`/api/v1/security-findings/${finding.id}`))).item as Finding
    expect(stored).toMatchObject({ description: markdown, evidence: evidenceText, remediation_advice: advice, asset_id: finding.asset_id, severity: 'high' })
    expect(stored.updated_at).not.toBe(finding.updated_at)
    if (width === 1440) await page.getByRole('button', { name: finding.title, exact: true }).click()
    const detail = page.getByRole('dialog', { name: '安全发现详情', exact: true })
    await expect(detail).toBeVisible()
    const descriptionRegion = detail.locator('.detail-block').filter({ has: page.getByRole('heading', { name: '漏洞描述', level: 3, exact: true }) }).getByRole('region')
    await assertRendered(descriptionRegion)
    await expect(detail.getByRole('heading', { name: '验证证据', level: 2, exact: true })).toBeVisible()
    await expect(detail.getByRole('heading', { name: '整改建议', level: 2, exact: true })).toBeVisible()
    expect(await page.evaluate(() => (window as Window & { __findingEditorExecuted?: boolean }).__findingEditorExecuted)).toBeUndefined()
    expect(imageRequests).toEqual([])
    await detail.getByRole('button', { name: '编辑安全发现', exact: true }).click()
    await expect(field(page, '漏洞描述').locator('textarea')).toHaveValue(markdown)
    await expect(field(page, '验证证据').locator('textarea')).toHaveValue(evidenceText)
    await expect(field(page, '整改建议').locator('textarea')).toHaveValue(advice)
    await page.getByRole('button', { name: '取消／返回', exact: true }).first().click()
    await expect(page).toHaveURL(url => url.pathname === '/security-work-orders' && url.searchParams.get('page') === '2')
    Object.assign(evidence, { width, returnedURL: returned.toString(), originalVersion: finding.updated_at, savedVersion: stored.updated_at, returnedIDs: [finding.id], imageRequests })
  })
}

test('真实权限拒绝、版本冲突恢复及浏览器未保存拦截', async ({ liveEditor: s }, testInfo) => {
  const { page, finding, operator, editor, org, evidence } = s
  await page.goto(`/security-work-orders?tab=findings&organization_id=${org.id}`)
  await clickRowAction(page, page.locator('.security-panel .el-table__body-wrapper tr').filter({ hasText: finding.title }), '编辑')
  await expect(field(page, '漏洞描述').locator('textarea')).toHaveValue(finding.description)
  const unsaved = '尚未保存的真实浏览器修改'
  await field(page, '漏洞描述').locator('textarea').fill(unsaved)
  await page.evaluate(() => window.history.back())
  const prompt = page.getByRole('dialog').filter({ hasText: '有尚未保存的修改' })
  await expect(prompt).toBeVisible()
  await prompt.getByRole('button', { name: '继续编辑', exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/security-findings/${finding.id}/edit`))
  await expect(field(page, '漏洞描述').locator('textarea')).toHaveValue(unsaved)
  const unloadDialog = page.waitForEvent('dialog')
  const reload = page.evaluate(() => window.location.reload())
  const dialog = await unloadDialog
  expect(dialog.type()).toBe('beforeunload')
  await dialog.dismiss()
  await reload
  await expect(field(page, '漏洞描述').locator('textarea')).toHaveValue(unsaved)

  const newerDescription = '另一个真实请求已保存的版本'
  const update = { title: finding.title, severity: finding.severity, status: finding.status,
    assignee_organization_id: finding.assignee_organization_id, asset_id: finding.asset_id,
    description: newerDescription, evidence: finding.evidence, remediation_advice: finding.remediation_advice,
    source: finding.source, discovered_at: finding.discovered_at, expected_updated_at: finding.updated_at }
  const newer = (await body(await operator.request.patch(`/api/v1/security-findings/${finding.id}`, { data: update }))).item as Finding
  expect(newer.updated_at).not.toBe(finding.updated_at)
  const conflictResponse = page.waitForResponse(response => response.request().method() === 'PATCH' && new URL(response.url()).pathname === `/api/v1/security-findings/${finding.id}`)
  await page.getByRole('button', { name: '保存修改', exact: true }).first().click()
  expect((await conflictResponse).status()).toBe(409)
  await expect(page.getByRole('alert').filter({ hasText: '安全发现已被其他人更新' }).first()).toBeVisible()
  await expect(field(page, '漏洞描述').locator('textarea')).toHaveValue(unsaved)
  expect((await body(await operator.request.get(`/api/v1/security-findings/${finding.id}`))).item.description).toBe(newerDescription)
  await page.getByRole('button', { name: '重新加载最新内容', exact: true }).click()
  await prompt.getByRole('button', { name: '放弃修改并离开', exact: true }).click()
  await expect(field(page, '漏洞描述').locator('textarea')).toHaveValue(newerDescription)
  const finalDescription = '重新加载最新版本后保存成功'
  await field(page, '漏洞描述').locator('textarea').fill(finalDescription)
  const recoveredResponse = page.waitForResponse(response => response.request().method() === 'PATCH' && new URL(response.url()).pathname === `/api/v1/security-findings/${finding.id}`)
  await page.getByRole('button', { name: '保存修改', exact: true }).first().click()
  expect((await recoveredResponse).status()).toBe(200)
  await expect(page).toHaveURL(url => url.pathname === '/security-work-orders')
  const beforeForbidden = (await body(await operator.request.get(`/api/v1/security-findings/${finding.id}`))).item
  expect(beforeForbidden.description).toBe(finalDescription)
  const forbiddenPage = await editor.newPage()
  await forbiddenPage.goto(`/security-findings/${finding.id}/edit`)
  await expect(forbiddenPage.getByText('当前账号没有编辑安全发现的权限', { exact: true })).toBeVisible()
  await expect(forbiddenPage.locator('textarea')).toHaveCount(0)
  await expect(forbiddenPage.getByText(finding.title, { exact: true })).toHaveCount(0)
  await expect(forbiddenPage.getByRole('button', { name: '保存修改', exact: true })).toHaveCount(0)
  const forbidden = await body(await editor.request.patch(`/api/v1/security-findings/${finding.id}`, {
    data: { ...update, description: '填报员不得写入', expected_updated_at: beforeForbidden.updated_at },
  }), 403)
  expect(forbidden.error.code).toBe('forbidden')
  expect((await body(await operator.request.get(`/api/v1/security-findings/${finding.id}`))).item).toEqual(beforeForbidden)
  await forbiddenPage.screenshot({ path: testInfo.outputPath('live-finding-editor-forbidden.png'), fullPage: true })
  Object.assign(evidence, { beforeUnload: true, historyBackGuard: true, staleWriteStatus: 409, forbiddenWriteStatus: 403, finalVersion: beforeForbidden.updated_at })
})
