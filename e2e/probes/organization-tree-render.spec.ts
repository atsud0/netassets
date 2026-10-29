import { expect, test, type Page } from '@playwright/test'

type Organization = {
  id: string; name: string; code: string; short_name?: string; type: string; status: string
  can_update?: boolean; can_create_child?: boolean; children?: Organization[]
}
const rowSelector = '.organization-tree-panel .el-table__body-wrapper tbody tr'

async function login(page: Page) {
  test.skip(!process.env.E2E_PASSWORD, 'E2E_PASSWORD 未配置')
  const response = await page.request.post('/api/v1/auth/login', {
    data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD },
  })
  expect(response.status()).toBe(200)
}
function flatten(items: Organization[]): Organization[] {
  return items.flatMap(item => [item, ...flatten(item.children ?? [])])
}

test('合成5003节点：真实表格DOM分页有界、搜索深层路径、折叠恢复，无额外请求或写入', async ({ page }) => {
  test.setTimeout(90_000)
  await login(page)
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  let reads = 0
  let writes = 0
  page.on('request', request => {
    if (request.url().includes('/api/v1/organizations') && !['GET', 'HEAD'].includes(request.method())) writes++
  })
  const children: Organization[] = Array.from({ length: 5000 }, (_, i) => ({
    id: `synthetic-${i}`, code: `SYNTHETIC_${i}`, name: `合成组织_${i}`, type: 'subsidiary', status: 'enabled',
    can_update: true, can_create_child: true,
  }))
  children[4999]!.children = [{ id: 'deep', code: 'SYNTHETIC_DEEP', name: '深层组织', type: 'department', status: 'enabled', children: [
    { id: 'leaf', code: 'SYNTHETIC_LEAF', name: '深层末级组织', short_name: '深层检索简称', type: 'department', status: 'enabled' },
  ] }]
  await page.route('**/api/v1/organizations/tree', route => {
    reads++
    return route.fulfill({ json: { total: 5003, items: [{
      id: 'synthetic-root', name: '合成集团', code: 'SYNTHETIC_ROOT', type: 'group', status: 'enabled',
      can_update: true, can_create_child: true, children,
    }] } })
  })
  await page.goto('/organizations')
  const rows = page.locator(rowSelector)
  await expect(rows).toHaveCount(50)
  await expect(page.getByText('5003 个组织节点', { exact: true })).toBeVisible()
  await page.locator('.organization-tree-panel .el-pagination__jump input').fill('101')
  await page.locator('.organization-tree-panel .el-pagination__jump input').press('Enter')
  await expect(rows).toHaveCount(3)
  await expect(rows.first().locator('strong')).toHaveText('合成组织_4999')
  const search = page.getByRole('textbox', { name: '搜索组织', exact: true })
  await search.fill(' 深层检索简称 ')
  await expect(rows).toHaveCount(4)
  await expect(rows.locator('strong')).toHaveText(['合成集团', '合成组织_4999', '深层组织', '深层末级组织'])
  await expect(rows.last().locator('.organization-tree-path')).toHaveText('合成集团 / 合成组织_4999 / 深层组织')
  await search.clear()
  await expect(rows).toHaveCount(50)
  await page.getByRole('button', { name: '收起全部', exact: true }).click()
  await expect(rows).toHaveCount(1)
  await search.fill('synthetic_leaf')
  await expect(rows).toHaveCount(4)
  await search.clear()
  await expect(rows).toHaveCount(1)
  await page.getByRole('button', { name: '展开：合成集团', exact: true }).click()
  await expect(rows).toHaveCount(50)
  await page.getByRole('button', { name: '展开全部', exact: true }).click()
  await expect(rows).toHaveCount(50)
  expect(reads).toBe(1)
  expect(writes).toBe(0)
  expect(errors).toEqual([])
})

test('真实集团授权组织树：首屏DOM有界、末尾节点可按编码定位且不重取完整树', async ({ page }) => {
  test.setTimeout(90_000)
  await login(page)
  let reads = 0
  let items: Organization[] = []
  page.on('response', async response => {
    if (response.url().endsWith('/api/v1/organizations/tree') && response.request().method() === 'GET' && response.ok()) {
      reads++
      items = (await response.json()).items
    }
  })
  const response = page.waitForResponse(response => response.url().endsWith('/api/v1/organizations/tree') && response.ok())
  await page.goto('/organizations')
  await response
  await expect.poll(() => items.length).toBeGreaterThan(0)
  const nodes = flatten(items)
  const rows = page.locator(rowSelector)
  await expect(rows).toHaveCount(Math.min(50, nodes.length))
  const last = nodes.at(-1)!
  await page.getByRole('textbox', { name: '搜索组织', exact: true }).fill(last.code)
  await expect(rows.locator('strong', { hasText: last.name }).filter({ hasText: last.name })).not.toHaveCount(0)
  expect(await rows.count()).toBeLessThanOrEqual(50)
  expect(reads).toBe(1)
})
