import { expect, test, type Page } from '@playwright/test'

const adminUsername = process.env.E2E_USERNAME ?? 'admin'
const adminPassword = process.env.E2E_PASSWORD

async function login(page: Page, username: string, password: string) {
  await page.goto('/login')
  await page.getByPlaceholder('请输入用户名').fill(username)
  await page.getByPlaceholder('请输入用户名').blur()
  await page.getByPlaceholder('请输入密码').fill(password)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page).not.toHaveURL(/\/login(?:\?|$)/)
}

async function logout(page: Page) {
  await page.getByRole('button', { name: '退出', exact: true }).click()
  await expect(page).toHaveURL(/\/login(?:\?|$)/)
}

async function apiRequest(page: Page, path: string, init?: { method?: string; data?: unknown }) {
  const response = await page.evaluate(async ({ path, init }) => {
    const result = await fetch(`/api/v1${path}`, {
      method: init?.method ?? 'GET',
      body: init?.data === undefined ? undefined : JSON.stringify(init.data),
      headers: { 'Content-Type': 'application/json' },
    })
    const text = await result.text()
    if (!result.ok) throw new Error(`${init?.method ?? 'GET'} ${path} failed: ${result.status} ${text}`)
    return text ? JSON.parse(text) : null
  }, { path, init })
  return response
}

test('子单位申请上线安全测试，集团审批通过后自动上报待上报资产', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  test.setTimeout(300_000)
  page.setDefaultTimeout(15_000)

  const suffix = Date.now().toString()
  const organizationCode = `STR_ORG_${suffix}`
  const organizationName = `上线测试子公司_${suffix}`
  const childUsername = `str_user_${suffix}`
  const temporaryPassword = 'LaunchRequest123!'
  const formalPassword = `${temporaryPassword}Changed`
  const systemCode = `STR_SYS_${suffix}`
  const systemName = `上线申请系统_${suffix}`
  const assetValue = `launch-test-${suffix}.example.com`
  const requestName = `客户服务平台上线安全测试_${suffix}`

  await login(page, adminUsername, adminPassword!)

  const fixture = await page.evaluate(async ({ organizationCode, organizationName, childUsername, temporaryPassword }) => {
    async function request(path: string, init?: RequestInit) {
      const response = await fetch(`/api/v1${path}`, {
        ...init,
        headers: { 'Content-Type': 'application/json', ...(init?.headers ?? {}) },
      })
      const text = await response.text()
      if (!response.ok) throw new Error(`${init?.method ?? 'GET'} ${path} failed: ${response.status} ${text}`)
      return text ? JSON.parse(text) : null
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
        manager: '上线测试负责人',
        contact: 'launch-test-owner@example.com',
        notes: '上线安全测试申请自动化测试组织',
      }),
    })

    const roles = await request('/roles/assignable?page=1&page_size=100')
    const organizationAdminRole = roles.items.find((item: { key: string }) => item.key === 'organization_admin')
    if (!organizationAdminRole) throw new Error('organization_admin role not found')

    const userResponse = await request('/users', {
      method: 'POST',
      body: JSON.stringify({
        username: childUsername,
        password: temporaryPassword,
        display_name: '上线测试子公司管理员',
        email: 'launch-test-child@example.com',
        mobile_phone: '',
        office_phone: '',
        global_role_ids: [],
        appointments: [{
          organization_id: organizationResponse.item.id,
          is_primary: true,
          assignments: [{ role_id: organizationAdminRole.id, data_scope: 'organization' }],
        }],
      }),
    })

    return {
      organizationID: organizationResponse.item.id as string,
      userID: userResponse.item.id as string,
    }
  }, { organizationCode, organizationName, childUsername, temporaryPassword })

  await logout(page)
  await login(page, childUsername, temporaryPassword)
  await expect(page).toHaveURL(/\/profile(?:\?|$)/)
  await page.locator('input[autocomplete="current-password"]').fill(temporaryPassword)
  await page.locator('input[autocomplete="new-password"]').nth(0).fill(formalPassword)
  await page.locator('input[autocomplete="new-password"]').nth(1).fill(formalPassword)
  await page.getByRole('button', { name: '修改密码', exact: true }).click()
  await expect(page).toHaveURL(/\/dashboard$/)

  // 子单位先补齐未上报的业务系统与公网资产工作版本，必填项满足提交校验。
  const working = await apiRequest(page, '/business-systems', {
    method: 'POST',
    data: {
      code: systemCode,
      name: systemName,
      short_name: '上线申请系统',
      organization_id: fixture.organizationID,
      owner_name: '王五',
      owner_contact: 'wangwu@example.com',
      status: 'running',
      level: 'important',
      deployment_type: '子公司公有云',
      internet_exposure: 'yes',
      description: '上线前申请集团安全测试的业务系统',
      contains_personal_information: 'yes',
      contains_sensitive_personal_information: 'no',
      personal_information_record_count: 5000,
      data_volume_over_100k: 'no',
      contains_important_data: 'yes',
      contains_core_data: 'no',
      data_storage_location: '上海数据中心',
      security_notes: '上线前需完成渗透测试',
      custom_values: [],
    },
  })
  expect(working.item.is_working_copy).toBe(true)
  expect(working.item.reporting_status).toBe('unreported')

  const asset = await apiRequest(page, '/assets', {
    method: 'POST',
    data: {
      type: 'domain',
      value: assetValue,
      name: '上线测试域名',
      organization_id: fixture.organizationID,
      business_system_id: working.item.id,
      environment: 'production',
      network_scope: 'public',
      status: 'active',
      importance: 'important',
      owner_name: '王五',
      notes: '',
      custom_values: [],
    },
  })
  expect(asset.item.is_working_copy).toBe(true)
  expect(asset.item.reporting_status).toBe('unreported')

  const candidates = await apiRequest(page, `/security-test-requests/scope-candidates?keyword=${encodeURIComponent(assetValue)}`)
  expect(candidates.total).toBe(1)
  expect(candidates.items[0].kind).toBe('working')

  const createRequest = async (name: string) => apiRequest(page, '/security-test-requests', {
    method: 'POST',
    data: {
      request_name: name,
      test_type: 'penetration',
      expected_launch_at: new Date(Date.now() + 30 * 24 * 60 * 60_000).toISOString(),
      test_window_start_at: new Date(Date.now() + 3 * 24 * 60 * 60_000).toISOString(),
      test_window_end_at: new Date(Date.now() + 10 * 24 * 60 * 60_000).toISOString(),
      contact_user_id: fixture.userID,
      background: '新系统对外提供服务，上线前申请集团渗透测试。',
      business_system_id: '',
      business_system_draft_id: working.item.id,
      published_asset_ids: [],
      working_asset_ids: [asset.item.id],
    },
  })

  const requestA = await createRequest(requestName)
  expect(requestA.item.status).toBe('draft')
  expect(requestA.item.scope_asset_count).toBe(1)
  expect(requestA.item.has_unreported_scope).toBe(true)
  expect(requestA.item.business_system_name).toBe(systemName)

  // 子单位在页面上查看草稿详情并提交申请。
  await page.goto('/security-test-requests')
  await expect(page.getByRole('heading', { name: '上线测试申请' })).toBeVisible()
  await expect(page.getByRole('button', { name: requestName })).toBeVisible()
  await expect(page.locator('tr', { hasText: requestName }).getByText('草稿')).toBeVisible()
  await page.getByRole('button', { name: requestName }).click()
  await expect(page.getByText('申请范围快照')).toBeVisible()
  await expect(page.locator('.el-drawer').getByText(assetValue)).toBeVisible()
  await expect(page.locator('.el-drawer').getByText('新增上报')).toBeVisible()
  await page.getByRole('button', { name: '范围自检' }).click()
  await expect(page.getByText(/必填项校验通过/)).toBeVisible()
  await page.getByRole('button', { name: '知道了' }).click()
  await page.keyboard.press('Escape')
  await page.locator('tr', { hasText: requestName }).getByTestId('submit-security-test-request').click()
  await page.getByRole('button', { name: '确认提交' }).click()
  await expect(page.locator('tr', { hasText: requestName }).getByText('待审核')).toBeVisible()

  const submittedA = await apiRequest(page, `/security-test-requests/${requestA.item.id}`)
  expect(submittedA.item.status).toBe('submitted')

  // 撤回与取消流转：申请 B 撤回后取消。
  const requestB = await createRequest(`上线测试申请撤回演练_${suffix}`)
  await apiRequest(page, `/security-test-requests/${requestB.item.id}/submit`, { method: 'POST' })
  const withdrawnB = await apiRequest(page, `/security-test-requests/${requestB.item.id}/withdraw`, { method: 'POST' })
  expect(withdrawnB.item.status).toBe('draft')
  const cancelledB = await apiRequest(page, `/security-test-requests/${requestB.item.id}/cancel`, {
    method: 'POST',
    data: { comment: '演练撤回后取消' },
  })
  expect(cancelledB.item.status).toBe('cancelled')

  // 驳回与集团取消流转：申请 C 提交后由集团驳回，申请 D 提交后由集团取消。
  const requestC = await createRequest(`上线测试申请驳回演练_${suffix}`)
  await apiRequest(page, `/security-test-requests/${requestC.item.id}/submit`, { method: 'POST' })
  const requestD = await createRequest(`上线测试申请集团取消演练_${suffix}`)
  await apiRequest(page, `/security-test-requests/${requestD.item.id}/submit`, { method: 'POST' })

  await logout(page)
  await login(page, adminUsername, adminPassword!)

  const rejectedC = await apiRequest(page, `/security-test-requests/${requestC.item.id}/reject`, {
    method: 'POST',
    data: { comment: '请补充业务系统数据安全属性后再提交' },
  })
  expect(rejectedC.item.status).toBe('rejected')
  expect(rejectedC.item.review_comment).toBe('请补充业务系统数据安全属性后再提交')
  const cancelledD = await apiRequest(page, `/security-test-requests/${requestD.item.id}/cancel`, {
    method: 'POST',
    data: { comment: '演练集团取消已提交申请' },
  })
  expect(cancelledD.item.status).toBe('cancelled')

  // 集团收到站内提醒，并在页面上审批通过，生成授权测试批次。
  const notifications = await apiRequest(page, '/notifications?category=security_test_request&page=1&page_size=20')
  expect(notifications.total).toBeGreaterThanOrEqual(2)

  await page.goto('/security-test-requests')
  await expect(page.locator('tr', { hasText: requestName }).getByText('待审核')).toBeVisible()
  await page.locator('tr', { hasText: requestName }).getByTestId('approve-security-test-request').click()
  await page.getByPlaceholder('说明授权人、授权依据、允许的测试方式与边界').fill('经集团安全委员会授权，允许在约定窗口内开展渗透测试。')
  await page.getByTestId('confirm-approve-security-test-request').click()
  await expect(page.locator('tr', { hasText: requestName }).getByText('测试中')).toBeVisible()

  const testingA = await apiRequest(page, `/security-test-requests/${requestA.item.id}`)
  expect(testingA.item.status).toBe('testing')
  expect(testingA.item.converted_batch_id).toBeTruthy()

  // 批次完成后申请自动进入待上线审批。
  await apiRequest(page, `/security-test-batches/${testingA.item.converted_batch_id}/start`, {
    method: 'POST',
    data: { comment: '按窗口开始测试' },
  })
  await apiRequest(page, `/security-test-batches/${testingA.item.converted_batch_id}/complete`, {
    method: 'POST',
    data: { comment: '测试完成，无遗留发现' },
  })

  await page.goto('/security-test-requests')
  await expect(page.locator('tr', { hasText: requestName }).getByText('待上线审批')).toBeVisible()
  await page.locator('tr', { hasText: requestName }).getByTestId('approve-security-test-launch').click()
  await page.locator('.el-message-box').getByRole('button', { name: '批准上线' }).click()
  await expect(page.locator('tr', { hasText: requestName }).getByText('已关闭')).toBeVisible()

  // 批准上线后申请关闭，待上报工作版本自动上报进入集团审核。
  const closedA = await apiRequest(page, `/security-test-requests/${requestA.item.id}`)
  expect(closedA.item.status).toBe('closed')
  expect(closedA.item.reported_task_id).toBeTruthy()

  const tasks = await apiRequest(page, '/inventory-tasks?page=1&page_size=20')
  const launchTask = tasks.items.find((item: { name: string }) => item.name.includes('上线发布') && item.name.includes(requestName))
  expect(launchTask).toBeTruthy()

  const reportedAssets = await apiRequest(page, `/inventory-tasks/${closedA.item.reported_task_id}/organizations/${fixture.organizationID}/asset-drafts?page=1&page_size=20`)
  const reportedAsset = reportedAssets.items.find((item: { value: string }) => item.value === assetValue)
  expect(reportedAsset).toBeTruthy()
  expect(reportedAsset.draft_status).toBe('submitted')
})