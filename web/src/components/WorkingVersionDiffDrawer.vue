<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'

interface FieldChange {
  field: string
  label: string
  before: unknown
  after: unknown
}

interface WorkingVersionDiff {
  resource_type: 'network_asset' | 'business_system'
  draft_id: string
  source_id?: string
  organization_id: string
  task_id?: string
  operation: 'create' | 'update' | 'delete'
  draft_status: 'editing' | 'submitted' | 'returned'
  resource_key: string
  resource_name: string
  changes: FieldChange[]
}

interface DiffResponse { working_version_diff: WorkingVersionDiff }

const props = defineProps<{ resourceType: 'network_asset' | 'business_system' }>()
const visible = ref(false)
const loading = ref(false)
const diff = ref<WorkingVersionDiff | null>(null)

const operationLabels: Record<string, string> = { create: '首次上报', update: '变更上报', delete: '下线申请' }
const operationTagTypes: Record<string, 'success' | 'warning' | 'danger'> = { create: 'success', update: 'warning', delete: 'danger' }
const statusLabels: Record<string, string> = { editing: '编辑中', submitted: '待审核', returned: '已退回' }
const valueLabels: Record<string, string> = {
  yes: '是', no: '否', unknown: '未知', partial: '部分', pending: '待确认',
  normal: '一般', important: '重要', core: '核心',
  planning: '规划中', running: '运行中', suspended: '暂停', offline: '已下线', archived: '已归档',
  active: '启用', disabled: '停用', production: '生产', staging: '预发布', testing: '测试', development: '开发', office: '办公', other: '其他',
  public: '公网', private: '内网', domain: '域名', ip: 'IP 地址', url: 'URL', service: '服务',
}

const title = computed(() => diff.value ? `${diff.value.resource_name} · 工作版本变更` : '工作版本变更')
const contextText = computed(() => {
  if (!diff.value) return ''
  if (diff.value.operation === 'create') return '该资源尚无集团正式版本，通过审核后将首次进入集团台账。'
  if (diff.value.operation === 'delete') return '该工作版本申请将资源从集团正式台账下线；原正式数据保留在下方用于确认。'
  if (!diff.value.changes.length) return '工作版本与当前集团正式版本没有字段差异。'
  return `工作版本相对集团正式版本共有 ${diff.value.changes.length} 个字段发生变化。`
})

function formatValue(value: unknown) {
  if (value === null || value === undefined || value === '') return '—'
  if (Array.isArray(value)) return value.length ? value.map((item) => valueLabels[String(item)] || String(item)).join('、') : '—'
  if (typeof value === 'boolean') return value ? '是' : '否'
  if (typeof value === 'number') return new Intl.NumberFormat('zh-CN').format(value)
  if (typeof value === 'object') return JSON.stringify(value)
  return valueLabels[String(value)] || String(value)
}

async function open(draftID: string) {
  visible.value = true
  loading.value = true
  diff.value = null
  try {
    const response = await apiClient.get<DiffResponse>(`/inventory-tasks/working-versions/${props.resourceType}/${draftID}/diff`)
    diff.value = response.data.working_version_diff
  } catch (error) {
    visible.value = false
    ElMessage.error(getAPIErrorMessage(error, '读取工作版本变更失败'))
  } finally {
    loading.value = false
  }
}

defineExpose({ open })
</script>

<template>
  <el-drawer v-model="visible" :title="title" size="760px" destroy-on-close>
    <div v-loading="loading" class="working-version-diff">
      <template v-if="diff">
        <div class="working-version-diff-heading">
          <div>
            <span>资源标识</span>
            <strong>{{ diff.resource_key }}</strong>
          </div>
          <div class="working-version-diff-tags">
            <el-tag :type="operationTagTypes[diff.operation]" effect="plain">{{ operationLabels[diff.operation] }}</el-tag>
            <el-tag type="info" effect="plain">{{ statusLabels[diff.draft_status] || diff.draft_status }}</el-tag>
          </div>
        </div>
        <el-alert
          :title="contextText"
          :type="diff.operation === 'delete' ? 'warning' : 'info'"
          :closable="false"
          show-icon
          class="working-version-diff-alert"
        />
        <div v-if="diff.changes.length" class="working-version-change-list">
          <div v-for="change in diff.changes" :key="change.field" class="submission-field-change">
            <strong>{{ change.label }}</strong>
            <span class="before">{{ formatValue(change.before) }}</span>
            <span class="change-arrow">→</span>
            <span class="after">{{ formatValue(change.after) }}</span>
          </div>
        </div>
        <el-empty v-else :description="diff.operation === 'delete' ? '本次仅申请下线，没有修改资源字段' : '工作版本与集团正式版本一致'" />
      </template>
    </div>
  </el-drawer>
</template>
