import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { ElSelect, ElSelectV2 } from 'element-plus'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../api/client'
import { loadOptions } from '../api/remediation'
import SecurityWorkOrdersView from './SecurityWorkOrdersView.vue'

const route = vi.hoisted(() => ({ query: {} as Record<string, string> }))
vi.mock('vue-router', () => ({ useRoute: () => route }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => ({ hasGroupSecurityRole: true, canManageSecurityFindings: true, canManageSecurityWorkOrders: true, canManageSecurityTests: false }) }))
vi.mock('../api/client', () => ({ apiClient: { get: vi.fn(), post: vi.fn() }, getAPIErrorMessage: () => '读取失败' }))
vi.mock('../api/remediation', () => ({ loadOptions: vi.fn() }))
vi.mock('element-plus', async importOriginal => ({ ...await importOriginal<typeof import('element-plus')>(), ElMessage: { error: vi.fn(), warning: vi.fn(), success: vi.fn() } }))
const Empty = defineComponent({ setup: () => () => h('div') })
const SavedViews = defineComponent({ setup: (_, { expose }) => { expose({ loadViews: vi.fn(), initialize: vi.fn() }); return () => h('div') } })
const wrappers: ReturnType<typeof mount>[] = []
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.clearAllMocks() })

describe('安全工单首屏不依赖编辑下拉选项加载', () => {
  it.each(['finding', 'order'] as const)('%s 责任组织保留全部选项但仅渲染可见窗口', async mode => {
    route.query = { tab: 'findings' }
    const organizations = Array.from({ length: 1403 }, (_, index) => ({ id: `org-${index}`, name: `组织-${index}`, status: 'enabled', type: 'subsidiary' }))
    vi.mocked(loadOptions).mockImplementation(async path => path.endsWith('/organizations') ? organizations : [])
    vi.mocked(apiClient.get).mockResolvedValue({ data: { items: [], total: 0, summary: {} } })
    const Dialog = defineComponent({ props: ['modelValue'], setup: (props, { slots }) => () => props.modelValue ? h('section', slots.default?.()) : null })
    const wrapper = mount(SecurityWorkOrdersView, { attachTo: document.body, global: { stubs: {
      ElTable: Empty, ElTabs: Empty, ElDialog: Dialog, ElDrawer: Empty, ElSelect, ElSelectV2, ElPagination: Empty,
      ElAlert: Empty, ElDropdown: Empty, ElCollapse: Empty, SavedFilterViews: SavedViews,
      ElDatePicker: Empty, ElRadioGroup: Empty, ElSwitch: Empty,
    }, directives: { loading: () => {} } } })
    wrappers.push(wrapper)
    await flushPromises()
    const state = (wrapper.vm.$ as unknown as { setupState: Record<string, any> }).setupState
    state[mode === 'finding' ? 'findingDialogVisible' : 'workOrderDialogVisible'] = true
    await flushPromises()
    const organization = wrapper.findAll('.el-form-item').find(item => item.find('.el-form-item__label').text() === '责任组织')!
    expect(organization.html()).toContain('<input')
    await organization.find('input').trigger('click')
    await flushPromises()
    expect(document.querySelectorAll('[role="option"]').length).toBeLessThan(100)
    const selector = organization.findComponent(ElSelectV2)
    expect(selector.props('options')).toHaveLength(1403)
    await organization.find('input').setValue('组织-1402')
    await flushPromises()
    const tail = Array.from(document.querySelectorAll<HTMLElement>('[role="option"]')).find(option => option.textContent?.trim() === '组织-1402')
    expect(tail).toBeDefined()
    tail!.click()
    await flushPromises()
    expect(state[mode === 'finding' ? 'findingForm' : 'workOrderForm'].assignee_organization_id).toBe('org-1402')
  })

  it.each(['orders', 'findings'] as const)('%s 列表及深链详情在基础选项与待派单选项仍在加载时就绪', async mode => {
    let finishOptions!: (value: never[]) => void
    const pendingOptions = new Promise<never[]>(resolve => { finishOptions = resolve })
    vi.mocked(loadOptions).mockReturnValue(pendingOptions)
    route.query = { tab: mode, organization_id: 'org', [mode === 'orders' ? 'work_order_id' : 'finding_id']: 'target' }
    const endpoint = mode === 'orders' ? '/security-work-orders' : '/security-findings'
    vi.mocked(apiClient.get).mockImplementation(async (path, config) => {
      // The unfiltered 100-item finding request supplies an editing selector,
      // not the org-filtered list currently being viewed.
      if (path === '/security-findings' && !config?.params?.organization_id) {
        await pendingOptions
        return { data: { items: [], total: 0 } }
      }
      if (path === endpoint) return { data: { items: [], total: 3 } }
      if (path === `${endpoint}/target`) return { data: { item: { id: 'target', status: 'pending', findings: [], events: [], attachments: [] } } }
      if (path === '/security-findings/summary') return { data: { summary: { total: 3 } } }
      return { data: { items: [], total: 0 } }
    })
    const wrapper = mount(SecurityWorkOrdersView, { global: { stubs: {
      ElTable: Empty, ElTabs: Empty, ElDialog: Empty, ElDrawer: Empty, ElSelect: Empty, ElPagination: Empty,
      ElAlert: Empty, ElDropdown: Empty, ElTooltip: Empty, ElCollapse: Empty, ElForm: Empty, SavedFilterViews: SavedViews,
    }, directives: { loading: () => {} } } })
    wrappers.push(wrapper)
    try {
      await flushPromises()
      const state = (wrapper.vm.$ as unknown as { setupState: Record<string, any> }).setupState
      expect(apiClient.get).toHaveBeenCalledWith(endpoint, expect.objectContaining({ params: expect.objectContaining({ organization_id: 'org' }) }))
      expect(state[mode === 'orders' ? 'orderTotal' : 'findingTotal']).toBe(3)
      expect(state.loading).toBe(false)
      expect(apiClient.get).toHaveBeenCalledWith(`${endpoint}/target`)
      expect(state[mode === 'orders' ? 'selectedOrder' : 'selectedFinding']?.id).toBe('target')
    } finally {
      finishOptions([])
      await flushPromises()
    }
  })
})


describe('漏洞导入完成后的弹窗意图', () => {
  it('刷新基础选项期间关闭导入弹窗，刷新完成后不得重新打开', async () => {
    route.query = { tab: 'findings', organization_id: 'org' }
    vi.mocked(loadOptions).mockResolvedValue([])
    vi.mocked(apiClient.get).mockResolvedValue({ data: { items: [], total: 0, summary: {} } })
    vi.mocked(apiClient.post).mockResolvedValue({ data: { item: { success_count: 2, pending_count: 0, failure_count: 0, duplicate_count: 0 } } })
    const wrapper = mount(SecurityWorkOrdersView, { global: { stubs: {
      ElTable: Empty, ElTabs: Empty, ElDialog: Empty, ElDrawer: Empty, ElSelect: Empty, ElPagination: Empty,
      ElAlert: Empty, ElDropdown: Empty, ElTooltip: Empty, ElCollapse: Empty, ElForm: Empty, SavedFilterViews: SavedViews,
    }, directives: { loading: () => {} } } })
    wrappers.push(wrapper)
    await flushPromises()
    const state = (wrapper.vm.$ as unknown as { setupState: Record<string, any> }).setupState
    state.openFindingImport()
    state.importFile = new File(['example'], 'findings.csv', { type: 'text/csv' })
    let release!: (value: never[]) => void
    vi.mocked(loadOptions).mockReturnValue(new Promise<never[]>(resolve => { release = resolve }))
    const submitted = state.submitFindingImport()
    try {
      await flushPromises()
      expect(state.importResult.success_count).toBe(2)
      expect(state.importDialogVisible).toBe(true)
      expect(state.importSubmitting).toBe(true)
      state.importDialogVisible = false
      await flushPromises()
    } finally {
      release([])
      await submitted
    }
    expect(state.importDialogVisible).toBe(false)
    expect(state.importSubmitting).toBe(false)
    expect(state.importResult.success_count).toBe(2)
  })
})
