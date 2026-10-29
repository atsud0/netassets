import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { ElForm, ElFormItem, ElInput, ElMessage, type FormInstance } from 'element-plus'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../api/client'
import InventoryTasksView from './InventoryTasksView.vue'

vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }), useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => ({ isGroupAdmin: true, user: { organization: { id: 'org' } } }) }))
vi.mock('../api/client', () => ({ apiClient: { get: vi.fn(), post: vi.fn() }, getAPIErrorMessage: () => '读取失败', getDownloadAPIError: vi.fn() }))
vi.mock('../api/remediation', () => ({ loadOptions: async () => [{ id: 'org', name: '测试组织', type: 'subsidiary', status: 'enabled' }] }))
vi.mock('element-plus', async importOriginal => ({ ...await importOriginal<typeof import('element-plus')>(), ElMessage: { warning: vi.fn(), error: vi.fn(), success: vi.fn() } }))
const Dialog = defineComponent({ props: ['modelValue'], setup: (props, { slots }) => () => props.modelValue ? h('section', [slots.default?.(), slots.footer?.()]) : null })
const Button = defineComponent({ setup: (_, { slots }) => () => h('button', slots.default?.()) })
const Empty = defineComponent({ setup: () => () => h('div') })
const wrappers: ReturnType<typeof mount>[] = []
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.clearAllMocks() })
async function fixture(mode: 'create' | 'submit') {
  vi.mocked(apiClient.get).mockResolvedValue({ data: { items: [], total: 0 } })
  vi.mocked(apiClient.post).mockResolvedValue({ data: { item: { id: 'task' }, scope_preview: { items: [], organization_count: 1 } } })
  const wrapper = mount(InventoryTasksView, { global: { stubs: {
    ElDialog: Dialog, ElButton: Button, ElForm, ElFormItem, ElInput,
    ElTable: Empty, ElSelect: Empty, ElPagination: Empty, ElAlert: Empty, ElDropdown: Empty, ElDrawer: Empty, ElTooltip: Empty,
    ElDivider: Empty, ElDatePicker: Empty, ElCheckbox: Empty, ElCheckboxGroup: Empty, ElSwitch: Empty,
    ElRadioGroup: Empty, ElRadioButton: Empty,
  }, directives: { loading: () => {} } } })
  wrappers.push(wrapper)
  await flushPromises()
  const state = (wrapper.vm.$ as unknown as { setupState: Record<string, any> }).setupState
  if (mode === 'create') {
    state.form.name = '合法任务'
    state.form.status = 'draft'
    state.form.organization_ids = ['org']
    state.createDialogVisible = true
  } else {
    state.selectedTask = { id: 'task', name: '填报任务', required_fields: ['business_systems'], require_review: true }
    state.selectedProgress = { organization_id: 'org', organization_name: '测试组织' }
    state.qualityCheck = { passed: true, issues: [], system_count: 1, asset_count: 0, error_count: 0, warning_count: 0 }
    state.submissionDialogVisible = true
  }
  await flushPromises()
  state.resetScopePreview()
  const submit = () => wrapper.findAll('button').find(button => button.text() === (mode === 'create' ? '保存草稿' : '确认提交'))!.trigger('click')
  return { wrapper, state, submit }
}
const fields = [['name', 255, '任务名称最多 255 字'], ['description', 5000, '任务说明最多 5000 字']] as const
function writes(path: string) { return vi.mocked(apiClient.post).mock.calls.filter(call => call[0] === path) }

describe('填报任务创建文字边界', () => {
  it('空白名称不请求', async () => {
    const { wrapper, state, submit } = await fixture('create')
    state.form.name = '　 '
    await submit(); await flushPromises()
    expect(writes('/inventory-tasks')).toHaveLength(0)
    await vi.waitFor(() => expect(wrapper.text()).toContain('请输入任务名称'))
  })
  it.each(['a', '中', '🙂'].flatMap(unit => fields.map(([field, limit, message]) => [unit, field, limit, message] as const)))('%s %s 超限保留原文且不创建', async (unit, field, limit, message) => {
    const { wrapper, state, submit } = await fixture('create')
    const formAPI = wrapper.findComponent(ElForm).vm.$.exposed as FormInstance
    state.form[field] = unit.repeat(limit + 1)
    await expect(formAPI.validate()).rejects.toHaveProperty(field)
    await submit(); await flushPromises()
    await vi.waitFor(() => expect(wrapper.text()).toContain(message))
    expect(writes('/inventory-tasks')).toHaveLength(0)
    expect(state.form[field]).toBe(unit.repeat(limit + 1))
    expect(state.createDialogVisible).toBe(true)
  })
  it.each(['a', '中', '🙂'])('%s 满额无原生截断且完整创建', async unit => {
    const { wrapper, submit } = await fixture('create')
    const form = wrapper.findComponent(ElForm)
    for (const [field, limit] of fields) {
      const input = form.findAllComponents(ElFormItem).find(item => item.props('label') === (field === 'name' ? '任务名称' : '任务说明'))!.find(field === 'name' ? 'input' : 'textarea')
      expect(input.attributes('maxlength')).toBeUndefined()
      await input.setValue(unit.repeat(limit))
      expect((input.element as HTMLInputElement).value).toBe(unit.repeat(limit))
    }
    await submit(); await flushPromises()
    expect(writes('/inventory-tasks')).toHaveLength(1)
    expect(writes('/inventory-tasks')[0]![1]).toMatchObject({ name: unit.repeat(255), description: unit.repeat(5000) })
  })
})

describe('填报提交说明文字边界', () => {
  it.each(['a', '中', '🙂'])('%s 超限保留弹窗原文且零请求', async unit => {
    const { wrapper, state, submit } = await fixture('submit')
    const input = wrapper.find('textarea')
    await input.setValue(unit.repeat(5001))
    await submit(); await flushPromises()
    expect(ElMessage.warning).toHaveBeenCalledWith('提交说明最多 5000 字')
    expect(writes('/inventory-tasks/task/organizations/org/submit')).toHaveLength(0)
    expect((input.element as HTMLTextAreaElement).value).toBe(unit.repeat(5001))
    expect(state.submissionDialogVisible).toBe(true)
  })
  it.each(['a', '中', '🙂'])('%s 满额无原生截断且完整提交', async unit => {
    const { wrapper, submit } = await fixture('submit')
    const input = wrapper.find('textarea')
    expect(input.attributes('maxlength')).toBeUndefined()
    await input.setValue(unit.repeat(5000))
    await submit(); await flushPromises()
    expect(writes('/inventory-tasks/task/organizations/org/submit')).toHaveLength(1)
    expect(writes('/inventory-tasks/task/organizations/org/submit')[0]![1]).toEqual({ note: unit.repeat(5000) })
  })
  it('留空说明仍可提交', async () => {
    const { submit } = await fixture('submit')
    await submit(); await flushPromises()
    expect(writes('/inventory-tasks/task/organizations/org/submit')[0]![1]).toEqual({ note: '' })
  })
  it('质量检查未通过仍不能提交', async () => {
    const { state, submit } = await fixture('submit')
    state.qualityCheck.passed = false
    await submit(); await flushPromises()
    expect(ElMessage.warning).toHaveBeenCalledWith('请先修正质量检查中的错误项并重新检查')
    expect(writes('/inventory-tasks/task/organizations/org/submit')).toHaveLength(0)
  })
})
