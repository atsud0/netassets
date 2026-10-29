import { expect, type APIRequestContext, type Locator, type Page } from '@playwright/test'

/** Locate fixture records in the real paginated API, without assuming a fresh database. */
export async function findRecordPage<T>(request: APIRequestContext, path: string, match: (item: T) => boolean, pageSize = 20) {
  for (let page = 1; ; page += 1) {
    const query = new URLSearchParams(path.split('?')[1])
    query.set('page', String(page))
    query.set('page_size', String(pageSize))
    const response = await request.get(`${path.split('?')[0]}?${query}`)
    expect(response.status(), `读取 ${path} 第 ${page} 页`).toBe(200)
    const body = await response.json() as { items: T[]; total: number }
    const item = body.items.find(match)
    if (item) return { item, page }
    if (page * pageSize >= body.total || body.items.length === 0) return undefined
  }
}

/** Exercise the actual pagination controls, not a mocked list or hidden DOM. */
export async function showRecordPage(page: Page, path: string, id: string, pagination: Locator) {
  const found = await findRecordPage<{ id: string }>(page.request, path, item => item.id === id)
  expect(found, `${path} 中应存在测试记录 ${id}`).toBeTruthy()
  for (let target = 2; target <= found!.page; target += 1) {
    const loaded = page.waitForResponse(response => {
      const url = new URL(response.url())
      return url.pathname === path.split('?')[0] && url.searchParams.get('page') === String(target) && response.request().method() === 'GET'
    })
    await pagination.locator('.btn-next').click()
    expect((await loaded).status()).toBe(200)
    await expect(pagination.locator('.number.is-active')).toHaveText(String(target))
  }
}
