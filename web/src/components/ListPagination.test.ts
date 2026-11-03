import { mount } from '@vue/test-utils'
import { defineComponent, h, nextTick } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ListPagination from './ListPagination.vue'
import { resetListScroll } from '../utils/list-scroll'

const Pagination = defineComponent({
  emits: ['current-change', 'size-change', 'update:current-page'],
  setup: () => () => h('div', { class: 'pagination-stub' }),
})
afterEach(() => { document.body.innerHTML = ''; vi.restoreAllMocks() })

describe('list pagination scroll reset', () => {
  function fixture(dialog = false) {
    const container = document.createElement('div')
    container.className = dialog ? 'el-drawer__body' : 'page-content'
    container.innerHTML = '<section><div class="el-table"><div class="el-table__body-wrapper"><div class="el-scrollbar__wrap"></div></div></div><footer></footer></section>'
    document.body.append(container)
    const table = container.querySelector<HTMLElement>('.el-table')!
    vi.spyOn(table, 'getClientRects').mockReturnValue([{}] as DOMRectList)
    vi.spyOn(table, 'getBoundingClientRect').mockReturnValue({ top: -600 } as DOMRect)
    vi.spyOn(container, 'getBoundingClientRect').mockReturnValue({ top: 100 } as DOMRect)
    container.scrollTop = 900
    return { container, table, footer: container.querySelector('footer')! }
  }

  it.each([false, true])('returns to the nearest list without scrolling the window (drawer=%s)', dialog => {
    const { container, table, footer } = fixture(dialog)
    const body = table.querySelector<HTMLElement>('.el-scrollbar__wrap')!
    body.scrollTop = 400
    resetListScroll(footer)
    expect(container.scrollTop).toBe(188)
    expect(body.scrollTop).toBe(0)
  })

  it('handles page, size, v-model and same-page filter data updates', async () => {
    const { container, footer } = fixture()
    const wrapper = mount(ListPagination, { attachTo: footer, props: { currentPage: 1, pageSize: 20, total: 80, resetKey: [] }, global: { stubs: { ElPagination: Pagination } } })
    const pagination = wrapper.findComponent(Pagination)
    pagination.vm.$emit('current-change', 2)
    await nextTick()
    expect(wrapper.emitted('current-change')).toEqual([[2]])
    expect(container.scrollTop).toBe(188)
    container.scrollTop = 900
    pagination.vm.$emit('size-change', 50)
    await nextTick()
    expect(wrapper.emitted('size-change')).toEqual([[50]])
    expect(container.scrollTop).toBe(188)
    container.scrollTop = 900
    await wrapper.setProps({ currentPage: 3 })
    await nextTick()
    expect(container.scrollTop).toBe(188)
    container.scrollTop = 900
    await wrapper.setProps({ resetKey: [{ id: 'filtered' }] })
    await nextTick()
    expect(container.scrollTop).toBe(188)
    wrapper.unmount()
  })

  it('does not scroll past the heading or filters on initial loading', () => {
    const { container, table, footer } = fixture()
    container.scrollTop = 0
    vi.mocked(table.getBoundingClientRect).mockReturnValue({ top: 450 } as DOMRect)
    resetListScroll(footer)
    expect(container.scrollTop).toBe(0)
  })
})
