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

interface SubmissionHistoryItem {
  submission_id: string
  task_id: string
  task_name: string
  organization_id: string
  version: number
  status: 'submitted' | 'approved' | 'returned'
  operation: 'create' | 'update' | 'delete' | 'first_report' | 'confirmed'
  submitted_at: string
  submitted_by_name?: string
  reviewed_at?: string
  reviewed_by_name?: string
  return_reason?: string
  note?: string
  resource_key: string
  resource_name: string
  changes: FieldChange[]
}

interface HistoryResponse {
  items: SubmissionHistoryItem[]
  total: number
  page: number
  page_size: number
}

const props = defineProps<{ resourceType: 'network_asset' | 'business_system' }>()
const visible = ref(false)
const loading = ref(false)
const resourceID = ref('')
const items = ref<SubmissionHistoryItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)

const operationLabels: Record<string, string> = {
  create: '首次新增上报', update: '变更上报', delete: '下线申请', first_report: '首次随任务确认', confirmed: '随任务确认',
}
const operationTagTypes: Record<string, 'success' | 'warning' | 'danger' | 'info'> = {
  create: 'success', update: 'warning', delete: 'danger', first_report: 'success', confirmed: 'info',
}
const statusLabels: Record<string, string> = { submitted: '待审核', approved: '已发布', returned: '已退回' }
const statusTagTypes: Record<string, 'warning' | 'success' | 'danger'> = { submitted: 'warning', approved: 'success', returned: 'danger' }
const valueLabels: Record<string, string> = {
  yes: '是', no: '否', unknown: '未知', partial: '部分', pending: '待确认',
  normal: '一般', important: '重要', core: '核心', planning: '规划中', running: '运行中', suspended: '暂停', offline: '已下线', archived: '已归档',
  active: '启用', disabled: '停用', production: '生产', staging: '预发布', testing: '测试', development: '开发', office: '办公', other: '其他',
  public: '公网', private: '内网', domain: '域名', ip: 'IP 地址', url: 'URL', service: '服务',
}

const title = computed(() => items.value[0]?.resource_name ? `${items.value[0].resource_name} · 上报历史` : '资源上报历史')

function formatDateTime(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}

function formatValue(value: unknown) {
  if (value === null || value === undefined || value === '') return '—'
  if (Array.isArray(value)) return value.length ? value.map((item) => valueLabels[String(item)] || String(item)).join('、') : '—'
  if (typeof value === 'boolean') return value ? '是' : '否'
  if (typeof value === 'number') return new Intl.NumberFormat('zh-CN').format(value)
  if (typeof value === 'object') return JSON.stringify(value)
  return valueLabels[String(value)] || String(value)
}

async function load() {
  if (!resourceID.value) return
  loading.value = true
  try {
    const response = await apiClient.get<HistoryResponse>(`/inventory-tasks/resource-submissions/${props.resourceType}/${resourceID.value}`, {
      params: { page: page.value, page_size: pageSize.value },
    })
    items.value = response.data.items
    total.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取资源上报历史失败'))
  } finally {
    loading.value = false
  }
}

async function open(id: string) {
  resourceID.value = id
  page.value = 1
  items.value = []
  total.value = 0
  visible.value = true
  await load()
}

function handlePageChange(value: number) {
  page.value = value
  void load()
}

function handlePageSizeChange(value: number) {
  pageSize.value = value
  page.value = 1
  void load()
}

defineExpose({ open })
</script>

<template>
  <el-drawer v-model="visible" :title="title" size="800px" destroy-on-close>
    <div v-loading="loading" class="resource-submission-history">
      <el-alert title="这里展示该资源历次不可变提交快照；“已发布”表示该版本已经通过审核并进入集团正式台账。" type="info" :closable="false" show-icon />
      <div v-if="items.length" class="resource-submission-timeline">
        <article v-for="item in items" :key="item.submission_id" class="resource-submission-card">
          <header>
            <div>
              <strong>{{ item.task_name }}</strong>
              <span>第 {{ item.version }} 次提交 · {{ formatDateTime(item.submitted_at) }}</span>
            </div>
            <div class="resource-submission-tags">
              <el-tag :type="operationTagTypes[item.operation]" effect="plain">{{ operationLabels[item.operation] || item.operation }}</el-tag>
              <el-tag :type="statusTagTypes[item.status]" effect="plain">{{ statusLabels[item.status] || item.status }}</el-tag>
            </div>
          </header>
          <div class="resource-submission-meta">
            <span>提交人：{{ item.submitted_by_name || '未知账号' }}</span>
            <span v-if="item.reviewed_at">审核时间：{{ formatDateTime(item.reviewed_at) }}</span>
            <span v-if="item.reviewed_by_name">审核人：{{ item.reviewed_by_name }}</span>
          </div>
          <el-alert v-if="item.return_reason" :title="`退回原因：${item.return_reason}`" type="error" :closable="false" show-icon />
          <p v-if="item.note" class="resource-submission-note">提交说明：{{ item.note }}</p>
          <div v-if="item.changes.length" class="working-version-change-list">
            <div v-for="change in item.changes" :key="change.field" class="submission-field-change">
              <strong>{{ change.label }}</strong>
              <span class="before">{{ formatValue(change.before) }}</span>
              <span class="change-arrow">→</span>
              <span class="after">{{ formatValue(change.after) }}</span>
            </div>
          </div>
          <el-empty v-else :image-size="56" description="本次确认未产生字段变化" />
        </article>
      </div>
      <el-empty v-else-if="!loading" description="该资源尚无上报记录" />
      <div v-if="total" class="table-pagination">
        <el-pagination background layout="total, sizes, prev, pager, next" :total="total" :current-page="page" :page-size="pageSize" :page-sizes="[20, 50, 100]" @current-change="handlePageChange" @size-change="handlePageSizeChange" />
      </div>
    </div>
  </el-drawer>
</template>
