import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { ElForm, ElFormItem, ElInput, type FormInstance } from 'element-plus'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../api/client'
import AssetCandidatesView from './AssetCandidatesView.vue'

vi.mock('../api/client', () => ({ apiClient: { get: vi.fn(), post: vi.fn() }, getAPIErrorMessage: () => '读取失败' }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => ({ isGroupAdmin: true }) }))
const Dialog = defineComponent({ name: 'ElDialog', props: ['modelValue'], setup: (props, { slots }) => () => props.modelValue ? h('section', [slots.default?.(), slots.footer?.()]) : null })
const Button = defineComponent({ name: 'ElButton', setup: (_, { slots }) => () => h('button', slots.default?.()) })
const Empty = defineComponent({ setup: () => () => h('div') })
const wrappers: ReturnType<typeof mount>[] = []
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.clearAllMocks() })

async function fixture() {
  vi.mocked(apiClient.get).mockImplementation(async url => ({ data: { items: url === '/organizations' ? [{ id: 'org', name: '测试组织', status: 'enabled' }] : [], total: 0 } }))
  vi.mocked(apiClient.post).mockResolvedValue({ data: {} })
  const wrapper = mount(AssetCandidatesView, { global: { stubs: {
    ElDialog: Dialog, ElButton: Button, ElForm, ElFormItem, ElInput,
    ElRadioGroup: Empty, ElRadioButton: Empty, ElTable: Empty, ElSelect: Empty, ElOption: Empty, ElPagination: Empty, ElAlert: Empty, ElTooltip: Empty,
  }, directives: { loading: () => {} } } })
  wrappers.push(wrapper)
  await flushPromises()
  await wrapper.findAll('button').find(button => button.text() === '新增待确认资产')!.trigger('click')
  const form = wrapper.findComponent(ElForm)
  const model = form.props('model')!
  const formAPI = form.vm.$.exposed as FormInstance
  model.asset_value = 'candidate.example.com'
  return { wrapper, form, model, formAPI }
}

describe('待确认资产创建字段校验', () => {
  it('空白资产值在表单内给出字段提示', async () => {
    const { wrapper, form, model, formAPI } = await fixture()
    model.asset_value = '　 '
    await expect(formAPI.validate()).rejects.toHaveProperty('asset_value')
    await vi.waitFor(() => expect(form.text()).toContain('请输入资产值'))
    await wrapper.findAll('button').find(button => button.text() === '保存线索')!.trigger('click')
    await flushPromises()
    expect(apiClient.post).not.toHaveBeenCalled()
  })

  it.each(['a', '中', '🙂'])('%s 按 Unicode 字符拒绝超限并保留原文', async unit => {
    const { form, model, formAPI } = await fixture()
    for (const [field, limit, message] of [['name', 255, '资产名称最多 255 字'], ['source_reference', 255, '来源参考最多 255 字'], ['notes', 5000, '备注最多 5000 字']] as const) {
      const value = unit.repeat(limit + 1)
      model[field] = value
      await expect(formAPI.validate()).rejects.toHaveProperty(field)
      await vi.waitFor(() => expect(form.text()).toContain(message))
      expect(model[field]).toBe(value)
      model[field] = ''
      formAPI.clearValidate()
    }
    expect(apiClient.post).not.toHaveBeenCalled()
  })

  it.each(['a', '中', '🙂'])('%s 满额合法输入完整提交、不截断', async unit => {
    const { wrapper, formAPI, model } = await fixture()
    model.name = unit.repeat(255)
    model.source_reference = unit.repeat(255)
    model.notes = unit.repeat(5000)
    await expect(formAPI.validate()).resolves.toBe(true)
    await wrapper.findAll('button').find(button => button.text() === '保存线索')!.trigger('click')
    await flushPromises()
    expect(apiClient.post).toHaveBeenCalledWith('/asset-candidates', expect.objectContaining({ name: unit.repeat(255), source_reference: unit.repeat(255), notes: unit.repeat(5000) }))
  })
})
