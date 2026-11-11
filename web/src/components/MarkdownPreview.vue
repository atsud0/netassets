<script setup lang="ts">
import { computed } from 'vue'
import { renderMarkdown } from '../utils/markdown'

const props = withDefaults(defineProps<{ content?: string; label?: string }>(), {
  content: '', label: 'Markdown 预览',
})
const html = computed(() => renderMarkdown(props.content))
</script>

<template>
  <div class="markdown-preview" role="region" :aria-label="label">
    <div v-if="content.trim()" class="markdown-body" v-html="html" />
    <p v-else class="markdown-empty">暂无内容</p>
  </div>
</template>

<style scoped>
.markdown-preview { min-width: 0; max-width: 100%; color: var(--ink, #17202a); overflow-wrap: anywhere; line-height: 1.7; }
.markdown-empty { margin: 0; color: var(--muted, #6b7785); }
.markdown-body :deep(> :first-child) { margin-top: 0; }
.markdown-body :deep(> :last-child) { margin-bottom: 0; }
.markdown-body :deep(p) { margin: 0 0 12px; white-space: normal; }
.markdown-body :deep(h1), .markdown-body :deep(h2), .markdown-body :deep(h3), .markdown-body :deep(h4), .markdown-body :deep(h5), .markdown-body :deep(h6) { margin: 20px 0 10px; line-height: 1.4; font-weight: 650; }
.markdown-body :deep(h1) { font-size: 1.65em; }
.markdown-body :deep(h2) { font-size: 1.4em; }
.markdown-body :deep(h3) { font-size: 1.2em; }
.markdown-body :deep(ul), .markdown-body :deep(ol) { margin: 0 0 12px; padding-left: 25px; }
.markdown-body :deep(li > ul), .markdown-body :deep(li > ol) { margin-bottom: 0; }
.markdown-body :deep(blockquote) { margin: 12px 0; padding: 4px 14px; border-left: 3px solid var(--cyan, #2a9d8f); background: #f5f9fb; color: var(--muted, #6b7785); }
.markdown-body :deep(blockquote p:last-child) { margin-bottom: 0; }
.markdown-body :deep(a) { color: var(--blue, #1d6fa5); text-decoration: underline; }
.markdown-body :deep(code) { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: .9em; padding: 2px 5px; border-radius: 4px; background: #eef2f6; white-space: break-spaces; }
.markdown-body :deep(pre) { max-width: 100%; overflow-x: auto; margin: 12px 0; padding: 14px; border-radius: 8px; background: #eef2f6; line-height: 1.5; }
.markdown-body :deep(pre code) { display: block; padding: 0; white-space: pre; overflow-wrap: normal; }
.markdown-body :deep(table) { display: block; max-width: 100%; overflow-x: auto; border-collapse: collapse; margin: 12px 0; }
.markdown-body :deep(th), .markdown-body :deep(td) { border: 1px solid var(--line, #dfe4ea); padding: 7px 10px; min-width: 70px; }
.markdown-body :deep(th) { background: #f5f9fb; font-weight: 600; }
.markdown-body :deep(hr) { margin: 18px 0; border: 0; border-top: 1px solid var(--line, #dfe4ea); }
.markdown-body :deep(.markdown-image-placeholder) { color: var(--muted, #6b7785); }
</style>
