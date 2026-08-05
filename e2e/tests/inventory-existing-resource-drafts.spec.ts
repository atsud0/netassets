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

test('已有资产和业务系统的修改删除先进入任务草稿，审核或免审核发布后才改变正式台账', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  test.setTimeout(360_000)
  page.setDefaultTimeout(20_000)

  const suffix = Date.now().toString()
  const childUsername = `existing_draft_${suffix}`
  const temporaryPassword = 'ExistingDraft123!'
  const formalPassword = `${temporaryPassword}Changed`
  const updatedSystemName = `审核后系统名称_${suffix}`
  const autoUpdatedSystemName = `自动发布系统名称_${suffix}`
  const updatedAssetName = `审核后资产名称_${suffix}`

  await login(page, adminUsername, adminPassword!)

  const fixture = await page.evaluate(async ({ suffix, childUsername, temporaryPassword }) => {
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

    const organization = (await request('/organizations', {
      method: 'POST',
      body: JSON.stringify({
        parent_id: group.id,
        code: `EXISTING_DRAFT_${suffix}`,
        name: `已有资源草稿子公司_${suffix}`,
        short_name: `草稿子公司_${suffix}`,
        type: 'subsidiary',
        manager: '已有资源草稿负责人',
        contact: 'existing-draft-owner@example.com',
        notes: '已有资产和业务系统任务草稿隔离自动化测试',
      }),
    })).item

    const roles = await request('/roles/assignable?page=1&page_size=100')
    const organizationAdminRole = roles.items.find((item: { key: string }) => item.key === 'organization_admin')
    if (!organizationAdminRole) throw new Error('organization_admin role not found')

    const child = (await request('/users', {
      method: 'POST',
      body: JSON.stringify({
        username: childUsername,
        password: temporaryPassword,
        display_name: '已有资源草稿子公司管理员',
        email: 'existing-draft-child@example.com',
        mobile_phone: '',
        office_phone: '',
        global_role_ids: [],
        appointments: [{
          organization_id: organization.id,
          is_primary: true,
          assignments: [{ role_id: organizationAdminRole.id, data_scope: 'organization' }],
        }],
      }),
    })).item

    const systemPayload = (code: string, name: string) => ({
      code,
      name,
      short_name: name,
      organization_id: organization.id,
      owner_name: '系统负责人',
      owner_contact: 'system-owner@example.com',
      status: 'running',
      level: 'normal',
      deployment_type: '子公司私有云',
      internet_exposure: 'yes',
      description: '已有正式业务系统',
      contains_personal_information: 'no',
      contains_sensitive_personal_information: 'no',
      personal_information_record_count: null,
      data_volume_over_100k: 'no',
      contains_important_data: 'no',
      contains_core_data: 'no',
      data_storage_location: '北京数据中心',
      security_notes: '',
    })

    const updateSystem = (await request('/business-systems', {
      method: 'POST',
      body: JSON.stringify(systemPayload(`SYS_UPDATE_${suffix}`, `待修改系统_${suffix}`)),
    })).item
    const deleteSystem = (await request('/business-systems', {
      method: 'POST',
      body: JSON.stringify(systemPayload(`SYS_DELETE_${suffix}`, `待删除系统_${suffix}`)),
    })).item
    const linkedSystem = (await request('/business-systems', {
      method: 'POST',
      body: JSON.stringify(systemPayload(`SYS_LINKED_${suffix}`, `有关联资产系统_${suffix}`)),
    })).item

    const assetPayload = (value: string, name: string, businessSystemID = '') => ({
      type: 'domain',
      value,
      name,
      organization_id: organization.id,
      business_system_id: businessSystemID,
      environment: 'production',
      network_scope: 'public',
      network_area_id: '',
      status: 'active',
      importance: 'normal',
      owner_user_id: child.id,
      owner_name: '资产负责人',
      notes: '已有正式网络资产',
      expires_at: null,
    })

    const updateAsset = (await request('/assets', {
      method: 'POST',
      body: JSON.stringify(assetPayload(`update-${suffix}.example.com`, `待修改资产_${suffix}`, updateSystem.id)),
    })).item
    const deleteAsset = (await request('/assets', {
      method: 'POST',
      body: JSON.stringify(assetPayload(`delete-${suffix}.example.com`, `待删除资产_${suffix}`)),
    })).item
    const linkedAsset = (await request('/assets', {
      method: 'POST',
      body: JSON.stringify(assetPayload(`linked-${suffix}.example.com`, `关联资产_${suffix}`, linkedSystem.id)),
    })).item

    const now = Date.now()
    const reviewTask = (await request('/inventory-tasks', {
      method: 'POST',
      body: JSON.stringify({
        name: `已有资源审核任务_${suffix}`,
        description: '验证已有资源修改删除在审核通过前不改变正式台账',
        start_at: new Date(now - 5 * 60_000).toISOString(),
        deadline_at: new Date(now + 24 * 60 * 60_000).toISOString(),
        status: 'active',
        asset_network_scope: 'all',
        required_fields: ['business_systems', 'data_security', 'network_assets'],
        quality_rules: [],
        require_review: true,
        allow_late_submission: false,
        organization_ids: [organization.id],
      }),
    })).item

    return {
      organizationID: organization.id as string,
      childID: child.id as string,
      reviewTaskID: reviewTask.id as string,
      updateSystem,
      deleteSystem,
      linkedSystem,
      updateAsset,
      deleteAsset,
      linkedAsset,
    }
  }, { suffix, childUsername, temporaryPassword })

  await logout(page)
  await login(page, childUsername, temporaryPassword)
  await expect(page).toHaveURL(/\/profile(?:\?|$)/)
  await page.locator('input[autocomplete="current-password"]').fill(temporaryPassword)
  await page.locator('input[autocomplete="new-password"]').nth(0).fill(formalPassword)
  await page.locator('input[autocomplete="new-password"]').nth(1).fill(formalPassword)
  await page.getByRole('button', { name: '修改密码', exact: true }).click()
  await expect(page).toHaveURL(/\/dashboard$/)

  const draftResult = await page.evaluate(async ({ fixture, updatedSystemName, updatedAssetName }) => {
    async function request(path: string, init?: RequestInit) {
      const response = await fetch(`/api/v1${path}`, {
        ...init,
        headers: { 'Content-Type': 'application/json', ...(init?.headers ?? {}) },
      })
      const text = await response.text()
      if (!response.ok) throw new Error(`${init?.method ?? 'GET'} ${path} failed: ${response.status} ${text}`)
      return text ? JSON.parse(text) : null
    }

    await request(`/inventory-tasks/${fixture.reviewTaskID}/organizations/${fixture.organizationID}/start`, { method: 'POST' })

    const updateSystemDraft = (await request(`/inventory-tasks/${fixture.reviewTaskID}/organizations/${fixture.organizationID}/business-systems/${fixture.updateSystem.id}/update-draft`, {
      method: 'POST',
      body: JSON.stringify({
        code: fixture.updateSystem.code,
        name: updatedSystemName,
        short_name: '审核后系统',
        organization_id: fixture.organizationID,
        owner_name: '新系统负责人',
        owner_contact: 'new-system-owner@example.com',
        status: 'running',
        level: 'important',
        deployment_type: '集团云',
        internet_exposure: 'yes',
        description: '任务内修改，审核后发布',
        contains_personal_information: 'yes',
        contains_sensitive_personal_information: 'yes',
        personal_information_record_count: 120000,
        data_volume_over_100k: 'yes',
        contains_important_data: 'yes',
        contains_core_data: 'no',
        data_storage_location: '上海数据中心',
        security_notes: '审核发布修改',
        custom_values: [],
      }),
    })).item

    const deleteSystemDraft = (await request(`/inventory-tasks/${fixture.reviewTaskID}/organizations/${fixture.organizationID}/business-systems/${fixture.deleteSystem.id}/delete-draft`, { method: 'POST' })).item

    const updateAssetDraft = (await request(`/inventory-tasks/${fixture.reviewTaskID}/organizations/${fixture.organizationID}/assets/${fixture.updateAsset.id}/update-draft`, {
      method: 'POST',
      body: JSON.stringify({
        type: fixture.updateAsset.type,
        value: fixture.updateAsset.value,
        name: updatedAssetName,
        organization_id: fixture.organizationID,
        business_system_id: fixture.updateSystem.id,
        environment: 'production',
        network_scope: 'public',
        network_area_id: '',
        status: 'active',
        importance: 'core',
        owner_user_id: fixture.childID,
        owner_name: '新资产负责人',
        notes: '任务内修改，审核后发布',
        expires_at: null,
      }),
    })).item

    const deleteAssetDraft = (await request(`/inventory-tasks/${fixture.reviewTaskID}/organizations/${fixture.organizationID}/assets/${fixture.deleteAsset.id}/delete-draft`, { method: 'POST' })).item

    const formalSystem = (await request(`/business-systems/${fixture.updateSystem.id}`)).item
    const formalAsset = (await request(`/assets/${fixture.updateAsset.id}`)).item
    const formalDeleteSystem = await fetch(`/api/v1/business-systems/${fixture.deleteSystem.id}`)
    const formalDeleteAsset = await fetch(`/api/v1/assets/${fixture.deleteAsset.id}`)
    const ownSystemDrafts = await request(`/inventory-tasks/${fixture.reviewTaskID}/organizations/${fixture.organizationID}/business-system-drafts?page=1&page_size=20`)
    const ownAssetDrafts = await request(`/inventory-tasks/${fixture.reviewTaskID}/organizations/${fixture.organizationID}/asset-drafts?page=1&page_size=20`)

    return {
      updateSystemDraft,
      deleteSystemDraft,
      updateAssetDraft,
      deleteAssetDraft,
      formalSystem,
      formalAsset,
      deleteSystemStatus: formalDeleteSystem.status,
      deleteAssetStatus: formalDeleteAsset.status,
      ownSystemDrafts,
      ownAssetDrafts,
    }
  }, { fixture, updatedSystemName, updatedAssetName })

  expect(draftResult.updateSystemDraft.operation).toBe('update')
  expect(draftResult.updateSystemDraft.source_system_id).toBe(fixture.updateSystem.id)
  expect(draftResult.deleteSystemDraft.operation).toBe('delete')
  expect(draftResult.updateAssetDraft.operation).toBe('update')
  expect(draftResult.updateAssetDraft.source_asset_id).toBe(fixture.updateAsset.id)
  expect(draftResult.deleteAssetDraft.operation).toBe('delete')
  expect(draftResult.formalSystem.name).toBe(fixture.updateSystem.name)
  expect(draftResult.formalAsset.name).toBe(fixture.updateAsset.name)
  expect(draftResult.deleteSystemStatus).toBe(200)
  expect(draftResult.deleteAssetStatus).toBe(200)
  expect(draftResult.ownSystemDrafts.total).toBe(2)
  expect(draftResult.ownAssetDrafts.total).toBe(2)

  await logout(page)
  await login(page, adminUsername, adminPassword!)

  const beforeSubmit = await page.evaluate(async ({ fixture }) => {
    const systems = await (await fetch(`/api/v1/inventory-tasks/${fixture.reviewTaskID}/organizations/${fixture.organizationID}/business-system-drafts?page=1&page_size=20`)).json()
    const assets = await (await fetch(`/api/v1/inventory-tasks/${fixture.reviewTaskID}/organizations/${fixture.organizationID}/asset-drafts?page=1&page_size=20`)).json()
    return { systems, assets }
  }, { fixture })
  expect(beforeSubmit.systems.total).toBe(0)
  expect(beforeSubmit.assets.total).toBe(0)

  await logout(page)
  await login(page, childUsername, formalPassword)
  const submission = await page.evaluate(async ({ fixture }) => {
    const response = await fetch(`/api/v1/inventory-tasks/${fixture.reviewTaskID}/organizations/${fixture.organizationID}/submit`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ note: '提交已有资源修改和删除草稿' }),
    })
    const text = await response.text()
    if (!response.ok) throw new Error(`submit task failed: ${response.status} ${text}`)
    return JSON.parse(text).item
  }, { fixture })
  expect(submission.status).toBe('submitted')
  expect(submission.submitted_system_count).toBe(3)
  expect(submission.submitted_asset_count).toBe(3)

  await logout(page)
  await login(page, adminUsername, adminPassword!)

  const awaitingReview = await page.evaluate(async ({ fixture }) => {
    async function json(path: string) {
      const response = await fetch(`/api/v1${path}`)
      const text = await response.text()
      if (!response.ok) throw new Error(`GET ${path} failed: ${response.status} ${text}`)
      return JSON.parse(text)
    }
    return {
      system: (await json(`/business-systems/${fixture.updateSystem.id}`)).item,
      asset: (await json(`/assets/${fixture.updateAsset.id}`)).item,
      deleteSystemStatus: (await fetch(`/api/v1/business-systems/${fixture.deleteSystem.id}`)).status,
      deleteAssetStatus: (await fetch(`/api/v1/assets/${fixture.deleteAsset.id}`)).status,
      systemDrafts: await json(`/inventory-tasks/${fixture.reviewTaskID}/organizations/${fixture.organizationID}/business-system-drafts?page=1&page_size=20`),
      assetDrafts: await json(`/inventory-tasks/${fixture.reviewTaskID}/organizations/${fixture.organizationID}/asset-drafts?page=1&page_size=20`),
      diff: (await json(`/inventory-tasks/${fixture.reviewTaskID}/organizations/${fixture.organizationID}/submission-diff`)).submission_diff,
      modifiedSystems: await json(`/inventory-tasks/${fixture.reviewTaskID}/organizations/${fixture.organizationID}/submission-diff/items?resource_type=business_system&change_type=modified&page=1&page_size=20`),
      modifiedAssets: await json(`/inventory-tasks/${fixture.reviewTaskID}/organizations/${fixture.organizationID}/submission-diff/items?resource_type=network_asset&change_type=modified&page=1&page_size=20`),
      removedSystems: await json(`/inventory-tasks/${fixture.reviewTaskID}/organizations/${fixture.organizationID}/submission-diff/items?resource_type=business_system&change_type=removed&page=1&page_size=20`),
      removedAssets: await json(`/inventory-tasks/${fixture.reviewTaskID}/organizations/${fixture.organizationID}/submission-diff/items?resource_type=network_asset&change_type=removed&page=1&page_size=20`),
      reviewResources: await json(`/inventory-tasks/${fixture.reviewTaskID}/organizations/${fixture.organizationID}/review-resources?page=1&page_size=20`),
    }
  }, { fixture })
  expect(awaitingReview.system.name).toBe(fixture.updateSystem.name)
  expect(awaitingReview.asset.name).toBe(fixture.updateAsset.name)
  expect(awaitingReview.deleteSystemStatus).toBe(200)
  expect(awaitingReview.deleteAssetStatus).toBe(200)
  expect(awaitingReview.systemDrafts.items.every((item: { draft_status: string }) => item.draft_status === 'submitted')).toBeTruthy()
  expect(awaitingReview.assetDrafts.items.every((item: { draft_status: string }) => item.draft_status === 'submitted')).toBeTruthy()
  expect(awaitingReview.diff.summary).toEqual({ added: 2, modified: 2, removed: 2 })
  expect(awaitingReview.modifiedSystems.total).toBe(1)
  expect(awaitingReview.modifiedSystems.items[0].operation).toBe('update')
  expect(awaitingReview.modifiedSystems.items[0].changes.some((item: { field: string; before: unknown; after: unknown }) => item.field === 'system_name' && item.before === fixture.updateSystem.name && item.after === updatedSystemName)).toBeTruthy()
  expect(awaitingReview.modifiedAssets.total).toBe(1)
  expect(awaitingReview.modifiedAssets.items[0].operation).toBe('update')
  expect(awaitingReview.modifiedAssets.items[0].changes.some((item: { field: string; before: unknown; after: unknown }) => item.field === 'asset_name' && item.before === fixture.updateAsset.name && item.after === updatedAssetName)).toBeTruthy()
  expect(awaitingReview.removedSystems.items).toHaveLength(1)
  expect(awaitingReview.removedSystems.items[0].operation).toBe('delete')
  expect(awaitingReview.removedAssets.items).toHaveLength(1)
  expect(awaitingReview.removedAssets.items[0].operation).toBe('delete')
  expect(awaitingReview.reviewResources.items.filter((item: { operation?: string }) => item.operation === 'update')).toHaveLength(2)
  expect(awaitingReview.reviewResources.items.filter((item: { operation?: string }) => item.operation === 'delete')).toHaveLength(2)

  await page.goto(`/inventory-tasks?task_id=${fixture.reviewTaskID}&organization_id=${fixture.organizationID}`)
  await expect(page.getByRole('heading', { name: `已有资源审核任务_${suffix}`, exact: true })).toBeVisible()
  await page.getByRole('button', { name: '查看差异', exact: true }).click()
  await expect(page.getByText('带“本次新增 / 本次修改 / 本次删除”标记的是子公司在本任务内发起、尚待发布的台账变更')).toBeVisible()
  const diffGroups = page.locator('.submission-diff-group')
  await diffGroups.nth(0).getByRole('tab', { name: '修改（1）', exact: true }).click()
  await expect(diffGroups.nth(0).getByText('本次修改', { exact: true })).toBeVisible()
  await diffGroups.nth(0).locator('.el-table__expand-icon').first().click()
  const systemNameChange = diffGroups
    .nth(0)
    .locator('.submission-field-change')
    .filter({ has: page.getByText('系统名称', { exact: true }) })
  await expect(systemNameChange.getByText(fixture.updateSystem.name, { exact: true })).toBeVisible()
  await expect(systemNameChange.getByText(updatedSystemName, { exact: true })).toBeVisible()
  await diffGroups.nth(1).getByRole('tab', { name: '删除/归档（1）', exact: true }).click()
  await expect(diffGroups.nth(1).getByText('本次删除', { exact: true })).toBeVisible()
  await page
    .locator('.el-drawer:visible')
    .filter({ has: page.getByRole('heading', { name: '填报提交差异', exact: true }) })
    .locator('.el-drawer__close-btn')
    .click()
  await expect(page.getByRole('heading', { name: '填报提交差异', exact: true })).toBeHidden()
  await page
    .locator('.el-drawer:visible')
    .filter({ has: page.getByRole('heading', { name: '填报任务详情', exact: true }) })
    .locator('.el-drawer__close-btn')
    .click()
  await expect(page.getByRole('heading', { name: '填报任务详情', exact: true })).toBeHidden()

  const published = await page.evaluate(async ({ fixture }) => {
    const reviewResponse = await fetch(`/api/v1/inventory-tasks/${fixture.reviewTaskID}/organizations/${fixture.organizationID}/review`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ decision: 'approved', return_reason: '', comments: [] }),
    })
    const reviewText = await reviewResponse.text()
    if (!reviewResponse.ok) throw new Error(`approve task failed: ${reviewResponse.status} ${reviewText}`)

    const systemResponse = await fetch(`/api/v1/business-systems/${fixture.updateSystem.id}`)
    const assetResponse = await fetch(`/api/v1/assets/${fixture.updateAsset.id}`)
    const systemDraftResponse = await fetch(`/api/v1/inventory-tasks/${fixture.reviewTaskID}/organizations/${fixture.organizationID}/business-system-drafts?page=1&page_size=20`)
    const assetDraftResponse = await fetch(`/api/v1/inventory-tasks/${fixture.reviewTaskID}/organizations/${fixture.organizationID}/asset-drafts?page=1&page_size=20`)
    return {
      system: (await systemResponse.json()).item,
      asset: (await assetResponse.json()).item,
      deleteSystemStatus: (await fetch(`/api/v1/business-systems/${fixture.deleteSystem.id}`)).status,
      deleteAssetStatus: (await fetch(`/api/v1/assets/${fixture.deleteAsset.id}`)).status,
      systemDrafts: await systemDraftResponse.json(),
      assetDrafts: await assetDraftResponse.json(),
    }
  }, { fixture })

  expect(published.system.name).toBe(updatedSystemName)
  expect(published.system.contains_sensitive_personal_information).toBe('yes')
  expect(published.system.personal_information_record_count).toBe(120000)
  expect(published.asset.name).toBe(updatedAssetName)
  expect(published.asset.importance).toBe('core')
  expect(published.deleteSystemStatus).toBe(404)
  expect(published.deleteAssetStatus).toBe(404)
  expect(published.systemDrafts.items.every((item: { draft_status: string }) => item.draft_status === 'published')).toBeTruthy()
  expect(published.assetDrafts.items.every((item: { draft_status: string }) => item.draft_status === 'published')).toBeTruthy()

  const autoTask = await page.evaluate(async ({ fixture, suffix }) => {
    const now = Date.now()
    const response = await fetch('/api/v1/inventory-tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: `已有资源自动发布任务_${suffix}`,
        description: '验证有关联资产的业务系统不能删除，并验证免审核修改自动发布',
        start_at: new Date(now - 5 * 60_000).toISOString(),
        deadline_at: new Date(now + 24 * 60 * 60_000).toISOString(),
        status: 'active',
        asset_network_scope: 'all',
        required_fields: ['business_systems', 'data_security'],
        quality_rules: [],
        require_review: false,
        allow_late_submission: false,
        organization_ids: [fixture.organizationID],
      }),
    })
    const text = await response.text()
    if (!response.ok) throw new Error(`create auto task failed: ${response.status} ${text}`)
    return JSON.parse(text).item
  }, { fixture, suffix })

  await logout(page)
  await login(page, childUsername, formalPassword)

  const blocked = await page.evaluate(async ({ fixture, taskID }) => {
    const startResponse = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${fixture.organizationID}/start`, { method: 'POST' })
    if (!startResponse.ok) throw new Error(`start auto task failed: ${startResponse.status} ${await startResponse.text()}`)

    const draftResponse = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${fixture.organizationID}/business-systems/${fixture.linkedSystem.id}/delete-draft`, { method: 'POST' })
    const draftText = await draftResponse.text()
    if (!draftResponse.ok) throw new Error(`create linked delete draft failed: ${draftResponse.status} ${draftText}`)
    const draft = JSON.parse(draftText).item

    const submitResponse = await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${fixture.organizationID}/submit`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ note: '尝试删除仍有关联资产的业务系统' }),
    })
    const submitBody = await submitResponse.json()
    const drafts = await (await fetch(`/api/v1/inventory-tasks/${taskID}/organizations/${fixture.organizationID}/business-system-drafts?page=1&page_size=20`)).json()
    return { draft, status: submitResponse.status, body: submitBody, drafts }
  }, { fixture, taskID: autoTask.id as string })

  expect(blocked.status).toBe(409)
  expect(blocked.body.error.code).toBe('resource_in_use')
  expect(blocked.drafts.items[0].draft_status).toBe('editing')

  const autoPublished = await page.evaluate(async ({ fixture, taskID, draftID, autoUpdatedSystemName }) => {
    async function request(path: string, init?: RequestInit) {
      const response = await fetch(`/api/v1${path}`, {
        ...init,
        headers: { 'Content-Type': 'application/json', ...(init?.headers ?? {}) },
      })
      const text = await response.text()
      if (!response.ok) throw new Error(`${init?.method ?? 'GET'} ${path} failed: ${response.status} ${text}`)
      return text ? JSON.parse(text) : null
    }

    await request(`/inventory-tasks/${taskID}/organizations/${fixture.organizationID}/business-system-drafts/${draftID}`, { method: 'DELETE' })
    await request(`/inventory-tasks/${taskID}/organizations/${fixture.organizationID}/business-systems/${fixture.linkedSystem.id}/update-draft`, {
      method: 'POST',
      body: JSON.stringify({
        code: fixture.linkedSystem.code,
        name: autoUpdatedSystemName,
        short_name: '自动发布系统',
        organization_id: fixture.organizationID,
        owner_name: '自动发布负责人',
        owner_contact: 'auto-owner@example.com',
        status: 'running',
        level: 'important',
        deployment_type: '集团云',
        internet_exposure: 'yes',
        description: '免审核任务修改',
        contains_personal_information: 'no',
        contains_sensitive_personal_information: 'no',
        personal_information_record_count: null,
        data_volume_over_100k: 'no',
        contains_important_data: 'yes',
        contains_core_data: 'no',
        data_storage_location: '广州数据中心',
        security_notes: '',
        custom_values: [],
      }),
    })
    const progress = (await request(`/inventory-tasks/${taskID}/organizations/${fixture.organizationID}/submit`, {
      method: 'POST',
      body: JSON.stringify({ note: '免审核修改自动发布' }),
    })).item
    const formal = (await request(`/business-systems/${fixture.linkedSystem.id}`)).item
    const drafts = await request(`/inventory-tasks/${taskID}/organizations/${fixture.organizationID}/business-system-drafts?page=1&page_size=20`)
    return { progress, formal, drafts }
  }, { fixture, taskID: autoTask.id as string, draftID: blocked.draft.id as string, autoUpdatedSystemName })

  expect(autoPublished.progress.status).toBe('approved')
  expect(autoPublished.formal.name).toBe(autoUpdatedSystemName)
  expect(autoPublished.drafts.total).toBe(1)
  expect(autoPublished.drafts.items[0].operation).toBe('update')
  expect(autoPublished.drafts.items[0].draft_status).toBe('published')
})
