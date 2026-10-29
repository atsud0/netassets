import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { ElForm, ElFormItem, ElInput, type FormInstance } from 'element-plus'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../api/client'
import BusinessSystemsView from './BusinessSystemsView.vue'

vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }), useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => ({ isGroupAdmin: true, user: { organization: { id: 'org' } } }) }))
vi.mock('../api/client', () => ({ apiClient: { get: vi.fn(), post: vi.fn() }, getAPIErrorMessage: () => '读取失败', getDownloadAPIError: vi.fn() }))
vi.mock('../api/remediation', () => ({ loadOptions: async () => [{ id: 'org', name: '测试组织', status: 'enabled' }] }))
const Dialog = defineComponent({ props: ['modelValue'], setup: (props, { slots }) => () => props.modelValue ? h('section', [slots.default?.(), slots.footer?.()]) : null })
const Button = defineComponent({ setup: (_, { slots }) => () => h('button', slots.default?.()) })
const Empty = defineComponent({ setup: () => () => h('div') })
const wrappers: ReturnType<typeof mount>[] = []
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.clearAllMocks() })
async function fixture() {
  vi.mocked(apiClient.get).mockResolvedValue({ data: { items: [], total: 0 } })
  vi.mocked(apiClient.post).mockResolvedValue({ data: { item: { id: 'created', version: 1 } } })
  const wrapper = mount(BusinessSystemsView, { global: { stubs: {
    ElDialog: Dialog, ElButton: Button, ElForm, ElFormItem, ElInput,
    ElTable: Empty, ElSelect: Empty, ElOption: Empty, ElPagination: Empty, ElAlert: Empty,
    ElDropdown: Empty, ElDrawer: Empty, ElTooltip: Empty, ElInputNumber: Empty,
    WorkingVersionDiffDrawer: Empty, ResourceSubmissionHistoryDrawer: Empty,
  }, directives: { loading: () => {} } } })
  wrappers.push(wrapper)
  await flushPromises()
  await wrapper.findAll('button').find(button => button.text() === '新增业务系统')!.trigger('click')
  const form = wrapper.findComponent(ElForm)
  const model = form.props('model')!
  model.code = 'FIELD_VALID'
  return { wrapper, form, model, formAPI: form.vm.$.exposed as FormInstance }
}
const fields = [['name', 255, '系统名称最多 255 字'], ['short_name', 128, '系统简称最多 128 字'], ['owner_name', 128, '负责人最多 128 字'], ['deployment_type', 32, '部署方式最多 32 字']] as const

describe('业务系统字段 Unicode 长度校验', () => {
  it('空白名称给出字段提示且不请求', async () => {
    const { wrapper, model, form, formAPI } = await fixture()
    model.name = '　 '
    await expect(formAPI.validate()).rejects.toHaveProperty('name')
    await vi.waitFor(() => expect(form.text()).toContain('请输入系统名称'))
    await wrapper.findAll('button').find(button => button.text() === '确认新增')!.trigger('click')
    await flushPromises()
    expect(apiClient.post).not.toHaveBeenCalled()
  })
  it.each(['a', '中', '🙂'])('%s 超限保持原文且不发送保存请求', async unit => {
    const { wrapper, model, form, formAPI } = await fixture()
    model.name = '合法系统'
    for (const [field, limit, message] of fields) {
      model[field] = unit.repeat(limit + 1)
      await expect(formAPI.validate()).rejects.toHaveProperty(field)
      await vi.waitFor(() => expect(form.text()).toContain(message))
      await wrapper.findAll('button').find(button => button.text() === '确认新增')!.trigger('click')
      await flushPromises()
      expect(apiClient.post).not.toHaveBeenCalled()
      expect(model[field]).toBe(unit.repeat(limit + 1))
      model[field] = field === 'name' ? '合法系统' : ''
      formAPI.clearValidate()
    }
  })
  it.each(['a', '中', '🙂'])('%s 满额输入完整显示并提交', async unit => {
    const { wrapper, form, model, formAPI } = await fixture()
    for (const [field, limit] of fields) {
      // Use the actual ElInput rather than assuming assigning the model tests browser length attributes.
      const item = form.findAllComponents(ElFormItem).find(item => item.props('prop') === field)!
      expect(item).toBeDefined()
      await item.find('input').setValue(unit.repeat(limit))
      expect(item.find('input').element.value).toBe(unit.repeat(limit))
      expect(item.find('input').attributes('maxlength')).toBeUndefined()
      expect(model[field]).toBe(unit.repeat(limit))
    }
    await expect(formAPI.validate()).resolves.toBe(true)
    await wrapper.findAll('button').find(button => button.text() === '确认新增')!.trigger('click')
    await flushPromises()
    expect(apiClient.post).toHaveBeenCalledWith('/business-systems', expect.objectContaining(Object.fromEntries(fields.map(([field, limit]) => [field, unit.repeat(limit)]))))
  })
})
