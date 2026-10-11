<script setup lang="ts">
import RowActions from '../components/RowActions.vue'
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox, type TableInstance } from 'element-plus'
import { useRouter } from 'vue-router'
import { apiClient, getAPIErrorMessage } from '../api/client'
import { useAuthStore } from '../stores/auth'
import SavedFilterViews from '../components/SavedFilterViews.vue'

interface Organization { id: string; code: string; name: string; status: string }
interface UserItem { id: string; username: string; display_name: string; status?: string }
interface QualityIssue {
  id: string; rule_id: string; rule_name: string; target_field: string; rule_type: 'required' | 'stale' | 'expiry'; stale_after_days?: number; expiry_warning_days?: number; severity: 'error' | 'warning'
  resource_type: 'asset' | 'business_system'; resource_id: string; resource_name: string
  organization_id: string; organization_code: string; organization_name: string
  status: 'pending' | 'in_progress' | 'pending_review' | 'closed' | 'exempted'
  assignee_user_id?: string; assignee_display_name?: string; rectification_note?: string
  submitted_by_name?: string; submitted_at?: string; review_note?: string; reviewed_by_name?: string; reviewed_at?: string
  exemption_reason?: string; exemption_expires_at?: string; resolution_type?: string
  first_detected_at: string; last_detected_at: string; occurrence_count: number; closed_at?: string; updated_at: string
}
interface IssueSummary { total: number; pending: number; in_progress: number; pending_review: number; closed: number; exempted: number; error: number; warning: number }
interface IssueEvent { id: number; event_type: string; from_status?: string; to_status: string; note?: string; actor_display_name?: string; created_at: string }
interface ListResponse<T> { items: T[]; total: number; page: number; page_size: number }
interface BatchOperationItem { id: string; success: boolean; code?: string; message: string; item?: QualityIssue; resource_name?: string }
interface BatchOperationResult { total_count: number; success_count: number; failure_count: number; items: BatchOperationItem[] }
const auth = useAuthStore()
const router = useRouter()
const loading = ref(false)
const acting = ref(false)
const items = ref<QualityIssue[]>([])
const issueTableRef = ref<TableInstance>()
const issuePage = ref(1)
const issuePageSize = ref(20)
const issueTotal = ref(0)
const summary = reactive<IssueSummary>({ total: 0, pending: 0, in_progress: 0, pending_review: 0, closed: 0, exempted: 0, error: 0, warning: 0 })
const organizations = ref<Organization[]>([])
const filterUsers = ref<UserItem[]>([])
const issueAssignees = ref<UserItem[]>([])
const assigneeLoading = ref(false)
const detailVisible = ref(false)
const detailLoading = ref(false)
const selected = ref<QualityIssue | null>(null)
const events = ref<IssueEvent[]>([])
const eventLoading = ref(false)
const eventPage = ref(1)
const eventPageSize = ref(20)
const eventTotal = ref(0)
const actionVisible = ref(false)
const actionType = ref<'assign' | 'start' | 'submit' | 'approve' | 'return' | 'exempt' | 'reopen'>('start')
const actionIssue = ref<QualityIssue | null>(null)
const actionForm = reactive({ assignee_user_id: '', note: '', expires_at: null as Date | null })
const selectedIssues = ref<QualityIssue[]>([])
const bulkLoading = ref(false)
const bulkActionVisible = ref(false)
const bulkActionType = ref<'assign' | 'start' | 'submit'>('assign')
const bulkForm = reactive({ assignee_user_id: '', note: '' })
const bulkAssignees = ref<UserItem[]>([])
const bulkAssigneeLoading = ref(false)
const bulkResultVisible = ref(false)
const bulkResult = ref<BatchOperationResult>()
const bulkResultTitle = ref('批量整改结果')
const filters = reactive({ organization_id: '', status: '', severity: '', resource_type: '', assignee: '' })
const savedFilterViews = ref<InstanceType<typeof SavedFilterViews>>()
const currentIssueFilters = computed<Record<string, string>>(() => Object.fromEntries(
  Object.entries(filters).filter(([, value]) => Boolean(value)),
))

const isGroupAdmin = computed(() => auth.isGroupAdmin)
const summaryCards = computed(() => [
  ['问题总数', summary.total, 'total'], ['待处理', summary.pending, 'danger'], ['整改中', summary.in_progress, 'warning'],
  ['待集团复核', summary.pending_review, 'primary'], ['已关闭', summary.closed, 'success'], ['已豁免', summary.exempted, 'info'],
])
const availableFilterUsers = computed(() => filterUsers.value.filter((item) => item.status === 'enabled'))
const actionTitle = computed(() => ({ assign: '调整负责人', start: '开始整改', submit: '提交集团复核', approve: '复核通过', return: '退回整改', exempt: '问题豁免', reopen: '重新打开问题' }[actionType.value]))
const bulkActionTitle = computed(() => ({ assign: '批量指派负责人', start: '批量开始整改', submit: '批量提交集团复核' }[bulkActionType.value]))
const actionButtonType = computed(() => actionType.value === 'return' || actionType.value === 'exempt' ? 'warning' : 'primary')

const statusLabels: Record<string, string> = { pending: '待处理', in_progress: '整改中', pending_review: '待复核', closed: '已关闭', exempted: '已豁免' }
const statusTypes: Record<string, string> = { pending: 'danger', in_progress: 'warning', pending_review: 'primary', closed: 'success', exempted: 'info' }
const eventLabels: Record<string, string> = { detected: '巡检发现', assigned: '调整负责人', started: '开始整改', submitted: '提交复核', approved: '复核通过', returned: '退回整改', exempted: '问题豁免', reopened: '重新打开', auto_resolved: '巡检自动关闭' }
const targetLabels: Record<string, string> = {
  value: '资产值', name: '名称', network_scope: '网络范围', network_area_id: '网络区域', owner_name: '负责人', business_system_id: '关联业务系统', importance: '重要性',
  code: '系统编码', owner_contact: '负责人联系方式', system_level: '系统等级', internet_exposure: '互联网暴露情况', contains_personal_information: '是否含个人信息',
  updated_at: '最后更新时间', expires_at: '有效期至', contains_sensitive_personal_information: '是否含敏感个人信息', data_volume_over_100k: '数据量是否超过10万条', contains_important_data: '是否含重要数据', contains_core_data: '是否含核心数据',
}

function formatTime(value?: string) {
  if (!value) return '—'
  return new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value))
}
function targetLabel(value: string, issue?: QualityIssue) {
  if (value === 'updated_at' && issue?.stale_after_days) return `最后更新时间（超过 ${issue.stale_after_days} 天未维护）`
  if (value === 'expires_at' && issue) return `有效期至（提前 ${issue.expiry_warning_days ?? 0} 天提醒）`
  return value.startsWith('custom:') ? `自定义字段：${value.slice(7)}` : (targetLabels[value] ?? value)
}
function resourceTypeLabel(value?: string) { return value === 'asset' ? '网络资产' : value === 'business_system' ? '业务系统' : '全部对象' }
function severityLabel(value?: string) { return value === 'error' ? '错误' : value === 'warning' ? '警告' : '全部级别' }
function resourcePath(item: QualityIssue) { return item.resource_type === 'asset' ? '/assets' : '/systems' }

async function loadReferenceData() {
  const requests: Promise<unknown>[] = [
    apiClient.get<ListResponse<Organization>>('/organizations', { params: { page: 1, page_size: 100 } }).then((response) => { organizations.value = response.data.items }),
  ]
  if (auth.hasPermission('user.manage')) {
    requests.push(apiClient.get<ListResponse<UserItem>>('/users', { params: { page: 1, page_size: 100 } }).then((response) => { filterUsers.value = response.data.items }))
  }
  const results = await Promise.allSettled(requests)
  if (results.every((item) => item.status === 'rejected')) ElMessage.warning('部分筛选数据读取失败，但不影响查看质量问题')
}

async function loadIssues() {
  loading.value = true
  try {
    const params: Record<string, string | number> = { page: issuePage.value, page_size: issuePageSize.value }
    if (filters.organization_id) params.organization_id = filters.organization_id
    if (filters.status) params.status = filters.status
    if (filters.severity) params.severity = filters.severity
    if (filters.resource_type) params.resource_type = filters.resource_type
    if (filters.assignee) params.assignee = filters.assignee
    const response = await apiClient.get<ListResponse<QualityIssue> & { summary: IssueSummary }>('/data-quality-issues', { params })
    items.value = response.data.items
    issueTotal.value = response.data.total
    Object.assign(summary, response.data.summary)
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取数据质量问题失败'))
  } finally {
    loading.value = false
  }
}

async function loadIssueEvents(issueID = selected.value?.id) {
  if (!issueID) return
  eventLoading.value = true
  try {
    const response = await apiClient.get<ListResponse<IssueEvent>>(`/data-quality-issues/${issueID}/events`, {
      params: { page: eventPage.value, page_size: eventPageSize.value },
    })
    if (selected.value?.id !== issueID) return
    events.value = response.data.items
    eventTotal.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取质量问题处理记录失败'))
  } finally {
    eventLoading.value = false
  }
}

async function openDetail(item: QualityIssue) {
  selected.value = item
  detailVisible.value = true
  detailLoading.value = true
  events.value = []
  eventPage.value = 1
  eventTotal.value = 0
  try {
    const response = await apiClient.get<{ issue: QualityIssue }>(`/data-quality-issues/${item.id}`)
    if (selected.value?.id !== item.id) return
    selected.value = response.data.issue
    await loadIssueEvents(item.id)
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取质量问题详情失败'))
  } finally {
    detailLoading.value = false
  }
}

function handleEventPageChange(value: number) {
  eventPage.value = value
  void loadIssueEvents()
}

function handleEventPageSizeChange(value: number) {
  eventPageSize.value = value
  eventPage.value = 1
  void loadIssueEvents()
}

async function openAction(type: typeof actionType.value, item: QualityIssue) {
  actionType.value = type
  actionIssue.value = item
  actionForm.assignee_user_id = ''
  actionForm.note = ''
  actionForm.expires_at = null
  issueAssignees.value = []
  actionVisible.value = true
  if (!['assign', 'start'].includes(type)) return

  assigneeLoading.value = true
  try {
    const response = await apiClient.get<ListResponse<UserItem>>(`/data-quality-issues/${item.id}/assignees`, { params: { page: 1, page_size: 100 } })
    issueAssignees.value = response.data.items
    const preferred = item.assignee_user_id || auth.user?.id || ''
    if (preferred && issueAssignees.value.some((user) => user.id === preferred)) {
      actionForm.assignee_user_id = preferred
    } else if (issueAssignees.value.length === 1) {
      actionForm.assignee_user_id = issueAssignees.value[0].id
    }
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取责任组织可选整改负责人失败'))
  } finally {
    assigneeLoading.value = false
  }
}

async function submitAction() {
  const item = actionIssue.value
  if (!item) return
  if (['submit', 'return', 'exempt'].includes(actionType.value) && !actionForm.note.trim()) {
    ElMessage.warning(actionType.value === 'exempt' ? '请填写豁免原因' : '请填写处理说明')
    return
  }
  if (['assign', 'start'].includes(actionType.value) && !actionForm.assignee_user_id) {
    ElMessage.warning('请选择整改负责人')
    return
  }
  acting.value = true
  try {
    const endpoint = `/data-quality-issues/${item.id}`
    if (actionType.value === 'assign') await apiClient.post(`${endpoint}/assign`, { assignee_user_id: actionForm.assignee_user_id, note: actionForm.note })
    if (actionType.value === 'start') await apiClient.post(`${endpoint}/start`, { assignee_user_id: actionForm.assignee_user_id, note: actionForm.note })
    if (actionType.value === 'submit') await apiClient.post(`${endpoint}/submit`, { note: actionForm.note })
    if (actionType.value === 'approve') await apiClient.post(`${endpoint}/review`, { approved: true, note: actionForm.note })
    if (actionType.value === 'return') await apiClient.post(`${endpoint}/review`, { approved: false, note: actionForm.note })
    if (actionType.value === 'exempt') await apiClient.post(`${endpoint}/exempt`, { reason: actionForm.note, expires_at: actionForm.expires_at?.toISOString() ?? '' })
    if (actionType.value === 'reopen') await apiClient.post(`${endpoint}/reopen`, { note: actionForm.note })
    ElMessage.success(`${actionTitle.value}成功`)
    actionVisible.value = false
    await refreshIssuesAfterMutation()
    if (detailVisible.value) await openDetail(item)
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, `${actionTitle.value}失败`))
  } finally {
    acting.value = false
  }
}

async function refreshIssuesAfterMutation() {
  issueTableRef.value?.clearSelection()
  selectedIssues.value = []
  await loadIssues()
  if (items.value.length === 0 && issueTotal.value > 0 && issuePage.value > 1) {
    issuePage.value -= 1
    await loadIssues()
  }
}

function handleSelectionChange(rows: QualityIssue[]) {
  selectedIssues.value = rows
}

async function openBulkAction(type: typeof bulkActionType.value) {
  if (!selectedIssues.value.length) {
    ElMessage.warning('请先选择要处理的数据质量问题')
    return
  }
  if (selectedIssues.value.length > 200) {
    ElMessage.warning('单次最多处理 200 个数据质量问题')
    return
  }
  bulkActionType.value = type
  bulkForm.assignee_user_id = ''
  bulkForm.note = ''
  bulkAssignees.value = []
  if (type === 'assign' || type === 'start') {
    const organizationIDs = new Set(selectedIssues.value.map((item) => item.organization_id))
    if (organizationIDs.size !== 1) {
      ElMessage.warning('批量指派或开始整改时，请选择同一责任组织的问题')
      return
    }
    bulkAssigneeLoading.value = true
    try {
      const first = selectedIssues.value[0]
      const response = await apiClient.get<ListResponse<UserItem>>(`/data-quality-issues/${first.id}/assignees`, { params: { page: 1, page_size: 100 } })
      bulkAssignees.value = response.data.items
      const sharedAssignee = selectedIssues.value.every((item) => item.assignee_user_id === first.assignee_user_id) ? first.assignee_user_id : ''
      const preferred = sharedAssignee || auth.user?.id || ''
      if (preferred && bulkAssignees.value.some((user) => user.id === preferred)) bulkForm.assignee_user_id = preferred
      else if (bulkAssignees.value.length === 1) bulkForm.assignee_user_id = bulkAssignees.value[0].id
    } catch (error) {
      ElMessage.error(getAPIErrorMessage(error, '读取责任组织可选整改负责人失败'))
      return
    } finally {
      bulkAssigneeLoading.value = false
    }
  }
  bulkActionVisible.value = true
}

async function submitBulkAction() {
  if ((bulkActionType.value === 'assign' || bulkActionType.value === 'start') && !bulkForm.assignee_user_id) {
    ElMessage.warning('请选择整改负责人')
    return
  }
  if (bulkActionType.value === 'submit' && !bulkForm.note.trim()) {
    ElMessage.warning('请填写批量整改说明')
    return
  }
  const targets = [...selectedIssues.value]
  try {
    await ElMessageBox.confirm(`确认对选中的 ${targets.length} 个数据质量问题执行“${bulkActionTitle.value}”吗？系统将逐项校验组织权限和当前状态。`, '批量操作确认', {
      type: 'warning', confirmButtonText: '确认执行', cancelButtonText: '取消',
    })
    bulkLoading.value = true
    const response = await apiClient.post<{ result: BatchOperationResult }>('/data-quality-issues/batch', {
      ids: targets.map((item) => item.id), action: bulkActionType.value,
      assignee_user_id: bulkForm.assignee_user_id, note: bulkForm.note,
    })
    const names = new Map(targets.map((item) => [item.id, item.resource_name]))
    bulkResult.value = {
      ...response.data.result,
      items: response.data.result.items.map((item) => ({ ...item, resource_name: item.item?.resource_name || names.get(item.id) || item.id })),
    }
    bulkResultTitle.value = `${bulkActionTitle.value}结果`
    bulkActionVisible.value = false
    bulkResultVisible.value = true
    if (response.data.result.failure_count === 0) ElMessage.success(`已成功处理 ${response.data.result.success_count} 个数据质量问题`)
    else ElMessage.warning(`批量处理完成：成功 ${response.data.result.success_count} 个，失败 ${response.data.result.failure_count} 个`)
    await refreshIssuesAfterMutation()
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error, `${bulkActionTitle.value}失败`))
  } finally {
    bulkLoading.value = false
  }
}

function applySavedIssueFilters(savedFilters: Record<string, string>) {
  Object.assign(filters, {
    organization_id: savedFilters.organization_id ?? '',
    status: savedFilters.status ?? '',
    severity: savedFilters.severity ?? '',
    resource_type: savedFilters.resource_type ?? '',
    assignee: savedFilters.assignee ?? '',
  })
  issuePage.value = 1
  void loadIssues()
}

function searchIssues() {
  issueTableRef.value?.clearSelection()
  selectedIssues.value = []
  issuePage.value = 1
  void loadIssues()
}

function resetFilters() {
  issueTableRef.value?.clearSelection()
  selectedIssues.value = []
  Object.assign(filters, { organization_id: '', status: '', severity: '', resource_type: '', assignee: '' })
  issuePage.value = 1
  void loadIssues()
}

function handleIssuePageChange(value: number) {
  issuePage.value = value
  void loadIssues()
}

function handleIssuePageSizeChange(value: number) {
  issuePageSize.value = value
  issuePage.value = 1
  void loadIssues()
}

onMounted(async () => {
  const defaultFilters = await savedFilterViews.value?.initialize()
  if (defaultFilters) {
    Object.assign(filters, {
      organization_id: defaultFilters.organization_id ?? '',
      status: defaultFilters.status ?? '',
      severity: defaultFilters.severity ?? '',
      resource_type: defaultFilters.resource_type ?? '',
      assignee: defaultFilters.assignee ?? '',
    })
  }
  await Promise.all([loadIssues(), loadReferenceData()])
})
</script>

<template>
  <section class="page-stack">
    <div class="page-heading">
      <div>
      <h1>质量整改</h1>
      </div>
      <el-button v-if="isGroupAdmin" type="primary" @click="router.push('/data-governance')">执行质量巡检</el-button>
    </div>

    <div class="quality-workflow-content">
      <div class="quality-workflow-summary">
      <div v-for="card in summaryCards" :key="String(card[0])" class="quality-workflow-card" :class="`tone-${card[2]}`"><span>{{ card[0] }}</span><strong>{{ card[1] }}</strong></div>
      </div>

      <div class="panel quality-workflow-filters">
      <el-select v-model="filters.organization_id" clearable filterable placeholder="全部组织"><el-option v-for="item in organizations" :key="item.id" :label="item.name" :value="item.id" /></el-select>
      <el-select v-model="filters.status" clearable placeholder="全部状态"><el-option v-for="(label, value) in statusLabels" :key="value" :label="label" :value="value" /></el-select>
      <el-select v-model="filters.severity" clearable placeholder="全部级别"><el-option label="错误" value="error" /><el-option label="警告" value="warning" /></el-select>
      <el-select v-model="filters.resource_type" clearable placeholder="全部对象"><el-option label="网络资产" value="asset" /><el-option label="业务系统" value="business_system" /></el-select>
      <el-select v-model="filters.assignee" clearable placeholder="全部负责人"><el-option label="只看我负责的" value="me" /><el-option v-for="user in availableFilterUsers" :key="user.id" :label="user.display_name" :value="user.id" /></el-select>
      <div class="quality-workflow-filter-actions"><el-button type="primary" @click="searchIssues">查询</el-button><el-button @click="resetFilters">重置</el-button></div>
      <SavedFilterViews ref="savedFilterViews" page-key="data_quality_issues" :current-filters="currentIssueFilters" class="quality-saved-views" @apply="applySavedIssueFilters" />
    </div>

      <div class="panel quality-workflow-table-panel quality-issue-panel">
      <div class="table-panel-heading"><div><strong>质量问题清单</strong><span>当前筛选命中 {{ summary.total }} 条</span></div><el-tag type="danger">错误 {{ summary.error }}</el-tag><el-tag type="warning">警告 {{ summary.warning }}</el-tag></div>
      <div class="batch-toolbar quality-batch-toolbar">
        <div><strong>批量整改</strong><span>已选择 {{ selectedIssues.length }} 个问题，单次最多 200 个；指派负责人和开始整改要求属于同一责任组织</span></div>
        <div class="button-row"><el-button :disabled="selectedIssues.length === 0" :loading="bulkLoading" @click="openBulkAction('assign')">批量指派</el-button><el-button type="primary" plain :disabled="selectedIssues.length === 0" :loading="bulkLoading" @click="openBulkAction('start')">批量开始整改</el-button><el-button type="success" plain :disabled="selectedIssues.length === 0" :loading="bulkLoading" @click="openBulkAction('submit')">批量提交复核</el-button></div>
      </div>
      <div class="quality-issue-table-scroll">
        <el-table ref="issueTableRef" v-loading="loading" :data="items" row-key="id" stripe max-height="560" empty-text="当前范围暂无数据质量问题" @selection-change="handleSelectionChange">
          <el-table-column type="selection" width="46" :reserve-selection="true" />
        <el-table-column label="问题" min-width="190"><template #default="scope"><el-button class="table-link" link type="primary" @click="openDetail(scope.row)">{{ scope.row.rule_name }}</el-button><div class="muted-text">检查项：{{ targetLabel(scope.row.target_field, scope.row) }}</div></template></el-table-column>
        <el-table-column label="资源 / 组织" min-width="180"><template #default="scope"><strong>{{ scope.row.resource_name }}</strong><div class="muted-text">{{ resourceTypeLabel(scope.row.resource_type) }}</div><span class="list-secondary">{{ scope.row.organization_name }}</span></template></el-table-column>

        <el-table-column label="级别" width="86"><template #default="scope"><el-tag :type="scope.row.severity === 'error' ? 'danger' : 'warning'">{{ severityLabel(scope.row.severity) }}</el-tag></template></el-table-column>
        <el-table-column label="状态" width="100"><template #default="scope"><el-tag :type="statusTypes[scope.row.status] as any">{{ statusLabels[scope.row.status] }}</el-tag></template></el-table-column>
        <el-table-column label="负责人" min-width="100"><template #default="scope">{{ scope.row.assignee_display_name || '未指派' }}</template></el-table-column>
        <el-table-column label="最近发现" width="135"><template #default="scope">{{ formatTime(scope.row.last_detected_at) }}</template></el-table-column>
        <el-table-column label="操作" fixed="right" width="88" align="right" header-align="right" class-name="table-action-column"><template #default="scope"><RowActions :direct="!isGroupAdmin && !['pending', 'in_progress'].includes(scope.row.status)" :label="`操作：${scope.row.name || scope.row.title || scope.row.code || scope.row.display_name || scope.row.id}`"><div class="row-actions">
          <el-button v-if="scope.row.status === 'pending'" link type="primary" @click="openAction('start', scope.row)">开始整改</el-button>
          <el-button v-if="scope.row.status === 'in_progress'" link type="primary" @click="openAction('assign', scope.row)">调整负责人</el-button>
          <el-button v-if="['pending','in_progress'].includes(scope.row.status)" link type="success" @click="openAction('submit', scope.row)">提交复核</el-button>
          <el-button v-if="isGroupAdmin && scope.row.status === 'pending_review'" link type="success" @click="openAction('approve', scope.row)">通过</el-button>
          <el-button v-if="isGroupAdmin && scope.row.status === 'pending_review'" link type="warning" @click="openAction('return', scope.row)">退回</el-button>
          <el-button v-if="isGroupAdmin && ['pending','in_progress','pending_review'].includes(scope.row.status)" link type="warning" @click="openAction('exempt', scope.row)">豁免</el-button>
          <el-button v-if="isGroupAdmin && ['closed','exempted'].includes(scope.row.status)" link type="primary" @click="openAction('reopen', scope.row)">重新打开</el-button>
          </div></RowActions></template></el-table-column>
        </el-table>
      </div>
      <div class="table-pagination">
        <el-pagination background layout="total, sizes, prev, pager, next" :total="issueTotal" :current-page="issuePage" :page-size="issuePageSize" :page-sizes="[20, 50, 100]" @current-change="handleIssuePageChange" @size-change="handleIssuePageSizeChange" />
      </div>
    </div>

    </div>

    <el-drawer v-model="detailVisible" title="质量问题详情" size="620px">
      <div v-loading="detailLoading" class="detail-drawer" v-if="selected">
        <div class="detail-hero"><div><span class="eyebrow">{{ selected.rule_name }}</span><h2>{{ selected.resource_name }}</h2><p>{{ selected.organization_name }} · {{ targetLabel(selected.target_field, selected) }}</p></div><el-tag :type="statusTypes[selected.status] as any">{{ statusLabels[selected.status] }}</el-tag></div>
        <div class="detail-grid">
          <div><span>问题级别</span><strong>{{ severityLabel(selected.severity) }}</strong></div><div><span>资源类型</span><strong>{{ resourceTypeLabel(selected.resource_type) }}</strong></div>
          <div><span>整改负责人</span><strong>{{ selected.assignee_display_name || '未指派' }}</strong></div><div><span>累计命中</span><strong>{{ selected.occurrence_count }} 次</strong></div>
          <div><span>首次发现</span><strong>{{ formatTime(selected.first_detected_at) }}</strong></div><div><span>最近发现</span><strong>{{ formatTime(selected.last_detected_at) }}</strong></div>
        </div>
        <div v-if="selected.rectification_note" class="quality-note"><span>整改说明</span><p>{{ selected.rectification_note }}</p></div>
        <div v-if="selected.review_note" class="quality-note"><span>复核意见</span><p>{{ selected.review_note }}</p></div>
        <div v-if="selected.exemption_reason" class="quality-note"><span>豁免原因</span><p>{{ selected.exemption_reason }}</p><small>有效期：{{ selected.exemption_expires_at ? formatTime(selected.exemption_expires_at) : '长期有效' }}</small></div>
        <div class="detail-actions"><el-button @click="router.push(resourcePath(selected))">前往{{ resourceTypeLabel(selected.resource_type) }}台账</el-button></div>
        <el-divider>处理记录</el-divider>
        <div v-loading="eventLoading" class="quality-event-list">
          <el-timeline v-if="events.length"><el-timeline-item v-for="event in events" :key="event.id" :timestamp="formatTime(event.created_at)" placement="top"><strong>{{ eventLabels[event.event_type] || event.event_type }}</strong><span class="quality-event-actor">{{ event.actor_display_name || '系统' }}</span><p v-if="event.note">{{ event.note }}</p></el-timeline-item></el-timeline>
          <el-empty v-else description="暂无人工处理记录；巡检命中历史可通过最近发现时间和命中次数查看" />
        </div>
        <el-pagination v-if="eventTotal > 0" class="table-pagination" background layout="total, sizes, prev, pager, next" :total="eventTotal" :current-page="eventPage" :page-size="eventPageSize" :page-sizes="[20, 50, 100]" @current-change="handleEventPageChange" @size-change="handleEventPageSizeChange" />
      </div>
    </el-drawer>

    <el-dialog v-model="bulkActionVisible" :title="bulkActionTitle" width="600px" destroy-on-close>
      <el-alert :title="`本次将处理 ${selectedIssues.length} 个问题，服务端会逐项校验并允许部分成功`" type="info" :closable="false" show-icon />
      <el-form label-width="110px" class="quality-batch-form">
        <el-form-item v-if="['assign','start'].includes(bulkActionType)" label="整改负责人" required>
          <div class="assignee-field">
            <el-select v-model="bulkForm.assignee_user_id" :loading="bulkAssigneeLoading" filterable clearable placeholder="请选择责任组织的有效任职用户" style="width:100%"><el-option v-for="user in bulkAssignees" :key="user.id" :label="`${user.display_name}（${user.username}）`" :value="user.id" /></el-select>
            <small v-if="!bulkAssigneeLoading && bulkAssignees.length === 0">该责任组织暂无可用用户，请先由有权限的管理员补充用户任职。</small>
          </div>
        </el-form-item>
        <el-form-item label="处理说明" :required="bulkActionType === 'submit'"><el-input v-model="bulkForm.note" type="textarea" :rows="4" maxlength="5000" show-word-limit :placeholder="bulkActionType === 'submit' ? '请填写本批问题的整改说明' : '可选，记录本次批量操作说明'" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="bulkActionVisible=false">取消</el-button><el-button type="primary" :loading="bulkLoading" @click="submitBulkAction">确认执行</el-button></template>
    </el-dialog>

    <el-dialog v-model="bulkResultVisible" :title="bulkResultTitle" width="760px">
      <template v-if="bulkResult">
        <el-alert :title="`共处理 ${bulkResult.total_count} 个，成功 ${bulkResult.success_count} 个，失败 ${bulkResult.failure_count} 个`" :type="bulkResult.failure_count ? 'warning' : 'success'" :closable="false" show-icon />
        <el-table :data="bulkResult.items" max-height="420" class="batch-result-table">
          <el-table-column prop="resource_name" label="问题资源" min-width="220" />
          <el-table-column label="结果" width="90"><template #default="scope"><el-tag :type="scope.row.success ? 'success' : 'danger'">{{ scope.row.success ? '成功' : '失败' }}</el-tag></template></el-table-column>
          <el-table-column prop="message" label="说明" min-width="260" />
        </el-table>
      </template>
      <template #footer><el-button type="primary" @click="bulkResultVisible=false">关闭</el-button></template>
    </el-dialog>

    <el-dialog v-model="actionVisible" :title="actionTitle" width="560px" destroy-on-close>
      <el-form label-width="100px">
        <el-form-item v-if="['assign','start'].includes(actionType)" label="整改负责人" required>
          <div class="assignee-field">
            <el-select v-model="actionForm.assignee_user_id" :loading="assigneeLoading" filterable clearable placeholder="请选择该责任组织的有效任职用户" style="width:100%"><el-option v-for="user in issueAssignees" :key="user.id" :label="`${user.display_name}（${user.username}）`" :value="user.id" /></el-select>
            <small v-if="!assigneeLoading && issueAssignees.length === 0">该责任组织暂无可用用户，请先由有权限的管理员补充用户任职。</small>
          </div>
        </el-form-item>
        <el-form-item v-if="actionType === 'exempt'" label="豁免有效期"><el-date-picker v-model="actionForm.expires_at" type="datetime" placeholder="留空表示长期有效" style="width:100%" /></el-form-item>
        <el-form-item :label="actionType === 'exempt' ? '豁免原因' : '处理说明'" :required="['submit','return','exempt'].includes(actionType)"><el-input v-model="actionForm.note" type="textarea" :rows="4" maxlength="5000" show-word-limit /></el-form-item>
      </el-form>
      <template #footer><el-button @click="actionVisible=false">取消</el-button><el-button :type="actionButtonType" :loading="acting" @click="submitAction">确认{{ actionTitle }}</el-button></template>
    </el-dialog>
  </section>
</template>

<style scoped>
.quality-workflow-content { display:grid; min-width:0; gap:16px; }
.quality-workflow-content > * { min-width:0; max-width:100%; }
.quality-workflow-summary { display:grid; grid-template-columns:repeat(auto-fit,minmax(min(100%,150px),1fr)); gap:12px; }
.quality-workflow-card { min-width:0; padding:16px 18px; border:1px solid var(--line); border-left:4px solid #5b8def; border-radius:10px; background:#fff; }
.quality-workflow-card span { display:block; margin-bottom:8px; color:var(--muted); font-size:12px; }
.quality-workflow-card strong { color:var(--navy); font-size:26px; }
.quality-workflow-card.tone-danger { border-left-color:#f56c6c; }.quality-workflow-card.tone-warning { border-left-color:#e6a23c; }.quality-workflow-card.tone-success { border-left-color:#67c23a; }.quality-workflow-card.tone-info { border-left-color:#909399; }.quality-workflow-card.tone-total { border-left-color:var(--cyan); }
.quality-workflow-filters { display:grid; grid-template-columns:repeat(auto-fit,minmax(min(100%,160px),1fr)); min-width:0; gap:12px; align-items:center; }
.quality-workflow-filters :deep(.el-select) { min-width:0; width:100%; }
.quality-saved-views { grid-column:1 / -1; min-width:0; padding-top:4px; border-top:1px dashed var(--line); }
.quality-workflow-filter-actions { display:flex; flex-wrap:wrap; gap:8px; }.table-pagination { display:flex; justify-content:flex-end; margin-top:18px; }.quality-workflow-table-panel { min-width:0; padding-top:14px; }.row-actions { display:flex; flex-wrap:wrap; justify-content:flex-end; gap:2px 8px; }
.quality-issue-panel .table-panel-heading { flex-wrap:wrap; gap:8px 12px; }.quality-issue-table-scroll { width:100%; min-width:0; overflow-x:auto; overscroll-behavior-x:contain; }.quality-issue-table-scroll :deep(.el-table) { min-width:100%; }
.quality-batch-toolbar { flex-wrap:wrap; margin:14px 0; }.quality-batch-toolbar .button-row { flex-wrap:wrap; margin-left:auto; justify-content:flex-end; }.quality-batch-form { margin-top:18px; }
.quality-note { margin:14px 0; padding:14px 16px; background:#f8fafc; border-radius:9px; }.quality-note span { color:var(--muted); font-size:12px; }.quality-note p { margin:7px 0; white-space:pre-wrap; line-height:1.65; }.quality-note small { color:var(--muted); }
.assignee-field { width:100%; }.assignee-field small { display:block; margin-top:7px; color:#e6a23c; line-height:1.5; }
.quality-event-list { min-height:120px; max-height:420px; overflow:auto; padding:4px 10px 0 2px; }
.quality-event-actor { margin-left:10px; color:var(--muted); font-size:12px; }.el-timeline-item p { margin:6px 0 0; color:var(--muted); white-space:pre-wrap; }
@media (max-width:760px) { .quality-workflow-filter-actions { width:100%; }.quality-workflow-filter-actions .el-button { flex:1; }.quality-batch-toolbar .button-row { width:100%; margin-left:0; justify-content:flex-start; }.table-pagination { justify-content:flex-start; overflow-x:auto; padding-bottom:2px; } }
</style>
