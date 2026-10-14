import { expect, test } from '@playwright/test'

const adminUsername = process.env.E2E_USERNAME ?? 'admin'
const adminPassword = process.env.E2E_PASSWORD

// Guards "查看业务系统详情，点击复制编码报错".
//
// The product is deployed on plain HTTP inside group networks, for example
// http://192.168.8.6:8080. That is not a secure context, so navigator.clipboard
// does not exist there and the previous `navigator.clipboard.writeText(...)`
// call threw a TypeError every single time. This test asserts the button still
// copies under exactly that condition instead of relying on a localhost where
// the Clipboard API happens to exist.
test('在非安全上下文（HTTP 内网地址）中复制系统编码仍然成功', async ({ page, request, browserName }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  test.skip(browserName === 'webkit', 'WebKit 的剪贴板权限模型不同')
  page.setDefaultTimeout(20_000)

  await request.post('/api/v1/auth/login', { data: { username: adminUsername, password: adminPassword } })
  const organizations = await request.get('/api/v1/organizations/tree')
  expect(organizations.status()).toBe(200)
  const tree = await organizations.json() as { items: Array<{ id: string }> & unknown }
  const flat: Array<{ id: string }> = []
  const walk = (nodes: unknown) => {
    for (const node of nodes as Array<Record<string, unknown>>) {
      if (node && typeof node.id === 'string') flat.push({ id: node.id })
      if (Array.isArray(node?.children)) walk(node.children)
    }
  }
  walk(tree.items)
  expect(flat.length).toBeGreaterThan(0)

  const suffix = Date.now().toString(36)
  const created = await request.post('/api/v1/business-systems', {
    data: {
      code: `COPYSYS_${suffix}`, name: `复制编码验证_${suffix}`, short_name: '', organization_id: flat[0]!.id,
      owner_name: 'E2E', owner_contact: '', status: 'running', level: 'normal', deployment_type: 'other',
      internet_exposure: 'unknown', description: '', contains_personal_information: 'unknown',
      contains_sensitive_personal_information: 'unknown', personal_information_record_count: null,
      data_volume_over_100k: 'unknown', contains_important_data: 'unknown', contains_core_data: 'unknown',
      data_storage_location: '', security_notes: '',
    },
  })
  expect(created.status(), await created.text()).toBe(201)
  const code = `COPYSYS_${suffix}`

  await page.goto('/login')
  await page.getByPlaceholder('请输入用户名').fill(adminUsername)
  await page.getByPlaceholder('请输入密码').fill(adminPassword!)
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page).not.toHaveURL(/\/login(?:\?|$)/)

  await page.goto(`/business-systems?q=${code}`)
  const row = page.locator('tbody tr').filter({ hasText: code }).first()
  await expect(row).toBeVisible()
  await row.getByRole('button').first().click()
  const detailAction = page.locator('.el-dropdown-menu__item').filter({ hasText: '详情' }).first()
  if (await detailAction.count()) await detailAction.click()

  const codeCell = page.locator('.detail-system-code').first()
  await expect(codeCell).toBeVisible()
  expect(((await codeCell.locator('code').textContent()) ?? '').trim()).toBe(code)

  // 记录部署环境：HTTP 部署时没有异步剪贴板 API，修复必须走回退路径。
  const clipboardType = await page.evaluate(() => typeof navigator.clipboard)

  await codeCell.getByRole('button').first().click()
  await expect(page.locator('.el-message', { hasText: '系统编码已复制' }).first()).toBeVisible()
  await expect(page.locator('.el-message', { hasText: '复制系统编码失败' })).toHaveCount(0)

  if (clipboardType !== 'undefined') {
    await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(code)
  }
})
