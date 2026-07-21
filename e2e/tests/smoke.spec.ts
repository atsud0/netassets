import { expect, test, type Locator, type Page } from '@playwright/test'

const adminUsername = process.env.E2E_USERNAME ?? 'admin'
const adminPassword = process.env.E2E_PASSWORD

async function login(page: Page, username: string, password: string) {
  await page.getByPlaceholder('请输入用户名').fill(username)
  await page.getByPlaceholder('请输入密码').fill(password)
  await page.getByRole('button', { name: '登录', exact: true }).click()
}

async function selectFormOption(page: Page, dialog: Locator, label: string, option: string) {
  const item = dialog.locator('.el-form-item').filter({ hasText: label }).first()
  await item.locator('.el-select').click()
  await page.locator('.el-select-dropdown:visible').getByText(option, { exact: true }).first().click()
}

test('集团创建子公司账号，子公司隔离登记业务系统和网络资产', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  test.setTimeout(90_000)

  await page.goto('/organizations')
  await expect(page).toHaveURL(/\/login\?redirect=(?:%2F|\/)organizations$/)
  await login(page, adminUsername, adminPassword!)
  await expect(page).toHaveURL(/\/organizations$/)
  await expect(page.getByRole('button', { name: '新增组织' })).toBeVisible()
  const rootOrganization = await page.evaluate(async () => {
    const response = await fetch('/api/v1/organizations')
    if (!response.ok) throw new Error(`organizations request failed: ${response.status}`)
    const data = await response.json()
    return data.items.find((item: { type: string }) => item.type === 'group') as {
      id: string; name: string; short_name?: string; manager?: string; contact?: string; notes?: string
    }
  })
  const rootOrganizationID = rootOrganization.id

  const suffix = Date.now().toString()
  const organizationCode = `SUB_PW_${suffix}`
  const organizationName = `端到端测试子公司_${suffix}`
  const subsidiaryUsername = `pw_user_${suffix}`
  const subsidiaryPassword = 'PwOrgUser123!'
  const systemCode = `SYS_PW_${suffix}`
  const systemName = `客户数据系统_${suffix}`
  const domainAsset = `portal-${suffix}.example.com`
  const importedIP = `198.51.100.${Number(suffix.slice(-2)) % 250 + 1}`
  const importFileName = `assets-${suffix}.csv`
  const groupAsset = `group-pw-${suffix}.example.com`
  const inventoryTaskName = `TASK_PW_${suffix}`
  const groupAssetID = await page.evaluate(async ({ organizationID, value }) => {
    const response = await fetch('/api/v1/assets', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        type: 'domain', value, name: `集团权限测试资产_${Date.now()}`, organization_id: organizationID,
        environment: 'production', network_scope: 'public', status: 'active', importance: 'normal',
      }),
    })
    if (!response.ok) throw new Error(`create group asset failed: ${response.status}`)
    const data = await response.json()
    return data.item.id as string
  }, { organizationID: rootOrganizationID, value: groupAsset })

  await page.getByRole('button', { name: '新增组织' }).click()
  let dialog = page.getByRole('dialog', { name: '新增组织机构' })
  await dialog.getByPlaceholder('如：SUBSIDIARY_01').fill(organizationCode)
  await dialog.getByPlaceholder('请输入完整名称').fill(organizationName)
  await dialog.getByRole('button', { name: '确认新增' }).click()
  await expect(page.getByText(organizationName, { exact: true })).toBeVisible()

  await page.getByRole('button', { name: '编辑集团信息', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '编辑集团基本信息' })
  const temporaryGroupName = `${rootOrganization.name}_编辑验证`
  await dialog.getByPlaceholder('请输入集团完整名称').fill(temporaryGroupName)
  await dialog.getByRole('button', { name: '保存集团信息' }).click()
  await expect(page.getByText('集团基本信息已更新').last()).toBeVisible()
  await expect(page.getByText(temporaryGroupName, { exact: true }).first()).toBeVisible()
  await page.getByRole('button', { name: '编辑集团信息', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '编辑集团基本信息' })
  await dialog.getByPlaceholder('请输入集团完整名称').fill(rootOrganization.name)
  await dialog.getByRole('button', { name: '保存集团信息' }).click()
  await expect(page.getByText('集团基本信息已更新').last()).toBeVisible()

  await page.getByRole('button', { name: '填报任务', exact: true }).click()
  await page.getByRole('button', { name: '创建任务', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '创建填报任务' })
  await dialog.getByPlaceholder('如：2026 年第三季度网络资产填报').fill(inventoryTaskName)
  await selectFormOption(page, dialog, '填报组织', `${organizationName}（${organizationCode}）`)
  await dialog.getByPlaceholder('填写本次填报范围、口径和注意事项').fill('端到端测试填报任务')
  await dialog.getByRole('button', { name: '创建并下发' }).click()
  await expect(page.getByText('填报任务已创建并下发')).toBeVisible()
  await expect(page.getByText(inventoryTaskName, { exact: true })).toBeVisible()

  await page.getByRole('button', { name: '用户与权限' }).click()
  await expect(page).toHaveURL(/\/users$/)
  await page.getByRole('button', { name: '新增子公司用户' }).click()
  dialog = page.getByRole('dialog', { name: '新增子公司用户' })
  await selectFormOption(page, dialog, '所属组织', `${organizationName}（${organizationCode}）`)
  await dialog.getByPlaceholder('如：subsidiary_admin').fill(subsidiaryUsername)
  await dialog.getByPlaceholder('请输入姓名').fill('端到端子公司管理员')
  await dialog.getByPlaceholder('至少 10 位').fill(subsidiaryPassword)
  await dialog.getByRole('button', { name: '确认新增' }).click()
  await expect(page.getByText(subsidiaryUsername, { exact: true })).toBeVisible()

  await page.getByRole('button', { name: '退出', exact: true }).click()
  await login(page, subsidiaryUsername, subsidiaryPassword)
  await expect(page).toHaveURL(/\/dashboard$/)
  await expect(page.getByText(organizationName, { exact: true }).first()).toBeVisible()
  await expect(page.getByRole('button', { name: '用户与权限' })).toHaveCount(0)

  await page.getByRole('button', { name: '填报任务', exact: true }).click()
  await expect(page.getByRole('button', { name: '创建任务', exact: true })).toHaveCount(0)
  await expect(page.getByText(inventoryTaskName, { exact: true })).toBeVisible()
  const forbiddenTaskStatus = await page.evaluate(async () => {
    const startAt = new Date(Date.now() + 60 * 60 * 1000)
    const deadlineAt = new Date(startAt.getTime() + 24 * 60 * 60 * 1000)
    const response = await fetch('/api/v1/inventory-tasks', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: '越权任务', start_at: startAt, deadline_at: deadlineAt, organization_ids: ['00000000-0000-0000-0000-000000000000'] }),
    })
    return response.status
  })
  expect(forbiddenTaskStatus).toBe(403)

  await page.getByRole('button', { name: '组织机构' }).click()
  await expect(page.getByRole('cell', { name: organizationName }).getByText(organizationName, { exact: true })).toBeVisible()
  await expect(page.locator('.organization-table-panel .el-table__body-wrapper tbody tr')).toHaveCount(1)

  await page.getByRole('button', { name: '业务系统' }).click()
  await page.getByRole('button', { name: '新增业务系统' }).click()
  dialog = page.getByRole('dialog', { name: '新增业务系统' })
  await dialog.getByPlaceholder('如：CRM_001').fill(systemCode)
  await dialog.getByPlaceholder('请输入系统名称').fill(systemName)
  await selectFormOption(page, dialog, '是否包含个人信息', '是')
  await selectFormOption(page, dialog, '是否包含敏感个人信息', '是')
  await dialog.locator('.el-form-item').filter({ hasText: '个人信息记录数' }).locator('input').fill('120000')
  await dialog.getByRole('button', { name: '确认新增' }).click()

  await expect(page.getByText('业务系统已新增')).toBeVisible()
  await expect(page.getByText(systemName, { exact: true })).toBeVisible()
  await expect(page.getByText(systemCode, { exact: true })).toBeVisible()
  await expect(page.getByText('超过 10 万条').locator('..').getByText('1', { exact: true })).toBeVisible()

  await page.getByRole('button', { name: '网络资产' }).click()
  await page.getByRole('button', { name: '新增资产' }).click()
  dialog = page.getByRole('dialog', { name: '新增网络资产' })
  await dialog.getByPlaceholder('如：portal.example.com').fill(domainAsset)
  await dialog.getByPlaceholder('便于识别的名称（可选）').fill(`门户资产_${suffix}`)
  await selectFormOption(page, dialog, '关联业务系统', `${systemName}（${systemCode}）`)
  await dialog.getByRole('button', { name: '确认新增' }).click()
  await expect(page.getByText('网络资产已新增')).toBeVisible()
  await expect(page.getByText(domainAsset, { exact: true })).toBeVisible()
  await expect(page.getByText(systemCode, { exact: true })).toBeVisible()

  await page.getByRole('button', { name: '批量导入', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '批量导入网络资产' })
  const csv = [
    '资产类型,资产值,资产名称,业务系统编码,环境,网络范围,重要程度,负责人,备注',
    `ip,${importedIP},导入IP,${systemCode},生产,内网,重要,安全团队,合法行`,
    `domain,${domainAsset},重复域名,${systemCode},生产,公网,重要,安全团队,重复行`,
    'url,ftp://invalid.example.com,非法URL,,生产,公网,一般,安全团队,非法行',
  ].join('\n')
  await dialog.locator('input[type=file]').setInputFiles({ name: importFileName, mimeType: 'text/csv', buffer: Buffer.from(csv) })
  await dialog.getByRole('button', { name: '开始导入' }).click()
  await expect(page.getByText('导入处理完成')).toBeVisible()
  const result = dialog.getByTestId('import-result')
  await expect(result.getByText('成功').locator('..').getByText('1', { exact: true })).toBeVisible()
  await expect(result.getByText('重复').locator('..').getByText('1', { exact: true })).toBeVisible()
  await expect(result.getByText('失败').locator('..').getByText('1', { exact: true })).toBeVisible()
  await dialog.getByRole('button', { name: '关闭' }).click()
  await expect(page.getByText(importedIP, { exact: true })).toBeVisible()

  let assetRow = page.locator('.data-table-panel .el-table__body-wrapper tbody tr').filter({ hasText: domainAsset }).first()
  await assetRow.getByRole('button', { name: '编辑', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '编辑网络资产' })
  const editedAssetName = `门户资产_编辑后_${suffix}`
  await dialog.getByPlaceholder('便于识别的名称（可选）').fill(editedAssetName)
  await dialog.getByRole('button', { name: '保存修改' }).click()
  await expect(page.getByText('网络资产已更新')).toBeVisible()
  assetRow = page.locator('.data-table-panel .el-table__body-wrapper tbody tr').filter({ hasText: domainAsset }).first()
  await expect(assetRow.getByText(editedAssetName, { exact: true })).toBeVisible()

  await assetRow.getByRole('button', { name: '归档', exact: true }).click()
  await page.getByRole('dialog', { name: '归档网络资产' }).getByRole('button', { name: '归档', exact: true }).click()
  await expect(page.getByText('网络资产已归档')).toBeVisible()
  assetRow = page.locator('.data-table-panel .el-table__body-wrapper tbody tr').filter({ hasText: domainAsset }).first()
  await expect(assetRow.getByText('已归档', { exact: true })).toBeVisible()

  await assetRow.getByRole('button', { name: '启用', exact: true }).click()
  await page.getByRole('dialog', { name: '启用网络资产' }).getByRole('button', { name: '启用', exact: true }).click()
  await expect(page.getByText('网络资产已启用')).toBeVisible()
  assetRow = page.locator('.data-table-panel .el-table__body-wrapper tbody tr').filter({ hasText: domainAsset }).first()
  await expect(assetRow.getByText('启用', { exact: true })).toBeVisible()

  const importedRow = page.locator('.data-table-panel .el-table__body-wrapper tbody tr').filter({ hasText: importedIP }).first()
  await importedRow.getByRole('button', { name: '删除', exact: true }).click()
  await page.getByRole('dialog', { name: '删除网络资产' }).getByRole('button', { name: '删除', exact: true }).click()
  await expect(page.getByText('网络资产已删除')).toBeVisible()
  await expect(page.getByText(importedIP, { exact: true })).toHaveCount(0)

  await page.getByRole('button', { name: '导入记录', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '资产导入记录' })
  const historyRow = dialog.locator('.el-table__body-wrapper tbody tr').filter({ hasText: importFileName }).first()
  await expect(historyRow.getByText(importFileName, { exact: true })).toBeVisible()
  await expect(historyRow.getByRole('cell').nth(3)).toHaveText('3')
  await expect(historyRow.getByRole('cell').nth(4)).toHaveText('1')
  await expect(historyRow.getByRole('cell').nth(5)).toHaveText('1')
  await expect(historyRow.getByRole('cell').nth(6)).toHaveText('1')
  await dialog.getByRole('button', { name: '关闭', exact: true }).click()

  const crossOrganizationStatus = await page.evaluate(async (organizationID) => {
    const response = await fetch('/api/v1/assets', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ type: 'domain', value: `forbidden-${Date.now()}.example.com`, organization_id: organizationID }),
    })
    return response.status
  }, rootOrganizationID)
  expect(crossOrganizationStatus).toBe(403)

  const crossOrganizationMutationStatuses = await page.evaluate(async ({ assetID, value }) => {
    const patchResponse = await fetch(`/api/v1/assets/${assetID}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        type: 'domain', value, name: '越权修改', organization_id: '00000000-0000-0000-0000-000000000000',
        environment: 'production', network_scope: 'public', status: 'active', importance: 'normal',
      }),
    })
    const deleteResponse = await fetch(`/api/v1/assets/${assetID}`, { method: 'DELETE' })
    return [patchResponse.status, deleteResponse.status]
  }, { assetID: groupAssetID, value: groupAsset })
  expect(crossOrganizationMutationStatuses).toEqual([403, 403])

  await page.goto('/users')
  await expect(page).toHaveURL(/\/dashboard$/)
})
