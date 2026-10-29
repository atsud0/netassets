import { expect, type Page } from '@playwright/test'

/** Personal views have a real 20-view quota. Do not consume or delete the shared
 * bootstrap admin's views: use a fresh real group administrator for each test. */
export async function loginSavedViewOwner(page: Page) {
  const login = await page.request.post('/api/v1/auth/login', {
    data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD },
  })
  expect(login.status()).toBe(200)
  const rolesResponse = await page.request.get('/api/v1/roles?page=1&page_size=100')
  expect(rolesResponse.status()).toBe(200)
  const roles = (await rolesResponse.json()).items as { id: string; key: string }[]
  const role = roles.find(item => item.key === 'group_admin')
  expect(role).toBeTruthy()
  const username = `view_owner_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`
  const password = 'ViewRegression123!'
  const created = await page.request.post('/api/v1/users', { data: {
    username, password, display_name: '个人视图隔离回归', global_role_ids: [role!.id], appointments: [],
  } })
  expect(created.status()).toBe(201)
  const ownerLogin = await page.request.post('/api/v1/auth/login', { data: { username, password } })
  expect(ownerLogin.status()).toBe(200)
  const { user } = await ownerLogin.json()
  expect(user.roles).toEqual(['group_admin'])
  if (user.must_change_password) {
    const changed = await page.request.put('/api/v1/auth/profile/password', { data: {
      current_password: password, new_password: `${password}Changed`,
    } })
    expect(changed.status()).toBe(204)
  }
  const views = await page.request.get('/api/v1/saved-filter-views?page_key=assets')
  expect(views.status()).toBe(200)
  expect((await views.json()).items).toEqual([])
}
