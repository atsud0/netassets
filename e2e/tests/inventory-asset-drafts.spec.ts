import { expect, test, type Page } from '@playwright/test'

const adminUsername = process.env.E2E_USERNAME ?? 'admin'
const adminPassword = process.env.E2E_PASSWORD

async function login(page: Page, username: string, password: string) {
  await page.goto('/login')
  await page.getByPlaceholder('请输入用户名').fill(username)
  await page.getByPlaceholder('请输入密码').fill(password)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page).not.toHaveURL(/\/login(?:\?|$)/)
}

async function logout(page: Page) {
  await page.getByRole('button', { name: '退出', exact: true }).click()
  await expect(page).toHaveURL(/\/login(?:\?|$)/)
}

test('任务中新建资产在提交和审核通过前与集团正式台账隔离', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  test.setTimeout(240_000)
  page.setDefaultTimeout(15_000)

  const suffix = Date.now().toString()
  const organizationCode = `DRAFT_${suffix}`
  const organizationName = `草稿隔离子公司_${suffix}`
  const childUsername = `draft_user_${suffix}`
  const temporaryPassword = 'DraftChild123!'
  const formalPassword = `${temporaryPassword}Changed`
  const taskName = `资产草稿隔离任务_${suffix}`
  const assetValue = `draft-${suffix}.example.com`
  const assetName = `待审核网络资产_${suffix}`

  await login(page, adminUsername, adminPassword!)

  const fixture = await page.evaluate(async ({ organizationCode, organizationName, childUsername, temporaryPassword, taskName }) => {
    async function request(path: string, init?: RequestInit) {
      const response = await fetch(`/api/v1${path}`, {
        ...init,
        headers: { 'Content-Type': 'application/json', ...(init?.headers ?? {}) },
      })
      const text = await response.text()
      const data = text ? JSON.parse(text) : null
      if (!response.ok) throw new Error(`${init?.method ?? 'GET'} ${path} failed: ${response.status} ${text}`)
      return data
    }

    const organizations = await request('/organizations?page=1&page_size=100')
    const group = organizations.items.find((item: { type: string }) => item.type === 'group')
    if (!group) throw new Error('group organization not found')

    const organizationResponse = await request('/organizations', {
      method: 'POST',
      body: JSON.stringify({
        parent_id: group.id,
        code: organizationCode,
        name: organizationName,
        short_name: organizationName,
        type: 'subsidiary',
        manager: '草稿隔离负责人',
        contact: 'draft-owner@example.com',
        notes: '填报任务资产草稿隔离自动化测试',
      }),
    })
    const organization = organizationResponse.item

    const roles = await request('/roles/assignable?page=1&page_size=100')
    const organizationAdminRole = roles.items.find((item: { key: string }) => item.key === 'organization_admin')
    if (!organizationAdminRole) throw new Error('organization_admin role not found')

    const userResponse = await request('/users', {
      method: 'POST',
      body: JSON.stringify({
        username: childUsername,
        password: temporaryPassword,
        display_name: '草稿隔离子公司管理员',
        email: 'draft-child@example.com',
        mobile_phone: '',
        office_phone: '',
        global_role_ids: [],
        appointments: [{
          organization_id: organization.id,
          is_primary: true,
          assignments: [{ role_id: organizationAdminRole.id, data_scope: 'organization' }],
        }],
      }),
    })

    const now = Date.now()
    const taskResponse = await request('/inventory-tasks', {
      method: 'POST',
      body: JSON.stringify({
        name: taskName,
        description: '验证子公司任务草稿在集团审核通过前不可进入正式台账',
        start_at: new Date(now - 5 * 60_000).toISOString(),
        deadline_at: new Date(now + 24 * 60 * 60_000).toISOString(),
        status: 'active',
        asset_network_scope: 'public',
        required_fields: ['network_assets'],
        quality_rules: [],
        require_review: true,
        allow_late_submission: false,
        organization_ids: [organization.id],
      }),
    })

    return {
      organizationID: organization.id as string,
      userID: userResponse.item.id as string,
      taskID: taskResponse.item.id as string,
    }
  }, { organizationCode, organizationName, childUsername, temporaryPassword, taskName })

  await logout(page)
  await login(page, childUsername, temporaryPassword)
  await expect(page).toHaveURL(/\/profile(?:\?|$)/)
  await page.locator('input[autocomplete="current-password"]').fill(temporaryPassword)
  await page.locator('input[autocomplete="new-password"]').nth(0).fill(formalPassword)
  await page.locator('input[autocomplete="new-password"]').nth(1).fill(formalPassword)
  await page.getByRole('button', { name: '修改密码', exact: true }).click()
  await expect(page).toHaveURL(/\/dashboard$/)

  const createdDraft = await page.evaluate(async ({ taskID, organizationID, userID, assetValue, assetName }) => {
    const startResponse = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${organizationID}/start`, { method: 'POST' })
    if (!startResponse.ok) throw new Error(`start task failed: ${startResponse.status} ${await startResponse.text()}`)

    const createResponse = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${organizationID}/asset-drafts`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        type: 'domain',
        value: assetValue,
        name: assetName,
        organization_id: organizationID,
        environment: 'production',
        network_scope: 'public',
        status: 'active',
        importance: 'normal',
        owner_user_id: userID,
        owner_name: '',
        notes: '尚未提交的任务资产草稿',
        expires_at: null,
      }),
    })
    const createText = await createResponse.text()
    if (!createResponse.ok) throw new Error(`create draft failed: ${createResponse.status} ${createText}`)
    const draft = JSON.parse(createText).item

    const formalResponse = await fetch(`/api/v1/assets?page=1&page_size=20&q=${encodeURIComponent(assetValue)}`)
    const formal = await formalResponse.json()
    const draftResponse = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${organizationID}/asset-drafts?page=1&page_size=20`)
    const drafts = await draftResponse.json()
    return { draft, formalTotal: formal.total, draftTotal: drafts.total }
  }, { ...fixture, assetValue, assetName })

  expect(createdDraft.formalTotal).toBe(0)
  expect(createdDraft.draftTotal).toBe(1)
  expect(createdDraft.draft.draft_status).toBe('editing')
  expect(createdDraft.draft.is_draft).toBe(true)

  await page.goto(`/assets?mode=inventory&task_id=${fixture.taskID}&organization_id=${fixture.organizationID}&task_name=${encodeURIComponent(taskName)}&asset_network_scope=public`)
  await expect(page.getByText(`当前为“${taskName}”填报草稿模式`)).toBeVisible()
  await expect(page.getByText(assetValue, { exact: true })).toBeVisible()
  await expect(page.getByText('未提交时集团在正式台账中不可见')).toBeVisible()

  await logout(page)
  await login(page, adminUsername, adminPassword!)
  const beforeSubmit = await page.evaluate(async ({ taskID, organizationID, assetValue }) => {
    const formalResponse = await fetch(`/api/v1/assets?page=1&page_size=20&q=${encodeURIComponent(assetValue)}`)
    const draftResponse = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${organizationID}/asset-drafts?page=1&page_size=20`)
    return { formal: await formalResponse.json(), drafts: await draftResponse.json() }
  }, { ...fixture, assetValue })
  expect(beforeSubmit.formal.total).toBe(0)
  expect(beforeSubmit.drafts.total).toBe(0)

  await logout(page)
  await login(page, childUsername, formalPassword)
  const submission = await page.evaluate(async ({ taskID, organizationID }) => {
    const response = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${organizationID}/submit`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ note: '提交资产草稿等待集团审核' }),
    })
    const text = await response.text()
    if (!response.ok) throw new Error(`submit task failed: ${response.status} ${text}`)
    return JSON.parse(text).item
  }, fixture)
  expect(submission.status).toBe('submitted')

  await logout(page)
  await login(page, adminUsername, adminPassword!)
  const awaitingReview = await page.evaluate(async ({ taskID, organizationID, assetValue }) => {
    const formalResponse = await fetch(`/api/v1/assets?page=1&page_size=20&q=${encodeURIComponent(assetValue)}`)
    const draftResponse = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${organizationID}/asset-drafts?page=1&page_size=20`)
    return { formal: await formalResponse.json(), drafts: await draftResponse.json() }
  }, { ...fixture, assetValue })
  expect(awaitingReview.formal.total).toBe(0)
  expect(awaitingReview.drafts.total).toBe(1)
  expect(awaitingReview.drafts.items[0].draft_status).toBe('submitted')

  const published = await page.evaluate(async ({ taskID, organizationID, assetValue }) => {
    const reviewResponse = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${organizationID}/review`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ decision: 'approved', return_reason: '', comments: [] }),
    })
    const reviewText = await reviewResponse.text()
    if (!reviewResponse.ok) throw new Error(`approve task failed: ${reviewResponse.status} ${reviewText}`)

    const formalResponse = await fetch(`/api/v1/assets?page=1&page_size=20&q=${encodeURIComponent(assetValue)}`)
    const draftResponse = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${organizationID}/asset-drafts?page=1&page_size=20`)
    return { formal: await formalResponse.json(), drafts: await draftResponse.json() }
  }, { ...fixture, assetValue })

  expect(published.formal.total).toBe(1)
  expect(published.formal.items[0].id).toBe(createdDraft.draft.id)
  expect(published.drafts.items[0].draft_status).toBe('published')
})
