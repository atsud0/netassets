import { expect, test, type APIResponse, type Browser, type BrowserContext } from '@playwright/test'

const adminUsername = process.env.E2E_USERNAME ?? 'admin'
const adminPassword = process.env.E2E_PASSWORD
const temporaryPassword = 'ExportIsolation123!'

type CurrentUser = { id: string; must_change_password: boolean }
type ExportJob = {
  id: string
  resource_type: string
  status: 'queued' | 'running' | 'completed' | 'failed' | 'expired'
  total_rows: number
  processed_rows: number
  file_name?: string
  file_size: number
  expires_at?: string
  error_message?: string
}

async function expectStatus(response: APIResponse, expected: number, label: string) {
  if (response.status() !== expected) {
    throw new Error(`${label}: HTTP ${response.status()}，预期 ${expected}；响应：${await response.text()}`)
  }
}

async function loginContext(browser: Browser, baseURL: string, username: string, password: string, changeTemporaryPassword = false) {
  const context = await browser.newContext({ baseURL })
  const response = await context.request.post('/api/v1/auth/login', { data: { username, password } })
  await expectStatus(response, 200, `${username} 登录`)
  const payload = await response.json() as { user: CurrentUser }
  if (changeTemporaryPassword || payload.user.must_change_password) {
    const changedPassword = `${password}Changed`
    const change = await context.request.put('/api/v1/auth/profile/password', {
      data: { current_password: password, new_password: changedPassword },
    })
    await expectStatus(change, 204, `${username} 修改临时密码`)
  }
  return context
}

async function waitForCompletion(context: BrowserContext, id: string) {
  const deadline = Date.now() + 90_000
  while (Date.now() < deadline) {
    const response = await context.request.get(`/api/v1/export-jobs/${id}`)
    await expectStatus(response, 200, '轮询后台导出任务')
    const item = (await response.json() as { item: ExportJob }).item
    if (item.status === 'completed') return item
    if (item.status === 'failed' || item.status === 'expired') {
      throw new Error(`后台导出任务异常结束：${JSON.stringify(item)}`)
    }
    await new Promise((resolve) => setTimeout(resolve, 500))
  }
  throw new Error('后台导出任务在 90 秒内未完成')
}

test('后台 XLSX 导出可完成、下载、通知且仅申请人可见', async ({ browser }, testInfo) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  test.setTimeout(180_000)

  const baseURL = String(testInfo.project.use.baseURL ?? 'http://127.0.0.1:8080')
  const suffix = Date.now().toString()
  const otherUsername = `export_isolation_${suffix}`
  const admin = await loginContext(browser, baseURL, adminUsername, adminPassword!)
  const contexts: BrowserContext[] = [admin]

  try {
    const create = await admin.request.post('/api/v1/export-jobs', {
      data: { resource_type: 'asset', format: 'xlsx', filter: {} },
    })
    await expectStatus(create, 202, '创建网络资产后台导出任务')
    const queued = (await create.json() as { item: ExportJob }).item
    expect(queued.id).toMatch(/^[0-9a-f-]{36}$/i)
    expect(['queued', 'running', 'completed']).toContain(queued.status)

    const rolesResponse = await admin.request.get('/api/v1/roles?page=1&page_size=100')
    await expectStatus(rolesResponse, 200, '读取角色用于越权隔离测试')
    const roles = (await rolesResponse.json() as { items: Array<{ id: string; key: string }> }).items
    const securityRole = roles.find((role) => role.key === 'security_operator')
    expect(securityRole, '缺少 security_operator 内置角色').toBeTruthy()

    const userResponse = await admin.request.post('/api/v1/users', {
      data: {
        username: otherUsername,
        password: temporaryPassword,
        display_name: '导出隔离测试用户',
        email: `${otherUsername}@example.invalid`,
        mobile_phone: '',
        office_phone: '',
        global_role_ids: [securityRole!.id],
        appointments: [],
      },
    })
    await expectStatus(userResponse, 201, '创建导出隔离测试用户')
    const other = await loginContext(browser, baseURL, otherUsername, temporaryPassword, true)
    contexts.push(other)

    const hidden = await other.request.get(`/api/v1/export-jobs/${queued.id}`)
    await expectStatus(hidden, 404, '其他用户不能查看申请人的导出任务')
    const hiddenDownload = await other.request.get(`/api/v1/export-jobs/${queued.id}/download`)
    await expectStatus(hiddenDownload, 404, '其他用户不能下载申请人的导出文件')

    const completed = await waitForCompletion(admin, queued.id)
    expect(completed.processed_rows).toBe(completed.total_rows)
    expect(completed.file_name).toMatch(/\.xlsx$/)
    expect(completed.file_size).toBeGreaterThan(0)
    expect(Date.parse(completed.expires_at ?? '')).toBeGreaterThan(Date.now() + 6 * 24 * 60 * 60 * 1000)

    const download = await admin.request.get(`/api/v1/export-jobs/${queued.id}/download`)
    await expectStatus(download, 200, '申请人下载后台 XLSX')
    expect(download.headers()['content-type']).toContain('spreadsheetml.sheet')
    expect((await download.body()).byteLength).toBeGreaterThan(100)

    const notifications = await admin.request.get('/api/v1/notifications?page=1&page_size=100&category=export_job')
    await expectStatus(notifications, 200, '读取后台导出完成消息')
    const notificationItems = (await notifications.json() as { items: Array<{ resource_id: string; category: string; action_path: string }> }).items
    expect(notificationItems.some((item) => item.category === 'export_job' && item.resource_id === queued.id && item.action_path.includes(queued.id))).toBeTruthy()

    const remove = await admin.request.delete(`/api/v1/export-jobs/${queued.id}`)
    await expectStatus(remove, 204, '删除已完成后台导出任务')
    const removed = await admin.request.get(`/api/v1/export-jobs/${queued.id}`)
    await expectStatus(removed, 404, '删除后任务不可再读取')
  } finally {
    await Promise.all(contexts.map((context) => context.close()))
  }
})
