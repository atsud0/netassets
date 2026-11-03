import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import ListToolbar from './ListToolbar.vue'
import ContextHelp from './ContextHelp.vue'

describe('ListToolbar', () => {
  it('separates main controls from trailing actions with an accessible group name', () => {
    const wrapper = mount(ListToolbar, { props: { label: '资产筛选' }, slots: {
      default: '<input aria-label="搜索" />', actions: '<button>保存视图</button>',
    } })
    expect(wrapper.attributes('aria-label')).toBe('资产筛选')
    expect(wrapper.get('.list-toolbar-main input').exists()).toBe(true)
    expect(wrapper.get('.list-toolbar-end button').text()).toBe('保存视图')
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
  })

  it('hides inactive batch controls and exposes live selection counts', async () => {
    const wrapper = mount(ListToolbar, { props: { selection: 0 }, slots: { actions: '<button>批量处理</button>' } })
    expect(wrapper.find('[role="group"]').exists()).toBe(false)
    await wrapper.setProps({ selection: 1 })
    expect(wrapper.get('[role="status"]').text()).toBe('已选 1 项')
    expect(wrapper.get('.list-toolbar-end button').exists()).toBe(true)
    await wrapper.setProps({ selection: 200 })
    expect(wrapper.get('[role="status"]').text()).toBe('已选 200 项')
    await wrapper.setProps({ selection: 0 })
    expect(wrapper.find('button').exists()).toBe(false)
  })

  it('does not introduce an empty trailing group', () => {
    const wrapper = mount(ListToolbar, { slots: { default: '<input />' } })
    expect(wrapper.find('.list-toolbar-end').exists()).toBe(false)
  })

  it('can hide auxiliary actions without unmounting saved-view state', async () => {
    const wrapper = mount(ListToolbar, { props: { actionsVisible: false }, slots: { actions: '<input value="保留视图" />' } })
    const input = wrapper.get('input').element
    expect((wrapper.get('.list-toolbar-end').element as HTMLElement).style.display).toBe('none')
    expect(wrapper.get('input').element.value).toBe('保留视图')
    await wrapper.setProps({ actionsVisible: true })
    expect((wrapper.get('.list-toolbar-end').element as HTMLElement).style.display).toBe('')
    expect(wrapper.get('input').element).toBe(input)
  })
})

describe('ContextHelp', () => {
  it('keeps rules available through a keyboard-accessible native disclosure', () => {
    const wrapper = mount(ContextHelp, { props: { label: '选择范围' }, slots: { default: '最多 200 项' } })
    expect(wrapper.element.tagName).toBe('DETAILS')
    expect(wrapper.attributes('open')).toBeUndefined()
    expect(wrapper.get('summary').text()).toBe('选择范围')
    expect(wrapper.get('.context-help-content').text()).toBe('最多 200 项')
  })
})
