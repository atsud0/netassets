import { expect, test } from '@playwright/test'

const adminUsername = process.env.E2E_USERNAME ?? 'admin'
const adminPassword = process.env.E2E_PASSWORD

test('回车登录只提示一次且只发送一次登录请求', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  page.setDefaultTimeout(15_000)

  let loginRequests = 0
  page.on('request', request => {
    if (request.method() === 'POST' && new URL(request.url()).pathname === '/api/v1/auth/login') {
      loginRequests++
    }
  })

  await page.goto('/login')
  const username = page.getByPlaceholder('请输入用户名')
  await username.fill(adminUsername)

  // 只输用户名时按回车：原生表单提交应只触发一次校验提示，而不是两条。
  await username.press('Enter')
  const warnings = page.locator('.el-message', { hasText: '请输入用户名和密码' })
  await expect(warnings.first()).toBeVisible()
  await expect(page.locator('.el-message')).toHaveCount(1)
  await expect(page).toHaveURL(/\/login(?:\?|$)/)
  expect(loginRequests).toBe(0)

  // 补齐密码后回车：只发送一次 /login 请求并完成登录。
  const password = page.getByPlaceholder('请输入密码')
  await password.fill(adminPassword!)
  await password.press('Enter')
  await expect(page).not.toHaveURL(/\/login(?:\?|$)/)
  expect(loginRequests).toBe(1)
})