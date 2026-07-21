import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import App from './App.vue'

const RouterViewStub = { template: '<div data-test="router-view" />' }

describe('App', () => {
  it('renders the active route outlet', () => {
    const wrapper = mount(App, { global: { stubs: { RouterView: RouterViewStub } } })
    expect(wrapper.find('[data-test="router-view"]').exists()).toBe(true)
  })
})
