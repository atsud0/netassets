import { expect, test, type Page } from '@playwright/test'

const adminUsername = process.env.E2E_USERNAME ?? 'admin'
const adminPassword = process.env.E2E_PASSWORD

async function login(page: Page, username: string, password: string) {
  await page.getByPlaceholder('请输入用户名').fill(username)
  await page.getByPlaceholder('请输入密码').fill(password)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page).not.toHaveURL(/\/login(?:\?|$)/)
}

test('应用壳层固定导航，安全测试页面使用统一台账布局', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')

  await page.goto('/security-test-batches')
  await expect(page).toHaveURL(/\/login\?redirect=(?:%2F|\/)security-test-batches$/)
  await login(page, adminUsername, adminPassword!)
  await expect(page).toHaveURL(/\/security-test-batches$/)
  await expect(page.getByRole('heading', { name: '安全测试', exact: true })).toBeVisible()

  await expect(page.locator('.organization-summary > div')).toHaveCount(4)
  await expect(page.locator('.panel.security-test-filter-panel')).toBeVisible()
  await expect(page.locator('.panel.data-table-panel')).toBeVisible()

  const layout = await page.evaluate(() => {
    const shell = document.querySelector<HTMLElement>('.app-shell')
    const sidebar = document.querySelector<HTMLElement>('.sidebar')
    const content = document.querySelector<HTMLElement>('.page-content')
    if (!shell || !sidebar || !content) throw new Error('应用布局节点不存在')
    return {
      viewportHeight: window.innerHeight,
      shellHeight: shell.getBoundingClientRect().height,
      sidebarHeight: sidebar.getBoundingClientRect().height,
      contentOverflowY: getComputedStyle(content).overflowY,
    }
  })
  expect(layout.shellHeight).toBe(layout.viewportHeight)
  expect(layout.sidebarHeight).toBe(layout.viewportHeight)
  expect(layout.contentOverflowY).toBe('auto')

  const sidebarTopBeforeScroll = await page.locator('.sidebar').evaluate((element) => element.getBoundingClientRect().top)
  await page.evaluate(() => {
    const content = document.querySelector<HTMLElement>('.page-content')
    if (!content) throw new Error('内容区不存在')
    const spacer = document.createElement('div')
    spacer.dataset.testid = 'layout-scroll-spacer'
    spacer.style.height = '120vh'
    spacer.style.pointerEvents = 'none'
    content.append(spacer)
    content.scrollTop = 200
  })
  await expect.poll(() => page.locator('.page-content').evaluate((element) => element.scrollTop)).toBeGreaterThan(0)
  const sidebarTopAfterScroll = await page.locator('.sidebar').evaluate((element) => element.getBoundingClientRect().top)
  expect(sidebarTopAfterScroll).toBe(sidebarTopBeforeScroll)
  await page.locator('[data-testid="layout-scroll-spacer"]').evaluate((element) => element.remove())
})

test('左侧导航按业务分组，消息中心仅保留在顶栏', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')

  await page.goto('/security-test-batches')
  await login(page, adminUsername, adminPassword!)

  const navigation = page.getByRole('navigation', { name: '主导航' })
  await expect(navigation.getByRole('button', { name: '集团工作台', exact: true })).toBeVisible()
  await expect(navigation.getByRole('button', { name: '资产治理', exact: true })).toBeVisible()
  await expect(navigation.getByRole('button', { name: '安全运营', exact: true })).toBeVisible()
  await expect(navigation.getByRole('button', { name: '系统设置', exact: true })).toBeVisible()

  await expect(navigation.getByRole('button', { name: '安全测试', exact: true })).toBeVisible()
  await expect(navigation.getByRole('button', { name: '安全工单', exact: true })).toBeVisible()
  await expect(navigation.getByRole('button', { name: '组织架构', exact: true })).toBeVisible()
  await expect(navigation.getByRole('button', { name: '用户和权限', exact: true })).toBeVisible()
  await expect(navigation.getByRole('button', { name: '回收站', exact: true })).toBeVisible()
  await expect(navigation.getByRole('button', { name: '审计日志', exact: true })).toBeVisible()
  await expect(navigation.getByRole('button', { name: '备份管理', exact: true })).toBeVisible()

  await expect(navigation.getByText('消息中心', { exact: true })).toHaveCount(0)
  await expect(page.locator('.topbar').getByText('消息中心', { exact: true })).toBeVisible()

  const systemSettings = navigation.getByRole('button', { name: '系统设置', exact: true })
  await systemSettings.click()
  await expect(navigation.getByRole('button', { name: '组织架构', exact: true })).toBeHidden()
  await systemSettings.click()
  await expect(navigation.getByRole('button', { name: '组织架构', exact: true })).toBeVisible()
})
