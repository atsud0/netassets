<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'

interface AuditOption { value: string; label: string }
interface AuditOptions { actions: AuditOption[]; resource_types: AuditOption[] }
interface AuditLog {
  id: number
  user_id?: string
  username?: string
  display_name?: string
  organization_id?: string
  organization_code?: string
  organization_name?: string
  action: string
  action_label: string
  resource_type?: string
  resource_type_label?: string
  resource_id?: string
  summary?: string
  details: Record<string, unknown>
  request_ip?: string
  request_id?: string
  result: 'success' | 'failure'
  result_label: string
  created_at: string
}
interface AuditListResponse { items: AuditLog[]; total: number; page: number; page_size: number }
interface Organization { id: string; code: string; name: string; type: string; status: string }
interface User { id: string; username: string; display_name: string; organization_name?: string; status: string }

const loading = ref(false)
const items = ref<AuditLog[]>([])
const total = ref(0)
const options = reactive<AuditOptions>({ actions: [], resource_types: [] })
const organizations = ref<Organization[]>([])
const users = ref<User[]>([])
const detailVisible = ref(false)
const selected = ref<AuditLog>()
const filters = reactive({
  page: 1,
  page_size: 20,
  action: '',
  resource_type: '',
  result: '',
  user_id: '',
  organization_id: '',
  q: '',
  date_range: null as string[] | null,
})

const displayedRange = computed(() => {
  if (total.value === 0) return '当前没有符合条件的日志'
  const start = (filters.page - 1) * filters.page_size + 1
  const end = Math.min(filters.page * filters.page_size, total.value)
  return `显示 ${start}-${end} 条，共 ${total.value} 条`
})

function formatTime(value?: string) {
  if (!value) return '—'
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  }).format(new Date(value))
}

function actorName(item: AuditLog) {
  if (!item.display_name && !item.username) return '未知用户'
  return item.display_name || item.username || '未知用户'
}

function detailJSON(value: Record<string, unknown>) {
  if (!value || Object.keys(value).length === 0) return '无扩展详情'
  return JSON.stringify(value, null, 2)
}

async function loadReferenceData() {
  try {
    const [optionResponse, organizationResponse, userResponse] = await Promise.all([
      apiClient.get<{ options: AuditOptions }>('/audit-logs/options'),
      apiClient.get<{ items: Organization[] }>('/organizations', { params: { page: 1, page_size: 100 } }),
      apiClient.get<{ items: User[] }>('/users', { params: { page_size: 100 } }),
    ])
    options.actions = optionResponse.data.options.actions ?? []
    options.resource_types = optionResponse.data.options.resource_types ?? []
    organizations.value = organizationResponse.data.items
    users.value = userResponse.data.items
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取审计筛选项失败'))
  }
}

async function loadLogs() {
  loading.value = true
  try {
    const params: Record<string, string | number> = { page: filters.page, page_size: filters.page_size }
    if (filters.action) params.action = filters.action
    if (filters.resource_type) params.resource_type = filters.resource_type
    if (filters.result) params.result = filters.result
    if (filters.user_id) params.user_id = filters.user_id
    if (filters.organization_id) params.organization_id = filters.organization_id
    if (filters.q.trim()) params.q = filters.q.trim()
    const dateRange = filters.date_range
    if (dateRange?.length === 2) {
      params.start_at = dateRange[0]
      params.end_at = dateRange[1]
    }
    const response = await apiClient.get<AuditListResponse>('/audit-logs', { params })
    items.value = response.data.items
    total.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取审计日志失败'))
  } finally {
    loading.value = false
  }
}

function search() {
  filters.page = 1
  void loadLogs()
}

function reset() {
  Object.assign(filters, {
    page: 1, page_size: 20, action: '', resource_type: '', result: '', user_id: '', organization_id: '', q: '', date_range: null,
  })
  void loadLogs()
}

function changePage(page: number) {
  filters.page = page
  void loadLogs()
}

function changePageSize(size: number) {
  filters.page = 1
  filters.page_size = size
  void loadLogs()
}

function showDetail(item: AuditLog) {
  selected.value = item
  detailVisible.value = true
}

onMounted(async () => {
  await Promise.all([loadReferenceData(), loadLogs()])
})
</script>

<template>
  <section>
    <div class="page-heading">
      <div>
        <p class="eyebrow">AUDIT & TRACEABILITY</p>
        <h1>审计日志</h1>
        <p>集团统一查询登录、组织、用户、台账、导入导出和填报审核操作，便于追踪责任人与操作结果。</p>
      </div>
    </div>

    <div class="panel audit-filter-panel">
      <el-input v-model="filters.q" clearable placeholder="搜索摘要、资源 ID、请求 ID、人员或组织" @keyup.enter="search" />
      <el-select v-model="filters.action" clearable filterable placeholder="全部操作">
        <el-option v-for="item in options.actions" :key="item.value" :label="item.label" :value="item.value" />
      </el-select>
      <el-select v-model="filters.resource_type" clearable filterable placeholder="全部资源">
        <el-option v-for="item in options.resource_types" :key="item.value" :label="item.label" :value="item.value" />
      </el-select>
      <el-select v-model="filters.result" clearable placeholder="全部结果">
        <el-option label="成功" value="success" />
        <el-option label="失败" value="failure" />
      </el-select>
      <el-select v-model="filters.user_id" clearable filterable placeholder="全部操作人">
        <el-option v-for="item in users" :key="item.id" :label="`${item.display_name}（${item.username}）`" :value="item.id" />
      </el-select>
      <el-select v-model="filters.organization_id" clearable filterable placeholder="全部组织">
        <el-option v-for="item in organizations" :key="item.id" :label="`${item.name}（${item.code}）`" :value="item.id" />
      </el-select>
      <el-date-picker
        v-model="filters.date_range"
        type="daterange"
        value-format="YYYY-MM-DD"
        range-separator="至"
        start-placeholder="开始日期"
        end-placeholder="结束日期"
        style="width: 100%"
      />
      <div class="audit-filter-actions">
        <el-button @click="reset">重置</el-button>
        <el-button type="primary" @click="search">查询</el-button>
      </div>
    </div>

    <div class="panel data-table-panel audit-table-panel" v-loading="loading">
      <div class="table-panel-heading">
        <div><strong>操作记录</strong><span>{{ displayedRange }}</span></div>
      </div>
      <!-- 审计记录可切换至每页 100 条；限制表格高度并由 Element Plus 提供独立纵向滚动。 -->
      <el-table :data="items" row-key="id" empty-text="暂无审计日志" :max-height="480">
        <el-table-column label="操作时间" min-width="172"><template #default="scope">{{ formatTime(scope.row.created_at) }}</template></el-table-column>
        <el-table-column label="操作" min-width="150"><template #default="scope"><div class="audit-action-cell"><strong>{{ scope.row.action_label }}</strong><span>{{ scope.row.action }}</span></div></template></el-table-column>
        <el-table-column label="操作人" min-width="160"><template #default="scope"><div class="audit-action-cell"><strong>{{ actorName(scope.row) }}</strong><span>{{ scope.row.username || '账号已删除' }}</span></div></template></el-table-column>
        <el-table-column label="所属组织" min-width="180"><template #default="scope"><div class="audit-action-cell"><strong>{{ scope.row.organization_name || '—' }}</strong><span>{{ scope.row.organization_code || '' }}</span></div></template></el-table-column>
        <el-table-column label="资源" min-width="180"><template #default="scope"><div class="audit-action-cell"><strong>{{ scope.row.resource_type_label || '—' }}</strong><span class="audit-resource-id">{{ scope.row.resource_id || '—' }}</span></div></template></el-table-column>
        <el-table-column prop="summary" label="摘要" min-width="190"><template #default="scope">{{ scope.row.summary || '—' }}</template></el-table-column>
        <el-table-column label="结果" width="86"><template #default="scope"><el-tag :type="scope.row.result === 'success' ? 'success' : 'danger'" effect="plain">{{ scope.row.result_label }}</el-tag></template></el-table-column>
        <el-table-column label="详情" width="82" fixed="right"><template #default="scope"><el-button link type="primary" @click="showDetail(scope.row)">查看</el-button></template></el-table-column>
      </el-table>
      <div class="audit-pagination">
        <el-pagination
          background
          layout="total, sizes, prev, pager, next"
          :total="total"
          :current-page="filters.page"
          :page-size="filters.page_size"
          :page-sizes="[20, 50, 100]"
          @current-change="changePage"
          @size-change="changePageSize"
        />
      </div>
    </div>

    <el-drawer v-model="detailVisible" title="审计日志详情" size="560px" destroy-on-close>
      <div v-if="selected" class="detail-drawer audit-detail">
        <div class="detail-hero">
          <div><p class="eyebrow">{{ selected.action }}</p><h2>{{ selected.action_label }}</h2><p>{{ selected.summary || '无操作摘要' }}</p></div>
          <el-tag :type="selected.result === 'success' ? 'success' : 'danger'">{{ selected.result_label }}</el-tag>
        </div>
        <div class="detail-grid">
          <div><span>操作时间</span><strong>{{ formatTime(selected.created_at) }}</strong></div>
          <div><span>操作人</span><strong>{{ actorName(selected) }}{{ selected.username ? `（${selected.username}）` : '' }}</strong></div>
          <div><span>所属组织</span><strong>{{ selected.organization_name || '—' }}</strong></div>
          <div><span>资源类型</span><strong>{{ selected.resource_type_label || '—' }}</strong></div>
          <div><span>资源 ID</span><strong>{{ selected.resource_id || '—' }}</strong></div>
          <div><span>来源 IP</span><strong>{{ selected.request_ip || '—' }}</strong></div>
          <div class="audit-request-id"><span>请求 ID</span><strong>{{ selected.request_id || '—' }}</strong></div>
        </div>
        <div class="audit-details-block"><span>扩展详情</span><pre>{{ detailJSON(selected.details) }}</pre></div>
      </div>
    </el-drawer>
  </section>
</template>
