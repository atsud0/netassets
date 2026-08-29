import { flushPromises, shallowMount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AssetExternalObservations from './AssetExternalObservations.vue'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
}))

vi.mock('../api/client', () => ({
  apiClient: mocks,
  getAPIErrorMessage: (_error: unknown, fallback: string) => fallback,
}))

const successItems = [
  {
    id: '6f34ea1a-4f7a-4d75-9e4b-58f2f1f6ef1b',
    asset_id: '28d545ab-4a7b-4132-9a12-8f025e6d5c48',
    observation_type: 'domain_registration' as const,
    endpoint_key: 'example.com',
    status: 'success' as const,
    source: 'rdap',
    source_reference: 'https://rdap.verisign.com/com/v1/domain/example.com',
    expires_at: '2026-08-30T00:00:00Z',
    last_attempt_at: '2026-08-29T08:00:00Z',
    last_success_at: '2026-08-29T08:00:00Z',
    next_refresh_at: '2026-08-29T14:00:00Z',
    consecutive_failures: 0,
    error_code: '',
    error_message: '',
  },
]

function mountObservations(props: Partial<InstanceType<typeof AssetExternalObservations>["$props"]> = {}) {
  return shallowMount(AssetExternalObservations, {
    props: { assetId: '28d545ab-4a7b-4132-9a12-8f025e6d5c48', assetType: 'domain', canRefresh: true, ...props },
    global: {
      directives: { loading: () => undefined },
    },
  })
}

describe('AssetExternalObservations', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.get.mockResolvedValue({ data: { items: successItems } })
    mocks.post.mockResolvedValue({ data: { result: { items: successItems } } })
  })

  it('loads observations for a supported formal asset', async () => {
    mountObservations()
    await flushPromises()

    expect(mocks.get).toHaveBeenCalledWith('/assets/28d545ab-4a7b-4132-9a12-8f025e6d5c48/external-observations')
  })

  it('does not query unsupported asset types', async () => {
    mountObservations({ assetType: 'ip' })
    await flushPromises()

    expect(mocks.get).not.toHaveBeenCalled()
  })

  it('refreshes and replaces observations from the refresh result', async () => {
    const wrapper = mountObservations()
    await flushPromises()
    const vm = wrapper.vm as unknown as { refresh: () => Promise<void> }

    await vm.refresh()

    expect(mocks.post).toHaveBeenCalledWith('/assets/28d545ab-4a7b-4132-9a12-8f025e6d5c48/external-observations/refresh')
    expect((wrapper.vm as unknown as { items: unknown }).items).toHaveLength(1)
  })

  it('explains that a failed refresh retains the last successful expiry', async () => {
    mocks.get.mockResolvedValue({
      data: {
        items: [{
          ...successItems[0],
          status: 'failed' as const,
          consecutive_failures: 2,
          error_code: 'rdap_http_502',
          error_message: 'RDAP service temporarily unavailable',
        }],
      },
    })
    const wrapper = mountObservations()
    await flushPromises()
    const vm = wrapper.vm as unknown as { statusLabel: (item: { status: string }) => string }

    expect(vm.statusLabel({ status: 'failed' })).toBe('采集失败')
    expect(wrapper.text()).toContain('本次刷新失败，以上到期时间来自最近一次成功采集。')
    expect(wrapper.text()).toContain('RDAP service temporarily unavailable')
  })
})
