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

test('任务中新建业务系统在提交和审核通过前与正式台账隔离，并支持提交后自动发布', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  test.setTimeout(300_000)
  page.setDefaultTimeout(15_000)

  const suffix = Date.now().toString()
  const organizationCode = `BS_DRAFT_${suffix}`
  const organizationName = `业务系统草稿子公司_${suffix}`
  const childUsername = `bs_draft_user_${suffix}`
  const temporaryPassword = 'BusinessDraft123!'
  const formalPassword = `${temporaryPassword}Changed`
  const taskName = `业务系统草稿审核任务_${suffix}`
  const autoTaskName = `业务系统草稿自动发布任务_${suffix}`
  const systemCode = `BS_REVIEW_${suffix}`
  const systemName = `待审核业务系统_${suffix}`
  const autoSystemCode = `BS_AUTO_${suffix}`
  const autoSystemName = `自动发布业务系统_${suffix}`
  const fieldKey = `bs_owner_${suffix.slice(-12)}`
  const customValue = `集团安全管理部_${suffix}`

  await login(page, adminUsername, adminPassword!)

  const fixture = await page.evaluate(async ({ organizationCode, organizationName, childUsername, temporaryPassword, taskName, fieldKey, suffix }) => {
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
        manager: '业务系统草稿负责人',
        contact: 'business-draft-owner@example.com',
        notes: '填报任务业务系统草稿隔离自动化测试',
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
        display_name: '业务系统草稿子公司管理员',
        email: 'business-draft-child@example.com',
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

    const customFieldResponse = await request('/custom-fields', {
      method: 'POST',
      body: JSON.stringify({
        resource_type: 'business_system',
        field_key: fieldKey,
        field_name: `系统归口部门_${suffix}`,
        description: '验证任务草稿保存、快照和发布时保留集团自定义字段',
        field_type: 'text',
        options: [],
        is_required: true,
        is_enabled: true,
        sort_order: 90000,
      }),
    })

    const now = Date.now()
    const taskResponse = await request('/inventory-tasks', {
      method: 'POST',
      body: JSON.stringify({
        name: taskName,
        description: '验证业务系统任务草稿在集团审核通过前不可进入正式台账',
        start_at: new Date(now - 5 * 60_000).toISOString(),
        deadline_at: new Date(now + 24 * 60 * 60_000).toISOString(),
        status: 'active',
        asset_network_scope: 'all',
        required_fields: ['business_systems', 'data_security'],
        quality_rules: [],
        require_review: true,
        allow_late_submission: false,
        organization_ids: [organization.id],
      }),
    })

    return {
      organizationID: organization.id as string,
      userID: userResponse.item.id as string,
      customFieldID: customFieldResponse.item.id as string,
      taskID: taskResponse.item.id as string,
    }
  }, { organizationCode, organizationName, childUsername, temporaryPassword, taskName, fieldKey, suffix })

  await logout(page)
  await login(page, childUsername, temporaryPassword)
  await expect(page).toHaveURL(/\/profile(?:\?|$)/)
  await page.locator('input[autocomplete="current-password"]').fill(temporaryPassword)
  await page.locator('input[autocomplete="new-password"]').nth(0).fill(formalPassword)
  await page.locator('input[autocomplete="new-password"]').nth(1).fill(formalPassword)
  await page.getByRole('button', { name: '修改密码', exact: true }).click()
  await expect(page).toHaveURL(/\/dashboard$/)

  const createdDraft = await page.evaluate(async ({ taskID, organizationID, systemCode, systemName, fieldKey, customValue }) => {
    const startResponse = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${organizationID}/start`, { method: 'POST' })
    if (!startResponse.ok) throw new Error(`start task failed: ${startResponse.status} ${await startResponse.text()}`)

    const createResponse = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${organizationID}/business-system-drafts`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        code: systemCode,
        name: systemName,
        short_name: '审核草稿系统',
        organization_id: organizationID,
        owner_name: '张三',
        owner_contact: 'zhangsan@example.com',
        status: 'running',
        level: 'important',
        deployment_type: '集团私有云',
        internet_exposure: 'yes',
        description: '尚未提交的业务系统任务草稿',
        contains_personal_information: 'yes',
        contains_sensitive_personal_information: 'yes',
        personal_information_record_count: 120000,
        data_volume_over_100k: 'unknown',
        contains_important_data: 'pending',
        contains_core_data: 'no',
        data_storage_location: '上海数据中心',
        security_notes: '需要定期开展安全测试',
        custom_values: [{ field_key: fieldKey, value: customValue }],
      }),
    })
    const createText = await createResponse.text()
    if (!createResponse.ok) throw new Error(`create business system draft failed: ${createResponse.status} ${createText}`)
    const draft = JSON.parse(createText).item

    const formalResponse = await fetch(`/api/v1/business-systems?page=1&page_size=20&q=${encodeURIComponent(systemCode)}`)
    const formal = await formalResponse.json()
    const draftResponse = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${organizationID}/business-system-drafts?page=1&page_size=20`)
    const drafts = await draftResponse.json()
    return { draft, formalTotal: formal.total, drafts }
  }, { ...fixture, systemCode, systemName, fieldKey, customValue })

  expect(createdDraft.formalTotal).toBe(0)
  expect(createdDraft.drafts.total).toBe(1)
  expect(createdDraft.draft.draft_status).toBe('editing')
  expect(createdDraft.draft.is_draft).toBe(true)
  expect(createdDraft.draft.data_volume_over_100k).toBe('yes')
  expect(createdDraft.draft.custom_values).toEqual([{ field_key: fieldKey, value: customValue }])

  await page.goto(`/systems?mode=inventory&task_id=${fixture.taskID}&organization_id=${fixture.organizationID}&task_name=${encodeURIComponent(taskName)}`)
  await expect(page.getByText(`正在填报：${taskName} · ${organizationName}`)).toBeVisible()
  await expect(page.getByText(systemName, { exact: true })).toBeVisible()
  await expect(page.getByText(/未提交时集团在正式台账中不可见/)).toBeVisible()

  await logout(page)
  await login(page, adminUsername, adminPassword!)
  const beforeSubmit = await page.evaluate(async ({ taskID, organizationID, systemCode }) => {
    const formalResponse = await fetch(`/api/v1/business-systems?page=1&page_size=20&q=${encodeURIComponent(systemCode)}`)
    const draftResponse = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${organizationID}/business-system-drafts?page=1&page_size=20`)
    return { formal: await formalResponse.json(), drafts: await draftResponse.json() }
  }, { ...fixture, systemCode })
  expect(beforeSubmit.formal.total).toBe(0)
  expect(beforeSubmit.drafts.total).toBe(0)

  await logout(page)
  await login(page, childUsername, formalPassword)
  const submission = await page.evaluate(async ({ taskID, organizationID }) => {
    const response = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${organizationID}/submit`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ note: '提交业务系统草稿等待集团审核' }),
    })
    const text = await response.text()
    if (!response.ok) throw new Error(`submit task failed: ${response.status} ${text}`)
    return JSON.parse(text).item
  }, fixture)
  expect(submission.status).toBe('submitted')

  await logout(page)
  await login(page, adminUsername, adminPassword!)
  const awaitingReview = await page.evaluate(async ({ taskID, organizationID, systemCode }) => {
    const formalResponse = await fetch(`/api/v1/business-systems?page=1&page_size=20&q=${encodeURIComponent(systemCode)}`)
    const draftResponse = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${organizationID}/business-system-drafts?page=1&page_size=20`)
    return { formal: await formalResponse.json(), drafts: await draftResponse.json() }
  }, { ...fixture, systemCode })
  expect(awaitingReview.formal.total).toBe(0)
  expect(awaitingReview.drafts.total).toBe(1)
  expect(awaitingReview.drafts.items[0].draft_status).toBe('submitted')

  const published = await page.evaluate(async ({ taskID, organizationID, systemCode }) => {
    const reviewResponse = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${organizationID}/review`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ decision: 'approved', return_reason: '', comments: [] }),
    })
    const reviewText = await reviewResponse.text()
    if (!reviewResponse.ok) throw new Error(`approve task failed: ${reviewResponse.status} ${reviewText}`)

    const formalResponse = await fetch(`/api/v1/business-systems?page=1&page_size=20&q=${encodeURIComponent(systemCode)}`)
    const formal = await formalResponse.json()
    const draftResponse = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${organizationID}/business-system-drafts?page=1&page_size=20`)
    const drafts = await draftResponse.json()
    const customResponse = await fetch(`/api/v1/business-systems/${formal.items[0].id}/custom-fields`)
    return { formal, drafts, custom: await customResponse.json() }
  }, { ...fixture, systemCode })

  expect(published.formal.total).toBe(1)
  expect(published.formal.items[0].id).toBe(createdDraft.draft.id)
  expect(published.formal.items[0].contains_sensitive_personal_information).toBe('yes')
  expect(published.formal.items[0].personal_information_record_count).toBe(120000)
  expect(published.formal.items[0].data_volume_over_100k).toBe('yes')
  expect(published.drafts.items[0].draft_status).toBe('published')
  expect(published.custom.values).toContainEqual({ field_key: fieldKey, value: customValue })

  const autoTask = await page.evaluate(async ({ organizationID, autoTaskName }) => {
    const now = Date.now()
    const response = await fetch('/api/v1/inventory-tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: autoTaskName,
        description: '验证无需审核的业务系统草稿在提交后自动发布',
        start_at: new Date(now - 5 * 60_000).toISOString(),
        deadline_at: new Date(now + 24 * 60 * 60_000).toISOString(),
        status: 'active',
        asset_network_scope: 'all',
        required_fields: ['business_systems', 'data_security'],
        quality_rules: [],
        require_review: false,
        allow_late_submission: false,
        organization_ids: [organizationID],
      }),
    })
    const text = await response.text()
    if (!response.ok) throw new Error(`create auto-publish task failed: ${response.status} ${text}`)
    return JSON.parse(text).item
  }, { organizationID: fixture.organizationID, autoTaskName })

  await logout(page)
  await login(page, childUsername, formalPassword)
  const autoPublished = await page.evaluate(async ({ taskID, organizationID, autoSystemCode, autoSystemName, fieldKey, customValue }) => {
    const startResponse = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${organizationID}/start`, { method: 'POST' })
    if (!startResponse.ok) throw new Error(`start auto task failed: ${startResponse.status} ${await startResponse.text()}`)
    const createResponse = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${organizationID}/business-system-drafts`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        code: autoSystemCode,
        name: autoSystemName,
        short_name: '自动发布系统',
        organization_id: organizationID,
        owner_name: '李四',
        owner_contact: 'lisi@example.com',
        status: 'running',
        level: 'normal',
        deployment_type: '子公司私有云',
        internet_exposure: 'no',
        description: '无需审核，提交后自动发布',
        contains_personal_information: 'no',
        contains_sensitive_personal_information: 'no',
        personal_information_record_count: null,
        data_volume_over_100k: 'no',
        contains_important_data: 'no',
        contains_core_data: 'no',
        data_storage_location: '北京数据中心',
        security_notes: '',
        custom_values: [{ field_key: fieldKey, value: customValue }],
      }),
    })
    const createText = await createResponse.text()
    if (!createResponse.ok) throw new Error(`create auto-publish draft failed: ${createResponse.status} ${createText}`)
    const draft = JSON.parse(createText).item

    const submitResponse = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${organizationID}/submit`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ note: '无需审核，提交后自动发布' }),
    })
    const submitText = await submitResponse.text()
    if (!submitResponse.ok) throw new Error(`submit auto-publish task failed: ${submitResponse.status} ${submitText}`)
    const progress = JSON.parse(submitText).item

    const formalResponse = await fetch(`/api/v1/business-systems?page=1&page_size=20&q=${encodeURIComponent(autoSystemCode)}`)
    const draftResponse = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${organizationID}/business-system-drafts?page=1&page_size=20`)
    return { draft, progress, formal: await formalResponse.json(), drafts: await draftResponse.json() }
  }, {
    taskID: autoTask.id as string,
    organizationID: fixture.organizationID,
    autoSystemCode,
    autoSystemName,
    fieldKey,
    customValue,
  })

  expect(autoPublished.progress.status).toBe('approved')
  expect(autoPublished.formal.total).toBe(1)
  expect(autoPublished.formal.items[0].id).toBe(autoPublished.draft.id)
  expect(autoPublished.drafts.items[0].draft_status).toBe('published')
})
