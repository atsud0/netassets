import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { ElSelect, ElSelectV2 } from 'element-plus'
import { afterEach, expect, it, vi } from 'vitest'
import { apiClient } from '../api/client'
import { loadOptions } from '../api/remediation'
import RemediationPeopleView from './RemediationPeopleView.vue'

vi.mock('../stores/auth', () => ({ useAuthStore: () => ({}) }))
vi.mock('../api/client', () => ({ apiClient: { get: vi.fn() }, getAPIErrorMessage: () => '读取失败' }))
vi.mock('../api/remediation', () => ({ loadOptions: vi.fn(), dateText: () => '' }))
const Empty = defineComponent({ setup: () => () => h('div') })
const Dialog = defineComponent({ props: ['modelValue'], setup: (props, { slots }) => () => props.modelValue ? h('section', slots.default?.()) : null })
const wrappers: ReturnType<typeof mount>[] = []
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.clearAllMocks() })

it.each(['person', 'vendor'])('%s 选项完整且大数据仅渲染可见窗口，支持搜索尾部选项', async kind => {
  const systems = Array.from({ length: 2806 }, (_, index) => ({ id: `system-${index}`, system_name: `系统-${index}`, organization_id: `org-${Math.floor(index / 2)}`, organization_name: `组织-${Math.floor(index / 2)}` }))
  vi.mocked(loadOptions).mockImplementation(async path => path === '/remediation-options' ? systems : [])
  vi.mocked(apiClient.get).mockResolvedValue({ data: { items: [], total: 0 } })
  const wrapper = mount(RemediationPeopleView, { attachTo: document.body, global: { stubs: {
    ElTable: Empty, ElTabs: Empty, ElDialog: Dialog, ElSelect, ElSelectV2,
    ElPagination: Empty, ElAlert: Empty, ElDatePicker: Empty, ElRadioGroup: Empty,
    RemediationAdministration: Empty,
  }, directives: { loading: () => {} } } })
  wrappers.push(wrapper)
  await flushPromises()
  const state = (wrapper.vm.$ as unknown as { setupState: Record<string, any> }).setupState
  if (kind === 'person') state.openPerson()
  else state.vendorVisible = true
  await flushPromises()
  const selector = wrapper.findComponent(ElSelectV2)
  expect(selector.exists()).toBe(true)
  expect(selector.props('options')).toHaveLength(kind === 'person' ? 2806 : 1403)
  const input = selector.get('input')
  await input.trigger('click')
  await input.setValue(kind === 'person' ? '系统-2805' : '组织-1402')
  await flushPromises()
  await vi.waitFor(() => expect(document.querySelectorAll('.el-select-dropdown__item').length).toBeGreaterThan(0))
  expect(document.querySelectorAll('.el-select-dropdown__item').length).toBeLessThan(100)
  const option = Array.from(document.querySelectorAll<HTMLElement>('.el-select-dropdown__item')).find(item => item.textContent?.includes(kind === 'person' ? '系统-2805' : '组织-1402'))
  expect(option).toBeDefined()
  option!.click()
  await flushPromises()
  expect(kind === 'person' ? state.form.business_system_id : state.vendor.organization_id).toBe(kind === 'person' ? 'system-2805' : 'org-1402')
  if (kind === 'person') expect(state.form.organization_id).toBe('org-1402')
})
