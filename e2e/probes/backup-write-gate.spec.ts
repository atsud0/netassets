import { expect, test, type APIResponse, type Response } from '@playwright/test'
import { writeFile } from 'node:fs/promises'

async function json(response: APIResponse | Response, status: number) {
  expect(response.status()).toBe(status)
  return response.json()
}

test('真实 Web 备份期间写请求维护拒绝、读取可用，完成后恢复且仅清理本次备份', async ({ page }, info) => {
  test.skip(!process.env.E2E_PASSWORD, 'E2E_PASSWORD 未配置')
  test.setTimeout(600_000)
  const stamp = `${Date.now()}_${Math.random().toString(36).slice(2, 7)}`
  const request = page.context().request
  await json(await request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }), 200)
  await page.context().setExtraHTTPHeaders({ 'X-Request-ID': `backup-gate-${stamp}` })
  const roots = await json(await request.get('/api/v1/organizations?page=1&page_size=100'), 200)
  const root = roots.items.find((item: { type: string }) => item.type === 'group')
  expect(root).toBeTruthy()
  const org = (await json(await request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `BACKUP_GATE_${stamp}`, name: `真实备份写门禁_${stamp}`, type: 'subsidiary' } }), 201)).item
  const value = `backup-gate-${stamp.replaceAll('_', '-')}.example.com`
  const evidence: Record<string, unknown> = { organization_id: org.id, value, window_statuses: [], checks: [] }
  let backup: { status: number; item?: { id: string }; error?: { code: string } } | undefined
  let result: Promise<void> | undefined
  let secondCreation: Promise<APIResponse> | undefined
  const ownBackupIDs = new Set<string>()
  let completed = false
  let responseError: unknown
  const started = Date.now()
  try {
    await page.goto('/backups')
    await expect(page.getByRole('heading', { name: '备份管理' })).toBeVisible()
    await page.getByRole('button', { name: '创建备份', exact: true }).first().click()
    const dialog = page.getByRole('dialog', { name: '创建系统备份' })
    await expect(dialog).toBeVisible()
    // Do not stub pg_dump or delay the application: this is the actual archive.
    // Always join the pending response before cleanup, even if an assertion fails.
    result = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/backups', { timeout: 360_000 })
      .then(async response => { backup = { ...(await response.json()), status: response.status() } })
      .catch(error => { responseError = error })
      .finally(() => { completed = true })
    const issued = page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname === '/api/v1/backups')
    await dialog.getByRole('button', { name: '开始备份', exact: true }).click()
    await issued
    let observedMaintenance = false
    while (!completed && Date.now() - started < 360_000) {
      // Invalid data cannot mutate the database before the backup acquires its gate.
      const probe = await request.post('/api/v1/assets', { data: {} })
      const data = await probe.json()
      ;(evidence.window_statuses as number[]).push(probe.status())
      if (probe.status() === 503) {
        expect(data.error.code).toBe('backup_in_progress')
        observedMaintenance = true
        break
      }
      expect(probe.status(), '等待真实维护窗口时只允许正常字段拒绝').toBe(400)
      await page.waitForTimeout(20)
    }
    expect(observedMaintenance, '真实备份必须出现 API 写维护门禁；若备份已结束而未观察到，不能当作已验证').toBe(true)
    // Run the checks together within the observed real backup window.
    secondCreation = request.post('/api/v1/backups').then(async response => {
      // A failed guard must not leave a second archive owned by this probe.
      if (response.status() === 201) {
        const data = await response.json()
        if (data.item?.id) ownBackupIDs.add(data.item.id)
      }
      return response
    })
    const checks = await Promise.all([
      request.post('/api/v1/assets', { data: { type: 'domain', value, organization_id: org.id, environment: 'testing', network_scope: 'public', status: 'active', importance: 'normal', direct_change_reason: '真实备份期间应拒绝写入' } }),
      request.put('/api/v1/auth/profile', { data: {} }),
      request.patch('/api/v1/security-findings/11111111-1111-4111-8111-111111111111', { data: {} }),
      request.delete('/api/v1/backups/not-a-backup'),
      request.get('/api/v1/data-quality-rules/scan'),
      request.get('/api/v1/backups?page=1&page_size=100'),
      request.get('/api/v1/auth/me'),
      request.get('/health/live'),
      secondCreation,
    ])
    evidence.checks = checks.map(response => ({ path: new URL(response.url()).pathname, status: response.status() }))
    for (const response of checks.slice(0, 5)) expect((await json(response, 503)).error.code).toBe('backup_in_progress')
    for (const response of checks.slice(5, 8)) expect(response.status()).toBe(200)
    expect((await json(checks[8]!, 409)).error.code).toBe('backup_in_progress')
    await result
    if (responseError) throw responseError
    expect(backup?.status).toBe(201)
    expect(backup?.item?.id).toMatch(/^netassets-[0-9]{8}T[0-9]{6}Z-[a-f0-9]{8}$/)
    await expect(page.getByText('系统备份已创建并完成校验', { exact: true })).toBeVisible()
    await json(await request.get(`/api/v1/backups/${backup!.item!.id}/verify`), 200)
    const assets = await json(await request.get(`/api/v1/assets?q=${encodeURIComponent(value)}&page=1&page_size=100`), 200)
    expect(assets.items.filter((item: { value: string }) => item.value === value)).toHaveLength(0)
    expect((await request.post('/api/v1/assets', { data: {} })).status()).toBe(400)
    evidence.recovered = true
  } finally {
    await result
    await secondCreation?.catch(() => {})
    Object.assign(evidence, { backup_id: backup?.item?.id, backup_status: backup?.status, elapsed_ms: Date.now() - started })
    if (backup?.item?.id) ownBackupIDs.add(backup.item.id)
    const cleanup: Array<{ id: string; status: number; verify_status: number }> = []
    try {
      // Never enumerate/delete pre-existing archives, including on a failed check.
      for (const id of ownBackupIDs) {
        const deleted = await request.delete(`/api/v1/backups/${id}`)
        const verified = await request.get(`/api/v1/backups/${id}/verify`)
        cleanup.push({ id, status: deleted.status(), verify_status: verified.status() })
      }
      expect(cleanup.every(item => item.status === 204 && item.verify_status === 404)).toBe(true)
    } finally {
      evidence.cleanup = cleanup
      await writeFile(info.outputPath('backup-gate-evidence.json'), JSON.stringify(evidence, null, 2))
    }
  }
})
