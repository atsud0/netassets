import { expect, test, type Page } from '@playwright/test'

// "待确认资产 已忽略/无效 tab 中，状态下理由过长需要显示缩略".
//
// The 状态 column is 120px wide and used to print the review note in full, so one
// long dismissal reason wrapped into dozens of lines and dragged its row - and
// with it every other cell in that row - to that height.
//
// The unit test covers the arithmetic of the clamp and the component test covers
// the style object; neither proves the rendered DOM. What matters in the browser is
// that a long reason costs a couple of lines instead of unbounded height, and that
// the full reason stays reachable. So the case is measured against a sibling row
// carrying a short reason in the same tab: if the note were printed in full the
// two rows would differ by hundreds of pixels.

const adminUsername = process.env.E2E_USERNAME ?? 'admin'
const adminPassword = process.env.E2E_PASSWORD

async function login(page: Page) {
  await page.goto('/login')
  await page.getByPlaceholder('请输入用户名').fill(adminUsername)
  await page.getByPlaceholder('请输入密码').fill(adminPassword!)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page).not.toHaveURL(/\/login(?:\?|$)/)
}

async function api<T>(page: Page, path: string, requestID: string, method = 'GET', body?: unknown): Promise<{ status: number; body: T }> {
  return page.evaluate(async ({ path, requestID, method, body }) => {
    const response = await fetch(`/api/v1${path}`, {
      method,
      credentials: 'include',
      headers: { 'Content-Type': 'application/json', 'X-Request-ID': requestID },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    const text = await response.text()
    let payload: unknown = null
    if (text) {
      try { payload = JSON.parse(text) } catch { payload = text }
    }
    return { status: response.status, body: payload }
  }, { path, requestID, method, body }) as Promise<{ status: number; body: T }>
}

const LONG_REASON = '该线索经核对为内部压力测试流量产生的误报，且扫描时间窗口与已知演练重合，资产责任人已确认不再作为真实暴露面跟踪，后续如有变化再重新评估。'.repeat(4)
const SHORT_REASON = '重复线索'

test('已忽略和无效的待确认资产长理由缩略显示且完整理由仍可查看', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  test.setTimeout(240_000)

  const stamp = Date.now()
  const prefix = `e2e-cand-r${stamp % 100000}`
  await login(page)

  const organizations = (await api<{ items: Array<{ id: string; type: string }> }>(page, '/organizations?page=1&page_size=100', `${prefix}-orgs`)).body.items
  const group = organizations.find((item) => item.type === 'group')
  expect(group, '未找到集团根组织').toBeTruthy()

  for (const [key, action] of [['ignored', 'ignore'], ['invalid', 'invalidate']] as const) {
    for (const [variant, note] of [['long', LONG_REASON], ['short', SHORT_REASON]] as const) {
      // Same shape for both rows so the only difference is the reason length.
      const value = `${prefix}-${key}-${variant}.test.local`
      const create = await api<{ item: { id: string } }>(page, '/asset-candidates', `${prefix}-${key}-${variant}`, 'POST', {
        asset_type: 'domain',
        asset_value: value,
        name: `${key}-${variant}`,
        organization_id: group!.id,
        source: 'manual',
      })
      expect(create.status, `创建 ${key}/${variant} 线索失败`).toBe(201)
      const review = await api(page, `/asset-candidates/${create.body.item.id}/${action}`, `${prefix}-${action}-${variant}-${key}`, 'POST', { review_note: note })
      expect(review.status, `${action} ${key}/${variant} 失败`).toBe(204)
    }
  }

  await page.goto('/asset-candidates')

  for (const key of ['ignored', 'invalid'] as const) {
    // el-radio-button keeps its real <input type=radio> visually hidden, so click the
    // visible label the way a reviewer does.
    await page.getByText(key === 'ignored' ? '已忽略' : '无效', { exact: true }).click()

    const longRow = page.locator('tbody tr').filter({ hasText: `${prefix}-${key}-long.test.local` }).first()
    const shortRow = page.locator('tbody tr').filter({ hasText: `${prefix}-${key}-short.test.local` }).first()
    await expect(longRow, `${key} 长理由行未渲染`).toBeVisible()
    await expect(shortRow, `${key} 短理由行未渲染`).toBeVisible()

    const cell = longRow.locator('.clamped-reason')
    await expect(cell).toHaveAttribute('title', LONG_REASON)

    const clamp = await cell.evaluate((el) => {
      const style = getComputedStyle(el)
      return {
        clamp: style.webkitLineClamp || style.getPropertyValue('-webkit-line-clamp'),
        overflow: style.overflow,
        scrollHeight: el.scrollHeight,
        clientHeight: el.clientHeight,
      }
    })
    expect(clamp.clamp, '长理由未裁剪为两行').toBe('2')
    expect(clamp.overflow).toBe('hidden')
    // Content genuinely overflows the box, i.e. text was cut rather than merely absent.
    expect(clamp.scrollHeight, '文字未被裁剪').toBeGreaterThan(clamp.clientHeight + 1)

    const [longBox, shortBox] = await Promise.all([longRow.boundingBox(), shortRow.boundingBox()])
    expect(longBox && shortBox, '行未渲染').toBeTruthy()
    // Printed in full the reason alone would need ~15 lines (~280px). Clamped, the
    // long row must stay close to the short row of the same tab.
    expect(
      longBox!.height - shortBox!.height,
      `${key}: 长理由把行撑高 ${Math.round(longBox!.height)}px，短理由行 ${Math.round(shortBox!.height)}px`,
    ).toBeLessThan(40)
  }
})
