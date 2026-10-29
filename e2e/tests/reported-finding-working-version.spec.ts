import { expect, test, type APIResponse, type BrowserContext } from '@playwright/test'
import { writeFile } from 'node:fs/promises'
import { clickRowAction } from '../helpers/row-actions'

async function body(response: APIResponse, status = 200) {
  const text = await response.text()
  expect(response.status(), text).toBe(status)
  return text ? JSON.parse(text) : null
}
type Org = { id: string; type: string; children?: Org[] }
function flatten(items: Org[]): Org[] { return items.flatMap(item => [item, ...flatten(item.children ?? [])]) }

// Retained, independent fixtures; no reset, SQL setup, or elevated child role.
test('真实子管理员确认工作版本并释放后续漏洞：跨组织拒绝、来源状态及重复保护', async ({ browser, baseURL }, testInfo) => {
  test.setTimeout(240_000)
  const contexts: BrowserContext[] = []
  const evidence: Record<string, any> = { pageErrors: [], serverErrors: [] }
  let page: Awaited<ReturnType<BrowserContext['newPage']>> | undefined
  try {
    const group = await browser.newContext({ baseURL }); contexts.push(group)
    await body(await group.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }))
    const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
    evidence.stamp = stamp
    const root = flatten((await body(await group.request.get('/api/v1/organizations/tree'))).items).find(item => item.type === 'group')!
    const roles = (await body(await group.request.get('/api/v1/roles?page=1&page_size=100'))).items
    const role = roles.find((item: { key: string }) => item.key === 'organization_admin')
    async function organization(side: string) {
      return (await body(await group.request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `FIND_DRAFT_${side}_${stamp}`, name: `漏洞工作版本_${side}_${stamp}`, type: 'subsidiary' } }), 201)).item
    }
    const org = await organization('A'), foreignOrg = await organization('B')
    async function child(organizationID: string, side: string) {
      const username = `finding_draft_${side}_${stamp}`, password = 'FindingDraft123!'
      const user = (await body(await group.request.post('/api/v1/users', { data: { username, password, display_name: `漏洞工作版本_${side}`, global_role_ids: [], appointments: [{ organization_id: organizationID, is_primary: true, assignments: [{ role_id: role.id, data_scope: 'organization' }] }] } }), 201)).item
      const context = await browser.newContext({ baseURL }); contexts.push(context)
      const login = await body(await context.request.post('/api/v1/auth/login', { data: { username, password } }))
      expect(login.user.roles).toContain('organization_admin')
      expect(login.user.roles).not.toContain('group_admin')
      expect(login.user.roles).not.toContain('security_operator')
      if (login.user.must_change_password) await body(await context.request.put('/api/v1/auth/profile/password', { data: { current_password: password, new_password: password + 'Changed' } }), 204)
      evidence[`child${side}`] = { id: user.id, roles: login.user.roles }
      return context
    }
    const own = await child(org.id, 'A'), foreign = await child(foreignOrg.id, 'B')
    Object.assign(evidence, { org, foreignOrg })
    const value = `working-finding-${stamp.replaceAll('_', '-')}.invalid`
    async function discovery(organizationID: string, assetValue: string) {
      return (await body(await group.request.post('/api/v1/asset-candidates', { data: { asset_type: 'domain', asset_value: assetValue, name: `工作版本_${assetValue}`, organization_id: organizationID, source: 'manual' } }), 201)).item
    }
    const discoveryA = await discovery(org.id, value)
    const discoveryB = await discovery(foreignOrg.id, `foreign-${value}`)
    Object.assign(evidence, { discoveryA, discoveryB, value })
    const header = (await (await group.request.get('/api/v1/security-findings/import-template?format=csv')).text()).replace(/^\uFEFF/, '').split(/\r?\n/)[0]
    async function imported(label: string, sourceStatus: string) {
      const title = `工作版本漏洞_${label}_${stamp}`
      const csv = `${header}\n${['domain', value, title, 'high', '工作版本回归', '保留源状态', '', '', new Date().toISOString(), sourceStatus].join(',')}\n`
      const history = (await body(await group.request.post('/api/v1/security-findings/import', { multipart: { file: { name: 'working-version.csv', mimeType: 'text/csv', buffer: Buffer.from(csv) } } }), 201)).item
      expect(history).toMatchObject({ total_count: 1, pending_count: 1, failure_count: 0, success_count: 0 })
      const path = `/api/v1/security-findings/import-candidates?keyword=${encodeURIComponent(title)}&page=1&page_size=20`
      const current = async () => {
        const rows = (await body(await group.request.get(path))).items.filter((item: { finding_import_id: string }) => item.finding_import_id === history.id)
        expect(rows).toHaveLength(1)
        return rows[0]
      }
      const candidate = await current()
      expect(candidate).toMatchObject({ status: 'pending', asset_candidate_id: discoveryA.id, import_status_normalized: sourceStatus })
      return { history, candidate, current }
    }
    const first = await imported('首次', 'confirmed')
    evidence.first = { history: first.history, before: first.candidate }
    const acceptData = { organization_id: org.id, environment: 'production', network_scope: 'public', status: 'active', importance: 'normal', owner_name: '隔离回归' }
    // No delegation: own read works, confirmation is refused and must not release findings.
    expect((await body(await own.request.get(`/api/v1/asset-candidates/${discoveryA.id}`))).item.id).toBe(discoveryA.id)
    const closed = await own.request.post(`/api/v1/asset-candidates/${discoveryA.id}/accept`, { data: acceptData })
    evidence.closedPolicy = { status: closed.status(), body: await closed.json() }
    expect(closed.status()).toBe(403)
    expect(await first.current()).toEqual(first.candidate)
    await body(await group.request.put('/api/v1/asset-candidates/self-service-policies', { data: { organization_id: org.id, allow_self_service: true, allowed_asset_types: ['domain'], require_enabled_organization: true } }))
    const denied = await foreign.request.post(`/api/v1/asset-candidates/${discoveryA.id}/accept`, { data: acceptData })
    expect(denied.status()).toBe(403)
    expect(await first.current()).toEqual(first.candidate)
    const foreignDraft = (await body(await group.request.post(`/api/v1/asset-candidates/${discoveryB.id}/accept`, { data: { ...acceptData, organization_id: foreignOrg.id } }))).item
    evidence.foreignDraft = foreignDraft
    page = await own.newPage()
    page.on('pageerror', error => evidence.pageErrors.push(error.message))
    page.on('response', response => { if (response.status() >= 500) evidence.serverErrors.push(`${response.status()} ${response.url()}`) })
    await page.goto('/asset-candidates')
    const row = page.locator('.el-table__body-wrapper tr').filter({ hasText: value })
    await clickRowAction(page, row, '确认入库')
    const dialog = page.getByRole('dialog', { name: '确认并入库', exact: true })
    await expect(dialog).toContainText('未上报工作版本')
    const acceptedResponse = page.waitForResponse(response => response.url().includes(`/asset-candidates/${discoveryA.id}/accept`) && response.request().method() === 'POST')
    await dialog.getByRole('button', { name: '确认入库', exact: true }).click()
    const draft = (await body(await acceptedResponse)).item
    await expect(dialog).toBeHidden()
    evidence.draft = draft
    const acceptedDiscovery = (await body(await own.request.get(`/api/v1/asset-candidates/${discoveryA.id}`))).item
    expect(acceptedDiscovery.working_version_draft_id).toBe(draft.id)
    evidence.acceptedDiscovery = acceptedDiscovery
    const firstLinked = await first.current()
    expect(firstLinked.status).toBe('imported')
    evidence.first.linked = firstLinked
    async function finding(id: string) { return (await body(await own.request.get(`/api/v1/security-findings/${id}`))).item }
    const firstFinding = await finding(firstLinked.imported_finding_id)
    expect(firstFinding).toMatchObject({ pending_asset_draft_id: draft.id, assignee_organization_id: org.id, status: 'confirmed', asset_link_state: 'pending' })
    expect(firstFinding.asset_id || '').toBe('')
    evidence.first.finding = firstFinding
    await page.screenshot({ path: testInfo.outputPath('child-confirmed-working-version.png'), fullPage: true })

    const later = await imported('后续', 'resolved')
    evidence.later = { history: later.history, before: later.candidate }
    const link = `/api/v1/security-findings/import-candidates/${later.candidate.id}/link`
    const release = { pending_asset_draft_id: draft.id, review_note: '释'.repeat(2000) }
    // No client asset_candidate_id: stored discovery is authoritative.
    for (const [actor, input, expected] of [
      [foreign, release, 403],
      [own, { ...release, pending_asset_draft_id: foreignDraft.id }, 409],
      [own, { ...release, asset_candidate_id: discoveryB.id }, 409],
      [own, { ...release, review_note: '释'.repeat(2001) }, 400],
    ] as const) {
      const rejected = await body(await actor.request.post(link, { data: input }), expected)
      if (expected === 409) expect(rejected.error).toEqual({ code: 'candidate_link_conflict', message: '待关联漏洞与目标资产或工作版本不匹配，请刷新后重新选择' })
      evidence.later.rejections ??= []
      evidence.later.rejections.push({ input, status: expected, error: rejected.error })
      expect(await later.current()).toEqual(later.candidate)
    }
    expect((await body(await own.request.get('/api/v1/security-findings/import-candidates?status=pending'), 403)).error.code).toBe('forbidden')
    const linked = (await body(await own.request.post(link, { data: release }))).item
    expect(linked).toMatchObject({ status: 'imported', asset_candidate_id: discoveryA.id, review_note: release.review_note })
    const laterFinding = await finding(linked.imported_finding_id)
    expect(laterFinding).toMatchObject({ pending_asset_draft_id: draft.id, assignee_organization_id: org.id, status: 'resolved', import_status_normalized: 'resolved', asset_link_state: 'pending' })
    expect(laterFinding.asset_id || '').toBe('')
    await body(await own.request.post(link, { data: release }), 409)
    expect(await later.current()).toEqual(linked)
    Object.assign(evidence.later, { linked, finding: laterFinding, rejectedUnchanged: true, repeatedUnchanged: true })

    // Accepted discovery is still unpublished; retry endpoint releases new rows and is idempotent.
    const retry = await imported('重试', 'fixed')
    evidence.retry = { history: retry.history, before: retry.candidate }
    await body(await foreign.request.post(`/api/v1/asset-candidates/${discoveryA.id}/retry-findings`), 403)
    expect(await retry.current()).toEqual(retry.candidate)
    const retried = (await body(await own.request.post(`/api/v1/asset-candidates/${discoveryA.id}/retry-findings`))).result
    expect(retried).toMatchObject({ total: 1, linked: 1, failed: 0, pending: 0 })
    const retryLinked = await retry.current()
    const retryFinding = await finding(retryLinked.imported_finding_id)
    expect(retryFinding).toMatchObject({ pending_asset_draft_id: draft.id, status: 'fixed', assignee_organization_id: org.id, asset_link_state: 'pending' })
    const noOp = (await body(await own.request.post(`/api/v1/asset-candidates/${discoveryA.id}/retry-findings`))).result
    expect(noOp).toMatchObject({ total: 0, linked: 0, failed: 0, pending: 0 })
    Object.assign(evidence.retry, { linked: retryLinked, finding: retryFinding, retried, noOp })
    // Same working-version title is a duplicate, never a fourth finding or a status overwrite.
    const duplicate = await imported('后续', 'pending')
    evidence.duplicate = { history: duplicate.history, before: duplicate.candidate }
    const duplicateLinked = (await body(await own.request.post(`/api/v1/security-findings/import-candidates/${duplicate.candidate.id}/link`, { data: { pending_asset_draft_id: draft.id, review_note: '重复扫描不能覆盖已确认的源状态' } }))).item
    expect(duplicateLinked).toMatchObject({ status: 'duplicate', imported_finding_id: laterFinding.id })
    expect(await finding(laterFinding.id)).toEqual(laterFinding)
    Object.assign(evidence.duplicate, { linked: duplicateLinked, existingFindingUnchanged: true })
    const histories = (await body(await group.request.get('/api/v1/security-findings/imports?page=1&page_size=100'))).items
    for (const item of [evidence.first, evidence.later, evidence.retry, evidence.duplicate]) {
      item.finalHistory = histories.find((history: { id: string }) => history.id === item.history.id)
      expect(item.finalHistory.pending_count).toBe(0)
    }
    const formal = await body(await group.request.get(`/api/v1/assets?q=${encodeURIComponent(value)}&page=1&page_size=100`))
    expect(formal.items.filter((asset: { value: string }) => asset.value === value)).toHaveLength(0)
    const visible = await body(await own.request.get(`/api/v1/security-findings?organization_id=${org.id}&page=1&page_size=100`))
    expect(visible.total).toBe(3)
    evidence.visibleFindingIDs = visible.items.map((item: { id: string }) => item.id)
    expect(evidence.pageErrors).toEqual([])
    expect(evidence.serverErrors).toEqual([])
  } finally {
    if (page && !page.isClosed()) await page.screenshot({ path: testInfo.outputPath('working-version-final.png'), fullPage: true }).catch(() => {})
    await writeFile(testInfo.outputPath('working-version-evidence.json'), JSON.stringify(evidence, null, 2))
    for (const context of contexts.reverse()) await context.close()
  }
})

// Publication regression: a finding waiting on a working-version asset must be
// moved to the formal asset when the real submission/review pipeline publishes it.
for (const publication of ['api', 'browser'] as const) {
  test(`工作版本发布后漏洞迁移到正式资产台账 ${publication}`, async ({ browser, baseURL }, testInfo) => {
    test.setTimeout(240_000)
    const contexts: BrowserContext[] = []
    const evidence: Record<string, any> = { publication, pageErrors: [], consoleErrors: [], serverErrors: [] }
    async function json(response: APIResponse, status = 200) {
      const text = await response.text(); expect(response.status(), text).toBe(status); return text ? JSON.parse(text) : null
    }
    const flatten = (items: any[]): any[] => items.flatMap(item => [item, ...flatten(item.children ?? [])])
    try {
      const group = await browser.newContext({ baseURL }); contexts.push(group)
      await json(await group.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }))
      evidence.phase = 'fixtures'
      const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
      const root = flatten((await json(await group.request.get('/api/v1/organizations/tree'))).items).find(item => item.type === 'group')
      const org = (await json(await group.request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `FIND_PUBLISH_${stamp}`, name: `漏洞发布_${stamp}`, type: 'subsidiary' } }), 201)).item
      const roles = (await json(await group.request.get('/api/v1/roles?page=1&page_size=100'))).items
      const role = roles.find((item: any) => item.key === 'organization_admin')
      const password = 'FindingPublish123!'
      const user = (await json(await group.request.post('/api/v1/users', { data: { username: `finding_publish_${stamp}`, password, display_name: '漏洞发布子管理员', global_role_ids: [], appointments: [{ organization_id: org.id, is_primary: true, assignments: [{ role_id: role.id, data_scope: 'organization' }] }] } }), 201)).item
      const child = await browser.newContext({ baseURL }); contexts.push(child)
      const login = await json(await child.request.post('/api/v1/auth/login', { data: { username: user.username, password } }))
      expect(login.user.roles).toContain('organization_admin')
      expect(login.user.roles).not.toContain('group_admin')
      expect(login.user.roles).not.toContain('security_operator')
      evidence.child = { id: login.user.id, roles: login.user.roles }
      if (login.user.must_change_password) await json(await child.request.put('/api/v1/auth/profile/password', { data: { current_password: password, new_password: password + 'Changed' } }), 204)
      await json(await group.request.put('/api/v1/asset-candidates/self-service-policies', { data: { organization_id: org.id, allow_self_service: true, allowed_asset_types: ['domain'], require_enabled_organization: true } }))
      const value = `publish-finding-${stamp}.invalid`
      const candidate = (await json(await group.request.post('/api/v1/asset-candidates', { data: { asset_type: 'domain', asset_value: value, name: `发布资产_${stamp}`, organization_id: org.id, source: 'manual' } }), 201)).item
      const header = (await (await group.request.get('/api/v1/security-findings/import-template?format=csv')).text()).replace(/^\uFEFF/, '').split(/\r?\n/)[0]
      const csv = `${header}\n${['domain', value, `发布前漏洞_${stamp}`, 'high', 'remote service', '工作版本发布回归', '测试证据', '整改建议', new Date().toISOString(), 'pending'].join(',')}\n`
      const history = (await json(await group.request.post('/api/v1/security-findings/import', { multipart: { file: { name: 'publish.csv', mimeType: 'text/csv', buffer: Buffer.from(csv) } } }), 201)).item
      const candidateRow = (await json(await group.request.get(`/api/v1/security-findings/import-candidates?keyword=${encodeURIComponent(`发布前漏洞_${stamp}`)}&page=1&page_size=20`))).items.find((x: any) => x.finding_import_id === history.id)
      const accepted = (await json(await group.request.post(`/api/v1/asset-candidates/${candidate.id}/accept`, { data: { organization_id: org.id, environment: 'production', network_scope: 'public', status: 'active', importance: 'normal', owner_name: '发布回归' } }))).item
      const draftID = accepted.id
      const releasedCandidate = (await json(await group.request.get(`/api/v1/security-findings/import-candidates?keyword=${encodeURIComponent(`发布前漏洞_${stamp}`)}&page=1&page_size=20`))).items.find((x: any) => x.finding_import_id === history.id)
      expect(releasedCandidate.status).toBe('imported')
      const findingBefore = (await json(await group.request.get(`/api/v1/security-findings/${releasedCandidate.imported_finding_id}`))).item
      expect(findingBefore.pending_asset_draft_id).toBe(draftID)
      expect(findingBefore.asset_id || '').toBe('')
      // A later scan can resolve to the same finding while the asset is still a
      // working version. Publication must attach duplicate rows too, without
      // changing their review metadata or creating a second finding.
      const duplicateHistory = (await json(await group.request.post('/api/v1/security-findings/import', { multipart: { file: { name: 'publish-duplicate.csv', mimeType: 'text/csv', buffer: Buffer.from(csv) } } }), 201)).item
      await json(await child.request.post(`/api/v1/asset-candidates/${candidate.id}/retry-findings`))
      const duplicateBefore = (await json(await group.request.get(`/api/v1/security-findings/import-candidates?keyword=${encodeURIComponent(`发布前漏洞_${stamp}`)}&page=1&page_size=20`))).items.find((x: any) => x.finding_import_id === duplicateHistory.id)
      expect(duplicateBefore).toMatchObject({ status: 'duplicate', imported_finding_id: findingBefore.id })
      expect(duplicateBefore.matched_asset_id || '').toBe('')
      const task = (await json(await group.request.post('/api/v1/inventory-tasks', { data: { name: `漏洞发布填报_${stamp}`, description: '工作版本发布回归', start_at: new Date(Date.now() - 60_000).toISOString(), deadline_at: new Date(Date.now() + 86_400_000).toISOString(), status: 'active', asset_network_scope: 'all', required_fields: ['network_assets'], quality_rules: [], require_review: true, allow_late_submission: true, organization_ids: [org.id] } }), 201)).item
      const assignment = `/api/v1/inventory-tasks/${task.id}/organizations/${org.id}`
      evidence.fixture = { org, task, candidate, history, candidateRow, releasedCandidate, accepted, duplicateHistory, duplicateBefore }
      async function assertUnpublished() {
        const formal = await json(await group.request.get(`/api/v1/assets?q=${encodeURIComponent(value)}&page=1&page_size=100`))
        expect(formal.items.filter((x: any) => x.value === value)).toEqual([])
        const finding = (await json(await child.request.get(`/api/v1/security-findings/${findingBefore.id}`))).item
        expect(finding.pending_asset_draft_id).toBe(draftID)
        expect(finding.asset_id || '').toBe('')
      }
      let selected: any, submitted: any, approved: any
      await assertUnpublished()
      if (publication === 'api') {
        await json(await child.request.post(`${assignment}/start`))
        selected = (await json(await child.request.post(`${assignment}/asset-drafts/${draftID}/select`, { data: { expected_version: accepted.version } }))).item
        const quality = await json(await child.request.get(`${assignment}/quality-check`))
        expect(quality.quality_check.passed).toBe(true)
        submitted = (await json(await child.request.post(`${assignment}/submit`, { data: { note: '发布漏洞迁移回归' } }))).item
        await assertUnpublished()
        approved = (await json(await group.request.post(`${assignment}/review`, { data: { decision: 'approved' } }))).item
      } else {
        evidence.phase = 'browser-start'
        const page = await child.newPage(), reviewer = await group.newPage()
        for (const p of [page, reviewer]) {
          p.setDefaultTimeout(20_000)
          p.on('pageerror', error => evidence.pageErrors.push(error.message))
          p.on('console', message => { if (message.type() === 'error') evidence.consoleErrors.push(message.text()) })
          p.on('response', response => { if (response.status() >= 500) evidence.serverErrors.push({ url: response.url(), status: response.status() }) })
        }
        async function taskDetail() {
          await page.goto('/inventory-tasks')
          await page.getByRole('button', { name: '从具体任务进入上报内容', exact: true }).click()
          await page.getByRole('menuitem', { name: task.name, exact: true }).click()
          const detail = page.getByRole('dialog', { name: '填报任务详情', exact: true })
          await expect(detail).toBeVisible()
          return detail
        }
        let detail = await taskDetail()
        const progressRow = () => detail.locator('.task-organization-table tbody tr').filter({ hasText: org.name })
        const started = page.waitForResponse(res => res.url().endsWith(`${assignment}/start`) && res.request().method() === 'POST')
        await clickRowAction(page, progressRow(), '开始填报')
        expect((await json(await started)).item.status).toBe('in_progress')
        await expect(progressRow()).toContainText('填报中')
        await clickRowAction(page, progressRow(), '维护网络资产')
        await expect(page).toHaveURL(/\/assets\?.*mode=inventory/)
        evidence.phase = 'browser-select'
        const selectedResponse = page.waitForResponse(res => res.url().endsWith(`${assignment}/asset-drafts/${draftID}/select`) && res.request().method() === 'POST')
        await clickRowAction(page, page.locator('.data-table-panel').filter({ hasNot: page.locator('.table-panel-heading') }).last().locator('tbody tr').filter({ hasText: value }), '选择上报')
        selected = (await json(await selectedResponse)).item
        expect(selected.task_id).toBe(task.id)
        await assertUnpublished()
        await page.getByRole('button', { name: '返回填报任务', exact: true }).click()
        await expect(page).toHaveURL(/\/inventory-tasks$/)
        detail = await taskDetail()
        evidence.phase = 'browser-submit'
        const checked = page.waitForResponse(res => res.url().includes(`${assignment}/quality-check`) && res.request().method() === 'GET')
        await clickRowAction(page, progressRow(), '提交')
        const quality = await json(await checked)
        expect(quality.quality_check.passed).toBe(true)
        expect(quality.quality_check.asset_count).toBe(1)
        evidence.quality = quality
        const dialog = page.getByRole('dialog', { name: '提交填报结果', exact: true })
        await expect(dialog.getByRole('button', { name: '确认提交', exact: true })).toBeEnabled()
        await dialog.getByLabel('填报说明', { exact: true }).fill('真实子管理员浏览器提交漏洞工作版本')
        const sent = page.waitForResponse(res => res.url().endsWith(`${assignment}/submit`) && res.request().method() === 'POST')
        await dialog.getByRole('button', { name: '确认提交', exact: true }).click()
        submitted = (await json(await sent)).item
        await expect(dialog).toBeHidden()
        await expect(progressRow()).toContainText('待审核')
        await assertUnpublished()
        await page.screenshot({ path: testInfo.outputPath('child-submitted.png'), fullPage: true })
        evidence.phase = 'browser-review'
        await reviewer.goto('/inventory-tasks')
        await reviewer.getByRole('button', { name: task.name, exact: true }).click()
        const reviewDetail = reviewer.getByRole('dialog', { name: '填报任务详情', exact: true })
        const reviewRow = reviewDetail.locator('.task-organization-table tbody tr').filter({ hasText: org.name })
        await clickRowAction(reviewer, reviewRow, '通过')
        const confirm = reviewer.getByRole('dialog', { name: '审核通过', exact: true })
        await expect(confirm).toContainText(org.name)
        const reviewed = reviewer.waitForResponse(res => res.url().endsWith(`${assignment}/review`) && res.request().method() === 'POST')
        await confirm.getByRole('button', { name: '通过', exact: true }).click()
        approved = (await json(await reviewed)).item
        await expect(confirm).toBeHidden()
        await expect(reviewRow).toContainText('已通过')
        await reviewer.screenshot({ path: testInfo.outputPath('group-published.png'), fullPage: true })
      }
      evidence.fixture.selected = selected
      expect(submitted.status).toBe('submitted')
      expect(approved.status).toBe('approved')
      evidence.phase = 'verify-publication'
      const formal = (await json(await group.request.get(`/api/v1/assets?q=${encodeURIComponent(value)}&page=1&page_size=100`))).items.filter((x: any) => x.value === value)
      expect(formal).toHaveLength(1)
      const findingAfter = (await json(await group.request.get(`/api/v1/security-findings/${releasedCandidate.imported_finding_id}`))).item
      expect(findingAfter.asset_id).toBe(formal[0].id)
      expect(findingAfter.pending_asset_draft_id || '').toBe('')
      expect(findingAfter.asset_link_state).toBe('live')
      const candidateAfter = (await json(await group.request.get(`/api/v1/asset-candidates/${candidate.id}`))).item
      expect(candidateAfter).toMatchObject({ status: 'accepted', matched_asset_id: formal[0].id, working_version_draft_id: draftID })
      const candidateAfterImport = (await json(await group.request.get(`/api/v1/security-findings/import-candidates?keyword=${encodeURIComponent(`发布前漏洞_${stamp}`)}&page=1&page_size=20`))).items.find((x: any) => x.finding_import_id === history.id)
      expect(candidateAfterImport).toMatchObject({ status: 'imported', matched_asset_id: formal[0].id, imported_finding_id: findingBefore.id, reviewed_at: releasedCandidate.reviewed_at })
      const duplicateAfter = (await json(await group.request.get(`/api/v1/security-findings/import-candidates?keyword=${encodeURIComponent(`发布前漏洞_${stamp}`)}&page=1&page_size=20`))).items.find((x: any) => x.finding_import_id === duplicateHistory.id)
      expect(duplicateAfter).toMatchObject({ status: 'duplicate', matched_asset_id: formal[0].id, imported_finding_id: findingBefore.id, reviewed_at: duplicateBefore.reviewed_at })
      const findings = await json(await child.request.get('/api/v1/security-findings?page=1&page_size=20'))
      expect(findings.items.map((x: any) => x.id)).toEqual([findingBefore.id])
      const duplicateHistoryAfter = (await json(await group.request.get('/api/v1/security-findings/imports?page=1&page_size=100'))).items.find((x: any) => x.id === duplicateHistory.id)
      expect(duplicateHistoryAfter.pending_count).toBe(0)
      const historyAfter = (await json(await group.request.get('/api/v1/security-findings/imports?page=1&page_size=100'))).items.find((x: any) => x.id === history.id)
      expect(historyAfter.pending_count).toBe(0)
      const publishedDrafts = await json(await child.request.get(`${assignment}/asset-drafts?page=1&page_size=100`))
      expect(publishedDrafts.items.find((x: any) => x.id === draftID)).toMatchObject({ draft_status: 'published', version: selected.version + 2 })
      evidence.result = { submitted, approved, formal, findingAfter, candidateAfter, candidateAfterImport, historyAfter, duplicateAfter, duplicateHistoryAfter, findings, publishedDrafts }
      expect(evidence.pageErrors).toEqual([])
      expect(evidence.consoleErrors).toEqual([])
      expect(evidence.serverErrors).toEqual([])
      evidence.phase = 'complete'
      await testInfo.attach('finding-publication-evidence', { body: Buffer.from(JSON.stringify(evidence, null, 2)), contentType: 'application/json' })
    } finally {
      const path = testInfo.outputPath('finding-publication-evidence.json')
      await writeFile(path, JSON.stringify(evidence, null, 2))
      await testInfo.attach('finding-publication-evidence-final', { path, contentType: 'application/json' }).catch(() => {})
      for (const context of contexts.reverse()) await context.close()
    }
  })
}

// Scanner service identities include their port. Confirmation must not strip it
// as if this were an IP discovery, or the real form returns an invalid-input 400.
test('漏洞导入网络服务线索在浏览器确认时保留主机端口并原子释放漏洞', async ({ browser, baseURL }, testInfo) => {
  // Two full group pages render the retained policy tree; keep the 15s row
  // readiness assertion separate from this two-endpoint workflow budget.
  test.setTimeout(240_000)
  const context = await browser.newContext({ baseURL })
  const evidence: Record<string, any> = { pageErrors: [], results: [] }
  try {
    await body(await context.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }))
    const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
    const roots = (await body(await context.request.get('/api/v1/organizations/tree'))).items
    const root = flatten(roots).find(item => item.type === 'group')!
    const org = (await body(await context.request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `SERVICE_${stamp}`, name: `服务端点回归_${stamp}`, type: 'subsidiary' } }), 201)).item
    evidence.org = org
    const header = (await (await context.request.get('/api/v1/security-findings/import-template?format=csv')).text()).replace(/^\uFEFF/, '').split(/\r?\n/)[0]
    const page = await context.newPage()
    page.on('pageerror', error => evidence.pageErrors.push(error.message))
    const serial = BigInt(Date.now())
    const ipv6 = `2001:db8:${((serial >> 32n) & 0xffffn).toString(16)}:${((serial >> 16n) & 0xffffn).toString(16)}:${(serial & 0xffffn).toString(16)}::7`
    for (const value of [`listener-${stamp.replaceAll('_', '-')}.example.invalid:8443`, `[${ipv6}]:1521`]) {
      const title = `服务端口保留_${value}`
      // Only this fixture's discovery is preassigned to its own organization;
      // the finding itself goes through the actual CSV import and release path.
      const discovery = (await body(await context.request.post('/api/v1/asset-candidates', { data: { asset_type: 'service', asset_value: value, name: title, organization_id: org.id, source: 'finding_import' } }), 201)).item
      const csv = `${header}\n${['service', value, title, 'high', '端口身份', '确认后仍待正式发布', '', '', new Date().toISOString(), 'confirmed'].join(',')}\n`
      const history = (await body(await context.request.post('/api/v1/security-findings/import', { multipart: { file: { name: 'service-endpoint.csv', mimeType: 'text/csv', buffer: Buffer.from(csv) } } }), 201)).item
      expect(history).toMatchObject({ total_count: 1, pending_count: 1, success_count: 0, failure_count: 0 })
      const path = `/api/v1/security-findings/import-candidates?keyword=${encodeURIComponent(title)}&page=1&page_size=20`
      const current = async () => {
        const matches = (await body(await context.request.get(path))).items.filter((item: any) => item.finding_import_id === history.id)
        expect(matches).toHaveLength(1)
        return matches[0]
      }
      expect(await current()).toMatchObject({ status: 'pending', asset_candidate_id: discovery.id })
      const listing = page.waitForResponse(response => {
        const url = new URL(response.url())
        return response.request().method() === 'GET' && url.pathname === '/api/v1/asset-candidates' && url.searchParams.get('status') === 'pending'
      })
      await page.goto('/asset-candidates')
      const listed = await body(await listing)
      expect(listed.items.some((item: { id: string }) => item.id === discovery.id)).toBe(true)
      // The preserved group's policy table is large; wait for the real list
      // render rather than assuming navigation has already painted its rows.
      const row = page.locator('.data-table-panel .el-table__body-wrapper tr').filter({ hasText: value })
      await expect(row).toBeVisible({ timeout: 15_000 })
      await clickRowAction(page, row, '确认入库')
      const dialog = page.getByRole('dialog', { name: '确认并入库', exact: true })
      await dialog.getByLabel('资产备注', { exact: true }).fill('服务的端口不能丢失')
      const accepted = page.waitForResponse(response => response.url().endsWith(`/asset-candidates/${discovery.id}/accept`) && response.request().method() === 'POST')
      await dialog.getByRole('button', { name: '确认入库', exact: true }).click()
      const draft = (await body(await accepted)).item
      await expect(dialog).toBeHidden()
      expect(draft).toMatchObject({ type: 'service', value, notes: '服务的端口不能丢失', is_working_copy: true, organization_id: org.id })
      const linked = await current()
      expect(linked).toMatchObject({ status: 'imported', asset_candidate_id: discovery.id })
      const finding = (await body(await context.request.get(`/api/v1/security-findings/${linked.imported_finding_id}`))).item
      expect(finding).toMatchObject({ pending_asset_draft_id: draft.id, assignee_organization_id: org.id, status: 'confirmed', asset_link_state: 'pending' })
      expect(finding.asset_id || '').toBe('')
      const savedDiscovery = (await body(await context.request.get(`/api/v1/asset-candidates/${discovery.id}`))).item
      expect(savedDiscovery).toMatchObject({ status: 'pending', working_version_draft_id: draft.id })
      evidence.results.push({ discovery, history, draft, linked, finding, savedDiscovery })
    }
    expect(evidence.pageErrors).toEqual([])
    await page.screenshot({ path: testInfo.outputPath('service-endpoint-confirmed.png'), fullPage: true })
  } finally {
    await writeFile(testInfo.outputPath('service-endpoint-evidence.json'), JSON.stringify(evidence, null, 2))
    await context.close().catch(() => {})
  }
})
