import { expect, test } from '@playwright/test'
import { writeFile } from 'node:fs/promises'

test('修复任务桌面紧凑布局仍完整显示不可执行原因和处理建议', async ({ page }, info) => {
  test.setTimeout(90_000)
  expect((await page.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } })).status()).toBe(200)
  // Explicit visual fixtures, not substitutes for the separate real-role task
  // workflow tests. Long actionable messages must wrap rather than be clipped.
  const cases = [
    ['system_unavailable', '业务系统已归档或已删除，任务暂不可执行', '业务系统重新启用后任务自动恢复；如需继续整改请联系集团安全管理员改派。'],
    ['organization_mismatch', '漏洞责任组织已变更，任务归属失效', '请联系集团安全管理员改派任务。'],
    ['authorization_expired', '当前人员授权已过期、被停用或超出有效期', '请联系集团安全管理员恢复授权，或安排改派。'],
  ]
  const items = cases.map(([reason], index) => ({
    id: `compact-task-${index}`, task_no: `RT-COMPACT-${index}`, title: `紧凑任务布局_${index}`,
    organization_name: '布局回归责任组织', system_name: '布局回归业务系统', assignee_name: '回归负责人', vendor_name: '隔离回归厂商',
    status: 'in_progress', severity: 'high', execution_blocked_reason: reason, due_at: '2026-10-28T08:00:00Z',
  }))
  const empty = { items: [], total: 0, page: 1, page_size: 100 }
  await page.route('**/api/v1/security-work-orders?*', route => route.fulfill({ json: empty }))
  await page.route('**/api/v1/remediation-options?*', route => route.fulfill({ json: empty }))
  await page.route('**/api/v1/remediation-tasks?*', route => route.fulfill({ json: { ...empty, items, total: items.length } }))
  await page.route('**/api/v1/remediation-tasks/statistics?*', route => route.fulfill({ json: {
    total: items.length, counts: { in_progress: items.length }, block_reasons: {}, workload: [],
    verified_fixed: 0, risk_accepted: 0, risk_expired: 0, false_positive: 0, overdue: 0, extended: 0, revived: 0,
    execution_seconds: 0, organization_seconds: 0, security_seconds: 0,
  } }))
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  await page.goto('/remediation')
  const table = page.locator('.repair-list-panel .el-table')
  await expect(table.locator('.el-table__row')).toHaveCount(items.length)
  const evidence: unknown[] = []
  for (const width of [1280, 1440]) {
    await page.setViewportSize({ width, height: 1000 })
    await expect.poll(() => table.evaluate(element => element.querySelector('.el-table__body')!.getBoundingClientRect().width - element.clientWidth)).toBeLessThanOrEqual(1)
    for (const [index, [, reason, advice]] of cases.entries()) {
      const row = table.locator('.el-table__row').filter({ hasText: items[index].title })
      await expect(row.locator('.task-block-reason')).toHaveText(reason)
      await expect(row.locator('.task-block-advice')).toHaveText(advice)
      for (const selector of ['.task-block-reason', '.task-block-advice']) {
        const geometry = await row.locator(selector).evaluate(element => ({
          whiteSpace: getComputedStyle(element).whiteSpace,
          horizontalClipping: element.scrollWidth - element.clientWidth,
          verticalClipping: element.scrollHeight - element.clientHeight,
        }))
        expect(geometry.whiteSpace).toBe('normal')
        expect(geometry.horizontalClipping).toBeLessThanOrEqual(1)
        expect(geometry.verticalClipping).toBeLessThanOrEqual(1)
        evidence.push({ width, index, selector, ...geometry })
      }
      await expect(row.getByRole('button', { name: '查看详情', exact: true })).toBeVisible()
    }
    await page.screenshot({ path: info.outputPath(`compact-task-${width}.png`), fullPage: true })
  }
  expect(errors).toEqual([])
  await writeFile(info.outputPath('compact-task-layout.json'), JSON.stringify({ evidence, errors }, null, 2))
})
