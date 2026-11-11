import { describe, expect, it } from 'vitest'
import { renderMarkdown } from './markdown'

function documentFor(source: string) {
  const element = document.createElement('div')
  element.innerHTML = renderMarkdown(source)
  return element
}

describe('Markdown rendering', () => {
  it('renders the supported report structures and keeps plain text line breaks', () => {
    const element = documentFor('# 风险报告\n\n第一行\n第二行\n\n- 条目一\n- 条目二\n\n1. 第一步\n2. 第二步\n\n> 引用\n\n`GET /api`\n\n```http\nGET /api HTTP/1.1\nX-Test: <evidence>\n```\n\n| 位置 | 结果 |\n| --- | --- |\n| /api | 受影响 |\n\n[材料](https://example.com/evidence)')
    expect(element.querySelector('h1')?.textContent).toBe('风险报告')
    expect(element.querySelector('p')?.innerHTML).toBe('第一行<br>\n第二行')
    expect(element.querySelectorAll('ul li')).toHaveLength(2)
    expect(element.querySelectorAll('ol li')).toHaveLength(2)
    expect(element.querySelector('blockquote')?.textContent).toContain('引用')
    expect(element.querySelector('pre code')?.textContent).toBe('GET /api HTTP/1.1\nX-Test: <evidence>\n')
    expect(element.querySelector('table tbody td')?.textContent).toBe('/api')
    const link = element.querySelector('a')!
    expect(link.getAttribute('href')).toBe('https://example.com/evidence')
    expect(link.getAttribute('target')).toBe('_blank')
    expect(link.getAttribute('rel')).toBe('noopener noreferrer')
  })

  it.each([
    '<script>alert(1)</script>',
    '<img src=x onerror=alert(1)>',
    '<iframe src="javascript:alert(1)"></iframe>',
    '<svg onload=alert(1)>',
    '<form id="app"><input name="parentNode"></form>',
  ])('shows HTML as text without active elements: %s', (source) => {
    const element = documentFor(source)
    expect(element.querySelector('script,img,iframe,svg,form,input')).toBeNull()
    expect(element.textContent).toContain(source)
  })

  it.each([
    '[bad](javascript:alert%281%29)',
    '[bad](JaVaScRiPt:alert%281%29)',
    '[bad](java&#x73;cript:alert%281%29)',
    '[bad](vbscript:msgbox%281%29)',
    '[bad](data:text/html;base64,PHNjcmlwdD4=)',
    '[bad](file:///etc/passwd)',
    '[bad](vscode://file/etc/passwd)',
    '[bad](data:image/png;base64,AAAA)',
  ])('does not create an actionable unsafe link: %s', (source) => {
    expect(documentFor(source).querySelector('a')).toBeNull()
  })

  it('does not fetch embedded images and escapes malicious image descriptions', () => {
    const element = documentFor('![证据截图](https://example.com/tracker.png)\n\n![<img src=x onerror=alert(1)>](https://example.com/image.png)')
    expect(element.querySelector('img')).toBeNull()
    expect(element.querySelectorAll('.markdown-image-placeholder')).toHaveLength(2)
    expect(element.textContent).toContain('[图片：证据截图]')
    expect(element.textContent).toContain('<img src=x onerror=alert(1)>')
  })

  it('escapes code without interpreting attributes supplied as a language name', () => {
    const element = documentFor('```"><img src=x onerror=alert(1)>\n<script>alert(1)</script>\n```')
    expect(element.querySelector('img,script')).toBeNull()
    expect(element.querySelector('code')?.textContent).toBe('<script>alert(1)</script>\n')
  })
})
