import { shallowMount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import SavedFilterViews from './SavedFilterViews.vue'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  delete: vi.fn(),
}))

vi.mock('../api/client', () => ({
  apiClient: mocks,
  getAPIErrorMessage: (_error: unknown, fallback: string) => fallback,
}))

const defaultView = {
  id: '2cf486e9-71c8-4a48-82e4-17e3ab76b53d',
  page_key: 'assets',
  name: '待确认资产',
  filters: { status: 'pending', type: 'domain' },
  is_default: true,
  created_at: '2026-07-28T00:00:00Z',
  updated_at: '2026-07-28T00:00:00Z',
}

function mountComponent() {
  return shallowMount(SavedFilterViews, {
    props: { pageKey: 'assets', currentFilters: { status: 'active' } },
    global: {
      stubs: {
        ElSelect: true,
        ElOption: true,
        ElButton: true,
        ElTag: true,
        ElDialog: true,
        ElForm: true,
        ElFormItem: true,
        ElInput: true,
        ElCheckbox: true,
      },
    },
  })
}

describe('SavedFilterViews', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('loads the bounded personal list and returns the default filters', async () => {
    mocks.get.mockResolvedValue({ data: { items: [defaultView], total: 1, page: 1, page_size: 20 } })
    const wrapper = mountComponent()

    const filters = await (wrapper.vm as unknown as { initialize: () => Promise<Record<string, string> | undefined> }).initialize()

    expect(mocks.get).toHaveBeenCalledWith('/saved-filter-views', {
      params: { page_key: 'assets', page: 1, page_size: 20 },
    })
    expect(filters).toEqual({ status: 'pending', type: 'domain' })
    expect(filters).not.toBe(defaultView.filters)
  })

  it('does not invent a default when the user has only ordinary views', async () => {
    mocks.get.mockResolvedValue({ data: { items: [{ ...defaultView, is_default: false }], total: 1, page: 1, page_size: 20 } })
    const wrapper = mountComponent()

    const filters = await (wrapper.vm as unknown as { initialize: () => Promise<Record<string, string> | undefined> }).initialize()

    expect(filters).toBeUndefined()
  })
})
