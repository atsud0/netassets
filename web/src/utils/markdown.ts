import MarkdownIt from 'markdown-it'

const markdown = new MarkdownIt({ html: false, breaks: true, linkify: false, typographer: false })
const validateLink = markdown.validateLink.bind(markdown)
markdown.validateLink = (url) => {
  if (!validateLink(url)) return false
  const scheme = /^([a-z][a-z\d+.-]*):/i.exec(url.trim())?.[1]?.toLowerCase()
  return !scheme || ['http', 'https', 'mailto'].includes(scheme)
}
markdown.renderer.rules.link_open = (tokens, index, options, _env, renderer) => {
  tokens[index].attrSet('target', '_blank')
  tokens[index].attrSet('rel', 'noopener noreferrer')
  return renderer.renderToken(tokens, index, options)
}
markdown.renderer.rules.image = (tokens, index) =>
  `<span class="markdown-image-placeholder">[图片：${markdown.utils.escapeHtml(tokens[index].content || '未命名图片')}]</span>`

export function renderMarkdown(content: string): string {
  return markdown.render(content)
}
