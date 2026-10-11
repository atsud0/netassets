<script setup lang="ts">
import { nextTick, ref, isVNode, type VNode } from 'vue'
import { ArrowDown } from '@element-plus/icons-vue'

withDefaults(defineProps<{ label?: string; direct?: boolean }>(), { label: '本行操作', direct: false })
// Inspect rendered conditional slots only to choose the compact presentation.
// Actions themselves remain the original buttons, handlers and permission gates.
function actionCount(nodes: VNode[] = []): number {
  return nodes.reduce((total, node) => {
    if (!isVNode(node)) return total
    const type = node.type as { name?: string }
    if (type?.name === 'ElButton' || node.type === 'a') return total + 1
    return total + (Array.isArray(node.children) ? actionCount(node.children as VNode[]) : 0)
  }, 0)
}
const visible = ref(false)
const trigger = ref<{ $el: HTMLButtonElement }>()
const panel = ref<HTMLElement>()
async function focusAction() {
  await nextTick()
  panel.value?.querySelector<HTMLButtonElement>('button:not(:disabled), a[href]')?.focus()
}
function close(restore = true) {
  visible.value = false
  if (restore) nextTick(() => trigger.value?.$el.focus())
}
function onAction(event: MouseEvent) {
  const target = (event.target as HTMLElement).closest('button, a[href]')
  if (target && !target.hasAttribute('disabled')) close(false)
}
</script>

<template>
  <slot v-if="direct || actionCount($slots.default?.()) <= 1" />
  <el-popover v-else v-model:visible="visible" trigger="click" placement="bottom-end" :width="180" popper-class="row-actions-popover" @after-enter="focusAction">
    <template #reference><el-button ref="trigger" link type="primary" :aria-label="label" :aria-expanded="visible" aria-haspopup="dialog" @keydown.esc.stop="close()">操作<el-icon class="el-icon--right"><ArrowDown /></el-icon></el-button></template>
    <div ref="panel" class="row-actions-panel" role="dialog" :aria-label="label" @keydown.esc.stop="close()" @click="onAction"><slot /></div>
  </el-popover>
</template>
