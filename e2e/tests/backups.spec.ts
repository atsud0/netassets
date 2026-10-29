import { expect, test, type Page } from '@playwright/test'

const adminUsername = process.env.E2E_USERNAME ?? 'admin'
const adminPassword = process.env.E2E_PASSWORD
const pendingCleanup = new Set<string>()

test.afterEach(async ({ request }, info) => {
  if (!pendingCleanup.size) return
  // This hook has its own timeout and API session: a timed-out page fixture
  // must not leave operational archive files behind.
  info.setTimeout(60_000)
  const login = await request.post('/api/v1/auth/login', { data: { username: adminUsername, password: adminPassword } })
  expect(login.status()).toBe(200)
  for (const id of pendingCleanup) {
    const response = await request.delete(`/api/v1/backups/${encodeURIComponent(id)}`)
    expect([204, 404]).toContain(response.status())
    pendingCleanup.delete(id)
  }
})

async function login(page: Page, username: string, password: string) {
  await page.getByPlaceholder('请输入用户名').fill(username)
  await page.getByPlaceholder('请输入密码').fill(password)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page).not.toHaveURL(/\/login(?:\?|$)/)
}

async function openBackupActions(page: Page, row: ReturnType<Page['locator']>) {
  // A preceding action closes the teleported popover asynchronously. Wait for
  // that leave transition before reopening, then for its native focus hook
  // (after-enter), so a click cannot target the departing menu's old geometry.
  await expect(page.locator('.row-actions-panel:visible')).toHaveCount(0)
  await row.getByRole('button', { name: /操作：/ }).click()
  const panel = page.locator('.row-actions-panel:visible')
  await expect(panel).toBeVisible()
  await expect(panel.getByRole('button').first()).toBeFocused()
  return panel
}

async function downloadBackupFile(page: Page, label: string, expectedFilename: string) {
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    page.locator('.row-actions-panel:visible').getByRole('link', { name: label, exact: true }).click(),
  ])
  expect(download.suggestedFilename()).toBe(expectedFilename)
}

test('集团管理员可在 Web 端完成系统备份、校验、下载和删除', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  // pg_dump、归档与归档可读性校验均在应用容器中执行，低配 Podman 环境下需要保留充足时间。
  test.setTimeout(600_000)
  page.setDefaultTimeout(30_000)

  const e2eRequestID = `e2e-backup-${Date.now()}`
  let backupID = ''
  try {
    await page.goto('/backups')
    await expect(page).toHaveURL(/\/login\?redirect=(?:%2F|\/)backups$/)
    await login(page, adminUsername, adminPassword!)
    await expect(page).toHaveURL(/\/backups$/)
    await expect(page.getByRole('heading', { name: '备份管理' })).toBeVisible()
    // 仅标记备份动作产生的审计记录，供清理脚本精确识别，避免误清理正常运维审计。
    await page.context().setExtraHTTPHeaders({ 'X-Request-ID': e2eRequestID })

    // The scheduled scan / expiry worker legitimately holds the shared write
    // gate briefly. Only its explicit busy response may be retried; fail all
    // other errors immediately instead of waiting six minutes for a toast.
    const gateDeadline = Date.now() + 90_000
    while (!backupID) {
      await page.getByRole('button', { name: '创建备份', exact: true }).first().click()
      const createDialog = page.getByRole('dialog', { name: '创建系统备份' })
      await expect(createDialog).toBeVisible()
      const responsePromise = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/backups', { timeout: 360_000 })
      await createDialog.getByRole('button', { name: '开始备份', exact: true }).click()
      const response = await responsePromise
      const data = await response.json()
      if (response.status() === 409) {
        expect(data.error.code).toBe('backup_in_progress')
        expect(Date.now(), '后台写入持续占用备份锁超过 90 秒').toBeLessThan(gateDeadline)
        await expect(page.getByText('已有备份任务正在执行，请等待完成后再试', { exact: true }).last()).toBeVisible()
        await page.waitForTimeout(500)
        continue
      }
      expect(response.status(), JSON.stringify(data)).toBe(201)
      backupID = data.item.id
      pendingCleanup.add(backupID)
    }
    await expect(page.getByText('系统备份已创建并完成校验', { exact: true })).toBeVisible()

    expect(backupID).toMatch(/^netassets-[0-9]{8}T[0-9]{6}Z-[a-f0-9]{8}$/)

    // 操作列使用 RowActions 菜单；按备份编号锁定真实行，避免分页或固定列副本误选。
    const actionRow = page.locator('.backup-table tbody tr').filter({ hasText: backupID }).first()
    await expect(actionRow).toBeVisible()
    let actions = await openBackupActions(page, actionRow)
    await actions.getByRole('button', { name: '校验', exact: true }).click()
    await expect(page.getByText('备份文件校验通过', { exact: true })).toBeVisible({ timeout: 180_000 })

    const expectedFiles: Array<[string, string]> = [
      ['数据库备份', `${backupID}-database.dump`],
      ['附件归档', `${backupID}-app-data.tar.gz`],
      ['校验清单', `${backupID}-manifest.txt`],
    ]
    for (const [label, filename] of expectedFiles) {
      actions = await openBackupActions(page, actionRow)
      await downloadBackupFile(page, label, filename)
    }

    actions = await openBackupActions(page, actionRow)
    await actions.getByRole('button', { name: '删除', exact: true }).click()
    const deleteDialog = page.getByRole('dialog', { name: '删除系统备份' })
    await expect(deleteDialog).toBeVisible()
    await deleteDialog.getByRole('button', { name: '永久删除', exact: true }).click()
    await expect(page.getByText('备份已删除', { exact: true })).toBeVisible()
    await expect.poll(async () => (await page.locator('code.backup-id').allTextContents()).map((id) => id.trim())).not.toContain(backupID)
    pendingCleanup.delete(backupID)
    backupID = ''
  } finally {
    // afterEach uses an independent authenticated API fixture even if this
    // browser test has exhausted its timeout. Only this test's exact ID is used.
    if (backupID) pendingCleanup.add(backupID)
  }
})
