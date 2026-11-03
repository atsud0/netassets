import { flushPromises, shallowMount } from '@vue/test-utils'
import { defineComponent, h, nextTick, type Component } from 'vue'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../api/client'

vi.mock('vue-router', () => ({ useRoute: () => ({ query: {}, params: {}, path: '/remediation-tasks' }), useRouter: () => ({ push: vi.fn() }) }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => ({ isGroupAdmin: true, hasGroupSecurityRole: true, canManageSecurityWorkOrders: true, canManageSecurityFindings: true, hasPermission: () => true, user: { id: 'user', organization: { id: 'org' } } }) }))
vi.mock('../api/client', () => ({ apiClient: { get: vi.fn(), post: vi.fn(), patch: vi.fn() }, getAPIErrorMessage: () => '加载失败', getDownloadAPIErrorMessage: () => '加载失败' }))
vi.mock('../api/remediation', async original => ({ ...await original<typeof import('../api/remediation')>(), loadOptions: vi.fn().mockResolvedValue([]) }))
vi.mock('element-plus', async original => ({ ...await original<typeof import('element-plus')>(), ElMessage: { error: vi.fn(), warning: vi.fn(), success: vi.fn() } }))

type State = Record<string, any>
const cases = [
  ['Assets', '/assets', 'statusFilter', 'status', 'page', 'search', 'q'],
  ['BusinessSystems', '/business-systems', 'statusFilter', 'status', 'page', 'search', 'q'],
  ['DataQualityIssues', '/data-quality-issues', 'filters.status', 'status', 'issuePage'],
  ['RemediationTasks', '/remediation-tasks', 'status', 'status', 'page', 'keyword', 'keyword'],
  ['Users', '/users', 'filters.status', 'status', 'page', 'filters.keyword', 'keyword'],
  ['AuditLogs', '/audit-logs', 'filters.result', 'result', 'filters.page', 'filters.q', 'q'],
  ['SecurityTestBatches', '/security-test-batches', 'statusFilter', 'status', 'page'],
  ['SecurityTestRequests', '/security-test-requests', 'statusFilter', 'status', 'page', 'keywordFilter', 'keyword'],
  ['ExportJobs', '/export-jobs', 'status', 'status', 'page'],
  ['AssetCandidates', '/asset-candidates', 'filterStatus', 'status', 'page'],
  ['SecurityWorkOrders', '/security-work-orders', 'statusFilter', 'status', 'orderPage'],
] as const
const modules = import.meta.glob<{ default: Component }>('./*View.vue')
const wrappers: ReturnType<typeof shallowMount>[] = []
const Empty = defineComponent({ setup: () => () => h('div') })
const Table = defineComponent({ setup: (_, { expose }) => { expose({ clearSelection: vi.fn(), toggleRowSelection: vi.fn() }); return () => h('div') } })
const SavedViews = defineComponent({ setup: (_, { expose }) => { expose({ initialize: vi.fn(), loadViews: vi.fn() }); return () => h('div') } })

function set(state: State, path: string, value: unknown) {
  const parts = path.split('.')
  const property = parts.pop()!
  let target = state
  for (const part of parts) target = target[part]
  target[property] = value
}

async function setup(name: string) {
  const view = await modules[`./${name}View.vue`]!()
  const wrapper = shallowMount(view.default, { global: { stubs: { ElTabs: Empty, ElDialog: Empty, ElDrawer: Empty, ElTable: Table, SavedFilterViews: SavedViews }, directives: { loading: () => {} } } })
  wrappers.push(wrapper)
  await flushPromises()
  vi.mocked(apiClient.get).mockClear()
  return (wrapper.vm.$ as unknown as { setupState: State }).setupState
}

function calls(endpoint: string) { return vi.mocked(apiClient.get).mock.calls.filter(([url]) => url === endpoint) }

beforeEach(() => {
  vi.mocked(apiClient.get).mockResolvedValue({ data: { items: [], total: 0, summary: {}, options: { actions: [], resource_types: [] } } })
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.useRealTimers(); vi.clearAllMocks() })

describe('list filter integration', () => {
  it.each(cases)('%s immediately selects and clears status with page reset and one request', async (name, endpoint, status, parameter, page) => {
    const state = await setup(name)
    const selectedStatus = name === 'AssetCandidates' ? 'ignored' : 'pending'
    set(state, page, 4)
    set(state, status, selectedStatus)
    await flushPromises()
    expect(calls(endpoint)).toHaveLength(1)
    expect(calls(endpoint)[0]![1]?.params).toMatchObject({ page: 1, [parameter]: selectedStatus })
    vi.mocked(apiClient.get).mockClear()
    set(state, page, 3)
    set(state, status, '')
    await flushPromises()
    expect(calls(endpoint)).toHaveLength(1)
    expect(calls(endpoint)[0]![1]?.params.page).toBe(1)
    expect(calls(endpoint)[0]![1]?.params[parameter]).toBeFalsy()
  }, 20_000)

  it.each(cases)('%s prevents older responses from overwriting the newest list', async (name, endpoint, status) => {
    const state = await setup(name)
    const pending: ((response: any) => void)[] = []
    vi.mocked(apiClient.get).mockImplementation(url => url === endpoint
      ? new Promise(resolve => pending.push(resolve))
      : Promise.resolve({ data: { items: [], total: 0, summary: {} } }))
    set(state, status, 'first')
    await nextTick()
    set(state, status, 'second')
    await nextTick()
    expect(pending).toHaveLength(2)
    pending[1]!({ data: { items: [], total: 22, summary: {} } })
    await flushPromises()
    pending[0]!({ data: { items: [], total: 11, summary: {} } })
    await flushPromises()
    expect(state[name === 'DataQualityIssues' ? 'issueTotal' : name === 'SecurityWorkOrders' ? 'orderTotal' : 'total']).toBe(22)
    expect(state[name === 'Users' ? 'listLoading' : 'loading']).toBe(false)
  })

  it.each([
    ['SecurityTestRequests', 'systemPicker', '/business-systems', 'q'],
    ['SecurityTestRequests', 'scopePicker', '/security-test-requests/scope-candidates', 'keyword'],
    ['SecurityTestBatches', 'scopePicker', '/security-reference/assets', 'q'],
  ])('%s %s picker searches and clears without submission', async (name, prefix, endpoint, parameter) => {
    const state = await setup(name!)
    state[`${prefix}Visible`] = true
    if (name === 'SecurityTestBatches') state.scopePickerMode = 'asset'
    await flushPromises()
    vi.mocked(apiClient.get).mockClear()
    vi.useFakeTimers()
    state[`${prefix}Page`] = 3
    state[`${prefix}Keyword`] = 'asset'
    await nextTick()
    await vi.advanceTimersByTimeAsync(250)
    expect(calls(endpoint!)).toHaveLength(1)
    expect(calls(endpoint!)[0]![1]?.params).toMatchObject({ page: 1, [parameter!]: 'asset' })
    state[`${prefix}Keyword`] = ''
    await nextTick()
    expect(calls(endpoint!)).toHaveLength(2)
  })

  it.each(cases.filter(item => item.length > 5))('%s queries typing and clearing without Enter', async (name, endpoint, _status, _parameter, page, text, parameter) => {
    const state = await setup(name)
    vi.useFakeTimers()
    set(state, page, 4)
    set(state, text!, 'new')
    await nextTick()
    await vi.advanceTimersByTimeAsync(250)
    expect(calls(endpoint)).toHaveLength(1)
    expect(calls(endpoint)[0]![1]?.params).toMatchObject({ page: 1, [parameter!]: 'new' })
    vi.mocked(apiClient.get).mockClear()
    set(state, text!, '')
    await nextTick()
    expect(calls(endpoint)).toHaveLength(1)
    expect(calls(endpoint)[0]![1]?.params[parameter!]).toBeFalsy()
  })

  it.each(['findings', 'candidates'])('keeps the %s tab filters and pagination isolated', async tab => {
    const state = await setup('SecurityWorkOrders')
    state.activeTab = tab
    await flushPromises()
    vi.mocked(apiClient.get).mockClear()
    const candidate = tab === 'candidates'
    const endpoint = candidate ? '/security-findings/import-candidates' : '/security-findings'
    state[candidate ? 'candidatePage' : 'findingPage'] = 5
    state[candidate ? 'candidateStatusFilter' : 'statusFilter'] = 'ignored'
    await flushPromises()
    expect(calls(endpoint)).toHaveLength(1)
    expect(calls(endpoint)[0]![1]?.params).toMatchObject({ page: 1, status: 'ignored' })
    state[candidate ? 'candidateStatusFilter' : 'statusFilter'] = ''
    await flushPromises()
    expect(calls(endpoint)).toHaveLength(2)
    expect(calls('/security-work-orders')).toHaveLength(0)
  })

  it('removes manual query buttons and Enter-only filters from all lists', () => {
    for (const name of readdirSync(__dirname).filter(name => name.endsWith('.vue'))) {
      const source = readFileSync(join(__dirname, name), 'utf8')
      expect(source, name).not.toMatch(/<el-button\b[^>]*>(查询|筛选|搜索)<\/el-button>/)
      expect(source, name).not.toContain('@keyup.enter=')
    }
  })
})
