import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiClient } from '../api/client'
import SecurityWorkOrdersView from './SecurityWorkOrdersView.vue'

const permissions = vi.hoisted(() => ({ hasGroupSecurityRole: true, canManageSecurityFindings: true, canManageSecurityWorkOrders: true, canManageSecurityTests: false, hasPermission: (_permission: string) => true }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }) }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => permissions }))
vi.mock('../api/client', () => ({ apiClient: { get: vi.fn(), post: vi.fn(), patch: vi.fn() }, getAPIErrorMessage: () => '加载失败' }))
vi.mock('../api/remediation', () => ({ loadOptions: vi.fn().mockResolvedValue([]) }))
vi.mock('element-plus', async original => ({ ...await original<typeof import('element-plus')>(), ElMessage: { error: vi.fn(), warning: vi.fn(), success: vi.fn() }, ElMessageBox: { confirm: vi.fn(), prompt: vi.fn() } }))
const Empty = defineComponent({ setup: () => () => h('div') })
const tableMethods = { clearSelection: vi.fn(), toggleRowSelection: vi.fn() }
const Table = defineComponent({ setup: (_, { expose }) => { expose(tableMethods); return () => h('div') } })
const SavedViews = defineComponent({ setup: (_, { expose }) => { expose({ loadViews: vi.fn(), initialize: vi.fn() }); return () => h('div') } })
const wrappers: ReturnType<typeof mount>[] = []

beforeEach(() => {
  permissions.canManageSecurityFindings = true
  permissions.canManageSecurityWorkOrders = true
  vi.mocked(apiClient.get).mockResolvedValue({ data: { items: [], total: 0, summary: {} } })
  vi.mocked(ElMessageBox.confirm).mockResolvedValue('confirm')
  vi.mocked(ElMessageBox.prompt).mockResolvedValue({ value: '统一原因', action: 'confirm' })
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.clearAllMocks() })

async function setup(showDrawer = false) {
  const wrapper = mount(SecurityWorkOrdersView, { global: { stubs: {
    ElTable: Table, ElTabs: Empty, ElDialog: Empty, ElDrawer: showDrawer ? defineComponent({ setup: (_, { slots }) => () => h('div', slots.default?.()) }) : Empty, ElSelect: Empty, ListPagination: Empty,
    ElAlert: Empty, ElDropdown: Empty, ElTooltip: Empty, ElCollapse: Empty, ElForm: Empty, SavedFilterViews: SavedViews,
  }, directives: { loading: () => {} } } })
  wrappers.push(wrapper)
  await flushPromises()
  return (wrapper.vm.$ as unknown as { setupState: Record<string, any> }).setupState
}

describe('security batch operations', () => {
  it('filters severity immediately, resets pagination and leaves order requests unchanged', async () => {
    const state = await setup()
    state.activeTab = 'findings'
    await flushPromises()
    state.findingPage = 4
    state.organizationFilter = 'organization'
    state.statusFilter = 'pending'
    state.severityFilter = 'critical'
    await flushPromises()
    expect(state.findingPage).toBe(1)
    expect(apiClient.get).toHaveBeenCalledWith('/security-findings', { params: expect.objectContaining({ severity: 'critical', organization_id: 'organization', status: 'pending', page: 1 }) })
    expect(wrappers.at(-1)!.findAll('.finding-summary-card').map(card => card.find('span').text())).toEqual(['发现总数', '待处理', '严重', '高危', '中危', '低危'])
    expect(wrappers.at(-1)!.find('.finding-summary-dimensions').exists()).toBe(false)
    state.severityFilter = ''
    await flushPromises()
    expect(apiClient.get).toHaveBeenLastCalledWith('/security-findings/summary', expect.anything())
    const findingCalls = vi.mocked(apiClient.get).mock.calls.filter(([path]) => path === '/security-findings')
    expect(findingCalls.at(-1)?.[1]?.params).not.toHaveProperty('severity')
    state.severityFilter = 'high'
    await flushPromises()
    state.resetFilters()
    expect(state.severityFilter).toBe('')
    state.severityFilter = 'info'
    state.activeTab = 'orders'
    await flushPromises()
    const orderCalls = vi.mocked(apiClient.get).mock.calls.filter(([path]) => path === '/security-work-orders')
    expect(orderCalls.at(-1)?.[1]?.params).not.toHaveProperty('severity')
  })

  it.each(['orders', 'findings', 'candidates'])('submits selected records and retains only failed selections: %s', async tab => {
    const state = await setup()
    state.activeTab = tab
    state.selectedRows = [{ id: 'one', status: 'pending', updated_at: '2026-10-30T00:00:00Z' }, { id: 'two', status: 'pending', updated_at: '2026-10-30T00:00:00Z' }]
    const result = { succeeded: 1, failed: 1, items: [{ id: 'one', success: true }, { id: 'two', success: false, code: 'invalid_state', message: '状态已变更' }] }
    vi.mocked(apiClient.post).mockResolvedValue({ data: result })
    vi.mocked(apiClient.patch).mockResolvedValue({ data: result })
    await state.submitBatch()
    const method = tab === 'findings' ? apiClient.patch : apiClient.post
    const path = tab === 'orders' ? '/security-work-orders/batch/cancel' : tab === 'findings' ? '/security-findings/batch' : '/security-findings/import-candidates/batch/ignore'
    expect(method).toHaveBeenCalledWith(path, expect.objectContaining({ items: [{ id: 'one', expected_updated_at: '2026-10-30T00:00:00Z' }, { id: 'two', expected_updated_at: '2026-10-30T00:00:00Z' }] }))
    expect(state.batchResult).toEqual(result)
    expect(state.batchResultVisible).toBe(true)
    expect(state.selectedRows.map((item: { id: string }) => item.id)).toEqual(['two'])
  })

  it('disables terminal records, prevents double submission and preserves selection on list reload', async () => {
    const state = await setup()
    expect(state.selectableForBatch({ status: 'closed' })).toBe(false)
    expect(state.selectableForBatch({ status: 'draft' })).toBe(true)
    state.selectedRows = [{ id: 'one' }]
    state.batchSubmitting = true
    await state.submitBatch()
    expect(apiClient.post).not.toHaveBeenCalled()
    state.batchSubmitting = false
    await state.loadActiveList()
    expect(state.selectedRows).toEqual([{ id: 'one' }])
  })

  it('merges pages, preserves original versions and deselects only visible records', async () => {
    const state = await setup()
    const first = { id: 'one', status: 'draft', updated_at: 'original' }
    const second = { id: 'two', status: 'draft' }
    state.workOrders = [first]
    state.handleBatchSelectionChange([first])
    state.workOrders = [second]
    state.handleBatchSelectionChange([second])
    expect(state.selectedRows).toEqual([first, second])
    state.workOrders = [{ ...first, updated_at: 'changed' }]
    state.handleBatchSelectionChange(state.workOrders)
    expect(state.selectedRows.find((item: { id: string }) => item.id === 'one').updated_at).toBe('original')
    state.handleBatchSelectionChange([])
    expect(state.selectedRows).toEqual([second])
  })

  it('preserves selection across filters, page sizes, tabs and target statuses, restoring checks on reload', async () => {
    const state = await setup()
    const order = { id: 'one', status: 'draft' }
    const finding = { id: 'one', status: 'pending' }
    state.selectedRows = [order]
    state.applyFilter()
    await flushPromises()
    state.handlePageSizeChange(50)
    await flushPromises()
    expect(state.selectedRows).toEqual([order])
    state.activeTab = 'findings'
    expect(state.selectedRows).toEqual([])
    state.selectedRows = [finding]
    state.batchFindingStatus = 'pending'
    await flushPromises()
    expect(state.selectedRows).toEqual([finding])
    state.activeTab = 'orders'
    vi.mocked(apiClient.get).mockResolvedValue({ data: { items: [order], total: 1 } })
    await state.loadActiveList()
    expect(state.selectedRows).toEqual([order])
    expect(tableMethods.toggleRowSelection).toHaveBeenCalledWith(order, true)
    state.clearBatchSelection()
    expect(state.selectedRows).toEqual([])
    state.activeTab = 'findings'
    expect(state.selectedRows).toEqual([finding])
    const reloaded = await setup()
    expect(reloaded.selectedRows).toEqual([])
  })

  it('limits cross-page selection to 200 records without discarding previous selections', async () => {
    const state = await setup()
    state.selectedRows = Array.from({ length: 200 }, (_, index) => ({ id: `selected-${index}`, status: 'draft' }))
    state.workOrders = [{ id: 'extra', status: 'draft' }]
    state.handleBatchSelectionChange(state.workOrders)
    await flushPromises()
    expect(state.selectedRows).toHaveLength(200)
    expect(ElMessage.warning).toHaveBeenCalledWith('单次最多选择 200 项')
  })

  it('does not allow batch operations without management permission', async () => {
    permissions.canManageSecurityWorkOrders = false
    const state = await setup()
    state.selectedRows = [{ id: 'one' }]
    expect(state.selectableForBatch({ status: 'draft' })).toBe(false)
    await state.submitBatch()
    expect(apiClient.post).not.toHaveBeenCalled()
  })

  it('returns to page one when a batch empties the last filtered page', async () => {
    const state = await setup()
    state.activeTab = 'candidates'
    state.candidatePage = 3
    await state.refreshAfterMutation()
    expect(state.candidatePage).toBe(1)
  })
})

describe('create work order from selected findings', () => {
  function finding(id: string, organization = 'organization-one', status = 'pending') {
    return { id, assignee_organization_id: organization, status, code: `SF-${id}`, title: `发现 ${id}`, severity: 'high' }
  }

  it('prefills all selected pages, including confirmed findings outside loaded options', async () => {
    const state = await setup()
    state.activeTab = 'findings'
    const rows = [finding('one'), finding('two', 'organization-one', 'confirmed')]
    state.selectedRows = rows
    state.openCreateWorkOrderFromSelection()
    await flushPromises()
    expect(state.workOrderDialogVisible).toBe(true)
    expect(state.workOrderForm.assignee_organization_id).toBe('organization-one')
    expect(state.workOrderForm.finding_ids).toEqual(['one', 'two'])
    expect(state.orderFindings.map((item: { id: string }) => item.id)).toEqual(['one', 'two'])
    expect(state.selectableForBatch(rows[1])).toBe(true)
    state.workOrderDialogVisible = false
    expect(state.selectedRows).toEqual(rows)
    state.openCreateWorkOrder(rows[1])
    await flushPromises()
    expect(state.workOrderForm.finding_ids).toEqual(['two'])
    state.workOrderForm.assignee_organization_id = 'other'
    expect(state.workOrderForm.finding_ids).toEqual([])
  })

  it.each(['empty', 'mixed-organizations', 'invalid-status', 'too-many'])('rejects %s without dropping selections', async mode => {
    const state = await setup()
    state.activeTab = 'findings'
    const rows = mode === 'empty' ? [] : mode === 'mixed-organizations' ? [finding('one'), finding('two', 'other')]
      : mode === 'invalid-status' ? [finding('one', 'organization-one', 'resolved')]
        : Array.from({ length: 101 }, (_, index) => finding(String(index)))
    state.selectedRows = rows
    state.openCreateWorkOrderFromSelection()
    expect(state.workOrderDialogVisible).toBe(false)
    expect(state.selectedRows).toEqual(rows)
    expect(ElMessage.warning).toHaveBeenCalled()
    expect(apiClient.post).not.toHaveBeenCalled()
  })

  it('allows work-order managers to select without granting finding status permissions', async () => {
    permissions.canManageSecurityFindings = false
    const state = await setup()
    permissions.canManageSecurityFindings = false
    state.activeTab = 'findings'
    await flushPromises()
    state.selectedRows = [finding('one')]
    expect(state.batchEnabled).toBe(true)
    expect(state.selectableForBatch(finding('one', 'organization-one', 'confirmed'))).toBe(true)
    await state.submitBatch()
    expect(apiClient.patch).not.toHaveBeenCalled()
    state.openCreateWorkOrderFromSelection()
    expect(state.workOrderDialogVisible).toBe(true)
    permissions.canManageSecurityWorkOrders = false
    state.workOrderDialogVisible = false
    state.openCreateWorkOrderFromSelection()
    expect(state.workOrderDialogVisible).toBe(false)
  })

  it.each(['draft', 'issue-failure', 'create-failure'])('submits exact selected IDs and handles %s safely', async mode => {
    const state = await setup()
    state.activeTab = 'findings'
    state.selectedRows = [finding('one'), finding('two')]
    state.openCreateWorkOrderFromSelection()
    await flushPromises()
    state.workOrderForm.issue_immediately = mode !== 'draft'
    state.workOrderFormRef = { validate: vi.fn().mockResolvedValue(true) }
    if (mode === 'create-failure') vi.mocked(apiClient.post).mockRejectedValueOnce(new Error('conflict'))
    else {
      vi.mocked(apiClient.post).mockResolvedValueOnce({ data: { item: { id: 'created-order' } } })
      if (mode === 'issue-failure') vi.mocked(apiClient.post).mockRejectedValueOnce(new Error('issue'))
    }
    await state.saveWorkOrder()
    expect(apiClient.post).toHaveBeenCalledWith('/security-work-orders', expect.objectContaining({ finding_ids: ['one', 'two'], assignee_organization_id: 'organization-one' }))
    if (mode === 'create-failure') {
      expect(state.workOrderDialogVisible).toBe(true)
      expect(state.batchSelections.findings).toHaveLength(2)
    } else {
      expect(state.workOrderDialogVisible).toBe(false)
      expect(state.batchSelections.findings).toEqual([])
      if (mode === 'issue-failure') expect(ElMessage.warning).toHaveBeenCalledWith(expect.stringContaining('工单草稿已创建'))
    }
  })
})

describe('work order item remediation', () => {
  const item = { id: 'finding-one', stage: 'in_progress', version: 3, finding_updated_at: '2026-10-30T00:00:00Z' }
  it('separates organization execution from group verification and personal tasks', async () => {
    const state = await setup()
    state.selectedOrder = { id: 'order', execution_mode: 'organization', status: 'in_progress' }
    expect(state.canProcessItem(item, 'submit')).toBe(true)
    expect(state.canProcessItem(item, 'pass')).toBe(false)
    expect(state.canProcessItem({ ...item, stage: 'pending_verification' }, 'pass')).toBe(true)
    permissions.canManageSecurityWorkOrders = false
    expect(state.canProcessItem({ ...item, stage: 'pending_verification' }, 'pass')).toBe(false)
    state.selectedOrder.execution_mode = 'individual'
    expect(state.canProcessItem(item, 'submit')).toBe(false)
  })
  it('hides group verification buttons for selected organization items', async () => {
    permissions.canManageSecurityWorkOrders = false
    const state = await setup(true)
    state.selectedOrder = { id: 'order', execution_mode: 'organization', status: 'in_progress', attachments: [], events: [] }
    state.itemSelection = [item]
    await flushPromises()
    const toolbar = wrappers.at(-1)!.find('[aria-label="工单逐项操作"]')
    expect(toolbar.exists()).toBe(true)
    expect(toolbar.text()).toContain('提交复测')
    expect(toolbar.text()).toContain('更新整改进度')
    expect(toolbar.text()).not.toContain('复测通过')
    expect(toolbar.text()).not.toContain('复测退回')
  })
  it('requires remediation evidence and submits both version tokens', async () => {
    const state = await setup()
    state.selectedOrder = { id: 'order', execution_mode: 'organization', status: 'in_progress' }
    await state.openItemAction('submit', [item])
    state.itemNote = '修复已完成'
    await state.submitItemAction()
    expect(apiClient.post).not.toHaveBeenCalled()
    state.itemEvidenceIDs = ['proof']
    vi.mocked(apiClient.post).mockResolvedValue({ data: { succeeded: 1, failed: 0, items: [{ id: item.id, success: true }] } })
    vi.mocked(apiClient.get).mockImplementation(async path => ({ data: path === '/security-work-orders/order' ? { item: { id: 'order', execution_mode: 'organization', status: 'pending_verification' } } : { items: [], total: 0 } }) as any)
    await state.submitItemAction()
    expect(apiClient.post).toHaveBeenCalledWith('/security-work-orders/order/findings/process', { action: 'submit', note: '修复已完成', evidence_ids: ['proof'], items: [{ id: item.id, version: 3, finding_updated_at: item.finding_updated_at }] })
    expect(state.selectedOrder.status).toBe('pending_verification')
    expect(state.itemDialogVisible).toBe(false)
  })
  it('rejects mixed stages instead of silently discarding selected records', async () => {
    const state = await setup()
    state.selectedOrder = { id: 'order', execution_mode: 'organization', status: 'in_progress' }
    await state.openItemAction('submit', [item, { ...item, id: 'verified', stage: 'verified' }])
    expect(state.itemDialogVisible).toBe(false)
    expect(ElMessage.warning).toHaveBeenCalled()
  })
  it('retains the dialog on request failure and prevents duplicate requests', async () => {
    const state = await setup()
    state.selectedOrder = { id: 'order', execution_mode: 'organization', status: 'in_progress' }
    await state.openItemAction('progress', [item])
    state.itemNote = '修复中'
    state.itemSubmitting = true
    await state.submitItemAction()
    expect(apiClient.post).not.toHaveBeenCalled()
    state.itemSubmitting = false
    vi.mocked(apiClient.post).mockRejectedValue(new Error('offline'))
    await state.submitItemAction()
    expect(state.itemDialogVisible).toBe(true)
    expect(state.itemSubmitting).toBe(false)
  })
})

describe('candidate read-only details', () => {
  it('loads fresh details, including imported finding and processing evidence', async () => {
    const state = await setup()
    const item = { id: 'candidate', status: 'imported', imported_finding_id: 'finding', imported_finding_code: 'SF-1', review_note: '已处理' }
    vi.mocked(apiClient.get).mockResolvedValue({ data: { item } })
    await state.openCandidateDetail({ id: 'candidate' })
    expect(apiClient.get).toHaveBeenCalledWith('/security-findings/import-candidates/candidate')
    expect(state.candidateDetail).toEqual(item)
    expect(state.candidateDialogVisible).toBe(false)
    expect(state.candidateDetailVisible).toBe(true)
  })

  it('shows a visible error and clears stale data on failed details', async () => {
    const state = await setup()
    state.candidateDetail = { id: 'old' }
    vi.mocked(apiClient.get).mockRejectedValue(new Error('network'))
    await state.openCandidateDetail({ id: 'candidate' })
    expect(ElMessage.error).toHaveBeenCalledWith('加载失败')
    expect(state.candidateDetail).toBeUndefined()
    expect(state.candidateDetailLoading).toBe(false)
  })
})
