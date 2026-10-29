import { expect, test, type APIResponse, type BrowserContext, type Locator, type Page } from '@playwright/test'

type Organization = { id: string; name: string; short_name?: string; type: string; children?: Organization[] }
type System = { id: string; name: string; version: number; status: string }
const suffix = `${Date.now()}_${Math.random().toString(36).slice(2, 6)}`
let group: BrowserContext
let child: BrowserContext
let organizations: Organization[]
let records: Record<string, string[]> = {}
const contexts: BrowserContext[] = []

async function body(response: Pick<APIResponse, 'text' | 'status'>, status: number) {
  const text = await response.text()
  expect(response.status(), text).toBe(status)
  return text ? JSON.parse(text) : null
}
function flatten(items: Organization[]): Organization[] { return items.flatMap(item => [item, ...flatten(item.children ?? [])]) }
async function newPage(context: BrowserContext, path: string, width = 1440, height = 900) {
  const page = await context.newPage()
  await page.setViewportSize({ width, height })
  await page.goto(path)
  return page
}
async function dropdown(page: Page, input: Locator) {
  await input.click()
  const popup = page.locator('.el-select__popper:visible')
  await expect(popup).toHaveCount(1)
  return popup
}
async function expectEllipsis(locator: Locator) {
  await expect(locator).toBeVisible()
  const geometry = await locator.evaluate(element => {
    const style = getComputedStyle(element)
    const rect = element.getBoundingClientRect()
    const cell = element.closest('td')!.getBoundingClientRect()
    return { client: element.clientWidth, scroll: element.scrollWidth, overflow: style.overflow, ellipsis: style.textOverflow, nowrap: style.whiteSpace, right: rect.right, cellRight: cell.right }
  })
  expect(geometry.client, JSON.stringify(geometry)).toBeGreaterThan(0)
  expect(geometry.scroll, JSON.stringify(geometry)).toBeGreaterThan(geometry.client)
  expect(geometry).toMatchObject({ overflow: 'hidden', ellipsis: 'ellipsis', nowrap: 'nowrap' })
  expect(geometry.right).toBeLessThanOrEqual(geometry.cellRight + 1)
}

test.beforeAll(async ({ browser, baseURL }) => {
  test.setTimeout(60_000)
  test.skip(!process.env.E2E_PASSWORD, 'E2E_PASSWORD 未配置')
  group = await browser.newContext({ baseURL })
  contexts.push(group)
  await body(await group.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME ?? 'admin', password: process.env.E2E_PASSWORD } }), 200)
  const tree = await body(await group.request.get('/api/v1/organizations/tree'), 200)
  const root = flatten(tree.items).find(item => item.type === 'group')!
  expect(root).toBeTruthy()
  organizations = []
  records = { 'business-systems': [], assets: [] }
  for (let index = 0; index < 2; index++) {
    const prefix = `长组织_${suffix}_${index}_`
    const name = prefix + '名称超长显示完整信息'.repeat(30).slice(0, 255 - prefix.length)
    const shortName = `长简称_${index}_` + '简称应当省略'.repeat(22).slice(0, 128 - `长简称_${index}_`.length)
    const result = await body(await group.request.post('/api/v1/organizations', { data: { parent_id: root.id, code: `LAYOUT_${suffix}_${index}`, name, short_name: shortName, type: 'subsidiary', contact: '13800000018' } }), 201)
    organizations.push(result.item)
    const system = await body(await group.request.post('/api/v1/business-systems', { data: { code: `FILTER_${suffix}_${index}`, name: `组织筛选系统_${suffix}_${index}`, organization_id: result.item.id, status: 'running', level: 'normal', internet_exposure: 'unknown', contains_personal_information: 'unknown', contains_sensitive_personal_information: 'unknown', data_volume_over_100k: 'unknown', contains_important_data: 'unknown', contains_core_data: 'unknown' } }), 201)
    const asset = await body(await group.request.post('/api/v1/assets', { data: { type: 'domain', value: `layout-${suffix.replaceAll('_', '-')}-${index}.example.com`, name: `组织筛选资产_${index}`, organization_id: result.item.id, environment: 'production', network_scope: 'private', status: 'active', importance: 'normal', direct_change_reason: '隔离组织筛选回归' } }), 201)
    records['business-systems'].push(system.item.id)
    records.assets.push(asset.item.id)
  }
  const roles = await body(await group.request.get('/api/v1/roles?page=1&page_size=100'), 200)
  const role = roles.items.find((item: { key: string }) => item.key === 'organization_admin')
  expect(role).toBeTruthy()
  const username = `layout_admin_${suffix}`
  const password = 'LayoutRegression123!'
  await body(await group.request.post('/api/v1/users', { data: { username, password, display_name: '多组织长名称管理员', global_role_ids: [], appointments: organizations.map((organization, index) => ({ organization_id: organization.id, is_primary: index === 0, assignments: [{ role_id: role.id, data_scope: 'organization' }] })) } }), 201)
  child = await browser.newContext({ baseURL })
  contexts.push(child)
  const login = await body(await child.request.post('/api/v1/auth/login', { data: { username, password } }), 200)
  expect(login.user.roles).toContain('organization_admin')
  expect(login.user.roles).not.toContain('group_admin')
  if (login.user.must_change_password) await body(await child.request.put('/api/v1/auth/profile/password', { data: { current_password: password, new_password: `${password}Changed` } }), 204)
})
test.afterAll(async () => { for (const context of contexts.reverse()) await context.close() })

test('组织架构的 255 字名称与 128 字简称在双视口真实省略，并保留完整名称', async () => {
  test.setTimeout(180_000)
  const page = await newPage(group, '/organizations')
  try {
    for (const width of [1440, 960]) {
      await page.setViewportSize({ width, height: 900 })
      for (const organization of organizations) {
        await page.getByRole('textbox', { name: '搜索组织', exact: true }).fill(organization.code)
        const name = page.locator(`.organization-name-cell strong[title="${organization.name}"]`)
        await name.scrollIntoViewIfNeeded()
        await expect(name).toHaveAttribute('title', organization.name)
        await expectEllipsis(name)
        const shortName = name.locator('..').getByText(organization.short_name!, { exact: true })
        await expectEllipsis(shortName)
        await expect(shortName).toHaveAttribute('title', organization.short_name!)
      }
    }
  } finally { await page.close() }
})

for (const [path, resource] of [['/systems', 'business-systems'], ['/assets', 'assets']]) {
  test(`${path} 组织筛选在桌面及窄视口向下展开，实际筛选请求携带所选组织`, async () => {
    test.setTimeout(90_000)
    const page = await newPage(group, path)
    try {
      for (const [width, height] of [[1440, 900], [960, 700], [800, 520], [390, 700]]) {
        await page.setViewportSize({ width, height })
        // Element Plus renders the placeholder as a span, not an input attribute.
        const select = page.locator('.asset-filter-panel .el-select').nth(resource === 'assets' ? 2 : 1)
        const trigger = select.locator('.el-select__wrapper')
        await trigger.scrollIntoViewIfNeeded()
        const popup = await dropdown(page, trigger)
        await expect(popup).toHaveAttribute('data-popper-placement', /^bottom/)
        await popup.screenshot({ path: test.info().outputPath(`organization-dropdown-full-${resource}-${width}.png`) })
        const fullTriggerBox = (await trigger.boundingBox())!
        const fullPopupBox = (await popup.boundingBox())!
        expect(fullPopupBox.y, `${width}x${height} 未搜索的完整菜单也必须向下展开`).toBeGreaterThanOrEqual(fullTriggerBox.y + fullTriggerBox.height - 1)
        expect(fullPopupBox.x).toBeGreaterThanOrEqual(0)
        expect(fullPopupBox.x + fullPopupBox.width).toBeLessThanOrEqual(width + 1)
        await select.locator('input').fill(`长组织_${suffix}_0_`)
        const option = popup.getByRole('option', { name: organizations[0].name, exact: true })
        await expect(option).toBeVisible()
        await expect(option).toHaveAttribute('title', organizations[0].name)
        await expect(popup).toHaveAttribute('data-popper-placement', /^bottom/)
        const triggerBox = (await trigger.boundingBox())!
        const popupBox = (await popup.boundingBox())!
        expect(popupBox.y, `${width}x${height} 下拉不得向上覆盖触发器`).toBeGreaterThanOrEqual(triggerBox.y + triggerBox.height - 1)
        expect(popupBox.x).toBeGreaterThanOrEqual(0)
        expect(popupBox.x + popupBox.width).toBeLessThanOrEqual(width + 1)
        await popup.screenshot({ path: test.info().outputPath(`organization-dropdown-${resource}-${width}.png`) })
        const response = page.waitForResponse(res => res.url().includes(`/api/v1/${resource}?`) && new URL(res.url()).searchParams.get('organization_id') === organizations[0].id && res.request().method() === 'GET')
        await option.click()
        const filtered = await body(await response, 200)
        expect(filtered.items.map((item: { id: string }) => item.id)).toContain(records[resource][0])
        expect(filtered.items.map((item: { id: string }) => item.id)).not.toContain(records[resource][1])
        expect(filtered.items.every((item: { organization_id: string }) => item.organization_id === organizations[0].id)).toBe(true)
        // Clear the real UI selection before repeating at another viewport.
        await select.hover()
        const clearResponse = page.waitForResponse(res => res.url().includes(`/api/v1/${resource}?`) && !new URL(res.url()).searchParams.get('organization_id') && res.request().method() === 'GET')
        await select.locator('.el-select__clear').click()
        await body(await clearResponse, 200)
        await expect(select).toContainText('全部组织')
      }
    } finally { await page.close() }
  })
}

test('真实多组织子管理员切换 255 字组织后，顶栏保持单行且控件不重叠', async () => {
  test.setTimeout(120_000)
  const page = await newPage(child, '/dashboard')
  try {
    for (const [index, width] of [1440, 1280, 1100, 960, 800, 760, 390].entries()) {
      await page.setViewportSize({ width, height: 900 })
      const organization = organizations[(index + 1) % 2]
      const switcher = page.locator('.work-organization-switcher')
      const popup = await dropdown(page, switcher)
      const response = page.waitForResponse(res => res.url().endsWith('/api/v1/auth/current-organization') && res.request().method() === 'PUT')
      await popup.getByRole('option', { name: organization.name, exact: true }).click()
      const switched = await body(await response, 200)
      expect(switched.user.organization.id).toBe(organization.id)
      await expect(page.locator('.topbar-organization strong')).toHaveText(organization.name)
      await expect(page.locator('.topbar-organization strong')).toHaveAttribute('title', organization.name)
      await expect(page).toHaveURL(/\/dashboard\?organization_changed=/)
      const me = await body(await child.request.get('/api/v1/auth/me'), 200)
      expect(me.user.organization.id).toBe(organization.id)
      const geometry = await page.locator('.topbar').evaluate(element => {
        const rect = (selector: string) => { const r = element.querySelector(selector)!.getBoundingClientRect(); return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width, height: r.height } }
        const title = element.querySelector('.topbar-organization strong')!
        const range = document.createRange(); range.selectNodeContents(title)
        // Ellipsis can produce multiple Range rectangles on the SAME line.
        // Distinct lines have disjoint vertical bands; rectangle count is not a line count.
        const textRects = Array.from(range.getClientRects())
        const singleLine = textRects.length > 0 && Math.max(...textRects.map(r => r.top)) < Math.min(...textRects.map(r => r.bottom))
        return { header: element.getBoundingClientRect().toJSON(), context: rect('.topbar-context'), actions: rect('.topbar-actions'), switcher: rect('.work-organization-switcher'), name: rect('.topbar-organization strong'), textRects: textRects.map(r => r.toJSON()), singleLine, nowrap: getComputedStyle(title).whiteSpace }
      })
      await test.info().attach(`topbar-${width}`, { body: JSON.stringify(geometry, null, 2), contentType: 'application/json' })
      expect(geometry.context.right, `${width}px 顶栏组织/切换器与操作按钮重叠`).toBeLessThanOrEqual(geometry.actions.left + 1)
      expect(geometry.switcher.right, `${width}px 切换器越过自身容器`).toBeLessThanOrEqual(geometry.context.right + 1)
      expect(geometry.switcher.right, `${width}px 切换器与操作按钮重叠`).toBeLessThanOrEqual(geometry.actions.left + 1)
      expect(geometry.name.right).toBeLessThanOrEqual(geometry.switcher.left)
      expect(geometry.actions.right).toBeLessThanOrEqual(width)
      expect(geometry.switcher.width).toBeGreaterThanOrEqual(100)
      expect(geometry.name.width, `${width}px 组织名称仍须保留可读空间`).toBeGreaterThanOrEqual(40)
      expect(geometry.singleLine, JSON.stringify(geometry.textRects)).toBe(true)
      expect(geometry.nowrap).toBe('nowrap')
      expect(geometry.switcher.top).toBeGreaterThanOrEqual(geometry.header.top)
      expect(geometry.switcher.bottom).toBeLessThanOrEqual(geometry.header.bottom)
      if ([1440, 800, 390].includes(width)) {
        await expect(page.getByText('已切换当前工作组织', { exact: true })).toBeHidden()
        await page.locator('.topbar').screenshot({ path: test.info().outputPath(`topbar-${width}.png`) })
      }
    }
    await page.reload()
    await expect(page.locator('.topbar-organization strong')).toHaveText(organizations[1].name)
  } finally { await page.close() }
})

test('浏览器全选 20 个业务系统批量归档及恢复，数量、版本与数据库状态一致', async () => {
  test.setTimeout(120_000)
  const systems: System[] = []
  const prefix = `批量20_${suffix}`
  for (let index = 0; index < 20; index++) {
    const created = await body(await group.request.post('/api/v1/business-systems', { data: { code: `UI20_${suffix}_${index}`, name: `${prefix}_${index}`, organization_id: organizations[0].id, status: 'running', level: 'normal', internet_exposure: 'unknown', contains_personal_information: 'unknown', contains_sensitive_personal_information: 'unknown', data_volume_over_100k: 'unknown', contains_important_data: 'unknown', contains_core_data: 'unknown' } }), 201)
    systems.push(created.item)
  }
  const page = await newPage(group, '/systems')
  try {
    const search = page.getByPlaceholder('搜索系统名称、组织或负责人，也可输入编码')
    await search.fill(prefix)
    const loaded = page.waitForResponse(res => res.url().includes('/api/v1/business-systems?') && new URL(res.url()).searchParams.get('q') === prefix)
    await search.press('Enter')
    await body(await loaded, 200)
    for (const [action, expectedStatus, version] of [['归档', 'archived', 2], ['恢复', 'running', 3]] as const) {
      const rows = page.locator('.data-table-panel .el-table__body-wrapper tbody tr')
      await expect(rows).toHaveCount(20)
      await page.locator('.data-table-panel .el-table__header-wrapper .el-checkbox').click()
      await expect(page.getByText('已选择 20 个业务系统', { exact: false })).toBeVisible()
      await page.getByRole('button', { name: `批量${action}`, exact: true }).click()
      const response = page.waitForResponse(res => res.url().endsWith('/api/v1/business-systems/batch/archive') && res.request().method() === 'POST')
      await page.getByRole('dialog', { name: `批量${action}确认` }).getByRole('button', { name: `确认${action}`, exact: true }).click()
      const res = await response
      const request = res.request().postDataJSON()
      expect(request.items).toHaveLength(20)
      expect(request.items.map((item: { id: string }) => item.id).sort()).toEqual(systems.map(item => item.id).sort())
      for (const item of request.items) expect(item.expected_version).toBe(version - 1)
      const result = await body(res, 200)
      expect(result.result).toMatchObject({ success_count: 20, failure_count: 0 })
      const dialog = page.getByRole('dialog', { name: `批量${action}结果` })
      await expect(dialog).toContainText('成功 20 个，失败 0 个')
      await dialog.getByRole('button', { name: '关闭', exact: true }).click()
      const saved = await body(await group.request.get(`/api/v1/business-systems?q=${encodeURIComponent(prefix)}&page=1&page_size=100`), 200)
      expect(saved.total).toBe(20)
      for (const item of saved.items as System[]) {
        expect(item.status).toBe(expectedStatus)
        expect(item.version).toBe(version)
      }
    }
  } finally { await page.close() }
})
