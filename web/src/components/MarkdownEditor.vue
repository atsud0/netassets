<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElInput } from 'element-plus'
import MarkdownPreview from './MarkdownPreview.vue'

const props = withDefaults(defineProps<{
  modelValue: string
  label?: string
  placeholder?: string
  disabled?: boolean
  rows?: number
}>(), { label: 'Markdown 内容', placeholder: '支持 Markdown 格式', disabled: false, rows: 12 })
const emit = defineEmits<{ 'update:modelValue': [value: string]; blur: [event: FocusEvent] }>()
const mode = ref<'edit' | 'preview' | 'split'>('edit')
const text = computed({ get: () => props.modelValue, set: (value: string) => emit('update:modelValue', value) })
</script>

<template>
  <div class="markdown-editor">
    <div class="markdown-editor-toolbar">
      <span class="markdown-format-label">Markdown</span>
      <div class="markdown-mode-switch" role="group" :aria-label="`${label}显示模式`">
        <button type="button" :aria-pressed="mode === 'edit'" @click="mode = 'edit'">编辑</button>
        <button type="button" :aria-pressed="mode === 'preview'" @click="mode = 'preview'">预览</button>
        <button type="button" :aria-pressed="mode === 'split'" @click="mode = 'split'">分栏</button>
      </div>
    </div>
    <div class="markdown-editor-panes" :class="{ 'is-split': mode === 'split' }">
      <div v-show="mode !== 'preview'" class="markdown-source"><ElInput v-model="text" type="textarea" :aria-label="label" :placeholder="placeholder" :rows="rows" :disabled="disabled" :spellcheck="false" @blur="emit('blur', $event)" /></div>
      <MarkdownPreview v-if="mode !== 'edit'" class="markdown-editor-preview" :content="modelValue" :label="`${label}预览`" />
    </div>
    <div class="markdown-editor-hint">支持标题、列表、引用、链接、代码块和表格</div>
  </div>
</template>

<style scoped>
.markdown-editor { width: 100%; min-width: 0; border: 1px solid var(--line, #dfe4ea); border-radius: 10px; overflow: hidden; }
.markdown-editor-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 10px; flex-wrap: wrap; padding: 8px 12px; background: #f5f9fb; border-bottom: 1px solid var(--line, #dfe4ea); }
.markdown-format-label { color: var(--muted, #6b7785); font-size: 12px; }
.markdown-mode-switch { display: flex; gap: 4px; }
.markdown-mode-switch button { border: 1px solid transparent; border-radius: 5px; background: transparent; color: var(--ink, #17202a); padding: 4px 12px; cursor: pointer; line-height: 22px; }
.markdown-mode-switch button[aria-pressed="true"] { background: white; border-color: var(--cyan, #2a9d8f); color: #137d72; }
.markdown-mode-switch button:focus-visible { outline: 2px solid var(--cyan, #2a9d8f); outline-offset: 1px; }
.markdown-editor-panes { min-width: 0; }
.markdown-editor-panes.is-split { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); }
.markdown-source { min-width: 0; }
.markdown-source :deep(textarea) { min-height: 240px; border-radius: 0; box-shadow: none; font-family: ui-monospace, SFMono-Regular, Consolas, monospace; line-height: 1.65; padding: 14px; resize: vertical; }
.markdown-source :deep(textarea:focus) { box-shadow: inset 0 0 0 1px var(--cyan, #2a9d8f); }
.markdown-editor-preview { min-height: 240px; padding: 14px; }
.is-split .markdown-editor-preview { border-left: 1px solid var(--line, #dfe4ea); }
.markdown-editor-hint { padding: 6px 12px; font-size: 12px; color: var(--muted, #6b7785); background: #f5f9fb; border-top: 1px solid var(--line, #dfe4ea); line-height: 20px; }
@media (max-width: 767px) {
  .markdown-editor-panes.is-split { grid-template-columns: minmax(0, 1fr); }
  .is-split .markdown-editor-preview { border-left: 0; border-top: 1px solid var(--line, #dfe4ea); }
  .markdown-mode-switch button { padding: 4px 10px; }
}
</style>
