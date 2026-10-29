import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { ElForm, ElFormItem, ElInput } from 'element-plus'
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
const fields = [
  ['finding', 'title', '漏洞标题', 255], ['finding', 'vulnerability_type', '漏洞类型', 128],
  ['finding', 'description', '漏洞描述', 20000], ['finding', 'evidence', '漏洞证据', 30000], ['finding', 'remediation_advice', '修复建议', 20000],
  ['order', 'title', '工单标题', 255], ['order', 'description', '工单说明', 10000], ['order', 'remediation_requirement', '整改要求', 20000],
] as const
async function fixture(mode: 'finding' | 'order') {
  vi.mocked(apiClient.get).mockResolvedValue({ data: { items: [], total: 0, stats: {} } })
  vi.mocked(apiClient.post).mockResolvedValue({ data: { item: { id: 'created' } } })
  const wrapper = mount(SecurityWorkOrdersView, { global: { stubs: {
    ElDialog: Dialog, ElForm, ElFormItem, ElInput, ElButton: true,
    ElTable: Empty, ElTabs: Empty, ElSelect: Empty, ElPagination: Empty, ElAlert: Empty, ElDropdown: Empty, ElDrawer: Empty, ElTooltip: Empty,
    ElDivider: Empty, ElDatePicker: Empty, ElSwitch: Empty, ElRadioGroup: Empty, SavedFilterViews: SavedViews,
  }, directives: { loading: () => {} } } })
  wrappers.push(wrapper)
  await flushPromises()
  const state = (wrapper.vm.$ as unknown as { setupState: Record<string, any> }).setupState
  const form = mode === 'finding' ? state.findingForm : state.workOrderForm
  form.title = '合法标题'; form.assignee_organization_id = 'org'
  await flushPromises()
  if (mode === 'finding') {
    form.asset_id = 'asset'; form.description = '合法漏洞描述'; state.findingDialogVisible = true
  } else {
    form.finding_ids = ['finding']; form.remediation_requirement = '合法整改要求'; form.issue_immediately = false; state.workOrderDialogVisible = true
  }
  await flushPromises()
  return { wrapper, form, submit: () => mode === 'finding' ? state.saveFinding() : state.saveWorkOrder() }
}
function writes(mode: 'finding' | 'order') { return vi.mocked(apiClient.post).mock.calls.filter(call => call[0] === (mode === 'finding' ? '/security-findings' : '/security-work-orders')) }

describe('安全发现和整改工单文本字段', () => {
  it.each([
    ['finding', 'title', '请输入漏洞标题'], ['finding', 'description', '请输入漏洞描述'],
    ['order', 'title', '请输入工单标题'], ['order', 'remediation_requirement', '请输入整改要求'],
  ] as const)('%s %s 拒绝纯空白且零请求', async (mode, field, message) => {
    const { wrapper, form, submit } = await fixture(mode)
    form[field] = '　 \n '
    await submit(); await flushPromises()
    expect(writes(mode)).toHaveLength(0)
    await vi.waitFor(() => expect(wrapper.text()).toContain(message))
  })
  it.each(['a', '中', '🙂'].flatMap(unit => fields.map(([mode, field, label, limit]) => [unit, mode, field, label, limit] as const)))('%s %s %s 满额原文可保存', async (unit, mode, field, _label, limit) => {
    const { wrapper, form, submit } = await fixture(mode)
    const value = unit.repeat(limit)
    const item = wrapper.findAll('.el-form-item').find(item => item.find('.el-form-item__label').text() === _label)
    // A native maxlength counts UTF-16 and truncates valid astral characters.
    if (field === 'title') { expect(item).toBeDefined(); expect(item!.find('input').attributes('maxlength')).toBeUndefined() }
    form[field] = value
    await submit(); await flushPromises()
    expect(writes(mode)).toHaveLength(1)
    expect((writes(mode)[0]![1] as Record<string, unknown>)[field]).toBe(value)
  })
  it.each(['a', '中', '🙂'].flatMap(unit => fields.map(([mode, field, label, limit]) => [unit, mode, field, label, limit] as const)))('%s %s %s 超限保留原文且零请求', async (unit, mode, field, label, limit) => {
    const { wrapper, form, submit } = await fixture(mode)
    const value = unit.repeat(limit + 1)
    form[field] = value
    await submit(); await flushPromises()
    expect(writes(mode)).toHaveLength(0)
    expect(form[field]).toBe(value)
    await vi.waitFor(() => expect(wrapper.text()).toContain(`${label}最多 ${limit} 字`))
  })
})
