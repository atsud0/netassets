<script setup lang="ts">
import RowActions from '../components/RowActions.vue'
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormItemRule, type FormRules } from 'element-plus'
import axios from 'axios'
import { useRoute } from 'vue-router'
import { apiClient, getAPIErrorMessage } from '../api/client'
import { useAuthStore } from '../stores/auth'

interface ListResponse<T> { items: T[]; total: number; page: number; page_size: number }
interface ItemResponse<T> { item: T }

interface ScopeAsset {
  id: string
  published_asset_id?: string
  working_asset_id?: string
  asset_type: string
  asset_value: string
  asset_name?: string
  organization_code: string
  organization_name: string
  business_system_code?: string
  business_system_name?: string
  network_scope: string
  network_area_name?: string
  environment: string
  importance: string
  report_action: string
  snapshot_at: string
}
interface SecurityTestRequest {
  id: string
  code: string
  request_name: string
  applicant_organization_id: string
  applicant_organization_code: string
  applicant_organization_name: string
  business_system_id?: string
  business_system_draft_id?: string
  business_system_code?: string
  business_system_name?: string
  test_type: string
  expected_launch_at: string
  test_window_start_at?: string
  test_window_end_at?: string
  contact_user_id?: string
  contact_name?: string
  background: string
  status: string
  scope_asset_count: number
  reviewed_by?: string
  reviewed_by_name?: string
  reviewed_at?: string
  review_comment?: string
  converted_batch_id?: string
  converted_batch_code?: string
  converted_batch_status?: string
  launch_decided_by?: string
  launch_decided_by_name?: string
  launch_decided_at?: string
  launch_comment?: string
  reported_task_id?: string
  has_unreported_scope: boolean
  created_by_name?: string
  created_at: string
  updated_at: string
  scope_assets?: ScopeAsset[]
  open_finding_count: number
}
interface RequestEvent {
  id: number
  action: string
  from_status?: string
  to_status?: string
  comment?: string
  details?: Record<string, unknown>
  actor_name?: string
  created_at: string
}
interface QualityIssue { code: string; severity: string; message: string; count: number }
interface QualityCheck {
  asset_count: number
  system_count: number
  issues: QualityIssue[]
  error_count: number
  warning_count: number
  passed: boolean
}
interface ContactCandidate { id: string; username: string; display_name: string }
interface BlockingFinding { code: string; title: string; status: string }
interface SystemPickerRow {
  id: string
  code: string
  name: string
  organization_id: string
  organization_name: string
  status: string
  reporting_status?: string
  is_working_copy?: boolean
  task_id?: string
  draft_status?: string
  operation?: string
}
// ScopeItem is the unified selection shape: candidates from the picker carry
// reporting_status, frozen scope rows loaded for editing carry report_action.
interface ScopeItem {
  id: string
  kind: 'working' | 'published'
  asset_type: string
  asset_value: string
  asset_name?: string
  organization_name: string
  business_system_name?: string
  network_area_name?: string
  environment: string
  importance: string
  reporting_status?: string
  report_action?: string
}
interface SelectedSystem { kind: 'published' | 'draft'; id: string; code: string; name: string }

const auth = useAuthStore()
const route = useRoute()
const isApplicant = computed(() => auth.canRequestSecurityTest)
const isManager = computed(() => auth.canManageSecurityTests)

const loading = ref(false)
const submitting = ref(false)
const actionLoading = ref(false)
const requests = ref<SecurityTestRequest[]>([])
const statusFilter = ref('')
const keywordFilter = ref('')
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const dialogVisible = ref(false)
const detailVisible = ref(false)
const editingID = ref('')
const formRef = ref<FormInstance>()
const currentRequest = ref<SecurityTestRequest | null>(null)
const contactCandidates = ref<ContactCandidate[]>([])
const selectedSystem = ref<SelectedSystem | null>(null)
const selectedScope = ref<ScopeItem[]>([])
const requestEvents = ref<RequestEvent[]>([])
const eventListLoading = ref(false)
const eventPage = ref(1)
const eventPageSize = ref(20)
const eventTotal = ref(0)
const qualityResult = ref<QualityCheck | null>(null)
const qualityCheckLoading = ref(false)
const qualityDialogVisible = ref(false)

const systemPickerVisible = ref(false)
const systemPickerLoading = ref(false)
const systemPickerRows = ref<SystemPickerRow[]>([])
const systemPickerKeyword = ref('')
const systemPickerPage = ref(1)
const systemPickerPageSize = ref(20)
const systemPickerTotal = ref(0)

const scopePickerVisible = ref(false)
const scopePickerLoading = ref(false)
const scopePickerRows = ref<ScopeItem[]>([])
const scopePickerKeyword = ref('')
const scopePickerType = ref('')
const scopePickerPage = ref(1)
const scopePickerPageSize = ref(20)
const scopePickerTotal = ref(0)

const approveVisible = ref(false)
const approveSubmitting = ref(false)
const approvingID = ref('')
const approveFormRef = ref<FormInstance>()
const approveForm = reactive({
  authorization_note: '',
  test_window_start_at: null as Date | null,
  test_window_end_at: null as Date | null,
  comment: '',
})

const form = reactive({
  request_name: '',
  test_type: 'penetration',
  expected_launch_at: null as Date | null,
  test_window_start_at: null as Date | null,
  test_window_end_at: null as Date | null,
  contact_user_id: '',
  background: '',
})

const statusLabels: Record<string, string> = {
  draft: '草稿', submitted: '待审核', rejected: '已驳回', testing: '测试中',
  pending_launch: '待上线审批', cancelled: '已取消', closed: '已关闭',
}
const statusTypes: Record<string, string> = {
  draft: 'info', submitted: 'warning', rejected: 'danger', testing: 'primary',
  pending_launch: 'warning', cancelled: 'info', closed: 'success',
}
const typeLabels: Record<string, string> = {
  penetration: '渗透测试', manual: '人工检查', scan: '扫描测试', retest: '复测', other: '其他',
}
const assetTypeLabels: Record<string, string> = { domain: '域名', ip: 'IP', url: 'URL', service: '网络服务', other: '其他' }
const reportActionLabels: Record<string, string> = { create: '新增上报', update: '变更上报', delete: '下线上报', none: '已发布无变更' }
const reportingStatusLabels: Record<string, string> = { reported: '已上报', unreported: '待上报', changed: '已变更', returned: '已退回' }
const eventLabels: Record<string, string> = {
  create: '创建申请', update: '更新申请', submit: '提交申请', withdraw: '撤回申请',
  reject: '驳回申请', approve: '审批通过', pending_launch: '达成上线条件',
  approve_launch: '批准上线', cancel: '取消申请',
}

const selectedOrganizationCount = computed(() => new Set(selectedScope.value.map(item => item.organization_name)).size)
const systemPickerSelectable = (row: SystemPickerRow) => {
  if (!row.is_working_copy) return true
  return !row.task_id && ['unreported', 'changed', 'returned'].includes(row.reporting_status ?? '')
}
const systemPickerHint = (row: SystemPickerRow) => {
  if (!row.is_working_copy) return '已上报版本'
  if (row.task_id) return '已纳入其他填报任务'
  if (row.reporting_status === 'pending_review') return '审核中'
  if (row.reporting_status === 'pending_offline') return '下线申请中'
  return '待上报版本'
}

function requestTextRule(limit: number, message: string): FormItemRule {
  return { validator: (_rule, value: string, callback) => callback(Array.from((value ?? '').trim()).length > limit ? new Error(message) : undefined), trigger: 'blur' }
}

const rules: FormRules = {
  request_name: [{ required: true, whitespace: true, message: '请输入申请名称', trigger: 'blur' }, requestTextRule(255, '申请名称不能超过 255 个字符')],
  test_type: [{ required: true, message: '请选择测试类型', trigger: 'change' }],
  expected_launch_at: [{ required: true, message: '请选择期望上线时间', trigger: 'change' }],
  contact_user_id: [{ required: true, message: '请选择联系人', trigger: 'change' }],
  background: [{ required: true, whitespace: true, message: '请描述申请背景与测试诉求', trigger: 'blur' }, requestTextRule(10000, '申请背景不能超过 10000 个字符')],
}
const approveRules: FormRules = {
  authorization_note: [{ required: true, whitespace: true, message: '请输入授权说明', trigger: 'blur' }, requestTextRule(20000, '授权说明不能超过 20000 个字符')],
  comment: [requestTextRule(2000, '审批意见不能超过 2000 个字符')],
  test_window_start_at: [{ required: true, message: '请选择最终测试开始时间', trigger: 'change' }],
  test_window_end_at: [{ required: true, message: '请选择最终测试结束时间', trigger: 'change' }],
}

onMounted(() => {
  // 通知中心跳转携带 keyword 查询参数，进入页面时直接按申请编号过滤。
  if (typeof route.query.keyword === 'string' && route.query.keyword.trim()) keywordFilter.value = route.query.keyword.trim()
  void load()
})

async function load() {
  loading.value = true
  try {
    const params: Record<string, string | number> = { page: page.value, page_size: pageSize.value }
    if (statusFilter.value) params.status = statusFilter.value
    if (keywordFilter.value.trim()) params.keyword = keywordFilter.value.trim()
    const response = await apiClient.get<ListResponse<SecurityTestRequest>>('/security-test-requests', { params })
    requests.value = response.data.items
    total.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取上线安全测试申请失败'))
  } finally {
    loading.value = false
  }
}

async function refreshAfterMutation() {
  await load()
  if (requests.value.length === 0 && total.value > 0 && page.value > 1) {
    page.value -= 1
    await load()
  }
  if (detailVisible.value && currentRequest.value) {
    const detail = await fetchDetail(currentRequest.value.id)
    if (detail) currentRequest.value = detail
  }
}

function applyStatusFilter() {
  page.value = 1
  void load()
}

function searchKeyword() {
  page.value = 1
  void load()
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

async function loadContactCandidates() {
  try {
    const response = await apiClient.get<{ items: ContactCandidate[] }>('/security-test-requests/contact-candidates')
    contactCandidates.value = response.data.items
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取组织联系人失败'))
  }
}

function contactLabel(id: string) {
  const contact = contactCandidates.value.find(item => item.id === id)
  return contact ? contact.display_name || contact.username : id
}

function openCreate() {
  editingID.value = ''
  selectedSystem.value = null
  selectedScope.value = []
  Object.assign(form, {
    request_name: '', test_type: 'penetration', expected_launch_at: null,
    test_window_start_at: null, test_window_end_at: null, contact_user_id: '', background: '',
  })
  dialogVisible.value = true
  if (contactCandidates.value.length === 0) void loadContactCandidates()
}

async function fetchDetail(id: string) {
  try {
    const response = await apiClient.get<ItemResponse<SecurityTestRequest>>(`/security-test-requests/${id}`)
    return response.data.item
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取申请详情失败'))
    return null
  }
}

async function openEdit(item: SecurityTestRequest) {
  const detail = await fetchDetail(item.id)
  if (!detail) return
  editingID.value = detail.id
  selectedSystem.value = detail.business_system_draft_id
    ? { kind: 'draft', id: detail.business_system_draft_id, code: detail.business_system_code ?? '', name: detail.business_system_name ?? '' }
    : { kind: 'published', id: detail.business_system_id ?? '', code: detail.business_system_code ?? '', name: detail.business_system_name ?? '' }
  selectedScope.value = (detail.scope_assets ?? []).map(row => ({
    id: row.published_asset_id || row.working_asset_id || row.id,
    kind: row.published_asset_id ? 'published' : 'working',
    asset_type: row.asset_type,
    asset_value: row.asset_value,
    asset_name: row.asset_name,
    organization_name: row.organization_name,
    business_system_name: row.business_system_name,
    network_area_name: row.network_area_name,
    environment: row.environment,
    importance: row.importance,
    report_action: row.report_action,
  }))
  Object.assign(form, {
    request_name: detail.request_name,
    test_type: detail.test_type,
    expected_launch_at: new Date(detail.expected_launch_at),
    test_window_start_at: detail.test_window_start_at ? new Date(detail.test_window_start_at) : null,
    test_window_end_at: detail.test_window_end_at ? new Date(detail.test_window_end_at) : null,
    contact_user_id: detail.contact_user_id ?? '',
    background: detail.background,
  })
  dialogVisible.value = true
  if (contactCandidates.value.length === 0) void loadContactCandidates()
}

async function openSystemPicker() {
  systemPickerKeyword.value = ''
  systemPickerPage.value = 1
  systemPickerVisible.value = true
  await loadSystemPicker()
}

async function loadSystemPicker() {
  systemPickerLoading.value = true
  try {
    const params: Record<string, string | number> = {
      page: systemPickerPage.value,
      page_size: systemPickerPageSize.value,
      organization_id: auth.user?.organization?.id ?? '',
    }
    if (systemPickerKeyword.value.trim()) params.q = systemPickerKeyword.value.trim()
    const response = await apiClient.get<ListResponse<SystemPickerRow>>('/business-systems', { params })
    systemPickerRows.value = response.data.items
    systemPickerTotal.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取业务系统失败'))
  } finally {
    systemPickerLoading.value = false
  }
}

function searchSystemPicker() {
  systemPickerPage.value = 1
  void loadSystemPicker()
}

function handleSystemPickerPageChange(value: number) {
  systemPickerPage.value = value
  void loadSystemPicker()
}

function handleSystemPickerPageSizeChange(value: number) {
  systemPickerPageSize.value = value
  systemPickerPage.value = 1
  void loadSystemPicker()
}

function selectSystem(row: SystemPickerRow) {
  if (!systemPickerSelectable(row)) {
    ElMessage.warning('该业务系统暂不能选为上线对象，请先在业务系统页面完成编辑')
    return
  }
  selectedSystem.value = row.is_working_copy
    ? { kind: 'draft', id: row.id, code: row.code, name: row.name }
    : { kind: 'published', id: row.id, code: row.code, name: row.name }
  systemPickerVisible.value = false
}

function clearSystem() {
  selectedSystem.value = null
}

async function openScopePicker() {
  scopePickerKeyword.value = ''
  scopePickerType.value = ''
  scopePickerPage.value = 1
  scopePickerVisible.value = true
  await loadScopePicker()
}

async function loadScopePicker() {
  scopePickerLoading.value = true
  try {
    const params: Record<string, string | number> = {
      page: scopePickerPage.value,
      page_size: scopePickerPageSize.value,
    }
    if (scopePickerKeyword.value.trim()) params.keyword = scopePickerKeyword.value.trim()
    if (scopePickerType.value) params.asset_type = scopePickerType.value
    const response = await apiClient.get<ListResponse<ScopeItem>>('/security-test-requests/scope-candidates', { params })
    scopePickerRows.value = response.data.items
    scopePickerTotal.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取可选公网资产失败'))
  } finally {
    scopePickerLoading.value = false
  }
}

function searchScopePicker() {
  scopePickerPage.value = 1
  void loadScopePicker()
}

function handleScopePickerPageChange(value: number) {
  scopePickerPage.value = value
  void loadScopePicker()
}

function handleScopePickerPageSizeChange(value: number) {
  scopePickerPageSize.value = value
  scopePickerPage.value = 1
  void loadScopePicker()
}

function isScopeSelected(id: string) {
  return selectedScope.value.some(item => item.id === id)
}

function toggleScope(item: ScopeItem) {
  if (isScopeSelected(item.id)) {
    selectedScope.value = selectedScope.value.filter(row => row.id !== item.id)
    return
  }
  if (selectedScope.value.length >= 200) {
    ElMessage.warning('申请范围最多只能包含 200 项资产')
    return
  }
  selectedScope.value.push(item)
}

function removeScope(id: string) {
  selectedScope.value = selectedScope.value.filter(row => row.id !== id)
}

async function save() {
  if (!formRef.value || !(await formRef.value.validate().catch(() => false))) return
  if (!selectedSystem.value) {
    ElMessage.warning('请选择申请上线的业务系统')
    return
  }
  if (selectedScope.value.length === 0) {
    ElMessage.warning('请至少选择 1 项公网资产')
    return
  }
  if (form.test_window_start_at && form.test_window_end_at && form.test_window_end_at <= form.test_window_start_at) {
    ElMessage.warning('期望测试窗口的结束时间必须晚于开始时间')
    return
  }
  if (!form.expected_launch_at) return
  submitting.value = true
  try {
    const payload = {
      request_name: form.request_name,
      test_type: form.test_type,
      expected_launch_at: form.expected_launch_at.toISOString(),
      test_window_start_at: form.test_window_start_at ? form.test_window_start_at.toISOString() : null,
      test_window_end_at: form.test_window_end_at ? form.test_window_end_at.toISOString() : null,
      contact_user_id: form.contact_user_id,
      background: form.background,
      business_system_id: selectedSystem.value.kind === 'published' ? selectedSystem.value.id : '',
      business_system_draft_id: selectedSystem.value.kind === 'draft' ? selectedSystem.value.id : '',
      published_asset_ids: selectedScope.value.filter(item => item.kind === 'published').map(item => item.id),
      working_asset_ids: selectedScope.value.filter(item => item.kind === 'working').map(item => item.id),
    }
    if (editingID.value) await apiClient.patch(`/security-test-requests/${editingID.value}`, payload)
    else await apiClient.post('/security-test-requests', payload)
    ElMessage.success(editingID.value ? '申请已更新' : '申请已创建，可继续编辑或直接提交')
    dialogVisible.value = false
    await refreshAfterMutation()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, editingID.value ? '更新申请失败' : '创建申请失败'))
  } finally {
    submitting.value = false
  }
}

async function openDetail(item: SecurityTestRequest) {
  const detail = await fetchDetail(item.id)
  if (!detail) return
  currentRequest.value = detail
  qualityResult.value = null
  requestEvents.value = []
  eventPage.value = 1
  eventTotal.value = 0
  detailVisible.value = true
  await loadRequestEvents()
}

async function loadRequestEvents() {
  if (!currentRequest.value) return
  eventListLoading.value = true
  try {
    const response = await apiClient.get<ListResponse<RequestEvent>>(`/security-test-requests/${currentRequest.value.id}/events`, {
      params: { page: eventPage.value, page_size: eventPageSize.value },
    })
    requestEvents.value = response.data.items
    eventTotal.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取申请时间线失败'))
  } finally {
    eventListLoading.value = false
  }
}

function handleEventPageChange(value: number) {
  eventPage.value = value
  void loadRequestEvents()
}

function handleEventPageSizeChange(value: number) {
  eventPageSize.value = value
  eventPage.value = 1
  void loadRequestEvents()
}

function showQualityResult(check: QualityCheck) {
  qualityResult.value = check
  qualityDialogVisible.value = true
}

async function runQualityCheck() {
  if (!currentRequest.value) return
  qualityCheckLoading.value = true
  try {
    const response = await apiClient.get<ItemResponse<QualityCheck>>(`/security-test-requests/${currentRequest.value.id}/quality-check`)
    showQualityResult(response.data.item)
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '范围自检失败'))
  } finally {
    qualityCheckLoading.value = false
  }
}

async function runSubmit(item: SecurityTestRequest) {
  try {
    await ElMessageBox.confirm(
      '提交后申请范围将被冻结并进入集团安全审核，确认提交吗？',
      '提交安全测试申请',
      { confirmButtonText: '确认提交', cancelButtonText: '返回', type: 'warning' },
    )
  } catch {
    return
  }
  actionLoading.value = true
  try {
    await apiClient.post(`/security-test-requests/${item.id}/submit`)
    ElMessage.success('申请已提交，等待集团安全审核')
    await refreshAfterMutation()
  } catch (error) {
    if (axios.isAxiosError(error) && error.response?.status === 422 && error.response.data?.quality_check) {
      showQualityResult(error.response.data.quality_check as QualityCheck)
    } else {
      ElMessage.error(getAPIErrorMessage(error, '提交申请失败'))
    }
  } finally {
    actionLoading.value = false
  }
}

async function runWithdraw(item: SecurityTestRequest) {
  try {
    await ElMessageBox.confirm('撤回后申请将退回草稿，可继续编辑后重新提交。确认撤回吗？', '撤回申请', {
      confirmButtonText: '确认撤回', cancelButtonText: '返回', type: 'warning',
    })
  } catch {
    return
  }
  actionLoading.value = true
  try {
    await apiClient.post(`/security-test-requests/${item.id}/withdraw`)
    ElMessage.success('申请已撤回为草稿')
    await refreshAfterMutation()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '撤回申请失败'))
  } finally {
    actionLoading.value = false
  }
}

async function runCancel(item: SecurityTestRequest) {
  let comment = ''
  try {
    const result = await ElMessageBox.prompt('请输入取消原因。', '取消申请', {
      confirmButtonText: '确认取消', cancelButtonText: '返回', type: 'warning',
      inputValidator: value => !value?.trim() ? '取消原因不能为空' : Array.from(value.trim()).length <= 2000 || '取消原因不能超过 2000 个字符',
    })
    comment = result.value ?? ''
  } catch {
    return
  }
  actionLoading.value = true
  try {
    await apiClient.post(`/security-test-requests/${item.id}/cancel`, { comment })
    ElMessage.success('申请已取消')
    await refreshAfterMutation()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '取消申请失败'))
  } finally {
    actionLoading.value = false
  }
}

async function runReject(item: SecurityTestRequest) {
  let comment = ''
  try {
    const result = await ElMessageBox.prompt('请输入驳回原因，申请人修改后可重新提交。', '驳回申请', {
      confirmButtonText: '确认驳回', cancelButtonText: '返回', type: 'warning',
      inputValidator: value => !value?.trim() ? '驳回原因不能为空' : Array.from(value.trim()).length <= 2000 || '驳回原因不能超过 2000 个字符',
    })
    comment = result.value ?? ''
  } catch {
    return
  }
  actionLoading.value = true
  try {
    await apiClient.post(`/security-test-requests/${item.id}/reject`, { comment })
    ElMessage.success('申请已驳回')
    await refreshAfterMutation()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '驳回申请失败'))
  } finally {
    actionLoading.value = false
  }
}

function openApprove(item: SecurityTestRequest) {
  approvingID.value = item.id
  Object.assign(approveForm, {
    authorization_note: '',
    test_window_start_at: item.test_window_start_at ? new Date(item.test_window_start_at) : null,
    test_window_end_at: item.test_window_end_at ? new Date(item.test_window_end_at) : null,
    comment: '',
  })
  approveVisible.value = true
}

async function submitApprove() {
  if (!approveFormRef.value || !(await approveFormRef.value.validate().catch(() => false))) return
  if (!approveForm.test_window_start_at || !approveForm.test_window_end_at ||
      approveForm.test_window_end_at <= approveForm.test_window_start_at) {
    ElMessage.warning('最终测试窗口的结束时间必须晚于开始时间')
    return
  }
  approveSubmitting.value = true
  try {
    await apiClient.post(`/security-test-requests/${approvingID.value}/approve`, {
      authorization_note: approveForm.authorization_note,
      test_window_start_at: approveForm.test_window_start_at.toISOString(),
      test_window_end_at: approveForm.test_window_end_at.toISOString(),
      comment: approveForm.comment,
    })
    ElMessage.success('申请已通过，安全测试批次已生成')
    approveVisible.value = false
    await refreshAfterMutation()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '审批通过失败'))
  } finally {
    approveSubmitting.value = false
  }
}

function escapeHtml(value: string) {
  return value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
}

async function runApproveLaunch(item: SecurityTestRequest) {
  let comment = ''
  try {
    const result = await ElMessageBox.prompt(
      `批准上线后将自动把申请范围内 ${item.scope_asset_count} 项资产的上报内容推送至集团台账审核流程。可补充上线说明（留空即可）。`,
      '批准上线',
      { confirmButtonText: '批准上线', cancelButtonText: '返回', type: 'success', inputValidator: value => Array.from((value ?? '').trim()).length <= 20000 || '上线说明不能超过 20000 个字符' },
    )
    comment = result.value ?? ''
  } catch {
    return
  }
  actionLoading.value = true
  try {
    await apiClient.post(`/security-test-requests/${item.id}/approve-launch`, { comment })
    ElMessage.success('已批准上线并完成自动上报')
    await refreshAfterMutation()
  } catch (error) {
    if (axios.isAxiosError(error) && error.response?.status === 409) {
      const findings = error.response.data?.items as BlockingFinding[] | undefined
      if (Array.isArray(findings) && findings.length > 0) {
        const list = findings.map(finding => `<li>[${escapeHtml(finding.code)}] ${escapeHtml(finding.title)}（${escapeHtml(finding.status)}）</li>`).join('')
        await ElMessageBox.alert(`<p>申请范围仍有 ${findings.length} 项未闭环的安全发现：</p><ul>${list}</ul>`, '暂不能批准上线', {
          dangerouslyUseHTMLString: true, confirmButtonText: '知道了', type: 'warning',
        })
        return
      }
    }
    ElMessage.error(getAPIErrorMessage(error, '批准上线失败'))
  } finally {
    actionLoading.value = false
  }
}

function canEdit(item: SecurityTestRequest) {
  return isApplicant.value && ['draft', 'rejected'].includes(item.status)
}
function canSubmit(item: SecurityTestRequest) {
  return isApplicant.value && item.status === 'draft'
}
function canWithdraw(item: SecurityTestRequest) {
  return isApplicant.value && item.status === 'submitted'
}
function canApprove(item: SecurityTestRequest) {
  return isManager.value && item.status === 'submitted'
}
function canApproveLaunch(item: SecurityTestRequest) {
  return isManager.value && ['testing', 'pending_launch'].includes(item.status)
}
function canCancel(item: SecurityTestRequest) {
  if (isApplicant.value && ['draft', 'rejected'].includes(item.status)) return true
  if (isManager.value && ['submitted', 'testing'].includes(item.status)) return item.converted_batch_status === '' || item.converted_batch_status === 'authorized'
  return false
}
function canRunQualityCheck(item: SecurityTestRequest) {
  return isApplicant.value && ['draft', 'rejected'].includes(item.status)
}

function formatTime(value?: string) {
  return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '-'
}
function formatDate(value?: string) {
  return value ? new Date(value).toLocaleDateString('zh-CN') : '-'
}
</script>

<template>
  <section data-testid="security-test-requests-page">
    <div class="page-heading"><h1>上线测试申请</h1>
      <div v-if="isApplicant" class="button-row">
        <el-button type="primary" data-testid="create-security-test-request" @click="openCreate">新建测试申请</el-button>
      </div>
    </div>

    <div class="panel request-filter-panel">
      <el-select v-model="statusFilter" clearable placeholder="全部状态" style="width: 170px" @change="applyStatusFilter">
        <el-option v-for="(label, value) in statusLabels" :key="value" :label="label" :value="value" />
      </el-select>
      <el-input v-model="keywordFilter" clearable placeholder="搜索申请编号、名称、业务系统或组织" style="width: 280px" @keyup.enter="searchKeyword" @clear="searchKeyword" />
      <el-button @click="searchKeyword">搜索</el-button>
      <span class="request-filter-hint">范围仅允许本组织未归档公网资产，单次最多 200 项。</span>
      <span class="request-filter-count">当前页 {{ requests.length }} 条，共 {{ total }} 个申请</span>
    </div>

    <div class="panel data-table-panel" v-loading="loading">
      <el-table :data="requests" row-key="id" empty-text="暂无上线安全测试申请">

        <el-table-column label="测试申请" min-width="210">
          <template #default="{ row }"><button class="link-button" type="button" @click="openDetail(row)">{{ row.request_name }}</button><span class="list-secondary">{{ row.code }}</span></template>
        </el-table-column>

        <el-table-column label="系统 / 申请组织" min-width="170"><template #default="{ row }">{{ row.business_system_name }}<span class="list-secondary">{{ row.applicant_organization_name }}</span></template></el-table-column>
        <el-table-column label="类型" width="90"><template #default="{ row }">{{ typeLabels[row.test_type] ?? row.test_type }}</template></el-table-column>
        <el-table-column label="范围" width="70"><template #default="{ row }">{{ row.scope_asset_count }} 项</template></el-table-column>
        <el-table-column label="未闭环发现" width="100">
          <template #default="{ row }">
            <el-tag v-if="['testing', 'pending_launch'].includes(row.status)" :type="row.open_finding_count > 0 ? 'danger' : 'success'" effect="plain">{{ row.open_finding_count }}</el-tag>
            <span v-else>-</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="statusTypes[row.status] as any">{{ statusLabels[row.status] ?? row.status }}</el-tag></template></el-table-column>
        <el-table-column label="期望上线" width="105"><template #default="{ row }">{{ formatDate(row.expected_launch_at) }}</template></el-table-column>
        <el-table-column label="操作" width="88" fixed="right" align="right" header-align="right" class-name="table-action-column">
          <template #default="{ row }"><RowActions :label="`操作：${row.name || row.title || row.code || row.display_name || row.id}`">
            <el-button link type="primary" @click="openDetail(row)">详情</el-button>
            <el-button v-if="canEdit(row)" link type="primary" @click="openEdit(row)">编辑</el-button>
            <el-button v-if="canSubmit(row)" link type="success" :loading="actionLoading" data-testid="submit-security-test-request" @click="runSubmit(row)">提交</el-button>
            <el-button v-if="canWithdraw(row)" link type="warning" :loading="actionLoading" @click="runWithdraw(row)">撤回</el-button>
            <el-button v-if="canApprove(row)" link type="success" :loading="actionLoading" data-testid="approve-security-test-request" @click="openApprove(row)">通过</el-button>
            <el-button v-if="canApprove(row)" link type="danger" :loading="actionLoading" data-testid="reject-security-test-request" @click="runReject(row)">驳回</el-button>
            <el-button v-if="canApproveLaunch(row)" link type="success" :loading="actionLoading" data-testid="approve-security-test-launch" @click="runApproveLaunch(row)">批准上线</el-button>
            <el-button v-if="canCancel(row)" link type="danger" :loading="actionLoading" @click="runCancel(row)">取消</el-button>
          </RowActions></template>
        </el-table-column>
      </el-table>
      <div class="table-pagination">
        <el-pagination
          background
          layout="total, sizes, prev, pager, next"
          :total="total"
          :current-page="page"
          :page-size="pageSize"
          :page-sizes="[20, 50, 100]"
          @current-change="handlePageChange"
          @size-change="handlePageSizeChange"
        />
      </div>
    </div>

    <el-dialog v-model="dialogVisible" :title="editingID ? '编辑上线安全测试申请' : '新建上线安全测试申请'" width="960px" destroy-on-close>
      <el-form ref="formRef" :model="form" :rules="rules" label-width="120px">
        <div class="form-grid-two">
          <el-form-item label="申请名称" prop="request_name"><el-input v-model="form.request_name" placeholder="如：客户服务平台二期上线安全测试" /></el-form-item>
          <el-form-item label="测试类型" prop="test_type"><el-select v-model="form.test_type" style="width:100%"><el-option v-for="(label, value) in typeLabels" :key="value" :label="label" :value="value" /></el-select></el-form-item>
          <el-form-item label="期望上线时间" prop="expected_launch_at"><el-date-picker v-model="form.expected_launch_at" type="date" placeholder="选择期望上线日期" style="width:100%" /></el-form-item>
          <el-form-item label="联系人" prop="contact_user_id">
            <el-select v-model="form.contact_user_id" filterable placeholder="选择本组织联系人" style="width:100%">
              <el-option v-for="contact in contactCandidates" :key="contact.id" :value="contact.id" :label="contact.display_name || contact.username" />
            </el-select>
          </el-form-item>
        </div>
        <el-form-item label="测试窗口">
          <div class="date-range-inline">
            <el-date-picker v-model="form.test_window_start_at" type="datetime" placeholder="期望开始时间（可留空，以集团批复为准）" />
            <span>至</span>
            <el-date-picker v-model="form.test_window_end_at" type="datetime" placeholder="期望结束时间" />
          </div>
        </el-form-item>
        <el-form-item label="上线系统" required>
          <div class="system-select-row">
            <template v-if="selectedSystem">
              <el-tag :type="selectedSystem.kind === 'draft' ? 'warning' : 'success'" effect="plain">{{ selectedSystem.kind === 'draft' ? '待上报版本' : '已上报版本' }}</el-tag>
              <strong>{{ selectedSystem.name }}</strong>
              <el-button link type="primary" data-testid="change-request-system" @click="openSystemPicker">更换</el-button>
              <el-button link type="danger" @click="clearSystem">清除</el-button>
            </template>
            <el-button v-else type="primary" plain data-testid="select-request-system" @click="openSystemPicker">选择业务系统</el-button>
          </div>
        </el-form-item>
        <el-form-item label="申请背景" prop="background"><el-input v-model="form.background" type="textarea" :rows="3" placeholder="说明上线背景、系统用途、对外暴露情况和希望测试覆盖的重点" /></el-form-item>
        <el-form-item label="测试范围" required>
          <div class="scope-picker">
            <div class="scope-picker-actions">
              <el-button type="primary" plain data-testid="open-request-scope-picker" @click="openScopePicker">选择公网资产</el-button>
            </div>
            <el-table v-if="selectedScope.length" :data="selectedScope" border max-height="260" class="selected-scope-table" data-testid="selected-request-scope">
              <el-table-column label="资产" min-width="240"><template #default="{ row }"><strong>[{{ assetTypeLabels[row.asset_type] ?? row.asset_type }}] {{ row.asset_value }}</strong><div class="scope-row-secondary">{{ row.asset_name || '-' }}</div></template></el-table-column>
              <el-table-column prop="organization_name" label="责任组织" min-width="130" />
              <el-table-column label="业务系统 / 网络区域" min-width="170"><template #default="{ row }">{{ row.business_system_name || '-' }}<div class="scope-row-secondary">{{ row.network_area_name || '-' }}</div></template></el-table-column>
              <el-table-column label="上报动作" width="130">
                <template #default="{ row }">
                  <el-tag v-if="row.report_action" :type="row.report_action === 'none' ? 'info' : 'warning'" effect="plain">{{ reportActionLabels[row.report_action] ?? row.report_action }}</el-tag>
                  <el-tag v-else :type="row.kind === 'working' ? 'warning' : 'success'" effect="plain">{{ reportingStatusLabels[row.reporting_status ?? ''] ?? (row.kind === 'working' ? '待上报' : '已上报') }}</el-tag>
                </template>
              </el-table-column>
              <el-table-column label="操作" width="72" fixed="right" align="right" header-align="right" class-name="table-action-column"><template #default="{ row }"><el-button link type="danger" @click="removeScope(row.id)">移除</el-button></template></el-table-column>
            </el-table>
            <el-empty v-else description="尚未选择测试资产；可从本组织的已上报资产或待上报工作版本中选择公网资产" :image-size="54" class="scope-empty" />
            <div class="scope-summary">已选 <strong>{{ selectedScope.length }}</strong> / 200 项资产，覆盖 <strong>{{ selectedOrganizationCount }}</strong> 家组织。提交时将冻结快照并校验必填项；批准上线时待上报版本会自动上报集团。</div>
          </div>
        </el-form-item>
      </el-form>
      <template #footer><el-button @click="dialogVisible = false">取消</el-button><el-button type="primary" :loading="submitting" data-testid="save-security-test-request" @click="save">{{ editingID ? '保存修改' : '保存草稿' }}</el-button></template>
    </el-dialog>

    <el-dialog v-model="systemPickerVisible" title="选择上线业务系统" width="780px" append-to-body destroy-on-close>
      <div class="scope-search-bar">
        <el-input v-model="systemPickerKeyword" clearable placeholder="搜索系统名称或负责人，也可输入编码" @keyup.enter="searchSystemPicker" @clear="searchSystemPicker" />
        <el-button type="primary" @click="searchSystemPicker">搜索</el-button>
      </div>
      <el-table v-loading="systemPickerLoading" :data="systemPickerRows" border height="390" data-testid="request-system-picker">
        <el-table-column label="业务系统" min-width="260"><template #default="{ row }"><strong>{{ row.name }}</strong><div class="scope-row-secondary">{{ systemPickerHint(row) }}</div></template></el-table-column>
        <el-table-column prop="organization_name" label="归属组织" min-width="140" />
        <el-table-column label="版本" width="110">
          <template #default="{ row }"><el-tag :type="row.is_working_copy ? 'warning' : 'success'" effect="plain">{{ row.is_working_copy ? '待上报' : '已上报' }}</el-tag></template>
        </el-table-column>
        <el-table-column label="操作" width="90" fixed="right" align="right" header-align="right" class-name="table-action-column">
          <template #default="{ row }">
            <el-button link type="primary" :disabled="!systemPickerSelectable(row)" @click="selectSystem(row)">选择</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div class="scope-picker-pagination">
        <span>只能选择本组织的业务系统；已进入其他填报流程或审核中的版本不可选。</span>
        <el-pagination background small layout="total, sizes, prev, pager, next" :total="systemPickerTotal" :current-page="systemPickerPage" :page-size="systemPickerPageSize" :page-sizes="[20, 50, 100]" @current-change="handleSystemPickerPageChange" @size-change="handleSystemPickerPageSizeChange" />
      </div>
    </el-dialog>

    <el-dialog v-model="scopePickerVisible" title="选择公网资产" width="860px" append-to-body destroy-on-close>
      <el-alert type="info" :closable="false" show-icon class="scope-picker-alert">
        <template #title>仅列出本组织可选的公网资产；内网资产待集团-子单位网络打通后开放。</template>
      </el-alert>
      <div class="scope-search-bar">
        <el-select v-model="scopePickerType" clearable placeholder="全部类型" style="width: 130px" @change="searchScopePicker">
          <el-option v-for="(label, value) in assetTypeLabels" :key="value" :label="label" :value="value" />
        </el-select>
        <el-input v-model="scopePickerKeyword" clearable placeholder="搜索资产值、名称、业务系统或负责人" @keyup.enter="searchScopePicker" @clear="searchScopePicker" />
        <el-button type="primary" @click="searchScopePicker">搜索</el-button>
      </div>
      <el-table v-loading="scopePickerLoading" :data="scopePickerRows" border height="390" data-testid="request-scope-picker">
        <el-table-column label="公网资产" min-width="260"><template #default="{ row }"><strong>[{{ assetTypeLabels[row.asset_type] ?? row.asset_type }}] {{ row.asset_value }}</strong><div class="scope-row-secondary">{{ row.asset_name || '-' }}</div></template></el-table-column>
        <el-table-column prop="organization_name" label="责任组织" min-width="130" />
        <el-table-column label="业务系统 / 网络区域" min-width="170"><template #default="{ row }">{{ row.business_system_name || '-' }}<div class="scope-row-secondary">{{ row.network_area_name || '-' }}</div></template></el-table-column>
        <el-table-column label="版本" width="100">
          <template #default="{ row }">
            <el-tag :type="row.kind === 'working' ? 'warning' : 'success'" effect="plain">{{ row.kind === 'working' ? '待上报' : '已上报' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="84" fixed="right" align="right" header-align="right" class-name="table-action-column">
          <template #default="{ row }">
            <el-button v-if="!isScopeSelected(row.id)" link type="primary" @click="toggleScope(row)">加入</el-button>
            <el-button v-else link type="danger" @click="toggleScope(row)">移除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div class="scope-picker-pagination">
        <span>已选 {{ selectedScope.length }} / 200 项</span>
        <el-pagination background small layout="total, sizes, prev, pager, next" :total="scopePickerTotal" :current-page="scopePickerPage" :page-size="scopePickerPageSize" :page-sizes="[20, 50, 100]" @current-change="handleScopePickerPageChange" @size-change="handleScopePickerPageSizeChange" />
      </div>
    </el-dialog>

    <el-dialog v-model="approveVisible" title="审批通过安全测试申请" width="680px" destroy-on-close>
      <el-alert type="warning" :closable="false" show-icon class="scope-picker-alert">
        <template #title>通过后将生成授权状态的安全测试批次并冻结申请范围，请确认最终测试窗口。</template>
      </el-alert>
      <el-form ref="approveFormRef" :model="approveForm" :rules="approveRules" label-width="120px">
        <el-form-item label="最终测试窗口" required>
          <div class="date-range-inline">
            <el-date-picker v-model="approveForm.test_window_start_at" type="datetime" placeholder="开始时间" />
            <span>至</span>
            <el-date-picker v-model="approveForm.test_window_end_at" type="datetime" placeholder="结束时间" />
          </div>
        </el-form-item>
        <el-form-item label="授权说明" prop="authorization_note"><el-input v-model="approveForm.authorization_note" type="textarea" :rows="3" placeholder="说明授权人、授权依据、允许的测试方式与边界" /></el-form-item>
        <el-form-item label="审批意见" prop="comment"><el-input v-model="approveForm.comment" type="textarea" :rows="2" placeholder="补充审批意见（可选）" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="approveVisible = false">返回</el-button><el-button type="primary" :loading="approveSubmitting" data-testid="confirm-approve-security-test-request" @click="submitApprove">确认通过</el-button></template>
    </el-dialog>

    <el-dialog v-model="qualityDialogVisible" title="范围自检结果" width="640px" append-to-body destroy-on-close>
      <template v-if="qualityResult">
        <el-alert :type="qualityResult.passed ? 'success' : 'error'" :closable="false" show-icon class="scope-picker-alert">
          <template #title>
            {{ qualityResult.passed
              ? `范围共 ${qualityResult.asset_count} 项资产，必填项校验通过`
              : `存在 ${qualityResult.error_count} 项必填缺失，请补全后重新提交；另有 ${qualityResult.warning_count} 项提醒` }}
          </template>
        </el-alert>
        <el-empty v-if="!qualityResult.issues.length" description="没有发现问题" :image-size="60" />
        <el-table v-else :data="qualityResult.issues" border max-height="320">
          <el-table-column label="级别" width="90"><template #default="{ row }"><el-tag :type="row.severity === 'error' ? 'danger' : 'warning'" effect="plain">{{ row.severity === 'error' ? '必填缺失' : '提醒' }}</el-tag></template></el-table-column>
          <el-table-column prop="message" label="问题" min-width="240" />
          <el-table-column prop="count" label="数量" width="80" />
        </el-table>
      </template>
      <template #footer><el-button type="primary" @click="qualityDialogVisible = false">知道了</el-button></template>
    </el-dialog>

    <el-drawer v-model="detailVisible" title="上线安全测试申请详情" size="76%">
      <template v-if="currentRequest">
        <div class="detail-heading">
          <div><p class="eyebrow">{{ currentRequest.code }}</p><h2>{{ currentRequest.request_name }}</h2></div>
          <el-tag :type="statusTypes[currentRequest.status] as any" size="large">{{ statusLabels[currentRequest.status] ?? currentRequest.status }}</el-tag>
        </div>
        <el-descriptions :column="3" border>
          <el-descriptions-item label="申请组织">{{ currentRequest.applicant_organization_name }}</el-descriptions-item>
          <el-descriptions-item label="上线系统">{{ currentRequest.business_system_code ? `${currentRequest.business_system_code} · ${currentRequest.business_system_name}` : currentRequest.business_system_name || '-' }}</el-descriptions-item>
          <el-descriptions-item label="测试类型">{{ typeLabels[currentRequest.test_type] ?? currentRequest.test_type }}</el-descriptions-item>
          <el-descriptions-item label="期望上线">{{ formatDate(currentRequest.expected_launch_at) }}</el-descriptions-item>
          <el-descriptions-item label="期望测试窗口">{{ currentRequest.test_window_start_at ? `${formatTime(currentRequest.test_window_start_at)} 至 ${formatTime(currentRequest.test_window_end_at ?? '')}` : '未指定，以集团批复为准' }}</el-descriptions-item>
          <el-descriptions-item label="联系人">{{ currentRequest.contact_name || '-' }}</el-descriptions-item>
          <el-descriptions-item label="范围规模">{{ currentRequest.scope_asset_count }} 项资产<el-tag v-if="currentRequest.has_unreported_scope" type="warning" effect="plain" class="detail-inline-tag">含待上报版本</el-tag></el-descriptions-item>
          <el-descriptions-item label="创建信息">{{ currentRequest.created_by_name || '-' }}<br>{{ formatTime(currentRequest.created_at) }}</el-descriptions-item>
          <el-descriptions-item label="最近更新">{{ formatTime(currentRequest.updated_at) }}</el-descriptions-item>
          <el-descriptions-item label="申请背景" :span="3">{{ currentRequest.background }}</el-descriptions-item>
          <el-descriptions-item v-if="currentRequest.reviewed_by_name" label="审核结果">{{ currentRequest.reviewed_by_name }} · {{ formatTime(currentRequest.reviewed_at) }}<br>{{ currentRequest.review_comment || '-' }}</el-descriptions-item>
          <el-descriptions-item v-if="currentRequest.converted_batch_code" label="测试批次">
            {{ currentRequest.converted_batch_code }}（{{ currentRequest.converted_batch_status || '-' }}）
          </el-descriptions-item>
          <el-descriptions-item v-if="currentRequest.launch_decided_by_name" label="上线决定">{{ currentRequest.launch_decided_by_name }} · {{ formatTime(currentRequest.launch_decided_at) }}<br>{{ currentRequest.launch_comment || '-' }}</el-descriptions-item>
          <el-descriptions-item v-if="currentRequest.reported_task_id" label="上报任务">{{ currentRequest.reported_task_id }}</el-descriptions-item>
        </el-descriptions>

        <div class="section-heading">
          <div><h3>申请范围快照</h3><p>提交时冻结的公网资产范围；已上报资产沿用集团台账数据，待上报版本批准上线时自动上报。</p></div>
          <el-button v-if="canRunQualityCheck(currentRequest)" type="primary" plain :loading="qualityCheckLoading" data-testid="run-request-quality-check" @click="runQualityCheck">范围自检</el-button>
        </div>
        <el-table :data="currentRequest.scope_assets ?? []" border max-height="360">
          <el-table-column label="类型" width="90"><template #default="{ row }">{{ assetTypeLabels[row.asset_type] ?? row.asset_type }}</template></el-table-column>
          <el-table-column prop="asset_value" label="资产值" min-width="220" />
          <el-table-column prop="asset_name" label="资产名称" min-width="130" />
          <el-table-column prop="organization_name" label="责任组织" min-width="140" />
          <el-table-column prop="business_system_name" label="业务系统" min-width="140" />
          <el-table-column label="上报动作" width="120"><template #default="{ row }"><el-tag :type="row.report_action === 'none' ? 'info' : 'warning'" effect="plain">{{ reportActionLabels[row.report_action] ?? row.report_action }}</el-tag></template></el-table-column>
          <el-table-column label="快照时间" min-width="165"><template #default="{ row }">{{ formatTime(row.snapshot_at) }}</template></el-table-column>
        </el-table>

        <div class="section-heading"><div><h3>处理时间线（{{ eventTotal }}）</h3><p>记录提交、审核、测试与上线发布的每次状态流转，最新记录优先显示。</p></div></div>
        <div v-loading="eventListLoading">
          <el-timeline v-if="requestEvents.length">
            <el-timeline-item v-for="event in requestEvents" :key="event.id" :timestamp="formatTime(event.created_at)" placement="top">
              <strong>{{ eventLabels[event.action] ?? event.action }}</strong><span v-if="event.from_status || event.to_status">：{{ statusLabels[event.from_status ?? ''] ?? event.from_status }} → {{ statusLabels[event.to_status ?? ''] ?? event.to_status }}</span><span v-if="event.actor_name"> · {{ event.actor_name }}</span>
              <p v-if="event.comment">{{ event.comment }}</p>
            </el-timeline-item>
          </el-timeline>
          <el-empty v-else-if="!eventListLoading" description="暂无处理记录" :image-size="60" />
          <div v-if="eventTotal > 0" class="table-pagination detail-pagination">
            <el-pagination background small layout="total, sizes, prev, pager, next" :total="eventTotal" :current-page="eventPage" :page-size="eventPageSize" :page-sizes="[20, 50, 100]" @current-change="handleEventPageChange" @size-change="handleEventPageSizeChange" />
          </div>
        </div>
      </template>
    </el-drawer>
  </section>
</template>

<style scoped>
.request-filter-panel { display:flex; align-items:center; gap:14px; margin-bottom:16px; }
.request-filter-hint { color:var(--muted); font-size:13px; }
.request-filter-count { margin-left:auto; color:var(--muted); font-size:13px; white-space:nowrap; }
.table-pagination { display:flex; justify-content:flex-end; margin-top:18px; }
.detail-pagination { overflow-x:auto; padding-bottom:2px; }
.link-button { padding:0; border:0; background:transparent; color:var(--blue); cursor:pointer; text-align:left; font:inherit; }
.form-grid-two { display:grid; grid-template-columns:1fr 1fr; gap:0 18px; }
.date-range-inline { display:flex; align-items:center; gap:8px; width:100%; }
.date-range-inline :deep(.el-date-editor) { flex:1; min-width:0; }
.system-select-row { display:flex; align-items:center; gap:10px; flex-wrap:wrap; }
.scope-picker { width:100%; }
.scope-picker-actions { display:flex; flex-wrap:wrap; gap:10px; margin-bottom:12px; }
.scope-picker-actions :deep(.el-button + .el-button) { margin-left:0; }
.selected-scope-table { width:100%; }
.scope-row-secondary { margin-top:4px; color:var(--muted); font-size:12px; font-weight:400; }
.scope-empty { padding:18px 0; border:1px dashed var(--line); border-radius:10px; }
.scope-summary { margin-top:10px; color:var(--muted); font-size:13px; }
.scope-picker-alert { margin-bottom:14px; }
.scope-search-bar { display:flex; gap:10px; margin-bottom:14px; }
.scope-picker-pagination { display:flex; justify-content:space-between; align-items:center; gap:16px; margin-top:16px; color:var(--muted); font-size:13px; }
.detail-heading, .section-heading { display:flex; justify-content:space-between; align-items:flex-start; gap:20px; }
.detail-heading { margin-bottom:22px; }
.detail-heading h2, .section-heading h3 { margin:0; }
.section-heading { margin:28px 0 14px; }
.section-heading p { margin:6px 0 0; color:var(--muted); }
.detail-inline-tag { margin-left:8px; }
@media (max-width: 900px) { .request-filter-panel { align-items:flex-start; flex-wrap:wrap; } .request-filter-count { width:100%; margin-left:0; } .form-grid-two { grid-template-columns:1fr; } .date-range-inline { flex-direction:column; align-items:stretch; } .scope-picker-pagination { align-items:flex-start; flex-direction:column; } }
</style>
