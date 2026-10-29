import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { ElMessage } from 'element-plus'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../api/client'
import SecurityWorkOrdersView from './SecurityWorkOrdersView.vue'

vi.mock('vue-router', () => ({ useRoute: () => ({ query: { tab: 'findings' } }) }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => ({ hasGroupSecurityRole: true, canManageSecurityFindings: true, canManageSecurityWorkOrders: true, canManageSecurityTests: false }) }))
vi.mock('../api/client', () => ({ apiClient: { get: vi.fn(), post: vi.fn(), patch: vi.fn() }, getAPIErrorMessage: () => '读取失败' }))
vi.mock('../api/remediation', () => ({ loadOptions: async () => [] }))
vi.mock('element-plus', async importOriginal => ({ ...await importOriginal<typeof import('element-plus')>(), ElMessage: { warning: vi.fn(), error: vi.fn(), success: vi.fn() } }))
const Dialog = defineComponent({ props: ['modelValue'], setup: (props, { slots }) => () => props.modelValue ? h('section', [slots.default?.(), slots.footer?.()]) : null })
const Empty = defineComponent({ setup: () => () => h('div') })
const SavedViews = defineComponent({ setup: (_, { expose }) => { expose({ loadViews: vi.fn(), initialize: vi.fn() }); return () => h('div') } })
const wrappers: ReturnType<typeof mount>[] = []
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.clearAllMocks() })

function deferred() {
  let resolve!: (value: any) => void
  let reject!: (error: Error) => void
  const promise = new Promise<any>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const option = (id: string) => ({ id, asset_id: id, asset_value: `${id}.invalid`, organization_name: '测试组织' })
const response = (...ids: string[]) => ({ data: { items: ids.map(option) } })
async function fixture() {
  vi.mocked(apiClient.get).mockResolvedValue({ data: { items: [], total: 0, stats: {} } })
  const wrapper = mount(SecurityWorkOrdersView, { global: { stubs: {
    ElDialog: Dialog, ElForm: Empty, ElTable: Empty, ElTabs: Empty, ElSelect: Empty,
    ElPagination: Empty, ElAlert: Empty, ElDropdown: Empty, ElDrawer: Empty, ElTooltip: Empty,
    ElDivider: Empty, ElDatePicker: Empty, ElSwitch: Empty, ElRadioGroup: Empty, SavedFilterViews: SavedViews,
  }, directives: { loading: () => {} } } })
  wrappers.push(wrapper)
  await flushPromises()
  const state = (wrapper.vm.$ as unknown as { setupState: Record<string, any> }).setupState
  vi.mocked(apiClient.get).mockClear()
  return state
}
describe('待关联漏洞资产远程搜索', () => {
  it('打开弹窗清旧选项并发送空关键字，不自动选中第一个无关资产', async () => {
    const state = await fixture()
    state.candidateScopeOptions = [option('old')]
    vi.mocked(apiClient.get).mockResolvedValue(response('unrelated'))
    await state.openFindingImportCandidate({ id: 'candidate', test_batch_id: '' })
    expect(apiClient.get).toHaveBeenLastCalledWith('/security-findings/import-candidates/candidate/scope-assets', { params: { keyword: '' } })
    expect(state.candidateScopeOptions).toEqual([option('unrelated')])
    expect(state.selectedCandidateScopeAssetID).toBe('')
  })
  it('搜索关键字去首尾空白并发送服务端，不仅过滤本地前200项', async () => {
    const state = await fixture()
    await state.openFindingImportCandidate({ id: 'candidate', test_batch_id: '' })
    vi.mocked(apiClient.get).mockResolvedValue(response('target'))
    await state.searchCandidateScopeOptions('  TARGET_%  ')
    expect(apiClient.get).toHaveBeenLastCalledWith('/security-findings/import-candidates/candidate/scope-assets', { params: { keyword: 'TARGET_%' } })
    expect(state.candidateScopeOptions).toEqual([option('target')])
  })
  it('旧请求后返回也不能覆盖最新结果或提前结束loading', async () => {
    const state = await fixture()
    await state.openFindingImportCandidate({ id: 'candidate' })
    const old = deferred(), latest = deferred()
    vi.mocked(apiClient.get).mockReturnValueOnce(old.promise).mockReturnValueOnce(latest.promise)
    const first = state.searchCandidateScopeOptions('first')
    const second = state.searchCandidateScopeOptions('second')
    old.resolve(response('old')); await first
    expect(state.candidateScopeLoading).toBe(true)
    expect(state.candidateScopeOptions).not.toEqual([option('old')])
    latest.resolve(response('new')); await second
    expect(state.candidateScopeOptions).toEqual([option('new')])
    expect(state.candidateScopeLoading).toBe(false)
  })
  it('最新成功后旧失败不得清空结果或显示过时错误', async () => {
    const state = await fixture()
    await state.openFindingImportCandidate({ id: 'candidate' })
    const old = deferred(), latest = deferred()
    vi.mocked(apiClient.get).mockReturnValueOnce(old.promise).mockReturnValueOnce(latest.promise)
    const first = state.searchCandidateScopeOptions('first'), second = state.searchCandidateScopeOptions('second')
    latest.resolve(response('new')); await second
    old.reject(new Error('stale')); await first
    expect(state.candidateScopeOptions).toEqual([option('new')])
    expect(ElMessage.error).not.toHaveBeenCalled()
  })
  it.each([false, true])('关闭弹窗后的%s成功或失败响应均丢弃', async fail => {
    const state = await fixture()
    await state.openFindingImportCandidate({ id: 'candidate' })
    const pending = deferred(); vi.mocked(apiClient.get).mockReturnValueOnce(pending.promise)
    const task = state.searchCandidateScopeOptions('late')
    state.candidateDialogVisible = false
    if (fail) pending.reject(new Error('late')); else pending.resolve(response('late'))
    await task
    expect(state.candidateScopeOptions).toEqual([])
    expect(ElMessage.error).not.toHaveBeenCalled()
  })
  it('切换候选后旧响应不能污染新候选', async () => {
    const state = await fixture()
    const old = deferred(), current = deferred()
    vi.mocked(apiClient.get).mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const first = state.openFindingImportCandidate({ id: 'first' })
    const second = state.openFindingImportCandidate({ id: 'second', test_batch_id: 'batch' })
    current.resolve(response('current')); await second
    old.resolve(response('old')); await first
    expect(state.selectedCandidate.id).toBe('second')
    expect(state.candidateScopeOptions).toEqual([option('current')])
  })
  it('保留返回列表内的显式选择，选择不在结果时清空但不替换成首项', async () => {
    const state = await fixture()
    vi.mocked(apiClient.get).mockResolvedValue(response('chosen', 'other'))
    await state.openFindingImportCandidate({ id: 'candidate', test_batch_id: 'batch', suggested_scope_asset_id: 'chosen' })
    expect(state.selectedCandidateScopeAssetID).toBe('chosen')
    vi.mocked(apiClient.get).mockResolvedValue(response('other'))
    await state.searchCandidateScopeOptions('other')
    expect(state.selectedCandidateScopeAssetID).toBe('')
  })
  it('当前请求失败清空旧选项和选择、提示错误且停止loading', async () => {
    const state = await fixture()
    vi.mocked(apiClient.get).mockResolvedValue(response('chosen'))
    await state.openFindingImportCandidate({ id: 'candidate', suggested_asset_id: 'chosen' })
    vi.mocked(apiClient.get).mockRejectedValue(new Error('failed'))
    await state.searchCandidateScopeOptions('missing')
    expect(state.candidateScopeOptions).toEqual([])
    expect(state.selectedCandidateScopeAssetID).toBe('')
    expect(state.candidateScopeLoading).toBe(false)
    expect(ElMessage.error).toHaveBeenCalledOnce()
  })
})
