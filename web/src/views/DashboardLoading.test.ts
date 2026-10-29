import { flushPromises, shallowMount } from '@vue/test-utils'
import { afterEach, expect, it, vi } from 'vitest'
import { apiClient } from '../api/client'
import DashboardView from './DashboardView.vue'

vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => ({ hasPermission: () => false }) }))
vi.mock('../api/client', () => ({ apiClient: { get: vi.fn() }, getAPIErrorMessage: () => '读取失败' }))
vi.mock('element-plus', async importOriginal => ({ ...await importOriginal<typeof import('element-plus')>(), ElMessage: { error: vi.fn() } }))

afterEach(() => vi.clearAllMocks())

it.each(['pending', 'rejected'])('概览请求 %s 时仍展示已返回的逾期统计', async mode => {
  vi.mocked(apiClient.get).mockImplementation(async endpoint => {
    if (endpoint === '/dashboard/summary') return { data: { summary: { overdue_work_order_count: 275 } } }
    if (mode === 'rejected') throw new Error('概览读取失败')
    return new Promise(() => {})
  })
  const wrapper = shallowMount(DashboardView, { global: { directives: { loading: () => {} } } })
  try {
    await flushPromises()
    const card = wrapper.findAll('.stat-card').find(item => item.text().includes('逾期整改工单'))
    expect(card?.find('strong').text()).toBe('275')
  } finally {
    wrapper.unmount()
  }
})
