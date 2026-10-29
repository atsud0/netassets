import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h, nextTick } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../api/client'
import OrganizationsView from './OrganizationsView.vue'

vi.mock('../api/client', () => ({ apiClient: { get: vi.fn() }, getAPIErrorMessage: () => '读取失败' }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => ({ isGroupAdmin: true }) }))

const Table = defineComponent({ name: 'ElTable', props: ['data'], setup: () => () => h('div') })
const Pagination = defineComponent({ name: 'ElPagination', props: ['currentPage', 'pageSize', 'total'], emits: ['update:currentPage', 'update:pageSize'], setup: () => () => h('div') })
const Input = defineComponent({
  name: 'ElInput', props: ['modelValue'], emits: ['update:modelValue'],
  setup: (props, { emit }) => () => h('input', { value: props.modelValue, onInput: (event: Event) => emit('update:modelValue', (event.target as HTMLInputElement).value) }),
})
const Button = defineComponent({ name: 'ElButton', setup: (_, { slots }) => () => h('button', slots.default?.()) })
const Dialog = defineComponent({ name: 'ElDialog', setup: () => () => null })
const wrappers: ReturnType<typeof mount>[] = []
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.clearAllMocks() })

async function fixture() {
  const children = Array.from({ length: 5000 }, (_, index) => ({
    id: `org-${index}`, parent_id: 'root', code: `ORG_${index}`, name: `组织 ${index}`, short_name: index === 4999 ? '末尾简称' : '',
    type: 'subsidiary', status: 'enabled', can_update: true, can_create_child: true, children: [],
  }))
  const root = { id: 'root', code: 'GROUP', name: '测试集团', type: 'group', status: 'enabled', can_update: true, can_create_child: true, children }
  vi.mocked(apiClient.get).mockResolvedValue({ data: { items: [root], total: 5001 } })
  const wrapper = mount(OrganizationsView, {
    global: { stubs: { ElTable: Table, ElPagination: Pagination, ElInput: Input, ElButton: Button, ElDialog: Dialog }, directives: { loading: () => {} } },
  })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}

describe('组织树分段渲染', () => {
  it('完整 5001 节点只向表格提供当前页的 50 个无嵌套节点，不改变完整总数', async () => {
    const wrapper = await fixture()
    const rows = wrapper.findComponent(Table).props('data')
    expect(rows).toHaveLength(50)
    expect(rows.every((row: { children?: unknown[] }) => !row.children?.length)).toBe(true)
    expect(rows.map((row: { id: string }) => row.id)).toEqual(['root', ...Array.from({ length: 49 }, (_, i) => `org-${i}`)])
    expect(wrapper.findComponent(Pagination).props('total')).toBe(5001)
    expect(wrapper.text()).toContain('5001 个组织节点')
  })

  it('本地翻页及按简称搜索保留祖先路径，不重复请求或隐式展开完整 DOM', async () => {
    const wrapper = await fixture()
    wrapper.findComponent(Pagination).vm.$emit('update:currentPage', 101)
    await nextTick()
    expect(wrapper.findComponent(Table).props('data').map((row: { id: string }) => row.id)).toEqual(['org-4999'])
    await wrapper.get('input[aria-label="搜索组织"]').setValue('末尾简称')
    await nextTick()
    const rows = wrapper.findComponent(Table).props('data')
    expect(rows.map((row: { id: string }) => row.id)).toEqual(['root', 'org-4999'])
    expect(rows[1].treePath).toEqual(['测试集团'])
    expect(wrapper.findComponent(Pagination).props('currentPage')).toBe(1)
    expect(apiClient.get).toHaveBeenCalledTimes(1)
  })

  it('折叠全部只保留根，搜索深层节点仍可达，清空搜索恢复折叠状态', async () => {
    const wrapper = await fixture()
    const collapse = wrapper.findAll('button').find(button => button.text() === '收起全部')!
    expect(collapse).toBeDefined()
    await collapse.trigger('click')
    expect(wrapper.findComponent(Table).props('data').map((row: { id: string }) => row.id)).toEqual(['root'])
    await wrapper.get('input[aria-label="搜索组织"]').setValue('ORG_4999')
    expect(wrapper.findComponent(Table).props('data').map((row: { id: string }) => row.id)).toEqual(['root', 'org-4999'])
    await wrapper.get('input[aria-label="搜索组织"]').setValue('')
    expect(wrapper.findComponent(Table).props('data').map((row: { id: string }) => row.id)).toEqual(['root'])
    expect(apiClient.get).toHaveBeenCalledTimes(1)
  })

  it('末页收起后页码钳制到有效页，变更每页条数重置页码且不丢完整总数', async () => {
    const wrapper = await fixture()
    const pagination = wrapper.findComponent(Pagination)
    pagination.vm.$emit('update:currentPage', 101)
    await nextTick()
    await wrapper.findAll('button').find(button => button.text() === '收起全部')!.trigger('click')
    expect(pagination.props('currentPage')).toBe(1)
    expect(pagination.props('total')).toBe(1)
    await wrapper.findAll('button').find(button => button.text() === '展开全部')!.trigger('click')
    expect(pagination.props('total')).toBe(5001)
    pagination.vm.$emit('update:currentPage', 20)
    pagination.vm.$emit('update:pageSize', 25)
    await nextTick()
    expect(pagination.props('currentPage')).toBe(1)
    expect(wrapper.findComponent(Table).props('data')).toHaveLength(25)
    expect(wrapper.text()).toContain('5001 个组织节点')
  })

})
