import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AssetCandidatesView from './AssetCandidatesView.vue'

// "待确认资产 已忽略tab/无效tab中，状态下理由过长需要显示缩略".
//
// The review note was printed in full inside a 120px-wide 状态 column. The cell
// wrapped instead of clipping, so a single long reason stretched its row to
// dozens of lines and pushed every column below it out of alignment.
//
// Truncation alone would be a regression of its own: the reason is the only
// explanation of why a lead was dismissed, and a security manager has to be able
// to read it. Both properties are asserted here - the element is clamped, and the
// full text is still on the element for a tooltip.

const mocks = vi.hoisted(() => ({ get: vi.fn() }))

vi.mock('../api/client', () => ({
  apiClient: mocks,
  getAPIErrorMessage: (_error: unknown, fallback: string) => fallback,
}))

vi.mock('../stores/auth', () => ({
  useAuthStore: () => ({ isGroupAdmin: true, hasPermission: () => true }),
}))

const LONG_REASON = '该线索由外部资产发现工具自动上报，资产值与集团现有域名记录重复，且所属组织已确认无需再次登记，因此本次忽略，不再生成正式资产。'
const SHORT_REASON = '重复资产'

function candidate(overrides: Record<string, unknown>) {
  return {
    id: 'candidate-1', asset_type: 'domain', asset_value: 'a.example.com',
    normalized_value: 'a.example.com', source: 'api', suggested_data: {},
    status: 'ignored', created_at: '2026-10-01T08:00:00Z', ...overrides,
  }
}

// Element Plus columns render their default slot once per row, driven by the table
// that owns them, so the real components are used rather than stubs. An earlier
// attempt stubbed them and provided the rows through provide/inject, which cannot
// work: slot content is compiled in the parent's scope, so the columns would never
// see the injected rows and the test would have asserted nothing.
function mountWith(rows: unknown[]) {
  mocks.get.mockImplementation((path: string) => {
    if (path === '/asset-candidates') return Promise.resolve({ data: { items: rows, total: rows.length, page: 1, page_size: 20 } })
    return Promise.resolve({ data: { items: [] } })
  })
  return mount(AssetCandidatesView, {
    attachTo: document.body,
    global: {
      stubs: {
        ElDialog: true, ElPagination: true, ElDatePicker: true, ElRadioGroup: true, ElRadioButton: true,
      },
      directives: { loading: () => undefined },
    },
  })
}

describe('待确认资产 review note is clamped instead of stretching the row', () => {
  beforeEach(() => vi.clearAllMocks())

  it('clamps a long reason to two lines and keeps the full text for a tooltip', async () => {
    const wrapper = mountWith([candidate({ review_note: LONG_REASON })])
    await new Promise(resolve => setTimeout(resolve, 0))
    await wrapper.vm.$nextTick()

    const clamped = wrapper.findAll('.clamped-reason')
    expect(clamped.length, 'the review note element was not rendered').toBe(1)
    const style = clamped[0].attributes('style') ?? ''
    expect(style).toContain('-webkit-box')
    expect(style).toContain('-webkit-line-clamp: 2')
    expect(style, 'without overflow:hidden the text still paints outside the clamp').toContain('overflow: hidden')
    // The whole reason must remain reachable, otherwise the user can no longer
    // find out why a lead was dismissed.
    expect(clamped[0].attributes('title')).toBe(LONG_REASON)
  })

  it('renders the short reason unclamped but still readable', async () => {
    const wrapper = mountWith([candidate({ review_note: SHORT_REASON })])
    await new Promise(resolve => setTimeout(resolve, 0))
    await wrapper.vm.$nextTick()

    const clamped = wrapper.find('.clamped-reason')
    expect(clamped.exists()).toBe(true)
    expect(clamped.text()).toBe(SHORT_REASON)
    expect(clamped.attributes('title')).toBe(SHORT_REASON)
  })

  it('renders no reason element when there is no note', async () => {
    const wrapper = mountWith([candidate({ status: 'invalid' })])
    await new Promise(resolve => setTimeout(resolve, 0))
    await wrapper.vm.$nextTick()
    expect(wrapper.findAll('.clamped-reason').length).toBe(0)
  })
})
