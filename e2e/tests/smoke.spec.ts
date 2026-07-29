import { expect, test, type Locator, type Page } from '@playwright/test'

const adminUsername = process.env.E2E_USERNAME ?? 'admin'
const adminPassword = process.env.E2E_PASSWORD

async function login(page: Page, username: string, password: string) {
  await page.getByPlaceholder('请输入用户名').fill(username)
  await page.getByPlaceholder('请输入密码').fill(password)
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page).not.toHaveURL(/\/login(?:\?|$)/)
}

async function selectFormOption(page: Page, dialog: Locator, label: string, option: string) {
  const item = dialog.locator('.el-form-item').filter({ hasText: label }).first()
  const combobox = item.getByRole('combobox')
  const listboxID = await combobox.getAttribute('aria-controls')
  if (!listboxID) throw new Error(`未找到“${label}”下拉框的选项列表`)
  await item.locator('.el-select').click()
  await page.locator(`[id="${listboxID}"]`).getByRole('option', { name: option, exact: true }).click()
}

async function ensureTaskExpanded(page: Page, taskName: string) {
  const row = page.locator('.data-table-panel > .el-table .el-table__body-wrapper tbody tr').filter({ hasText: taskName }).first()
  const button = row.locator('.el-table__expand-icon')
  const expanded = await button.evaluate((element) => element.classList.contains('el-table__expand-icon--expanded'))
  if (!expanded) await button.click()
  return row
}

test('集团创建子公司账号，子公司隔离登记业务系统和网络资产', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  // 该主流程串联组织、台账、填报、审核、工单与审计全链路；在 Podman 的低配 CI/本地虚拟机中，
  // 下载和前端渲染会显著拉长耗时，保留足够总超时以避免业务已完成后被测试框架中断。
  test.setTimeout(600_000)
  page.setDefaultTimeout(15_000)

  await page.goto('/organizations')
  await expect(page).toHaveURL(/\/login\?redirect=(?:%2F|\/)organizations$/)
  await login(page, adminUsername, adminPassword!)
  await expect(page).toHaveURL(/\/organizations$/)
  await expect(page.getByRole('button', { name: '新增组织' })).toBeVisible()

  // 备份页只做访问与列表渲染校验，不创建备份，避免在回归测试中遗留运维数据。
  await page.getByRole('button', { name: '备份管理', exact: true }).click()
  await expect(page).toHaveURL(/\/backups$/)
  await expect(page.getByRole('heading', { name: '备份管理' })).toBeVisible()
  await expect(page.getByText('恢复操作仅能在部署宿主机执行')).toBeVisible()
  await page.getByRole('button', { name: '组织架构', exact: true }).click()
  await expect(page).toHaveURL(/\/organizations$/)
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
  const scopeExcludedIP = `10.250.${Number(suffix.slice(-4, -2)) % 250}.${Number(suffix.slice(-2)) % 250 + 1}`
  const importFileName = `assets-${suffix}.csv`
  const systemImportFileName = `business-systems-${suffix}.csv`
  const importedSystemCode = `IMP_${suffix}`
  const importedSystemName = `导入业务系统_${suffix}`
  const groupAsset = `group-pw-${suffix}.example.com`
  const shanghaiOffice = `上海办事处_PW_${suffix}`
  const shenzhenOffice = `深圳办事处_PW_${suffix}`
  const inventoryTaskName = `TASK_PW_${suffix}`
  const remediationAttachmentName = `整改证明_PW_${suffix}.txt`
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

  await page.getByRole('button', { name: '网络资产', exact: true }).click()
  await page.getByRole('button', { name: '网络区域配置', exact: true }).click()
  const areaManagement = page.getByRole('dialog', { name: '网络区域配置' })
  for (const [name, code] of [[shanghaiOffice, '10.20.0.0/16'], [shenzhenOffice, '10.30.0.0/16']]) {
    await areaManagement.getByRole('button', { name: '新增网络区域', exact: true }).click()
    dialog = page.getByRole('dialog', { name: '新增网络区域' })
    await selectFormOption(page, dialog, '所属组织', `${organizationName}（${organizationCode}）`)
    await dialog.getByPlaceholder('如：上海办事处、生产云 VPC').fill(name)
    await dialog.getByPlaceholder('如：SH-OFFICE、10.20.0.0/16').fill(code)
    await dialog.getByRole('button', { name: '保存', exact: true }).click()
    await expect(page.getByText('网络区域已新增').last()).toBeVisible()
  }
  await expect(areaManagement.getByText(shanghaiOffice, { exact: true })).toBeVisible()
  await expect(areaManagement.getByText(shenzhenOffice, { exact: true })).toBeVisible()
  await page.keyboard.press('Escape')

  await page.getByRole('button', { name: '填报任务', exact: true }).click()
  await page.getByRole('button', { name: '创建任务', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '创建填报任务' })
  await dialog.getByPlaceholder('如：2026 年第三季度网络资产填报').fill(inventoryTaskName)
  await selectFormOption(page, dialog, '填报组织', `${organizationName}（${organizationCode}）`)
  const publicScope = dialog.locator('.el-radio-button').filter({ hasText: '仅公网资产' })
  await publicScope.click()
  await expect(publicScope.locator('input')).toBeChecked()
  const scopePreview = dialog.getByTestId('scope-preview')
  await expect(scopePreview.getByTestId('scope-preview-summary')).toContainText('1 家组织')
  await expect(scopePreview.getByTestId('scope-preview-summary')).toContainText('0 个业务系统')
  await expect(scopePreview.getByTestId('scope-preview-summary')).toContainText('0 项网络资产')
  await expect(scopePreview).toContainText('1 家组织在当前核查范围内暂无资源')
  await dialog.getByPlaceholder('填写本次填报范围、口径和注意事项').fill('端到端测试填报任务')
  await dialog.getByRole('button', { name: '创建并下发' }).click()
  await expect(page.getByText('填报任务已创建并下发')).toBeVisible()
  await expect(page.getByText(inventoryTaskName, { exact: true })).toBeVisible()
  const createdTaskRow = await ensureTaskExpanded(page, inventoryTaskName)
  const createdTaskDetailRow = createdTaskRow.locator('xpath=following-sibling::tr[1]')
  await expect(createdTaskDetailRow).toContainText('网络资产范围仅公网资产')

  await page.getByRole('button', { name: '用户和权限', exact: true }).click()
  await expect(page).toHaveURL(/\/users$/)
  await page.getByRole('button', { name: '新增用户', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '新增用户' })
  // 新增用户对话框在存在可管理组织时会自动添加第一条任职；只有空表单时才手动新增，
  // 避免完整回归已有其他测试组织时误加第二个任职，导致测试账号获得额外组织权限。
  if (await dialog.locator('.appointment-card').count() === 0) {
    await dialog.getByRole('button', { name: '新增任职组织', exact: true }).click()
  }
  await dialog.locator('.appointment-topline .el-select').first().click()
  await page.locator('.el-select-dropdown:visible').getByText(`${organizationName}（${organizationCode}）`, { exact: true }).first().click()
  await dialog.locator('.assignment-row .el-select').first().click()
  await page.locator('.el-select-dropdown:visible').getByText('子公司管理员', { exact: true }).first().click()
  await dialog.locator('.el-form-item').filter({ hasText: '用户名' }).locator('input').fill(subsidiaryUsername)
  await dialog.locator('.el-form-item').filter({ hasText: '姓名' }).locator('input').fill('端到端子公司管理员')
  await dialog.locator('.el-form-item').filter({ hasText: '临时密码' }).locator('input').fill(subsidiaryPassword)
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(page.getByText('用户已新增').last()).toBeVisible()

  await page.getByRole('button', { name: '退出', exact: true }).click()
  await login(page, subsidiaryUsername, subsidiaryPassword)
  await expect(page).toHaveURL(/\/profile(?:\?|$)/)
  await expect(page.getByText('首次登录必须修改临时密码')).toBeVisible()
  const formalPassword = `${subsidiaryPassword}Changed`
  await page.locator('input[autocomplete="current-password"]').fill(subsidiaryPassword)
  await page.locator('input[autocomplete="new-password"]').nth(0).fill(formalPassword)
  await page.locator('input[autocomplete="new-password"]').nth(1).fill(formalPassword)
  await page.getByRole('button', { name: '修改密码', exact: true }).click()
  await expect(page).toHaveURL(/\/dashboard$/)
  await expect(page.getByText(organizationName, { exact: true }).first()).toBeVisible()
  await expect(page.getByRole('heading', { name: '组织安全治理工作台', exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: '安全治理概览', exact: true })).toBeVisible()
  await expect(page.getByText('本组织整改效能', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '用户和权限', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '用户管理', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '网络区域配置' })).toHaveCount(0)

  await page.goto('/notifications')
  let inventoryNotificationCard = page.locator('.notification-card').filter({ hasText: inventoryTaskName }).first()
  await expect(inventoryNotificationCard).toContainText('收到新的填报任务')
  await inventoryNotificationCard.getByRole('button', { name: '查看处理', exact: true }).click()
  await expect(page).toHaveURL(/\/inventory-tasks\?task_id=[^&]+&organization_id=[^&]+$/)
  await expect(page.getByText('当前正在查看消息关联的填报任务及目标组织')).toBeVisible()
  await expect(page.getByText(inventoryTaskName, { exact: true })).toBeVisible()
  await expect(page.locator('.task-organization-table .el-table__body-wrapper tbody tr').filter({ hasText: organizationName }).first()).toBeVisible()
  await page.getByRole('button', { name: '查看全部任务', exact: true }).click()
  await expect(page).toHaveURL(/\/inventory-tasks$/)

  await page.getByRole('button', { name: '填报任务', exact: true }).click()
  await expect(page.getByRole('button', { name: '创建任务', exact: true })).toHaveCount(0)
  await expect(page.getByText(inventoryTaskName, { exact: true })).toBeVisible()
  await ensureTaskExpanded(page, inventoryTaskName)
  let qualityProgressRow = page.locator('.task-organization-table .el-table__body-wrapper tbody tr').filter({ hasText: organizationName }).first()
  await qualityProgressRow.getByRole('button', { name: '开始填报', exact: true }).click()
  await expect(page.getByText('已开始填报，请完善业务系统和网络资产后提交')).toBeVisible()
  await ensureTaskExpanded(page, inventoryTaskName)
  qualityProgressRow = page.locator('.task-organization-table .el-table__body-wrapper tbody tr').filter({ hasText: organizationName }).first()
  await qualityProgressRow.getByRole('button', { name: '提交', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '提交填报结果' })
  await expect(dialog.getByText('发现 2 项错误，修正后才能提交')).toBeVisible()
  await expect(dialog.getByText('尚未录入业务系统')).toBeVisible()
  await expect(dialog.getByText('尚未录入网络资产')).toBeVisible()
  await expect(dialog.getByRole('button', { name: '确认提交', exact: true })).toBeDisabled()
  await dialog.getByRole('button', { name: '取消', exact: true }).click()
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

  await page.getByRole('button', { name: '组织架构', exact: true }).click()
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

  await page.getByRole('button', { name: '批量导入', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '批量导入业务系统' })
  const systemCSV = [
    '系统编码,系统名称',
    `${importedSystemCode},${importedSystemName}`,
    `${systemCode},重复业务系统`,
    ',缺少系统编码',
  ].join('\n')
  await dialog.locator('input[type=file]').setInputFiles({ name: systemImportFileName, mimeType: 'text/csv', buffer: Buffer.from(systemCSV) })
  await dialog.getByRole('button', { name: '预检文件' }).click()
  await expect(page.getByText('文件预检完成，尚未写入台账')).toBeVisible()
  const systemImportResult = dialog.getByTestId('business-system-import-result')
  await expect(systemImportResult.getByText('可导入').locator('..').getByText('1', { exact: true })).toBeVisible()
  await expect(systemImportResult.getByText('重复').locator('..').getByText('1', { exact: true })).toBeVisible()
  await expect(systemImportResult.getByText('失败').locator('..').getByText('1', { exact: true })).toBeVisible()
  await expect(systemImportResult.getByText('系统编码已存在，未覆盖原记录')).toBeVisible()
  await expect(systemImportResult.getByText('系统编码不能为空', { exact: true })).toBeVisible()
  const importedSystemBeforeConfirmation = await page.evaluate(async (code) => {
    const response = await fetch(`/api/v1/business-systems?q=${encodeURIComponent(code)}&page=1&page_size=20`)
    const data = await response.json()
    return data.total
  }, importedSystemCode)
  expect(importedSystemBeforeConfirmation).toBe(0)
  await dialog.getByRole('button', { name: '确认导入' }).click()
  await expect(page.getByText('导入处理完成')).toBeVisible()
  await expect(systemImportResult.getByText('成功').locator('..').getByText('1', { exact: true })).toBeVisible()
  await dialog.getByRole('button', { name: '关闭' }).click()

  await page.getByRole('button', { name: '导入记录', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '业务系统导入记录' })
  const systemHistoryRow = dialog.locator('.el-table__body-wrapper tbody tr').filter({ hasText: systemImportFileName }).first()
  await expect(systemHistoryRow).toContainText(organizationName)
  await expect(systemHistoryRow).toContainText('总数 3 · 成功 1 · 重复 1 · 失败 1')
  await dialog.getByRole('button', { name: '关闭' }).click()
  const importedSystemCleanupStatus = await page.evaluate(async (code) => {
    const listResponse = await fetch(`/api/v1/business-systems?q=${encodeURIComponent(code)}&page=1&page_size=20`)
    const data = await listResponse.json()
    const imported = data.items.find((item: { code: string }) => item.code === code)
    if (!imported) return 404
    const deleteResponse = await fetch(`/api/v1/business-systems/${imported.id}`, { method: 'DELETE' })
    return deleteResponse.status
  }, importedSystemCode)
  expect(importedSystemCleanupStatus).toBe(204)

  let systemRow = page.locator('.data-table-panel .el-table__body-wrapper tbody tr').filter({ hasText: systemCode }).first()
  await systemRow.getByRole('button', { name: systemName, exact: true }).click()
  let systemDrawer = page.getByRole('dialog', { name: '业务系统详情' })
  await expect(systemDrawer.getByText(systemName, { exact: true })).toBeVisible()
  await expect(systemDrawer.getByText('暂未关联网络资产')).toBeVisible()
  await page.keyboard.press('Escape')

  await systemRow.getByRole('button', { name: '编辑', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '编辑业务系统' })
  await dialog.locator('.el-form-item').filter({ hasText: '负责人' }).first().locator('input').fill('端到端系统负责人')
  await dialog.locator('.el-form-item').filter({ hasText: '联系方式' }).locator('input').fill('13800000000')
  await dialog.getByRole('button', { name: '保存修改', exact: true }).click()
  await expect(page.getByText('业务系统已更新')).toBeVisible()
  systemRow = page.locator('.data-table-panel .el-table__body-wrapper tbody tr').filter({ hasText: systemCode }).first()
  await expect(systemRow.getByText('端到端系统负责人', { exact: true })).toBeVisible()

  const systemDownloadPromise = page.waitForEvent('download')
  await page.getByRole('button', { name: '导出台账', exact: true }).click()
  await page.locator('.el-dropdown-menu:visible').getByText('导出 XLSX', { exact: true }).click()
  const systemDownload = await systemDownloadPromise
  expect(systemDownload.suggestedFilename()).toBe('业务系统台账.xlsx')
  const systemCSVExport = await page.evaluate(async (expectedCode) => {
    const response = await fetch('/api/v1/business-systems/export?format=csv')
    return { status: response.status, contentType: response.headers.get('content-type'), count: response.headers.get('x-export-count'), text: await response.text(), expectedCode }
  }, systemCode)
  expect(systemCSVExport.status).toBe(200)
  expect(systemCSVExport.contentType).toContain('text/csv')
  expect(systemCSVExport.count).toBe('1')
  expect(systemCSVExport.text).toContain(systemCSVExport.expectedCode)
  expect(systemCSVExport.text).toContain('是否包含敏感个人信息')

  await page.getByRole('button', { name: '网络资产' }).click()
  await page.getByRole('button', { name: '新增资产' }).click()
  dialog = page.getByRole('dialog', { name: '新增网络资产' })
  await dialog.getByPlaceholder('如：portal.example.com').fill(domainAsset)
  await dialog.getByPlaceholder('便于识别的名称（可选）').fill(`门户资产_${suffix}`)
  await selectFormOption(page, dialog, '关联业务系统', `${systemName}（${systemCode}）`)
  await selectFormOption(page, dialog, '网络区域', `${shanghaiOffice}（10.20.0.0/16）`)
  await dialog.getByRole('button', { name: '确认新增' }).click()
  await expect(page.getByText('网络资产已新增')).toBeVisible()
  await expect(page.getByText(domainAsset, { exact: true })).toBeVisible()
  await expect(page.getByText(systemCode, { exact: true })).toBeVisible()
  await expect(page.getByText(shanghaiOffice, { exact: true })).toBeVisible()

  await page.getByPlaceholder('搜索资产值、名称、系统或负责人').fill(domainAsset)
  const assetDownloadPromise = page.waitForEvent('download')
  await page.getByRole('button', { name: '导出台账', exact: true }).click()
  await page.locator('.el-dropdown-menu:visible').getByText('导出 XLSX', { exact: true }).click()
  const assetDownload = await assetDownloadPromise
  expect(assetDownload.suggestedFilename()).toBe('网络资产台账.xlsx')
  const assetCSVExport = await page.evaluate(async ({ expectedAsset, forbiddenAsset }) => {
    const response = await fetch(`/api/v1/assets/export?format=csv&type=domain&status=active&q=${encodeURIComponent(expectedAsset)}`)
    return { status: response.status, contentType: response.headers.get('content-type'), count: response.headers.get('x-export-count'), text: await response.text(), expectedAsset, forbiddenAsset }
  }, { expectedAsset: domainAsset, forbiddenAsset: groupAsset })
  expect(assetCSVExport.status).toBe(200)
  expect(assetCSVExport.contentType).toContain('text/csv')
  expect(assetCSVExport.count).toBe('1')
  expect(assetCSVExport.text).toContain(assetCSVExport.expectedAsset)
  expect(assetCSVExport.text).toContain(shanghaiOffice)
  expect(assetCSVExport.text).not.toContain(assetCSVExport.forbiddenAsset)
  await page.getByPlaceholder('搜索资产值、名称、系统或负责人').clear()

  await page.getByRole('button', { name: '业务系统' }).click()
  systemRow = page.locator('.data-table-panel .el-table__body-wrapper tbody tr').filter({ hasText: systemCode }).first()
  await systemRow.getByRole('button', { name: systemName, exact: true }).click()
  systemDrawer = page.getByRole('dialog', { name: '业务系统详情' })
  await expect(systemDrawer.getByText(domainAsset, { exact: true })).toBeVisible()
  await expect(systemDrawer.getByText('关联网络资产（1）')).toBeVisible()
  await page.keyboard.press('Escape')

  const inUseSystemDeleteStatus = await page.evaluate(async (code) => {
    const listResponse = await fetch('/api/v1/business-systems')
    const data = await listResponse.json()
    const system = data.items.find((item: { code: string }) => item.code === code)
    const response = await fetch(`/api/v1/business-systems/${system.id}`, { method: 'DELETE' })
    return response.status
  }, systemCode)
  expect(inUseSystemDeleteStatus).toBe(409)

  await systemRow.getByRole('button', { name: '归档', exact: true }).click()
  await page.getByRole('dialog', { name: '归档确认' }).getByRole('button', { name: '确认', exact: true }).click()
  await expect(page.getByText('业务系统已归档')).toBeVisible()
  systemRow = page.locator('.data-table-panel .el-table__body-wrapper tbody tr').filter({ hasText: systemCode }).first()
  await expect(systemRow.getByText('已归档', { exact: true })).toBeVisible()
  await systemRow.getByRole('button', { name: '恢复', exact: true }).click()
  await page.getByRole('dialog', { name: '恢复确认' }).getByRole('button', { name: '确认', exact: true }).click()
  await expect(page.getByText('业务系统已恢复')).toBeVisible()

  await page.getByRole('button', { name: '网络资产' }).click()
  await page.getByRole('button', { name: '批量导入', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '批量导入网络资产' })
  const csv = [
    '资产类型,资产值,资产名称,业务系统编码,环境,网络范围,重要程度,负责人,备注',
    `ip,${importedIP},导入IP,${systemCode},生产,内网,重要,安全团队,合法行`,
    `domain,${domainAsset},重复域名,${systemCode},生产,公网,重要,安全团队,重复行`,
    'url,ftp://invalid.example.com,非法URL,,生产,公网,一般,安全团队,非法行',
  ].join('\n')
  await dialog.locator('input[type=file]').setInputFiles({ name: importFileName, mimeType: 'text/csv', buffer: Buffer.from(csv) })
  await dialog.getByRole('button', { name: '预检文件' }).click()
  await expect(page.getByText('文件预检完成，尚未写入台账')).toBeVisible()
  const result = dialog.getByTestId('import-result')
  await expect(result.getByText('可导入').locator('..').getByText('1', { exact: true })).toBeVisible()
  await expect(result.getByText('重复').locator('..').getByText('1', { exact: true })).toBeVisible()
  await expect(result.getByText('失败').locator('..').getByText('1', { exact: true })).toBeVisible()
  const importedAssetBeforeConfirmation = await page.evaluate(async (value) => {
    const response = await fetch(`/api/v1/assets?q=${encodeURIComponent(value)}&page=1&page_size=20`)
    const data = await response.json()
    return data.total
  }, importedIP)
  expect(importedAssetBeforeConfirmation).toBe(0)
  await dialog.getByRole('button', { name: '确认导入' }).click()
  await expect(page.getByText('导入处理完成')).toBeVisible()
  await expect(result.getByText('成功').locator('..').getByText('1', { exact: true })).toBeVisible()
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

  const excludedAssetStatus = await page.evaluate(async ({ organizationCode: code, value }) => {
    const organizationResponse = await fetch('/api/v1/organizations?page=1&page_size=100')
    if (!organizationResponse.ok) throw new Error(`organizations request failed: ${organizationResponse.status}`)
    const organizations = await organizationResponse.json()
    const organization = organizations.items.find((item: { code: string }) => item.code === code)
    if (!organization) throw new Error(`organization not found: ${code}`)

    const response = await fetch('/api/v1/assets', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        type: 'ip',
        value,
        name: '专项任务范围外内网资产',
        organization_id: organization.id,
        environment: 'production',
        network_scope: 'private',
        status: 'active',
        importance: 'normal',
        owner_name: '范围验证负责人',
      }),
    })
    return response.status
  }, { organizationCode, value: scopeExcludedIP })
  expect(excludedAssetStatus).toBe(201)

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
  await expect(page).toHaveURL(/\/users$/)
  await expect(page.getByRole('heading', { name: '用户管理' })).toBeVisible()
  await expect(page.getByRole('tab', { name: '角色与权限' })).toHaveCount(0)
  await expect(page.getByText('本人资料请到账户设置维护')).toBeVisible()

  await page.getByRole('button', { name: '填报任务', exact: true }).click()
  let taskRow = await ensureTaskExpanded(page, inventoryTaskName)
  let progressRow = page.locator('.task-organization-table .el-table__body-wrapper tbody tr').filter({ hasText: organizationName }).first()
  await progressRow.getByRole('button', { name: '提交', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '提交填报结果' })
  await expect(dialog.getByText('检查通过，可以提交')).toBeVisible()
  await expect(dialog.getByText('1 个系统 / 1 项资产')).toBeVisible()
  await dialog.getByPlaceholder('可填写数据范围、统计口径或需要集团关注的事项').fill('首次提交，请集团审核')
  await dialog.getByRole('button', { name: '确认提交', exact: true }).click()
  await expect(page.getByText('填报结果已提交，等待集团审核')).toBeVisible()

  await page.getByRole('button', { name: '退出', exact: true }).click()
  await login(page, adminUsername, adminPassword!)

  await page.goto('/notifications')
  inventoryNotificationCard = page.locator('.notification-card').filter({ hasText: inventoryTaskName }).first()
  await expect(inventoryNotificationCard).toContainText('填报任务待审核')
  await expect(inventoryNotificationCard).toContainText(organizationName)
  await inventoryNotificationCard.getByRole('button', { name: '查看处理', exact: true }).click()
  await expect(page).toHaveURL(/\/inventory-tasks\?task_id=[^&]+&organization_id=[^&]+$/)
  await expect(page.getByText('当前正在查看消息关联的填报任务及目标组织')).toBeVisible()
  await expect(page.locator('.task-organization-table .el-table__body-wrapper tbody tr').filter({ hasText: organizationName }).first()).toContainText('待审核')
  await page.getByRole('button', { name: '查看全部任务', exact: true }).click()

  const populatedScopePreview = await page.evaluate(async ({ code }) => {
    const organizationsResponse = await fetch('/api/v1/organizations?page=1&page_size=100')
    const organizations = await organizationsResponse.json()
    const organization = organizations.items.find((item: { code: string }) => item.code === code)
    if (!organization) throw new Error(`organization not found: ${code}`)
    const response = await fetch('/api/v1/inventory-tasks/scope-preview', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        organization_ids: [organization.id],
        required_fields: ['business_systems', 'network_assets'],
        asset_network_scope: 'public',
      }),
    })
    return { status: response.status, body: await response.json() }
  }, { code: organizationCode })
  expect(populatedScopePreview.status).toBe(200)
  expect(populatedScopePreview.body.scope_preview).toMatchObject({
    organization_count: 1, system_count: 1, asset_count: 1, empty_organization_count: 0,
  })
  expect(populatedScopePreview.body.scope_preview.items).toHaveLength(1)
  expect(populatedScopePreview.body.scope_preview.items[0]).toMatchObject({
    organization_code: organizationCode, system_count: 1, asset_count: 1,
  })
  await page.getByRole('button', { name: '填报任务', exact: true }).click()
  taskRow = await ensureTaskExpanded(page, inventoryTaskName)
  progressRow = page.locator('.task-organization-table .el-table__body-wrapper tbody tr').filter({ hasText: organizationName }).first()
  await expect(progressRow.getByText('1 个系统 / 1 项资产')).toBeVisible()
  await progressRow.getByRole('button', { name: '查看差异', exact: true }).click()
  let diffDrawer = page.locator('.el-drawer').filter({ hasText: '填报提交差异' }).last()
  await expect(diffDrawer.getByText('首次提交，无历史基线')).toBeVisible()
  await expect(diffDrawer.locator('.submission-diff-summary .added strong')).toHaveText('2')
  await page.keyboard.press('Escape')
  await expect(diffDrawer).toBeHidden()
  await progressRow.getByRole('button', { name: '退回', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '退回填报结果' })
  const reviewComment = `请核实资产负责人_PW_${suffix}`
  await dialog.getByPlaceholder('请明确说明本次退回的总体原因').fill('请补充统计口径说明')
  const reviewAssetRow = dialog.locator('.el-table__body-wrapper tbody tr').filter({ hasText: domainAsset }).first()
  await reviewAssetRow.getByPlaceholder('针对该条数据填写整改要求（可选）').fill(reviewComment)
  await dialog.getByRole('button', { name: '确认退回', exact: true }).click()
  await expect(page.getByText('填报结果已退回修改')).toBeVisible()

  await page.getByRole('button', { name: '退出', exact: true }).click()
  await login(page, subsidiaryUsername, formalPassword)
  await page.goto('/notifications')
  inventoryNotificationCard = page.locator('.notification-card').filter({ hasText: inventoryTaskName }).first()
  await expect(inventoryNotificationCard).toContainText('填报任务已退回')
  await inventoryNotificationCard.getByRole('button', { name: '查看处理', exact: true }).click()
  await expect(page.getByText('当前正在查看消息关联的填报任务及目标组织')).toBeVisible()
  taskRow = await ensureTaskExpanded(page, inventoryTaskName)
  progressRow = page.locator('.task-organization-table .el-table__body-wrapper tbody tr').filter({ hasText: organizationName }).first()
  await expect(progressRow.getByText('退回：请补充统计口径说明')).toBeVisible()
  await progressRow.getByRole('button', { name: '审核意见', exact: true }).click()
  const reviewDialog = page.getByRole('dialog', { name: '逐条审核意见' })
  await expect(reviewDialog).toContainText(reviewComment)
  await expect(reviewDialog).toContainText(domainAsset)
  await reviewDialog.getByRole('button', { name: '关闭', exact: true }).click()
  await progressRow.getByRole('button', { name: '提交', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '提交填报结果' })
  await expect(dialog.getByText('检查通过，可以提交')).toBeVisible()
  await dialog.getByPlaceholder('可填写数据范围、统计口径或需要集团关注的事项').fill('已补充统计口径并重新提交')
  await dialog.getByRole('button', { name: '确认提交', exact: true }).click()
  await expect(page.getByText('填报结果已提交，等待集团审核')).toBeVisible()

  await page.getByRole('button', { name: '退出', exact: true }).click()
  await login(page, adminUsername, adminPassword!)
  await page.getByRole('button', { name: '填报任务', exact: true }).click()
  taskRow = await ensureTaskExpanded(page, inventoryTaskName)
  progressRow = page.locator('.task-organization-table .el-table__body-wrapper tbody tr').filter({ hasText: organizationName }).first()
  await progressRow.getByRole('button', { name: '查看差异', exact: true }).click()
  diffDrawer = page.locator('.el-drawer').filter({ hasText: '填报提交差异' }).last()
  await expect(diffDrawer.getByText(/第 2 版/).first()).toBeVisible()
  await expect(diffDrawer.getByText(new RegExp(`${inventoryTaskName} / 第 1 版`))).toBeVisible()
  await expect(diffDrawer.locator('.submission-diff-summary .added strong')).toHaveText('0')
  await expect(diffDrawer.locator('.submission-diff-summary .modified strong')).toHaveText('0')
  await expect(diffDrawer.locator('.submission-diff-summary .removed strong')).toHaveText('0')
  await page.keyboard.press('Escape')
  await expect(diffDrawer).toBeHidden()
  await progressRow.getByRole('button', { name: '通过', exact: true }).click()
  await page.getByRole('dialog', { name: '审核通过' }).getByRole('button', { name: '通过', exact: true }).click()
  await expect(page.getByText('填报结果已审核通过')).toBeVisible()
  const completedTaskRow = page.locator('.data-table-panel > .el-table .el-table__body-wrapper tbody tr').filter({ hasText: inventoryTaskName }).first()
  await expect(completedTaskRow.getByText('已完成', { exact: true })).toBeVisible()

  const taskDownloadPromise = page.waitForEvent('download')
  await completedTaskRow.getByRole('button', { name: '导出', exact: true }).click()
  await page.locator('.el-dropdown-menu:visible').getByText('导出 XLSX', { exact: true }).click()
  const taskDownload = await taskDownloadPromise
  expect(taskDownload.suggestedFilename()).toBe(`填报任务结果-${inventoryTaskName}.xlsx`)
  const taskCSVExport = await page.evaluate(async ({ taskName, expectedOrganization }) => {
    const listResponse = await fetch('/api/v1/inventory-tasks')
    const list = await listResponse.json()
    const task = list.items.find((item: { name: string }) => item.name === taskName)
    const response = await fetch(`/api/v1/inventory-tasks/${task.id}/export?format=csv`)
    return { status: response.status, contentType: response.headers.get('content-type'), count: response.headers.get('x-export-count'), text: await response.text(), expectedOrganization }
  }, { taskName: inventoryTaskName, expectedOrganization: organizationName })
  expect(taskCSVExport.status).toBe(200)
  expect(taskCSVExport.contentType).toContain('text/csv')
  expect(taskCSVExport.count).toBe('1')
  expect(taskCSVExport.text).toContain(taskCSVExport.expectedOrganization)
  expect(taskCSVExport.text).toContain('已通过')
  expect(taskCSVExport.text).toContain('已补充统计口径并重新提交')

  const securityFixture = await page.evaluate(async ({ expectedOrganizationCode, expectedAsset, expectedSystemCode, suffixValue }) => {
    const [organizationsResponse, assetsResponse, systemsResponse] = await Promise.all([
      fetch('/api/v1/organizations'), fetch('/api/v1/assets'), fetch('/api/v1/business-systems'),
    ])
    const organizations = await organizationsResponse.json()
    const assets = await assetsResponse.json()
    const systems = await systemsResponse.json()
    const organization = organizations.items.find((item: { code: string }) => item.code === expectedOrganizationCode)
    const asset = assets.items.find((item: { value: string }) => item.value === expectedAsset)
    const system = systems.items.find((item: { code: string }) => item.code === expectedSystemCode)
    if (!organization || !asset || !system) throw new Error('security work order fixture references not found')

    const findingResponse = await fetch('/api/v1/security-findings', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        title: `未授权访问漏洞_PW_${suffixValue}`, severity: 'high', status: 'open',
        assignee_organization_id: organization.id, asset_id: asset.id, business_system_id: system.id,
        vulnerability_type: '未授权访问', description: '互联网入口未校验登录态即可访问敏感功能。',
        evidence: 'E2E 脱敏验证记录', remediation_advice: '补充统一身份认证与服务端权限校验。',
        source: 'manual', discovered_at: new Date().toISOString(),
      }),
    })
    if (!findingResponse.ok) throw new Error(`create security finding failed: ${findingResponse.status} ${await findingResponse.text()}`)
    const finding = (await findingResponse.json()).item
    const dueAt = new Date()
    dueAt.setDate(dueAt.getDate() + 14)
    const orderResponse = await fetch('/api/v1/security-work-orders', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        title: `互联网资产高危漏洞整改_PW_${suffixValue}`, assignee_organization_id: organization.id,
        description: '集团渗透测试发现高危未授权访问问题。',
        remediation_requirement: '完成权限校验修复、自测并提交整改说明。', due_at: dueAt.toISOString(), finding_ids: [finding.id],
      }),
    })
    if (!orderResponse.ok) throw new Error(`create security work order failed: ${orderResponse.status} ${await orderResponse.text()}`)
    const order = (await orderResponse.json()).item
    const issueResponse = await fetch(`/api/v1/security-work-orders/${order.id}/issue`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ comment: 'E2E 集团下发' }),
    })
    if (!issueResponse.ok) throw new Error(`issue security work order failed: ${issueResponse.status} ${await issueResponse.text()}`)
    return { id: order.id as string, number: order.number as string, title: order.title as string }
  }, { expectedOrganizationCode: organizationCode, expectedAsset: domainAsset, expectedSystemCode: systemCode, suffixValue: suffix })
  const workOrderNumber = securityFixture.number

  await page.getByRole('button', { name: '安全工单', exact: true }).click()
  await expect(page).toHaveURL(/\/security-work-orders$/)
  let securityRow = page.locator('.security-panel .el-table__body-wrapper tbody tr').filter({ hasText: securityFixture.title }).first()
  await expect(securityRow).toContainText('已下发')
  await expect(securityRow).toContainText(organizationName)

  await page.getByRole('button', { name: '退出', exact: true }).click()
  await login(page, subsidiaryUsername, formalPassword)

  await page.goto('/notifications')
  await expect(page.getByRole('heading', { name: '消息中心', exact: true })).toBeVisible()
  let notificationCard = page.locator('.notification-card').filter({ hasText: securityFixture.title }).first()
  await expect(notificationCard).toContainText('收到新的安全工单')
  await notificationCard.getByRole('button', { name: '标记已读', exact: true }).click()
  await expect(notificationCard).toContainText('已读')

  await page.getByRole('button', { name: '安全工单', exact: true }).click()
  securityRow = page.locator('.security-panel .el-table__body-wrapper tbody tr').filter({ hasText: securityFixture.title }).first()
  await expect(securityRow).toBeVisible()
  await securityRow.getByRole('button', { name: '接收', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '接收安全工单' })
  await dialog.getByRole('button', { name: '确认接收安全工单', exact: true }).click()
  await expect(page.getByText('安全工单已接收')).toBeVisible()

  securityRow = page.locator('.security-panel .el-table__body-wrapper tbody tr').filter({ hasText: securityFixture.title }).first()
  await securityRow.getByRole('button', { name: '更新进度', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '更新整改进度' })
  await dialog.locator('textarea').first().fill('已完成权限中间件修复，正在开展回归测试。')
  await dialog.getByRole('button', { name: '确认更新整改进度', exact: true }).click()
  await expect(page.getByText('整改进度已更新')).toBeVisible()

  securityRow = page.locator('.security-panel .el-table__body-wrapper tbody tr').filter({ hasText: securityFixture.title }).first()
  await securityRow.getByRole('button', { name: '详情', exact: true }).click()
  let workOrderDrawer = page.getByRole('dialog', { name: '安全工单详情' })
  await workOrderDrawer.locator('input[type=file]').setInputFiles({ name: remediationAttachmentName, mimeType: 'text/plain', buffer: Buffer.from('PW remediation evidence') })
  await workOrderDrawer.getByPlaceholder('附件说明（可选）').fill('子公司整改证明')
  await workOrderDrawer.getByRole('button', { name: '上传附件', exact: true }).click()
  await expect(page.getByText('工单附件已上传')).toBeVisible()
  await expect(workOrderDrawer.getByText(remediationAttachmentName, { exact: true })).toBeVisible()
  await workOrderDrawer.locator('.el-drawer__close-btn').click()

  securityRow = page.locator('.security-panel .el-table__body-wrapper tbody tr').filter({ hasText: securityFixture.title }).first()
  await securityRow.getByRole('button', { name: '提交复测', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '提交集团复测' })
  await dialog.locator('textarea').first().fill('已上线权限校验修复，自测未再发现未授权访问。')
  await dialog.getByRole('button', { name: '确认提交集团复测', exact: true }).click()
  await expect(page.getByText('已提交集团复测')).toBeVisible()

  await page.getByRole('button', { name: '退出', exact: true }).click()
  await login(page, adminUsername, adminPassword!)

  await page.goto('/notifications')
  notificationCard = page.locator('.notification-card').filter({ hasText: securityFixture.title }).first()
  await expect(notificationCard).toContainText('安全工单待复测')
  await notificationCard.getByRole('button', { name: '查看处理', exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/security-work-orders\\?work_order_id=${securityFixture.id}$`))
  let notificationWorkOrderDrawer = page.getByRole('dialog', { name: '安全工单详情' })
  await expect(notificationWorkOrderDrawer).toContainText(securityFixture.title)
  await notificationWorkOrderDrawer.locator('.el-drawer__close-btn').click()

  securityRow = page.locator('.security-panel .el-table__body-wrapper tbody tr').filter({ hasText: securityFixture.title }).first()
  await securityRow.getByRole('button', { name: '退回', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '复测不通过并退回' })
  await dialog.locator('textarea').first().fill('复测发现一个接口仍可绕过权限校验，请继续整改。')
  await dialog.getByRole('button', { name: '确认复测不通过并退回', exact: true }).click()
  await expect(page.getByText('安全工单已退回')).toBeVisible()

  await page.getByRole('button', { name: '退出', exact: true }).click()
  await login(page, subsidiaryUsername, formalPassword)
  await page.getByRole('button', { name: '安全工单', exact: true }).click()
  securityRow = page.locator('.security-panel .el-table__body-wrapper tbody tr').filter({ hasText: securityFixture.title }).first()
  await expect(securityRow).toContainText('已退回')
  await securityRow.getByRole('button', { name: '提交复测', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '提交集团复测' })
  await dialog.locator('textarea').first().fill('已补齐遗漏接口的服务端鉴权并完成全量回归。')
  await dialog.getByRole('button', { name: '确认提交集团复测', exact: true }).click()
  await expect(page.getByText('已提交集团复测')).toBeVisible()

  await page.getByRole('button', { name: '退出', exact: true }).click()
  await login(page, adminUsername, adminPassword!)
  await page.getByRole('button', { name: '安全工单', exact: true }).click()
  securityRow = page.locator('.security-panel .el-table__body-wrapper tbody tr').filter({ hasText: securityFixture.title }).first()
  await securityRow.getByRole('button', { name: '复测通过', exact: true }).click()
  dialog = page.getByRole('dialog', { name: '复测通过并关闭' })
  await dialog.locator('textarea').first().fill('集团复测通过，未授权访问问题已修复。')
  await dialog.getByRole('button', { name: '确认复测通过并关闭', exact: true }).click()
  await expect(page.getByText('安全工单已关闭')).toBeVisible()
  securityRow = page.locator('.security-panel .el-table__body-wrapper tbody tr').filter({ hasText: securityFixture.title }).first()
  await expect(securityRow).toContainText('已关闭')
  await securityRow.getByRole('button', { name: '详情', exact: true }).click()
  workOrderDrawer = page.getByRole('dialog', { name: '安全工单详情' })
  await expect(workOrderDrawer).toContainText('集团复测通过，未授权访问问题已修复。')
  await expect(workOrderDrawer).toContainText('复测退回')
  await expect(workOrderDrawer.getByText(remediationAttachmentName, { exact: true })).toBeVisible()
  const attachmentDownloadPromise = page.waitForEvent('download')
  await workOrderDrawer.getByText(remediationAttachmentName, { exact: true }).click()
  const attachmentDownload = await attachmentDownloadPromise
  expect(attachmentDownload.suggestedFilename()).toBe(remediationAttachmentName)
  await workOrderDrawer.locator('tr').filter({ hasText: remediationAttachmentName }).getByRole('button', { name: '删除', exact: true }).click()
  await page.getByRole('dialog', { name: '删除工单附件' }).getByRole('button', { name: '删除', exact: true }).click()
  await expect(page.getByText('工单附件已删除')).toBeVisible()
  await expect(workOrderDrawer.getByText(remediationAttachmentName, { exact: true })).toHaveCount(0)
  await workOrderDrawer.locator('.el-drawer__close-btn').click()

  await page.getByRole('button', { name: '审计日志', exact: true }).click()
  await expect(page).toHaveURL(/\/audit-logs$/)
  await expect(page.getByRole('heading', { name: '审计日志', exact: true })).toBeVisible()
  await page.getByPlaceholder('搜索摘要、资源 ID、请求 ID、人员或组织').fill(inventoryTaskName)
  await page.getByRole('button', { name: '查询', exact: true }).click()
  const auditRow = page.locator('.audit-table-panel .el-table__body-wrapper tbody tr').filter({ hasText: '导出填报结果' }).first()
  await expect(auditRow).toBeVisible()
  await auditRow.getByRole('button', { name: '查看', exact: true }).click()
  const auditDrawer = page.getByRole('dialog', { name: '审计日志详情' })
  await expect(auditDrawer).toBeVisible()
  await expect(auditDrawer).toContainText('导出填报结果')
  await expect(auditDrawer).toContainText(inventoryTaskName)
  await auditDrawer.locator('.el-drawer__close-btn').click()
  await page.getByPlaceholder('搜索摘要、资源 ID、请求 ID、人员或组织').fill(workOrderNumber)
  await page.getByRole('button', { name: '查询', exact: true }).click()
  const workOrderAuditRow = page.locator('.audit-table-panel .el-table__body-wrapper tbody tr').filter({ hasText: '关闭安全工单' }).first()
  await expect(workOrderAuditRow).toContainText(workOrderNumber)
})

test('集团创建安全测试批次并批量导入授权范围内漏洞', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  test.setTimeout(180_000)

  const suffix = Date.now().toString()
  const batchName = `安全测试批次_PW_${suffix}`
  const assetValue = `scope-pw-${suffix}.example.com`
  const outOfScopeValue = `out-of-scope-pw-${suffix}.example.com`
  const scopeIP = `198.51.100.${Number(suffix.slice(-3)) % 250 + 1}`
  const importFindingTitle = `批量导入漏洞_PW_${suffix}`
  const linkedFindingTitle = `端口标识待关联漏洞_PW_${suffix}`
  const reportName = `测试报告_PW_${suffix}.txt`
  const organizationCode = `SCOPE_PW_${suffix}`
  const organizationName = `测试范围子公司_PW_${suffix}`

  await page.goto('/security-test-batches')
  await login(page, adminUsername, adminPassword!)
  await expect(page).toHaveURL(/\/security-test-batches$/)
  await expect(page.getByRole('heading', { name: '安全测试', exact: true })).toBeVisible()

  await page.evaluate(async ({ value, scopeIP, outOfScopeValue, code, name }) => {
    const organizationsResponse = await fetch('/api/v1/organizations')
    if (!organizationsResponse.ok) throw new Error(`organizations request failed: ${organizationsResponse.status}`)
    const organizations = await organizationsResponse.json()
    const group = organizations.items.find((item: { type: string }) => item.type === 'group')
    const organizationResponse = await fetch('/api/v1/organizations', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ parent_id: group.id, code, name, short_name: '范围测试', type: 'subsidiary', manager: 'PW', contact: '', notes: 'Playwright' }),
    })
    if (!organizationResponse.ok) throw new Error(`create scope organization failed: ${organizationResponse.status}`)
    const organization = (await organizationResponse.json()).item
    for (const asset of [
      { type: 'domain', value, name: `授权范围测试域名_${Date.now()}`, organization_id: organization.id },
      { type: 'ip', value: scopeIP, name: `授权范围测试主机_${Date.now()}`, organization_id: organization.id },
      { type: 'domain', value: outOfScopeValue, name: `批次外测试资产_${Date.now()}`, organization_id: group.id },
    ]) {
      const response = await fetch('/api/v1/assets', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          ...asset,
          environment: 'production', network_scope: 'public', status: 'active', importance: 'important',
        }),
      })
      if (!response.ok) throw new Error(`create scope asset failed: ${response.status}`)
    }
  }, { value: assetValue, scopeIP, outOfScopeValue, code: organizationCode, name: organizationName })
  await page.reload()

  await page.getByTestId('create-security-test-batch').click()
  const dialog = page.getByRole('dialog', { name: '新建安全测试批次' })
  await dialog.getByPlaceholder('如：2026年第三季度互联网资产渗透测试').fill(batchName)
  await dialog.getByPlaceholder('说明授权人、授权依据、允许的测试方式与边界').fill('经集团信息安全负责人授权，仅对本批次资产执行非破坏性渗透测试。')
  await dialog.getByTestId('scope-add-organization').click()
  const scopePicker = page.getByRole('dialog', { name: '按组织纳入公网资产' })
  await scopePicker.getByPlaceholder('搜索组织').fill(organizationName)
  await scopePicker.getByRole('button', { name: '搜索', exact: true }).click()
  const organizationRow = scopePicker.locator('.el-table__body-wrapper tbody tr').filter({ hasText: organizationName }).first()
  await expect(organizationRow).toBeVisible()
  await organizationRow.locator('label.el-checkbox').click()
  await scopePicker.getByTestId('apply-bulk-security-test-scope').click()
  await expect(page.getByText('已纳入 2 项公网资产', { exact: false })).toBeVisible()
  const selectedScope = dialog.getByTestId('selected-security-test-assets')
  await expect(selectedScope).toContainText(assetValue)
  await expect(selectedScope).toContainText(scopeIP)
  await expect(selectedScope).not.toContainText(outOfScopeValue)
  await dialog.getByTestId('save-security-test-batch').click()
  await expect(page.getByText('安全测试批次已创建')).toBeVisible()

  let row = page.locator('.el-table__body-wrapper tbody tr').filter({ hasText: batchName }).first()
  await expect(row).toContainText('草稿')
  await expect(row).toContainText('2 项')
  await row.getByRole('button', { name: '授权', exact: true }).click()
  let prompt = page.getByRole('dialog', { name: '确认授权测试' })
  await prompt.getByRole('button', { name: '确认', exact: true }).click()
  await expect(page.getByText('批次已授权')).toBeVisible()

  row = page.locator('.el-table__body-wrapper tbody tr').filter({ hasText: batchName }).first()
  await expect(row).toContainText('已授权')
  await expect(row.getByRole('button', { name: '编辑', exact: true })).toHaveCount(0)
  await row.getByRole('button', { name: '开始', exact: true }).click()
  prompt = page.getByRole('dialog', { name: '开始安全测试' })
  await prompt.getByRole('button', { name: '确认', exact: true }).click()
  await expect(page.getByText('测试已开始')).toBeVisible()

  row = page.locator('.el-table__body-wrapper tbody tr').filter({ hasText: batchName }).first()
  await row.getByRole('button', { name: '完成', exact: true }).click()
  prompt = page.getByRole('dialog', { name: '完成安全测试' })
  await prompt.locator('input').fill('已按授权范围完成测试。')
  await prompt.getByRole('button', { name: '确认', exact: true }).click()
  await expect(page.getByText('测试已完成', { exact: true })).toBeVisible()

  row = page.locator('.el-table__body-wrapper tbody tr').filter({ hasText: batchName }).first()
  await row.getByRole('button', { name: '详情', exact: true }).click()
  const drawer = page.getByRole('dialog', { name: '安全测试批次详情' })
  await expect(drawer).toContainText(assetValue)
  await expect(drawer).toContainText('授权范围快照')
  await expect(drawer).toContainText('完成测试')
  await drawer.getByTestId('security-test-batch-attachment-input').setInputFiles({
    name: reportName, mimeType: 'text/plain', buffer: Buffer.from(`安全测试报告 ${suffix}`),
  })
  await drawer.getByRole('button', { name: '上传附件', exact: true }).click()
  await expect(page.getByText('测试附件已上传', { exact: true })).toBeVisible()
  const attachmentTable = drawer.getByTestId('security-test-batch-attachments')
  const attachmentRow = attachmentTable.locator('.el-table__body-wrapper tbody tr').filter({ hasText: reportName }).first()
  await expect(attachmentRow).toContainText('测试报告')
  const reportDownloadPromise = page.waitForEvent('download')
  await attachmentRow.getByRole('button', { name: reportName, exact: true }).click()
  const reportDownload = await reportDownloadPromise
  expect(reportDownload.suggestedFilename()).toBe(reportName)
  await attachmentRow.getByRole('button', { name: '删除', exact: true }).click()
  const deleteAttachmentDialog = page.getByRole('dialog', { name: '删除测试附件' })
  await deleteAttachmentDialog.getByRole('button', { name: '删除', exact: true }).click()
  await expect(page.getByText('测试附件已删除', { exact: true })).toBeVisible()
  await expect(attachmentTable).toHaveCount(0)
  await expect(drawer).toContainText('上传附件')
  await expect(drawer).toContainText('删除附件')
  await drawer.locator('.el-drawer__close-btn').click()

  await page.getByRole('button', { name: '安全工单', exact: true }).click()
  await page.getByRole('tab', { name: '漏洞与安全发现', exact: true }).click()
  await page.getByRole('button', { name: '批量导入漏洞', exact: true }).click()
  const importDialog = page.getByRole('dialog', { name: '批量导入漏洞与安全发现' })
  await importDialog.locator('.el-select').first().click()
  await page.locator('.el-select-dropdown:visible').getByText(new RegExp(batchName)).click()
  const csv = [
    '资产类型,资产值,漏洞标题,严重等级,漏洞类型,漏洞描述,验证证据,整改建议,发现时间',
    `domain,${assetValue},${importFindingTitle},高危,配置风险,授权范围内测试发现,测试证据,完成安全配置加固,2026-07-22 10:00:00`,
    `ip,${scopeIP}:443,${linkedFindingTitle},中危,端口暴露,扫描器以IP端口输出,测试证据,核查端口服务并完成加固,2026-07-22 10:05:00`,
  ].join('\n')
  await importDialog.locator('input[type=file]').setInputFiles({ name: `findings-${suffix}.csv`, mimeType: 'text/csv', buffer: Buffer.from(csv) })
  await importDialog.getByRole('button', { name: '开始导入', exact: true }).click()
  await expect(page.getByText(/导入完成：成功 1 条，待关联 1 条，失败 0 条，重复 0 条/)).toBeVisible()
  await expect(importDialog.getByText('待关联', { exact: true })).toBeVisible()
  await importDialog.getByRole('button', { name: '处理 1 条待关联漏洞', exact: true }).click()
  await expect(page.getByRole('tab', { name: /待关联漏洞/ })).toHaveAttribute('aria-selected', 'true')
  const candidateRow = page.locator('.security-panel .el-table__body-wrapper tbody tr').filter({ hasText: linkedFindingTitle }).first()
  await expect(candidateRow).toContainText(`${scopeIP}:443`)
  await expect(candidateRow).toContainText(scopeIP)
  await expect(candidateRow).toContainText('IP 与端口主机匹配 90%')
  await candidateRow.getByRole('button', { name: '关联资产', exact: true }).click()
  const candidateDialog = page.getByRole('dialog', { name: '关联漏洞到测试范围资产' })
  await expect(candidateDialog).toContainText('只允许关联当前安全测试批次中的授权资产')
  await expect(candidateDialog).toContainText(`${scopeIP}:443`)
  const candidateSelect = candidateDialog.locator('.el-form-item').filter({ hasText: '关联授权资产（必选）' }).locator('.el-select')
  await candidateSelect.click()
  const candidateOptions = page.locator('.el-select-dropdown:visible')
  await expect(candidateOptions).toContainText(scopeIP)
  await expect(candidateOptions).toContainText(assetValue)
  await expect(candidateOptions).not.toContainText(outOfScopeValue)
  await page.keyboard.press('Escape')
  await candidateDialog.getByPlaceholder('可记录扫描器标识与台账资产的对应依据').fill('确认扫描器端口标识属于该授权主机')
  await candidateDialog.getByRole('button', { name: '确认关联并生成安全发现', exact: true }).click()
  await expect(page.getByText(/已生成安全发现 F/)).toBeVisible()
  await page.getByRole('tab', { name: '漏洞与安全发现', exact: true }).click()
  const linkedFindingRow = page.locator('.security-panel .el-table__body-wrapper tbody tr').filter({ hasText: linkedFindingTitle }).first()
  await expect(linkedFindingRow).toContainText(scopeIP)
  await expect(linkedFindingRow).toContainText(batchName)
  const findingRow = page.locator('.security-panel .el-table__body-wrapper tbody tr').filter({ hasText: importFindingTitle }).first()
  await expect(findingRow).toContainText(batchName)
  await expect(findingRow).toContainText(organizationName)
  await findingRow.getByRole('button', { name: '详情', exact: true }).click()
  const findingDrawer = page.getByRole('dialog', { name: '安全发现详情' })
  await expect(findingDrawer).toContainText(importFindingTitle)
  await expect(findingDrawer).toContainText(assetValue)
  await findingDrawer.getByRole('button', { name: '接受风险', exact: true }).click()
  const riskDialog = page.getByRole('dialog', { name: '接受风险' })
  await riskDialog.getByPlaceholder('说明暂不整改的业务原因、补偿措施和责任判断').fill('业务系统正在进行重大版本切换，当前采用访问控制和专项监控作为补偿措施。')
  await riskDialog.locator('.el-date-editor input').fill('2026-08-22 18:00:00')
  await riskDialog.locator('.el-date-editor input').press('Enter')
  await riskDialog.getByRole('button', { name: '确认接受风险', exact: true }).click()
  await expect(page.getByText('风险接受已保存', { exact: true })).toBeVisible()
  await expect(findingDrawer).toContainText('风险接受中')
  await expect(findingDrawer).toContainText('2026/8/22')

  const findingID = await page.evaluate(async (title) => {
    const response = await fetch('/api/v1/security-findings')
    const data = await response.json()
    return data.items.find((item: { title: string }) => item.title === title)?.id as string
  }, importFindingTitle)
  const acceptedAudit = await page.evaluate(async (id) => {
    const response = await fetch(`/api/v1/audit-logs?resource_type=security_finding&q=${encodeURIComponent(id)}&page_size=100`)
    if (!response.ok) throw new Error(`audit request failed: ${response.status}`)
    return response.json()
  }, findingID)
  expect(acceptedAudit.items.some((item: { action: string }) => item.action === 'security_finding.accept_risk')).toBeTruthy()

  await findingDrawer.getByRole('button', { name: '复核并延长有效期', exact: true }).click()
  const reviewDialog = page.getByRole('dialog', { name: '复核风险' })
  await reviewDialog.getByPlaceholder('记录本次复核结论、现有补偿措施及继续接受风险的理由').fill('已复核补偿措施和业务切换计划，同意继续接受风险至下一复核节点。')
  await reviewDialog.locator('.el-date-editor input').fill('2026-09-22 18:00:00')
  await reviewDialog.locator('.el-date-editor input').press('Enter')
  await reviewDialog.getByRole('button', { name: '确认复核风险', exact: true }).click()
  await expect(page.getByText('风险复核已完成', { exact: true })).toBeVisible()
  await expect(findingDrawer).toContainText('2026/9/22')

  await findingDrawer.getByRole('button', { name: '撤销风险接受', exact: true }).click()
  const revokePrompt = page.getByRole('dialog', { name: '撤销风险接受' })
  await revokePrompt.getByRole('button', { name: '撤销', exact: true }).click()
  await expect(page.getByText('风险接受已撤销', { exact: true })).toBeVisible()
  await expect(findingDrawer).toContainText('待处置')
})

test('集团管理员可从回收站恢复误删网络资产并保留审计记录', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  test.setTimeout(120_000)

  await page.goto('/login')
  await login(page, adminUsername, adminPassword!)
  await page.goto('/assets')
  await expect(page).toHaveURL(/\/assets\/?$/)

  const suffix = Date.now().toString()
  const assetValue = `restore-pw-${suffix}.example.com`
  const created = await page.evaluate(async (value) => {
    const organizationResponse = await fetch('/api/v1/organizations')
    if (!organizationResponse.ok) throw new Error(`organizations request failed: ${organizationResponse.status}`)
    const organizationData = await organizationResponse.json()
    const group = organizationData.items.find((item: { type: string }) => item.type === 'group')
    const response = await fetch('/api/v1/assets', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        type: 'domain', value, name: `回收站测试资产_${Date.now()}`, organization_id: group.id,
        environment: 'production', network_scope: 'public', status: 'active', importance: 'normal',
      }),
    })
    if (!response.ok) throw new Error(`create asset failed: ${response.status}`)
    return response.json()
  }, assetValue)
  const assetID = created.item.id as string

  const deleteStatus = await page.evaluate(async (id) => {
    const response = await fetch(`/api/v1/assets/${id}`, { method: 'DELETE' })
    return response.status
  }, assetID)
  expect(deleteStatus).toBe(204)

  await page.goto('/recycle-bin')
  await expect(page).toHaveURL(/\/recycle-bin\/?$/)
  await expect(page.getByRole('heading', { name: '回收站', exact: true })).toBeVisible()
  const row = page.locator('.data-table-panel .el-table__body-wrapper tbody tr').filter({ hasText: assetValue }).first()
  await expect(row).toBeVisible()
  await row.getByRole('button', { name: '恢复', exact: true }).click()
  await page.getByRole('dialog', { name: '恢复网络资产' }).getByRole('button', { name: '确认恢复', exact: true }).click()
  await expect(page.getByText('网络资产已恢复', { exact: true })).toBeVisible()
  await expect(row).toHaveCount(0)

  const restored = await page.evaluate(async ({ id, value }) => {
    const [assetsResponse, auditResponse] = await Promise.all([
      fetch('/api/v1/assets'),
      fetch(`/api/v1/audit-logs?resource_type=asset&q=${encodeURIComponent(id)}&page_size=100`),
    ])
    if (!assetsResponse.ok || !auditResponse.ok) throw new Error('restore verification request failed')
    const assets = await assetsResponse.json()
    const audit = await auditResponse.json()
    return {
      exists: assets.items.some((item: { id: string; value: string }) => item.id === id && item.value === value),
      restoredAudited: audit.items.some((item: { action: string }) => item.action === 'asset.restore'),
    }
  }, { id: assetID, value: assetValue })
  expect(restored.exists).toBeTruthy()
  expect(restored.restoredAudited).toBeTruthy()
})

test('资产与业务系统保存字段级变更历史并可在资产页面查看', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  test.setTimeout(180_000)

  await page.goto('/login')
  await login(page, adminUsername, adminPassword!)
  const suffix = Date.now().toString()
  const assetValue = `history-pw-${suffix}.example.com`
  const originalAssetName = `历史测试资产_${suffix}`
  const updatedAssetName = `${originalAssetName}_已更新`
  const systemCode = `HISTORY_${suffix}`

  const result = await page.evaluate(async ({ assetValue, originalAssetName, updatedAssetName, systemCode, suffix }) => {
    const organizationsResponse = await fetch('/api/v1/organizations')
    if (!organizationsResponse.ok) throw new Error(`organizations request failed: ${organizationsResponse.status}`)
    const organizations = await organizationsResponse.json()
    const group = organizations.items.find((item: { type: string }) => item.type === 'group')

    const createAssetResponse = await fetch('/api/v1/assets', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        type: 'domain', value: assetValue, name: originalAssetName, organization_id: group.id,
        environment: 'production', network_scope: 'public', status: 'active', importance: 'normal', owner_name: '张三', notes: '首次登记',
      }),
    })
    if (!createAssetResponse.ok) throw new Error(`create asset failed: ${createAssetResponse.status}`)
    const createdAsset = await createAssetResponse.json()
    const assetID = createdAsset.item.id as string
    const updateAssetResponse = await fetch(`/api/v1/assets/${assetID}`, {
      method: 'PATCH', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        type: 'domain', value: assetValue, name: updatedAssetName, organization_id: group.id,
        environment: 'testing', network_scope: 'private', status: 'active', importance: 'important', owner_name: '李四', notes: '',
      }),
    })
    if (!updateAssetResponse.ok) throw new Error(`update asset failed: ${updateAssetResponse.status}`)
    const assetHistoryResponse = await fetch(`/api/v1/assets/${assetID}/change-history`)
    if (!assetHistoryResponse.ok) throw new Error(`asset history failed: ${assetHistoryResponse.status}`)
    const assetHistory = await assetHistoryResponse.json()

    const createSystemResponse = await fetch('/api/v1/business-systems', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        code: systemCode, name: `历史测试系统_${suffix}`, organization_id: group.id, status: 'running', level: 'normal',
        internet_exposure: 'unknown', contains_personal_information: 'yes', contains_sensitive_personal_information: 'no',
        personal_information_record_count: 50000, data_volume_over_100k: 'no', contains_important_data: 'unknown', contains_core_data: 'no',
      }),
    })
    if (!createSystemResponse.ok) throw new Error(`create system failed: ${createSystemResponse.status}`)
    const createdSystem = await createSystemResponse.json()
    const systemID = createdSystem.item.id as string
    const updateSystemResponse = await fetch(`/api/v1/business-systems/${systemID}`, {
      method: 'PATCH', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        code: systemCode, name: `历史测试系统_${suffix}`, organization_id: group.id, status: 'running', level: 'important',
        internet_exposure: 'partial', contains_personal_information: 'yes', contains_sensitive_personal_information: 'yes',
        personal_information_record_count: 120000, data_volume_over_100k: 'no', contains_important_data: 'pending', contains_core_data: 'no',
        data_storage_location: '上海数据中心', security_notes: '需完成加密整改',
      }),
    })
    if (!updateSystemResponse.ok) throw new Error(`update system failed: ${updateSystemResponse.status}`)
    const systemHistoryResponse = await fetch(`/api/v1/business-systems/${systemID}/change-history`)
    if (!systemHistoryResponse.ok) throw new Error(`system history failed: ${systemHistoryResponse.status}`)
    const systemHistory = await systemHistoryResponse.json()

    return { assetID, systemID, assetHistory, systemHistory }
  }, { assetValue, originalAssetName, updatedAssetName, systemCode, suffix })

  const assetUpdate = result.assetHistory.items.find((item: { action: string }) => item.action === 'asset.update')
  expect(assetUpdate.details.changes).toEqual(expect.arrayContaining([
    expect.objectContaining({ field: 'asset_name', before: originalAssetName, after: updatedAssetName }),
    expect.objectContaining({ field: 'environment', before: '生产', after: '测试' }),
    expect.objectContaining({ field: 'network_scope', before: '公网', after: '内网' }),
  ]))
  const systemUpdate = result.systemHistory.items.find((item: { action: string }) => item.action === 'business_system.update')
  expect(systemUpdate.details.changes).toEqual(expect.arrayContaining([
    expect.objectContaining({ field: 'personal_information_record_count', before: '50000', after: '120000' }),
    expect.objectContaining({ field: 'data_volume_over_100k', before: '否', after: '是' }),
    expect.objectContaining({ field: 'contains_sensitive_personal_information', before: '否', after: '是' }),
  ]))

  // Direct navigation verifies that the SPA route no longer conflicts with the built static asset directory.
  await page.goto('/assets')
  await expect(page).toHaveURL(/\/assets$/)
  await expect(page.getByRole('heading', { name: '网络资产', exact: true })).toBeVisible()
  await page.getByPlaceholder('搜索资产值、名称、系统或负责人').fill(assetValue)
  const assetRow = page.locator('.data-table-panel .el-table__body-wrapper tbody tr').filter({ hasText: assetValue }).first()
  await assetRow.getByRole('button', { name: '历史', exact: true }).click()
  const historyDrawer = page.getByRole('dialog', { name: `${updatedAssetName}的变更历史` })
  await expect(historyDrawer.getByText('资产名称', { exact: true })).toBeVisible()
  await expect(historyDrawer.getByText(originalAssetName, { exact: true })).toBeVisible()
  await expect(historyDrawer.getByText(updatedAssetName, { exact: true })).toBeVisible()

  await page.evaluate(async ({ assetID, systemID }) => {
    await fetch(`/api/v1/assets/${assetID}`, { method: 'DELETE' })
    await fetch(`/api/v1/business-systems/${systemID}`, { method: 'DELETE' })
  }, { assetID: result.assetID, systemID: result.systemID })
})

test('集团管理员可批量归档恢复业务系统并批量调整资产状态和网络区域', async ({ page }) => {
  test.skip(!adminPassword, 'E2E_PASSWORD/BOOTSTRAP_ADMIN_PASSWORD 未配置')
  test.setTimeout(240_000)

  await page.goto('/login')
  await login(page, adminUsername, adminPassword!)
  await page.evaluate(async () => {
    const [assetResponse, systemResponse] = await Promise.all([fetch('/api/v1/assets'), fetch('/api/v1/business-systems')])
    const assetData = await assetResponse.json()
    const systemData = await systemResponse.json()
    for (const item of assetData.items.filter((entry: { name?: string }) => entry.name?.startsWith('批量测试资产_'))) {
      await fetch(`/api/v1/assets/${item.id}`, { method: 'DELETE' })
    }
    for (const item of systemData.items.filter((entry: { name?: string }) => entry.name?.startsWith('批量测试系统_'))) {
      await fetch(`/api/v1/business-systems/${item.id}`, { method: 'DELETE' })
    }
  })
  const suffix = Date.now().toString()
  const systemNames = [`批量测试系统_${suffix}_1`, `批量测试系统_${suffix}_2`]
  const assetNames = [`批量测试资产_${suffix}_1`, `批量测试资产_${suffix}_2`]

  const fixture = await page.evaluate(async ({ suffix, systemNames, assetNames }) => {
    const requestJSON = async (url: string, init?: RequestInit) => {
      const response = await fetch(url, init)
      if (!response.ok) throw new Error(`${url} failed: ${response.status} ${await response.text()}`)
      return response.json()
    }
    const organizations = await requestJSON('/api/v1/organizations')
    const group = organizations.items.find((item: { type: string }) => item.type === 'group')
    const areaList = await requestJSON(`/api/v1/network-areas?organization_id=${encodeURIComponent(group.id)}`)
    let area = areaList.items.find((item: { status: string }) => item.status === 'enabled')
    if (!area) {
      const createdArea = await requestJSON('/api/v1/network-areas', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ organization_id: group.id, name: `批量测试区域_${suffix}`, code: `BATCH_${suffix}`, status: 'enabled', sort_order: 9999 }),
      })
      area = createdArea.item
    }
    const systems = []
    for (let index = 0; index < systemNames.length; index += 1) {
      const created = await requestJSON('/api/v1/business-systems', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          code: `BATCH_SYS_${suffix}_${index + 1}`, name: systemNames[index], organization_id: group.id,
          status: 'running', level: 'normal', internet_exposure: 'unknown', contains_personal_information: 'unknown',
          contains_sensitive_personal_information: 'unknown', data_volume_over_100k: 'unknown',
          contains_important_data: 'unknown', contains_core_data: 'unknown',
        }),
      })
      systems.push(created.item)
    }
    const assets = []
    for (let index = 0; index < assetNames.length; index += 1) {
      const created = await requestJSON('/api/v1/assets', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          type: 'domain', value: `batch-${suffix}-${index + 1}.example.com`, name: assetNames[index], organization_id: group.id,
          environment: 'production', network_scope: 'public', status: 'active', importance: 'normal',
        }),
      })
      assets.push(created.item)
    }
    return { area, systems, assets }
  }, { suffix, systemNames, assetNames })

  await page.goto('/systems')
  for (const name of systemNames) {
    const row = page.locator('.data-table-panel .el-table__body-wrapper tbody tr').filter({ hasText: name }).first()
    await expect(row).toBeVisible()
    await row.locator('label.el-checkbox').click()
  }
  await expect(page.getByText('已选择 2 个业务系统', { exact: false })).toBeVisible()
  await page.getByRole('button', { name: '批量归档', exact: true }).click()
  await page.getByRole('dialog', { name: '批量归档确认' }).getByRole('button', { name: '确认归档', exact: true }).click()
  const archiveResult = page.getByRole('dialog', { name: '批量归档结果' })
  await expect(archiveResult).toContainText('成功 2 个，失败 0 个')
  await archiveResult.getByRole('button', { name: '关闭', exact: true }).click()

  for (const name of systemNames) {
    const row = page.locator('.data-table-panel .el-table__body-wrapper tbody tr').filter({ hasText: name }).first()
    await expect(row).toContainText('已归档')
    await row.locator('label.el-checkbox').click()
  }
  await page.getByRole('button', { name: '批量恢复', exact: true }).click()
  await page.getByRole('dialog', { name: '批量恢复确认' }).getByRole('button', { name: '确认恢复', exact: true }).click()
  const restoreResult = page.getByRole('dialog', { name: '批量恢复结果' })
  await expect(restoreResult).toContainText('成功 2 个，失败 0 个')
  await restoreResult.getByRole('button', { name: '关闭', exact: true }).click()

  await page.getByRole('button', { name: '网络资产', exact: true }).click()
  await expect(page).toHaveURL(/\/assets$/)
  await page.getByPlaceholder('搜索资产值、名称、系统或负责人').fill(`批量测试资产_${suffix}`)
  for (const name of assetNames) {
    const row = page.locator('.data-table-panel .el-table__body-wrapper tbody tr').filter({ hasText: name }).first()
    await expect(row).toBeVisible()
    await row.locator('label.el-checkbox').click()
  }
  await page.locator('.batch-toolbar-actions .el-select').nth(0).click()
  await page.locator('.el-select-dropdown:visible').getByText('停用', { exact: true }).click()
  await page.getByRole('dialog', { name: '批量设置状态为“停用”确认' }).getByRole('button', { name: '确认执行', exact: true }).click()
  const statusResult = page.getByRole('dialog', { name: '批量设置状态为“停用”结果' })
  await expect(statusResult).toContainText('成功 2 个，失败 0 个')
  await statusResult.getByRole('button', { name: '关闭', exact: true }).click()

  for (const name of assetNames) {
    const row = page.locator('.data-table-panel .el-table__body-wrapper tbody tr').filter({ hasText: name }).first()
    await expect(row).toContainText('停用')
    await row.locator('label.el-checkbox').click()
  }
  await page.locator('.batch-toolbar-actions .el-select').nth(1).click()
  await page.locator('.el-select-dropdown:visible').getByText(fixture.area.name, { exact: false }).first().click()
  await page.getByRole('dialog', { name: `批量设置网络区域为“${fixture.area.name}”确认` }).getByRole('button', { name: '确认执行', exact: true }).click()
  const areaResult = page.getByRole('dialog', { name: `批量设置网络区域为“${fixture.area.name}”结果` })
  await expect(areaResult).toContainText('成功 2 个，失败 0 个')
  await areaResult.getByRole('button', { name: '关闭', exact: true }).click()

  const verification = await page.evaluate(async ({ assetIDs, systemIDs, areaID }) => {
    const [assetResponse, systemResponse] = await Promise.all([fetch('/api/v1/assets'), fetch('/api/v1/business-systems')])
    const assetData = await assetResponse.json()
    const systemData = await systemResponse.json()
    const assets = assetData.items.filter((item: { id: string }) => assetIDs.includes(item.id))
    const systems = systemData.items.filter((item: { id: string }) => systemIDs.includes(item.id))
    return {
      assetsUpdated: assets.length === 2 && assets.every((item: { status: string; network_area_id?: string }) => item.status === 'disabled' && item.network_area_id === areaID),
      systemsRestored: systems.length === 2 && systems.every((item: { status: string }) => item.status === 'running'),
    }
  }, { assetIDs: fixture.assets.map((item: { id: string }) => item.id), systemIDs: fixture.systems.map((item: { id: string }) => item.id), areaID: fixture.area.id })
  expect(verification.assetsUpdated).toBeTruthy()
  expect(verification.systemsRestored).toBeTruthy()

  await page.evaluate(async ({ assetIDs, systemIDs }) => {
    for (const id of assetIDs) await fetch(`/api/v1/assets/${id}`, { method: 'DELETE' })
    for (const id of systemIDs) await fetch(`/api/v1/business-systems/${id}`, { method: 'DELETE' })
  }, { assetIDs: fixture.assets.map((item: { id: string }) => item.id), systemIDs: fixture.systems.map((item: { id: string }) => item.id) })
})
