import { expect, test as base, type APIResponse, type BrowserContext, type Page } from '@playwright/test'
import { writeFile } from 'node:fs/promises'
import { clickRowAction } from '../helpers/row-actions'

async function body(response: APIResponse, status = 200) {
  const text = await response.text()
  expect(response.status(), text).toBe(status)
  return text ? JSON.parse(text) : null
}
type Org = { id: string; type: string; children?: Org[] }
function flatten(items: Org[]): Org[] { return items.flatMap(item => [item, ...flatten(item.children ?? [])]) }
const actions = ['authorize', 'start', 'complete', 'cancel'] as const
type Action = typeof actions[number]
const labels = { authorize: '授权', start: '开始', complete: '完成', cancel: '取消' }
const statuses = { authorize: 'authorized', start: 'in_progress', complete: 'completed', cancel: 'cancelled' }
const statusLabels = { authorize: '已授权', start: '测试中', complete: '已完成', cancel: '已取消' }
type Fixture = {
  operator: BrowserContext; child: BrowserContext; page: Page
  createBatch: (suffix: string) => Promise<{ id: string; name: string }>
  snapshot: (id: string) => Promise<{ item: Record<string, unknown>; events: { items: { action: string; comment?: string; actor_name: string }[]; total: number } }>
  evidence: Record<string, unknown>; pageErrors: string[]; serverErrors: string[]
}
const test = base.extend<{ batches: Fixture }>({
  batches: async ({ browser, baseURL }, use, testInfo) => {
    const contexts: BrowserContext[] = []
    const evidence: Record<string, unknown> = { batches: [] }
    const pageErrors: string[] = []
    const serverErrors: string[] = []
    let page: Page | undefined
    try {
      const group = await browser.newContext({ baseURL }); contexts.push(group)
      await body(await group.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }))
      const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
      const root = flatten((await body(await group.request.get('/api/v1/organizations/tree'))).items).find(item => item.type === 'group')!
      const org = (await body(await group.request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `ST_BOUND_${stamp}`, name: `安全测试边界_${stamp}`, type: 'subsidiary' } }), 201)).item
      const asset = (await body(await group.request.post('/api/v1/assets', { data: { type: 'domain', value: `st-bound-${stamp.replaceAll('_', '-')}.example.com`, organization_id: org.id, environment: 'production', network_scope: 'public', status: 'active', importance: 'normal', direct_change_reason: '仅隔离环境安全测试边界验收' } }), 201)).item
      const roles = (await body(await group.request.get('/api/v1/roles?page=1&page_size=100'))).items
      async function actor(key: string, global: boolean) {
        const role = roles.find((item: { key: string }) => item.key === key)
        expect(role).toBeTruthy()
        const username = `${global ? 'st_op' : 'st_child'}_${stamp}`
        const password = 'SecurityBatch123!'
        const user = (await body(await group.request.post('/api/v1/users', { data: { username, password, display_name: `批次边界_${key}_${stamp}`, global_role_ids: global ? [role.id] : [], appointments: global ? [] : [{ organization_id: org.id, is_primary: true, assignments: [{ role_id: role.id, data_scope: 'organization' }] }] } }), 201)).item
        const context = await browser.newContext({ baseURL }); contexts.push(context)
        const login = await body(await context.request.post('/api/v1/auth/login', { data: { username, password } }))
        expect(login.user.roles).toEqual([key])
        if (login.user.must_change_password) await body(await context.request.put('/api/v1/auth/profile/password', { data: { current_password: password, new_password: password + 'Changed' } }), 204)
        evidence[key] = { id: user.id, roles: login.user.roles, display_name: user.display_name }
        return context
      }
      const operator = await actor('security_operator', true)
      const child = await actor('organization_admin', false)
      page = await operator.newPage()
      page.on('pageerror', error => pageErrors.push(error.message))
      page.on('response', response => { if (response.status() >= 500) serverErrors.push(`${response.status()} ${response.url()}`) })
      async function createBatch(suffix: string) {
        const batch = (await body(await operator.request.post('/api/v1/security-test-batches', { data: { batch_name: `边界_${suffix}_${stamp}`, test_type: 'penetration', provider: '隔离回归', authorization_note: '仅隔离环境验证', start_at: new Date(Date.now() - 3600000).toISOString(), end_at: new Date(Date.now() + 86400000).toISOString(), asset_ids: [asset.id] } }), 201)).item
        ;(evidence.batches as unknown[]).push(batch)
        return batch
      }
      async function snapshot(id: string) {
        return {
          item: (await body(await operator.request.get(`/api/v1/security-test-batches/${id}`))).item,
          events: await body(await operator.request.get(`/api/v1/security-test-batches/${id}/events?page=1&page_size=100`)),
        }
      }
      Object.assign(evidence, { stamp, org, asset, pageErrors, serverErrors })
      await use({ operator, child, page, createBatch, snapshot, evidence, pageErrors, serverErrors })
    } finally {
      if (page && !page.isClosed()) await page.screenshot({ path: testInfo.outputPath('security-batch-boundaries.png'), fullPage: true }).catch(() => {})
      await writeFile(testInfo.outputPath('security-batch-boundaries-evidence.json'), JSON.stringify(evidence, null, 2))
      for (const context of contexts.reverse()) await context.close()
    }
  },
})
test.setTimeout(180_000)

for (const [language, character] of [['中文', '测'], ['英文', 'T']] as const) {
  test(`真实安全运营员${language}2000字授权开始完成及取消浏览器闭环，超限和越权不改数据`, async ({ batches: s }, testInfo) => {
    const { operator, child, page, createBatch, snapshot, evidence } = s
    const completed: unknown[] = []
    evidence.flows = completed
    for (const terminal of ['complete', 'cancel'] as const) {
      const batch = await createBatch(`${language}_${terminal}`)
      const path = `/api/v1/security-test-batches/${batch.id}`
      expect((await body(await child.request.get(path), 403)).error.code).toBe('forbidden')
      await page.goto('/security-test-batches')
      const row = page.locator('.el-table__body-wrapper tr').filter({ hasText: batch.name })
      for (const action of ['authorize', 'start', terminal] as const) {
        const before = await snapshot(batch.id)
        const comment = character.repeat(2000)
        expect((await body(await child.request.post(`${path}/${action}`, { data: { comment } }), 403)).error.code).toBe('forbidden')
        expect(await snapshot(batch.id)).toEqual(before)
        expect((await body(await operator.request.post(`${path}/${action}`, { data: { comment: comment + character } }), 400)).error.code).toBe('invalid_security_test_batch')
        expect(await snapshot(batch.id)).toEqual(before)
        await clickRowAction(page, row, labels[action])
        const prompt = page.locator('.el-message-box:visible')
        let posts = 0
        const countPost = (request: { url(): string; method(): string }) => { if (request.url().endsWith(`${path}/${action}`) && request.method() === 'POST') posts++ }
        page.on('request', countPost)
        try {
          if (action === 'cancel') {
            await prompt.locator('input').fill('   ')
            await prompt.getByRole('button', { name: '确认', exact: true }).click()
            await expect(prompt).toContainText('取消原因不能为空')
            await expect(prompt).toBeVisible()
            expect(posts).toBe(0)
            expect(await snapshot(batch.id)).toEqual(before)
          }
          await prompt.locator('input').fill(comment + character)
          await prompt.getByRole('button', { name: '确认', exact: true }).click()
          await expect(prompt).toContainText(`${action === 'cancel' ? '取消原因' : '操作说明'}最多 2000 字`)
          await expect(prompt.locator('input')).toHaveValue(comment + character)
          expect(posts).toBe(0)
          expect(await snapshot(batch.id)).toEqual(before)
          await prompt.locator('input').fill(comment)
          const response = page.waitForResponse(r => r.url().endsWith(`${path}/${action}`) && r.request().method() === 'POST')
          await prompt.getByRole('button', { name: '确认', exact: true }).click()
          const changed = (await body(await response)).item
          expect(changed.status).toBe(statuses[action])
          await expect(prompt).not.toBeVisible()
          await expect(row.getByText(statusLabels[action], { exact: true })).toBeVisible()
          expect(posts).toBe(1)
        } finally { page.off('request', countPost) }
        const after = await snapshot(batch.id)
        const events = after.events.items.filter(event => event.action === action)
        expect(after.events.total).toBe(before.events.total + 1)
        expect(events).toHaveLength(1)
        expect(events[0].comment).toBe(comment)
        expect(events[0].actor_name).toBe((evidence.security_operator as { display_name: string }).display_name)
        if (action === terminal) {
          expect(after.item[action === 'complete' ? 'completed_at' : 'cancelled_at']).toBeTruthy()
          for (const rejected of actions) {
            expect((await body(await operator.request.post(`${path}/${rejected}`, { data: { comment: '终态不可重复变更' } }), 409)).error.code).toBe('invalid_state')
            expect(await snapshot(batch.id)).toEqual(after)
          }
          completed.push({ terminal, batch_id: batch.id, after })
          await page.screenshot({ path: testInfo.outputPath(`${language}-${terminal}.png`), fullPage: true })
        }
      }
    }
    expect(s.pageErrors).toEqual([])
    expect(s.serverErrors).toEqual([])
  })
}

test('安全测试操作说明API字段提示准确，取消必填，其他三种操作允许留空', async ({ batches: s }) => {
  const batch = await s.createBatch('API字段提示')
  const path = `/api/v1/security-test-batches/${batch.id}`
  const initial = await s.snapshot(batch.id)
  const invalid: unknown[] = []
  s.evidence.invalid = invalid
  for (const action of actions) {
    expect((await body(await s.child.request.post(`${path}/${action}`, { data: { comment: '   ' } }), 403)).error.code).toBe('forbidden')
    expect(await s.snapshot(batch.id)).toEqual(initial)
    const error = (await body(await s.operator.request.post(`${path}/${action}`, { data: { comment: '测'.repeat(2001) } }), 400)).error
    expect(error.code).toBe('invalid_security_test_batch')
    expect(error.message).toBe(`${action === 'cancel' ? '取消原因' : '操作说明'}最多 2000 字`)
    expect(await s.snapshot(batch.id)).toEqual(initial)
    invalid.push({ action, error })
  }
  for (const comment of ['', '   ']) {
    const error = (await body(await s.operator.request.post(`${path}/cancel`, { data: { comment } }), 400)).error
    expect(error.message).toBe('取消原因不能为空')
    expect(await s.snapshot(batch.id)).toEqual(initial)
    invalid.push({ action: 'cancel', comment, error })
  }
  for (const action of ['authorize', 'start', 'complete'] as const) {
    const item = (await body(await s.operator.request.post(`${path}/${action}`, { data: { comment: '   ' } }))).item
    expect(item.status).toBe(statuses[action])
    const snapshot = await s.snapshot(batch.id)
    const events = snapshot.events.items.filter(event => event.action === action)
    expect(events).toHaveLength(1)
    expect(events[0].comment).toBeFalsy()
  }
  s.evidence.final = await s.snapshot(batch.id)
  const cancellations: unknown[] = []
  s.evidence.earlyCancellations = cancellations
  for (const fromStatus of ['draft', 'authorized']) {
    const early = await s.createBatch(`取消_${fromStatus}`)
    const earlyPath = `/api/v1/security-test-batches/${early.id}`
    if (fromStatus === 'authorized') await body(await s.operator.request.post(`${earlyPath}/authorize`, { data: {} }))
    const before = await s.snapshot(early.id)
    expect(before.item.status).toBe(fromStatus)
    expect((await body(await s.operator.request.post(`${earlyPath}/cancel`, { data: { comment: '取消边界验收' } }))).item.status).toBe('cancelled')
    const after = await s.snapshot(early.id)
    expect(after.events.total).toBe(before.events.total + 1)
    const event = after.events.items.find(item => item.action === 'cancel')!
    expect(event.comment).toBe('取消边界验收')
    for (const action of actions) {
      expect((await body(await s.operator.request.post(`${earlyPath}/${action}`, { data: { comment: '终态不可重复变更' } }), 409)).error.code).toBe('invalid_state')
      expect(await s.snapshot(early.id)).toEqual(after)
    }
    cancellations.push({ from_status: fromStatus, batch_id: early.id, after })
  }
})
