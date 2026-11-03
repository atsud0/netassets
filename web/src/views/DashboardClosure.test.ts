import { flushPromises, shallowMount } from '@vue/test-utils'
import { afterEach, expect, it, vi } from 'vitest'
import { apiClient } from '../api/client'
import DashboardView from './DashboardView.vue'

const { push } = vi.hoisted(() => ({ push: vi.fn() }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push }) }))
vi.mock('../stores/auth', () => ({ useAuthStore: () => ({ hasPermission: () => false }) }))
vi.mock('../api/client', () => ({ apiClient: { get: vi.fn() }, getAPIErrorMessage: () => '读取失败' }))
vi.mock('element-plus', async importOriginal => ({ ...await importOriginal<typeof import('element-plus')>(), ElMessage: { error: vi.fn() } }))
afterEach(() => vi.clearAllMocks())

async function mountDashboard(canViewUndispatched = true) {
  vi.mocked(apiClient.get).mockImplementation(async endpoint => endpoint === '/dashboard/summary'
    ? { data: { summary: {} } }
    : { data: { overview: {
      can_view_undispatched: canViewUndispatched,
      closure_funnel: { discovered_finding_count: 10, asset_linked_finding_count: 8, dispatched_finding_count: 6, dispatched_work_order_count: 3, remediating_finding_count: 3, pending_verification_finding_count: 2, closed_finding_count: 1 },
      undispatched_findings: { finding_count: 4, critical_count: 1, high_count: 2, organization_count: 2, business_system_count: 3 },
      work_order_findings: { active_work_order_count: 2, active_finding_count: 5 },
      finding_severity: [{ key: 'high', label: '高危', count: 2 }], finding_status: [], organization_performance: [],
      finding_trend: [{ date: '2026-11-03', new_finding_count: 3, dispatched_finding_count: 2, closed_finding_count: 1 }],
    } } })
  const wrapper = shallowMount(DashboardView, { global: { directives: { loading: () => {} } } })
  await flushPromises()
  return wrapper
}

it('renders linked closure stages and distinguishes findings from work orders', async () => {
  const wrapper = await mountDashboard()
  expect(wrapper.find('[data-stage="dispatched"]').text()).toContain('6 条')
  expect(wrapper.find('[data-stage="dispatched"]').text()).toContain('3 张工单')
  expect(wrapper.find('[data-coverage="active"]').text()).toContain('涉及漏洞 5 条')
  expect(wrapper.find('[data-testid="undispatched-gap"]').text()).toContain('75%')
  await wrapper.find('[data-stage="asset_linked"]').trigger('click')
  expect(push).toHaveBeenLastCalledWith({ path: '/security-work-orders', query: { tab: 'findings', dashboard_scope: '1', asset_link_state: 'linked' } })
  await wrapper.find('[data-testid="undispatched-gap"]').trigger('click')
  expect(push).toHaveBeenLastCalledWith({ path: '/security-work-orders', query: { tab: 'findings', dashboard_scope: '1', dispatch_state: 'undispatched' } })
  await wrapper.find('[data-coverage="active"]').trigger('click')
  expect(push).toHaveBeenLastCalledWith({ path: '/security-work-orders', query: { tab: 'orders', dashboard_scope: '1', status: 'active' } })
  await wrapper.find('.breakdown-row').trigger('click')
  expect(push).toHaveBeenLastCalledWith({ path: '/security-work-orders', query: { tab: 'findings', dashboard_scope: '1', severity: 'high' } })
  await wrapper.find('.trend-bar.dispatched').trigger('click')
  expect(push).toHaveBeenLastCalledWith({ path: '/security-work-orders', query: { tab: 'findings', dashboard_scope: '1', dispatched_date: '2026-11-03' } })
  wrapper.unmount()
})

it('does not display the undispatched group queue to subsidiaries', async () => {
  const wrapper = await mountDashboard(false)
  expect(wrapper.find('[data-testid="undispatched-gap"]').exists()).toBe(false)
  wrapper.unmount()
})
