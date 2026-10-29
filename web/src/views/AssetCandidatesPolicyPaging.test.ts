import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h, nextTick } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../api/client'
import AssetCandidatesView from './AssetCandidatesView.vue'

const principal = vi.hoisted(() => ({ isGroupAdmin: true }))
vi.mock('../api/client', () => ({ apiClient: { get: vi.fn() }, getAPIErrorMessage: () => '读取失败' }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => principal }))
const Table = defineComponent({ name: 'ElTable', props: ['data'], setup: () => () => h('div') })
const Pagination = defineComponent({ name: 'ElPagination', props: ['currentPage', 'pageSize', 'total'], emits: ['update:currentPage', 'update:pageSize'], setup: () => () => h('div') })
const Input = defineComponent({ name: 'ElInput', props: ['modelValue'], emits: ['update:modelValue'], setup: (props, { emit }) => () => h('input', { value: props.modelValue, onInput: (event: Event) => emit('update:modelValue', (event.target as HTMLInputElement).value) }) })
const Empty = defineComponent({ setup: () => () => null })
const wrappers: ReturnType<typeof mount>[] = []
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.clearAllMocks(); principal.isGroupAdmin = true })

async function fixture() {
  const policies = Array.from({ length: 5001 }, (_, i) => ({ organization_id: `org-${i}`, organization_name: `组织策略 ${i}`, allow_self_service: false, allowed_asset_types: [], require_enabled_organization: true }))
  vi.mocked(apiClient.get).mockImplementation(async url => ({ data: { items: url === '/asset-candidates/self-service-policies' ? policies : [], total: 0 } }))
  const wrapper = mount(AssetCandidatesView, { global: { stubs: { ElTable: Table, ElPagination: Pagination, ElInput: Input, ElDialog: Empty, ElRadioGroup: Empty }, directives: { loading: () => {} } } })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}

describe('待确认资产策略表有界渲染', () => {
  it('5001 个授权策略只向表格传入当前页 50 条，完整总数保留', async () => {
    const wrapper = await fixture()
    expect(wrapper.findAllComponents(Table).at(-1)!.props('data')).toHaveLength(50)
    const pagination = wrapper.findAllComponents(Pagination).at(-1)!
    expect(pagination.props('total')).toBe(5001)
    expect(pagination.props('pageSize')).toBe(50)
  })

  it('尾页与本地搜索均可访问完整列表，换页后未保存编辑不丢失', async () => {
    const wrapper = await fixture()
    const pagination = wrapper.findAllComponents(Pagination).at(-1)!
    pagination.vm.$emit('update:currentPage', 101)
    await nextTick()
    const table = wrapper.findAllComponents(Table).at(-1)!
    expect(table.props('data')).toHaveLength(1)
    expect(table.props('data')[0].organization_id).toBe('org-5000')
    table.props('data')[0].allow_self_service = true
    const before = vi.mocked(apiClient.get).mock.calls.length
    await wrapper.get('input[aria-label="搜索组织自助确认策略"]').setValue('组织策略 5000')
    expect(pagination.props('currentPage')).toBe(1)
    expect(pagination.props('total')).toBe(1)
    expect(table.props('data')[0].allow_self_service).toBe(true)
    await wrapper.get('input[aria-label="搜索组织自助确认策略"]').setValue('')
    pagination.vm.$emit('update:currentPage', 101)
    await nextTick()
    expect(table.props('data')[0].allow_self_service).toBe(true)
    pagination.vm.$emit('update:pageSize', 20)
    await nextTick()
    expect(pagination.props('currentPage')).toBe(1)
    expect(table.props('data')).toHaveLength(20)
    expect(apiClient.get).toHaveBeenCalledTimes(before)
  })

  it('非集团账号不渲染策略维护表，不改变原权限边界', async () => {
    principal.isGroupAdmin = false
    const wrapper = await fixture()
    expect(wrapper.findAllComponents(Table)).toHaveLength(1)
    expect(wrapper.text()).not.toContain('组织自助确认策略')
  })
})
