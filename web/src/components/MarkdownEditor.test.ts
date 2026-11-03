import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import MarkdownEditor from './MarkdownEditor.vue'
import MarkdownPreview from './MarkdownPreview.vue'

function clickMode(wrapper: ReturnType<typeof mount>, text: string) {
  return wrapper.findAll('button').find((button) => button.text() === text)!.trigger('click')
}

describe('MarkdownEditor', () => {
  it('edits original source and preserves it while switching between modes', async () => {
    const source = '# 漏洞\n\n```http\nGET /api\n```\n'
    const wrapper = mount(MarkdownEditor, { props: { modelValue: source, label: '漏洞描述' } })
    const input = wrapper.get('textarea')
    expect(input.attributes('aria-label')).toBe('漏洞描述')
    expect(input.element.value).toBe(source)
    const changed = `${source}\n1. 复现步骤\n`
    await input.setValue(changed)
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([changed])
    await wrapper.setProps({ modelValue: changed })
    await clickMode(wrapper, '预览')
    expect(wrapper.get('[aria-label="漏洞描述预览"] h1').text()).toBe('漏洞')
    expect((wrapper.get('.markdown-source').element as HTMLElement).style.display).toBe('none')
    await clickMode(wrapper, '分栏')
    expect((wrapper.get('.markdown-source').element as HTMLElement).style.display).not.toBe('none')
    expect(wrapper.get('textarea').element.value).toBe(changed)
    expect(wrapper.get('[aria-label="漏洞描述预览"] ol').text()).toBe('复现步骤')
    await clickMode(wrapper, '编辑')
    expect(wrapper.findComponent(MarkdownPreview).exists()).toBe(false)
    expect(wrapper.get('textarea').element.value).toBe(changed)
    expect(wrapper.get('[aria-pressed="true"]').text()).toBe('编辑')
  })

  it('updates preview from the same model without converting emitted source to HTML', async () => {
    const wrapper = mount(MarkdownEditor, { props: { modelValue: '', label: '验证证据' } })
    await clickMode(wrapper, '分栏')
    expect(wrapper.get('[aria-label="验证证据预览"]').text()).toBe('暂无内容')
    await wrapper.setProps({ modelValue: '**证据**\n下一行' })
    expect(wrapper.get('strong').text()).toBe('证据')
    expect(wrapper.get('textarea').element.value).toBe('**证据**\n下一行')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('retains the disabled edit state while allowing read-only preview', async () => {
    const wrapper = mount(MarkdownEditor, { props: { modelValue: '# 记录', disabled: true } })
    expect(wrapper.get('textarea').attributes('disabled')).toBeDefined()
    await clickMode(wrapper, '预览')
    expect(wrapper.get('h1').text()).toBe('记录')
  })
})

describe('MarkdownPreview', () => {
  it('uses the same renderer for saved detail and updates when the content changes', async () => {
    const wrapper = mount(MarkdownPreview, { props: { content: '普通文本\n下一行' } })
    expect(wrapper.get('p').html()).toContain('<br>')
    await wrapper.setProps({ content: '<script>alert(1)</script>\n\n# 新内容' })
    expect(wrapper.find('script').exists()).toBe(false)
    expect(wrapper.text()).toContain('<script>alert(1)</script>')
    expect(wrapper.get('h1').text()).toBe('新内容')
  })
})
