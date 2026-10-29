import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { ElForm, ElFormItem, ElInput, ElTable, ElTableColumn, ElMessage } from 'element-plus'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../api/client'
import InventoryTasksView from './InventoryTasksView.vue'

vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }), useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => ({ isGroupAdmin: true, user: { organization: { id: 'org' } } }) }))
vi.mock('../api/client', () => ({ apiClient: { get: vi.fn(), post: vi.fn() }, getAPIErrorMessage: () => '读取失败', getDownloadAPIError: vi.fn() }))
vi.mock('../api/remediation', () => ({ loadOptions: async () => [] }))
vi.mock('element-plus', async importOriginal => ({ ...await importOriginal<typeof import('element-plus')>(), ElMessage: { warning: vi.fn(), error: vi.fn(), success: vi.fn() } }))
const Dialog = defineComponent({ props: ['modelValue'], setup: (props, { slots }) => () => props.modelValue ? h('section', [slots.default?.(), slots.footer?.()]) : null })
const Button = defineComponent({ setup: (_, { slots }) => () => h('button', slots.default?.()) })
const Empty = defineComponent({ setup: () => () => h('div') })
const wrappers: ReturnType<typeof mount>[] = []
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.clearAllMocks() })
async function fixture() {
  vi.mocked(apiClient.get).mockResolvedValue({ data: { items: [], total: 0 } })
  vi.mocked(apiClient.post).mockResolvedValue({ data: { item: { status: 'returned' } } })
  const wrapper = mount(InventoryTasksView, { global: { stubs: {
    ElDialog: Dialog, ElButton: Button, ElForm, ElFormItem, ElInput, ElTable, ElTableColumn,
    ElSelect: Empty, ElPagination: Empty, ElAlert: Empty, ElDropdown: Empty, ElDrawer: Empty, ElTooltip: Empty,
    ElDivider: Empty, ElDatePicker: Empty, ElCheckbox: Empty, ElCheckboxGroup: Empty, ElSwitch: Empty,
  }, directives: { loading: () => {} } } })
  wrappers.push(wrapper)
  await flushPromises()
  // Exercise the rendered dialog and its real ElInputs without manufacturing
  // task/detail API workflows in this focused component regression.
  const state = (wrapper.vm.$ as unknown as { setupState: Record<string, any> }).setupState
  state.selectedTask = { id: 'task', name: '审核任务' }
  state.selectedProgress = { organization_id: 'org', organization_name: '测试组织' }
  const resource = { resource_type: 'business_system', resource_id: 'system', resource_key: 'SYS', resource_name: '待审核系统', snapshot_data: {} }
  state.returnResources = [resource]
  state.returnResourceIndex['business_system:system'] = resource
  state.returnDialogVisible = true
  state.returnForm.reason = '合法整体说明'
  await flushPromises()
  const reason = wrapper.find('textarea')
  const comment = wrapper.find('input[placeholder="针对该条数据填写整改要求（可选）"]')
  const submit = () => wrapper.findAll('button').find(button => button.text() === '确认退回')!.trigger('click')
  return { wrapper, state, reason, comment, submit }
}

describe('填报退回原因与逐条意见 Unicode 边界', () => {
  it('整体原因空白提示且不请求', async () => {
    const { reason, submit } = await fixture()
    await reason.setValue('　 ')
    await submit(); await flushPromises()
    expect(ElMessage.warning).toHaveBeenCalledWith('请填写退回原因')
    expect(apiClient.post).not.toHaveBeenCalled()
  })
  it.each(['a', '中', '🙂'])('%s 两字段超限保留原文并阻止请求', async unit => {
    const { reason, comment, submit, state } = await fixture()
    for (const [input, message] of [[reason, '退回原因最多 2000 字'], [comment, '逐条审核意见最多 2000 字']] as const) {
      await input.setValue(unit.repeat(2001))
      await submit(); await flushPromises()
      expect(ElMessage.warning).toHaveBeenLastCalledWith(message)
      expect(apiClient.post).not.toHaveBeenCalled()
      expect((input.element as HTMLInputElement | HTMLTextAreaElement).value).toBe(unit.repeat(2001))
      expect(state.returnDialogVisible).toBe(true)
      await input.setValue('合法说明')
    }
  })
  it.each(['a', '中', '🙂'])('%s 满额原文无原生截断并完整提交', async unit => {
    const { reason, comment, submit } = await fixture()
    for (const input of [reason, comment]) {
      expect(input.attributes('maxlength')).toBeUndefined()
      await input.setValue(unit.repeat(2000))
      expect((input.element as HTMLInputElement | HTMLTextAreaElement).value).toBe(unit.repeat(2000))
    }
    await submit(); await flushPromises()
    expect(apiClient.post).toHaveBeenCalledWith('/inventory-tasks/task/organizations/org/review', {
      decision: 'returned', return_reason: unit.repeat(2000), comments: [{ resource_type: 'business_system', resource_id: 'system', comment: unit.repeat(2000) }],
    })
  })
})
