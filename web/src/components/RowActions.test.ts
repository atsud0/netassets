import { mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import RowActions from './RowActions.vue'

const Popover = defineComponent({
  name: 'ElPopover', props: ['visible', 'persistent'], emits: ['update:visible'],
  setup(props, { slots }) { return () => h('div', [slots.reference?.(), props.visible ? slots.default?.() : null]) },
})
const Button = defineComponent({ name: 'ElButton', setup(_, { slots }) { return () => h('button', slots.default?.()) } })

function fixture(disabled = false) {
  const action = vi.fn((event: MouseEvent) => (event.currentTarget as HTMLElement).setAttribute('disabled', ''))
  const wrapper = mount(RowActions, {
    slots: { default: () => [h(Button, { disabled, onClick: action }, () => '校验'), h('a', { href: '/download' }, '下载')] },
    global: { stubs: { ElPopover: Popover, ElButton: Button, ElIcon: true } },
  })
  return { wrapper, action }
}

describe('RowActions', () => {
  it('closes an enabled action before its handler can set loading/disabled, without swallowing the action', async () => {
    const { wrapper, action } = fixture()
    await wrapper.get('button').trigger('click')
    const popover = wrapper.findComponent(Popover)
    await wrapper.get('.row-actions-panel button').trigger('click')
    expect(action).toHaveBeenCalledOnce()
    expect(popover.props('visible')).toBe(false)
    wrapper.unmount()
  })

  it('keeps disabled actions unavailable', async () => {
    const { wrapper, action } = fixture(true)
    await wrapper.get('button').trigger('click')
    const popover = wrapper.findComponent(Popover)
    await wrapper.get('.row-actions-panel button').trigger('click')
    expect(action).not.toHaveBeenCalled()
    expect(popover.props('visible')).toBe(true)
    wrapper.unmount()
  })

  it('creates a popover only on first use and releases its closed content', async () => {
    const { wrapper } = fixture()
    expect(wrapper.findComponent(Popover).exists()).toBe(false)
    await wrapper.get('button').trigger('click')
    const popover = wrapper.findComponent(Popover)
    expect(popover.props('visible')).toBe(true)
    expect(popover.props('persistent')).toBe(false)
    wrapper.unmount()
  })
})
