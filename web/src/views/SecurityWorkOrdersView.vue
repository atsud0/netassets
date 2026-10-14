<script setup lang="ts">
import RowActions from '../components/RowActions.vue'
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import type { FormInstance, FormRules } from 'element-plus'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useRoute } from 'vue-router'
import { apiClient, getAPIErrorMessage } from '../api/client'
import { useAuthStore } from '../stores/auth'
import SavedFilterViews from '../components/SavedFilterViews.vue'
import {
  FINDING_STATUS_CONFIRMED,
  FINDING_STATUS_PENDING,
  FINDING_STATUS_RISK_ACCEPTED,
  findingStatusLabels,
  findingStatusTagType,
} from '../constants/findingStatus'

interface ListResponse<T> { items: T[]; total: number; page: number; page_size: number }
interface FindingSummaryStats {
  total: number; pending: number; critical: number; high: number; medium: number; low: number
  pending_asset_link: number; pending_acceptance: number; pending_organization_review: number
  pending_security_review: number; risk_accepted: number; risk_accepted_expired: number
}

// "待处理" is only the vulnerability lifecycle status. Every other waiting state
// is a separate dimension so nobody reads one number as the whole backlog.
const findingSummaryDimensions = [
  { key: 'pending', label: '待处理', hint: '仅统计漏洞生命周期状态为「待处理」的发现', tone: 'tone-pending' },
  { key: 'pending_asset_link', label: '待关联资产', hint: '尚未绑定正式资产的发现（含待关联漏洞候选）', tone: 'tone-pending' },
  { key: 'pending_acceptance', label: '待整改受理', hint: '已进入整改任务但尚未被受理', tone: 'tone-pending' },
  { key: 'pending_organization_review', label: '待组织复核', hint: '整改结果已提交，等待资产所属组织复核', tone: 'tone-pending' },
  { key: 'pending_security_review', label: '待安全复核', hint: '等待集团安全管理员复核整改结果', tone: 'tone-pending' },
  { key: 'risk_accepted', label: '风险接受中', hint: '已接受风险且尚未到期', tone: 'tone-total' },
  { key: 'risk_accepted_expired', label: '风险接受已到期', hint: '接受期限已到但状态尚未回收，需要重新处置', tone: 'tone-critical' },
] as const

function findingSummaryValue(key: keyof FindingSummaryStats) {
  return findingSummary[key] ?? 0
}
interface ItemResponse<T> { item: T }
interface Organization { id: string; code: string; name: string; type: string; status: string }
interface Asset { id: string; value: string; name?: string; organization_id: string; business_system_id?: string; network_scope: string; status: string }
interface BusinessSystem { id: string; code: string; name: string; organization_id: string; status: string }
interface FindingSummary {
  id: string; code: string; title: string; severity: string; status: string
  asset_id?: string; asset_value?: string; asset_name?: string; business_system_id?: string; business_system_name?: string
  asset_link_state?: string; pending_asset_draft_id?: string; asset_deleted?: boolean
}
interface Finding extends FindingSummary {
  assignee_organization_id: string; assignee_organization_code: string; assignee_organization_name: string
  business_system_code?: string; test_batch_id?: string; test_batch_code?: string; test_batch_name?: string
  vulnerability_type?: string; description: string; evidence?: string
  remediation_advice?: string; source: string; discovered_at: string; created_by_name?: string; created_at: string; updated_at: string
  risk_accepted_at?: string; risk_accepted_until?: string; risk_acceptance_reason?: string; risk_accepted_by_name?: string
  risk_accepted_from_status?: string; risk_reviewed_at?: string; risk_reviewed_by_name?: string; risk_review_comment?: string; risk_acceptance_expired?: boolean
}
interface SecurityTestScopeAsset { asset_id?: string; organization_id?: string; business_system_id?: string }
interface SecurityTestBatch {
  id: string; code: string; name: string; status: string; asset_count: number; scope_assets?: SecurityTestScopeAsset[]
}
interface FindingImportError { row: number; code: string; message: string }
interface FindingImportResult {
  id: string; test_batch_id: string; test_batch_code?: string; test_batch_name?: string; file_name: string; file_type: string
  status: string; total_count: number; success_count: number; failure_count: number; duplicate_count: number; pending_count: number; errors: FindingImportError[]
  created_by_name?: string; created_at?: string
}
interface FindingImportCandidate {
  id: string; finding_import_id: string; test_batch_id?: string; test_batch_code?: string; test_batch_name?: string
  source_row_number: number; asset_type: string; asset_value: string; normalized_value?: string
  title: string; severity: string; vulnerability_type?: string; description: string; evidence?: string; remediation_advice?: string
  discovered_at: string; status: string; suggested_scope_asset_id?: string; suggested_asset_id?: string; suggested_asset_value?: string
  suggested_organization_name?: string; matched_scope_asset_id?: string; matched_asset_id?: string; matched_asset_value?: string
  matched_organization_name?: string; match_method?: string; match_confidence?: number; review_note?: string
  reviewed_by_name?: string; reviewed_at?: string; imported_finding_id?: string; imported_finding_code?: string
  created_at: string; updated_at: string
}
interface FindingImportScopeOption {
  id: string; asset_id: string; asset_type: string; asset_value: string; asset_name?: string
  organization_id: string; organization_code: string; organization_name: string
  business_system_id?: string; business_system_code?: string; business_system_name?: string; suggested: boolean
}
interface WorkOrderEvent {
  id: number; action: string; from_status?: string; to_status?: string; comment?: string
  actor_name?: string; actor_organization_name?: string; created_at: string
}
interface WorkOrderAttachment {
  id: string; category: string; file_name: string; content_type: string; size_bytes: number
  description?: string; uploaded_by?: string; uploaded_by_name?: string; uploader_organization_name?: string; created_at: string
}
interface WorkOrder {
  execution_mode: string
  remediation_counts: Record<string, number>
  id: string; number: string; title: string; assignee_organization_id: string
  assignee_organization_code: string; assignee_organization_name: string; severity: string; status: string
  description?: string; remediation_requirement: string; due_at: string; overdue: boolean
  issued_at?: string; accepted_at?: string; submitted_at?: string; verified_at?: string; closed_at?: string; cancelled_at?: string
  remediation_progress?: string; remediation_result?: string; verification_result?: string; return_reason?: string
  created_by_name?: string; finding_count: number; findings: FindingSummary[]; events?: WorkOrderEvent[]; attachments?: WorkOrderAttachment[]
  created_at: string; updated_at: string
}

type ActionMode = 'issue' | 'accept' | 'progress' | 'submit' | 'return' | 'close' | 'cancel'

const auth = useAuthStore()
const route = useRoute()
const activeTab = ref<'orders' | 'findings' | 'candidates'>('orders')
const loading = ref(false)
const submitting = ref(false)
const organizations = ref<Organization[]>([])
const assets = ref<Asset[]>([])
const systems = ref<BusinessSystem[]>([])
const findings = ref<Finding[]>([])
const findingSummary = reactive<FindingSummaryStats>({
  total: 0, pending: 0, critical: 0, high: 0, medium: 0, low: 0,
  pending_asset_link: 0, pending_acceptance: 0, pending_organization_review: 0,
  pending_security_review: 0, risk_accepted: 0, risk_accepted_expired: 0,
})
const findingOptions = ref<Finding[]>([])
const workOrders = ref<WorkOrder[]>([])
const orderPage = ref(1)
const findingPage = ref(1)
const pageSize = ref(20)
const orderTotal = ref(0)
const findingTotal = ref(0)
const testBatches = ref<SecurityTestBatch[]>([])
const findingBatchScopeAssetIDs = ref<string[]>([])
const organizationFilter = ref('')
const statusFilter = ref('')
const assetLinkStateFilter = ref('')
const overdueOnly = ref(false)
const savedFilterViews = ref<InstanceType<typeof SavedFilterViews>>()

const findingDialogVisible = ref(false)
const findingFormRef = ref<FormInstance>()
const editingFindingID = ref('')
// The row version this form was opened with. Sending it back lets the API reject
// a save that would overwrite a newer edit by another security operator.
const editingFindingUpdatedAt = ref('')
const findingForm = reactive({
  title: '', severity: 'high', status: FINDING_STATUS_PENDING, assignee_organization_id: '', asset_id: '', business_system_id: '', test_batch_id: '',
  vulnerability_type: '', description: '', evidence: '', remediation_advice: '', source: 'manual', discovered_at: new Date() as Date | string,
})


const importDialogVisible = ref(false)
const importSubmitting = ref(false)
const importFileInput = ref<HTMLInputElement>()
const importFile = ref<File>()
const importTestBatchID = ref('')
const importResult = ref<FindingImportResult>()
const findingImports = ref<FindingImportResult[]>([])
const findingImportCandidates = ref<FindingImportCandidate[]>([])
const candidatePage = ref(1)
const candidateTotal = ref(0)
const candidateStatusFilter = ref('pending')
const candidateBatchFilter = ref('')
const candidateAssetTypeFilter = ref('')
const candidateKeyword = ref('')
const candidateDialogVisible = ref(false)
const candidateSubmitting = ref(false)
const selectedCandidate = ref<FindingImportCandidate>()
const candidateScopeOptions = ref<FindingImportScopeOption[]>([])
const selectedCandidateScopeAssetID = ref('')
const candidateReviewNote = ref('')

const workOrderDialogVisible = ref(false)
const workOrderFormRef = ref<FormInstance>()
const workOrderForm = reactive({
  execution_mode: 'organization',
  title: '', assignee_organization_id: '', description: '', remediation_requirement: '', due_at: defaultDueAt() as Date | string,
  finding_ids: [] as string[], issue_immediately: true,
})

const detailVisible = ref(false)
const detailLoading = ref(false)
const selectedOrder = ref<WorkOrder>()
const orderDetailFindings = ref<FindingSummary[]>([])
const orderFindingLoading = ref(false)
const orderFindingPage = ref(1)
const orderFindingPageSize = ref(20)
const orderFindingTotal = ref(0)
const orderEvents = ref<WorkOrderEvent[]>([])
const orderEventLoading = ref(false)
const orderEventPage = ref(1)
const orderEventPageSize = ref(20)
const orderEventTotal = ref(0)
const orderAttachments = ref<WorkOrderAttachment[]>([])
const orderAttachmentLoading = ref(false)
const orderAttachmentPage = ref(1)
const orderAttachmentPageSize = ref(20)
const orderAttachmentTotal = ref(0)
const findingDetailVisible = ref(false)
const findingDetailLoading = ref(false)
const selectedFinding = ref<Finding>()
const riskDialogVisible = ref(false)
const riskSubmitting = ref(false)
const riskMode = ref<'accept' | 'review'>('accept')
const riskForm = reactive({ reason: '', comment: '', accepted_until: defaultRiskUntil() as Date | string })
const attachmentUploading = ref(false)
const selectedAttachmentFile = ref<File>()
const attachmentFileInput = ref<HTMLInputElement>()
const attachmentForm = reactive({ category: 'remediation', description: '' })

const actionDialogVisible = ref(false)
const actionMode = ref<ActionMode>('accept')
const actionOrder = ref<WorkOrder>()
const actionForm = reactive({ comment: '', progress: '', remediation_result: '', verification_result: '', return_reason: '' })

const severityLabels: Record<string, string> = { critical: '严重', high: '高危', medium: '中危', low: '低危', info: '提示' }
const riskActionLabels: Record<string, string> = { accept: '接受风险', review: '复核风险' }
const orderStatusLabels: Record<string, string> = {
  draft: '草稿', issued: '已下发', in_progress: '整改中', pending_verification: '待复测', returned: '已退回', closed: '已关闭', cancelled: '已取消',
}
const eventLabels: Record<string, string> = {
  create: '创建工单', issue: '集团下发', accept: '子公司接收', progress: '更新整改进度', submit: '提交复测', return: '复测退回', close: '复测通过并关闭', cancel: '取消工单', attachment_upload: '上传工单附件', attachment_delete: '删除工单附件',
}
const attachmentCategoryLabels: Record<string, string> = { remediation: '整改证明', verification: '复测材料', reference: '参考资料' }
const candidateStatusLabels: Record<string, string> = { pending: '待关联', imported: '已导入', ignored: '已忽略', invalid: '数据无效', duplicate: '重复漏洞' }
const assetTypeLabels: Record<string, string> = { domain: '域名', ip: 'IP 地址', url: 'URL', service: '服务', other: '其他' }
const matchMethodLabels: Record<string, string> = { normalized_exact: '标准化精确匹配', endpoint_host: 'IP 与端口主机匹配', cidr_contains: '网段包含', same_host: '相同主机', cross_type_host: '跨类型主机匹配', wildcard_domain: '泛域名匹配' }

const currentWorkOrderFilters = computed<Record<string, string>>(() => ({
  ...(organizationFilter.value ? { organization_id: organizationFilter.value } : {}),
  ...(statusFilter.value ? { status: statusFilter.value } : {}),
  ...(overdueOnly.value ? { overdue: 'true' } : {}),
}))

function setWorkOrderFilters(filters: Record<string, string>) {
  organizationFilter.value = filters.organization_id ?? ''
  statusFilter.value = filters.status ?? ''
  overdueOnly.value = filters.overdue === 'true'
}

function applySavedWorkOrderFilters(filters: Record<string, string>) {
  setWorkOrderFilters(filters)
  orderPage.value = 1
  void loadActiveList()
}

const enabledOrganizations = computed(() => organizations.value.filter(item => item.status === 'enabled' && item.type !== 'group'))
const scopedAssets = computed(() => assets.value.filter(item => item.organization_id === findingForm.assignee_organization_id && item.status !== 'archived' && (!findingForm.test_batch_id || findingBatchScopeAssetIDs.value.includes(item.id))))
const scopedSystems = computed(() => systems.value.filter(item => item.organization_id === findingForm.assignee_organization_id && item.status !== 'archived'))
const usableTestBatches = computed(() => testBatches.value.filter(item => ['authorized', 'in_progress', 'completed'].includes(item.status)))
const orderFindings = computed(() => findingOptions.value.filter(item => item.assignee_organization_id === workOrderForm.assignee_organization_id && [FINDING_STATUS_PENDING, FINDING_STATUS_CONFIRMED].includes(item.status)))
const currentPage = computed(() => activeTab.value === 'orders' ? orderPage.value : activeTab.value === 'findings' ? findingPage.value : candidatePage.value)
const currentTotal = computed(() => activeTab.value === 'orders' ? orderTotal.value : activeTab.value === 'findings' ? findingTotal.value : candidateTotal.value)
const activeOrderCount = computed(() => workOrders.value.filter(item => !['closed', 'cancelled'].includes(item.status)).length)
const overdueCount = computed(() => workOrders.value.filter(item => item.overdue).length)
const pendingVerificationCount = computed(() => workOrders.value.filter(item => item.status === 'pending_verification').length)

const findingRules: FormRules = {
  title: [{ required: true, message: '请输入漏洞标题', trigger: 'blur' }],
  severity: [{ required: true, message: '请选择严重等级', trigger: 'change' }],
  assignee_organization_id: [{ required: true, message: '请选择责任组织', trigger: 'change' }],
  description: [{ required: true, message: '请输入漏洞描述', trigger: 'blur' }],
  discovered_at: [{ required: true, message: '请选择发现时间', trigger: 'change' }],
}
const workOrderRules: FormRules = {
  title: [{ required: true, message: '请输入工单标题', trigger: 'blur' }],
  assignee_organization_id: [{ required: true, message: '请选择责任组织', trigger: 'change' }],
  finding_ids: [{ required: true, type: 'array', min: 1, message: '请选择至少一项安全发现', trigger: 'change' }],
  remediation_requirement: [{ required: true, message: '请输入整改要求', trigger: 'blur' }],
  due_at: [{ required: true, message: '请选择整改期限', trigger: 'change' }],
}

watch(() => findingForm.assignee_organization_id, () => {
  if (!scopedAssets.value.some(item => item.id === findingForm.asset_id)) findingForm.asset_id = ''
  if (!scopedSystems.value.some(item => item.id === findingForm.business_system_id)) findingForm.business_system_id = ''
})
watch(() => workOrderForm.assignee_organization_id, () => { workOrderForm.finding_ids = [] })
watch(() => findingForm.test_batch_id, async (batchID) => {
  findingBatchScopeAssetIDs.value = []
  if (!batchID) return
  try {
    const response = await apiClient.get<ItemResponse<SecurityTestBatch>>(`/security-test-batches/${batchID}`)
    findingBatchScopeAssetIDs.value = (response.data.item.scope_assets ?? []).map(item => item.asset_id).filter((id): id is string => Boolean(id))
    if (findingForm.asset_id && !findingBatchScopeAssetIDs.value.includes(findingForm.asset_id)) findingForm.asset_id = ''
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取测试批次授权范围失败'))
    findingForm.test_batch_id = ''
  }
})
watch(() => findingForm.asset_id, (assetID) => {
  if (!findingForm.test_batch_id || !assetID) return
  const asset = assets.value.find(item => item.id === assetID)
  if (asset) {
    findingForm.assignee_organization_id = asset.organization_id
    findingForm.business_system_id = asset.business_system_id ?? ''
  }
})


function applyRouteFilters() {
  const tab = typeof route.query.tab === 'string' ? route.query.tab : ''
  if (tab === 'findings' || tab === 'orders' || (tab === 'candidates' && auth.canManageSecurityFindings)) activeTab.value = tab
  organizationFilter.value = typeof route.query.organization_id === 'string' ? route.query.organization_id : ''
  statusFilter.value = typeof route.query.status === 'string' ? route.query.status : ''
  assetLinkStateFilter.value = typeof route.query.asset_link_state === 'string' ? route.query.asset_link_state : ''
  overdueOnly.value = route.query.overdue === '1'
}

onMounted(async () => {
  applyRouteFilters()
  await nextTick()
  const hasRouteContext = ['organization_id', 'status', 'overdue', 'work_order_id', 'finding_id', 'asset_link_state'].some(key => route.query[key] !== undefined)
  if (activeTab.value === 'orders' && !hasRouteContext) {
    const defaultFilters = await savedFilterViews.value?.initialize()
    if (defaultFilters) setWorkOrderFilters(defaultFilters)
  } else {
    await savedFilterViews.value?.loadViews()
  }
  await Promise.all([loadSupportingData(), loadFindingOptions()])
  await loadActiveList()
  const workOrderID = typeof route.query.work_order_id === 'string' ? route.query.work_order_id : ''
  const findingID = typeof route.query.finding_id === 'string' ? route.query.finding_id : ''
  if (workOrderID) await openDetail({ id: workOrderID } as WorkOrder)
  else if (findingID) {
    activeTab.value = 'findings'
    await loadActiveList()
    await openFindingDetail({ id: findingID } as Finding)
  }
})

async function loadSupportingData() {
  try {
    const listParams = { page: 1, page_size: 100 }
    const emptyResponse = { data: { items: [], total: 0, page: 1, page_size: 20 } }
    const referencePrefix = auth.hasGroupSecurityRole ? '/security-reference' : ''
    const [orgResponse, assetResponse, systemResponse, batchResponse, importResponse] = await Promise.all([
      apiClient.get<ListResponse<Organization>>(`${referencePrefix}/organizations`, { params: listParams }),
      apiClient.get<ListResponse<Asset>>(`${referencePrefix}/assets`, { params: listParams }),
      apiClient.get<ListResponse<BusinessSystem>>(`${referencePrefix}/business-systems`, { params: listParams }),
      auth.canManageSecurityTests ? apiClient.get<ListResponse<SecurityTestBatch>>('/security-test-batches', { params: listParams }) : Promise.resolve(emptyResponse),
      auth.canManageSecurityFindings ? apiClient.get<ListResponse<FindingImportResult>>('/security-findings/imports', { params: listParams }) : Promise.resolve(emptyResponse),
    ])
    organizations.value = orgResponse.data.items
    assets.value = assetResponse.data.items
    systems.value = systemResponse.data.items
    testBatches.value = batchResponse.data.items
    findingImports.value = importResponse.data.items
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取安全工单基础数据失败'))
  }
}

async function loadFindingOptions() {
  if (!auth.canManageSecurityWorkOrders) {
    findingOptions.value = []
    return
  }
  try {
    const response = await apiClient.get<ListResponse<Finding>>('/security-findings', {
      params: { page: 1, page_size: 100 },
    })
    findingOptions.value = response.data.items
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取待派单安全发现失败'))
  }
}

async function loadFindingSummary() {
  if (!auth.canManageSecurityFindings && !auth.canManageSecurityWorkOrders) return
  try {
    const params: Record<string, string> = {}
    if (organizationFilter.value) params.organization_id = organizationFilter.value
    const response = await apiClient.get<{ summary: FindingSummaryStats }>('/security-findings/summary', { params })
    Object.assign(findingSummary, response.data.summary)
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取漏洞与安全发现汇总失败'))
  }
}

async function loadActiveList() {
  loading.value = true
  try {
    if (activeTab.value === 'candidates') {
      const params: Record<string, string | number> = { page: candidatePage.value, page_size: pageSize.value }
      if (candidateStatusFilter.value) params.status = candidateStatusFilter.value
      if (candidateBatchFilter.value) params.test_batch_id = candidateBatchFilter.value
      if (candidateAssetTypeFilter.value) params.asset_type = candidateAssetTypeFilter.value
      if (candidateKeyword.value.trim()) params.keyword = candidateKeyword.value.trim()
      const response = await apiClient.get<ListResponse<FindingImportCandidate>>('/security-findings/import-candidates', { params })
      findingImportCandidates.value = response.data.items
      candidateTotal.value = response.data.total
      return
    }
    const isOrders = activeTab.value === 'orders'
    const params: Record<string, string | number | boolean> = {
      page: isOrders ? orderPage.value : findingPage.value,
      page_size: pageSize.value,
    }
    if (organizationFilter.value) params.organization_id = organizationFilter.value
    if (statusFilter.value) params.status = statusFilter.value
    if (!isOrders && assetLinkStateFilter.value) params.asset_link_state = assetLinkStateFilter.value
    if (isOrders && overdueOnly.value) params.overdue = true
    const endpoint = isOrders ? '/security-work-orders' : '/security-findings'
    const response = await apiClient.get<ListResponse<WorkOrder | Finding>>(endpoint, { params })
    if (isOrders) {
      workOrders.value = response.data.items as WorkOrder[]
      orderTotal.value = response.data.total
    } else {
      findings.value = response.data.items as Finding[]
      findingTotal.value = response.data.total
      await loadFindingSummary()
    }
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取安全运营列表失败'))
  } finally {
    loading.value = false
  }
}

async function refreshAfterMutation() {
  await Promise.all([loadSupportingData(), loadFindingOptions()])
  await loadActiveList()
  const items = activeTab.value === 'orders' ? workOrders.value : activeTab.value === 'findings' ? findings.value : findingImportCandidates.value
  const total = activeTab.value === 'orders' ? orderTotal.value : activeTab.value === 'findings' ? findingTotal.value : candidateTotal.value
  const page = activeTab.value === 'orders' ? orderPage : activeTab.value === 'findings' ? findingPage : candidatePage
  if (items.length === 0 && total > 0 && page.value > 1) {
    page.value -= 1
    await loadActiveList()
  }
}

function applyFilter() {
  if (activeTab.value === 'orders') orderPage.value = 1
  else if (activeTab.value === 'findings') findingPage.value = 1
  else candidatePage.value = 1
  void loadActiveList()
}

function resetFilters() {
  if (activeTab.value === 'candidates') {
    candidateStatusFilter.value = 'pending'
    candidateBatchFilter.value = ''
    candidateAssetTypeFilter.value = ''
    candidateKeyword.value = ''
  } else {
    organizationFilter.value = ''
    statusFilter.value = ''
    assetLinkStateFilter.value = ''
    overdueOnly.value = false
  }
  applyFilter()
}

function handlePageChange(value: number) {
  if (activeTab.value === 'orders') orderPage.value = value
  else if (activeTab.value === 'findings') findingPage.value = value
  else candidatePage.value = value
  void loadActiveList()
}

function handlePageSizeChange(value: number) {
  pageSize.value = value
  if (activeTab.value === 'orders') orderPage.value = 1
  else if (activeTab.value === 'findings') findingPage.value = 1
  else candidatePage.value = 1
  void loadActiveList()
}

function defaultOrganizationID() {
  return auth.hasGroupSecurityRole ? (enabledOrganizations.value[0]?.id ?? '') : (auth.user?.organization?.id ?? '')
}

function defaultDueAt() {
  const value = new Date()
  value.setDate(value.getDate() + 14)
  value.setHours(18, 0, 0, 0)
  return value
}

function defaultRiskUntil() {
  const value = new Date()
  value.setDate(value.getDate() + 30)
  value.setHours(18, 0, 0, 0)
  return value
}

function resetFindingForm() {
  editingFindingUpdatedAt.value = ''
  Object.assign(findingForm, {
    title: '', severity: 'high', status: FINDING_STATUS_PENDING, assignee_organization_id: defaultOrganizationID(), asset_id: '', business_system_id: '', test_batch_id: '',
    vulnerability_type: '', description: '', evidence: '', remediation_advice: '', source: 'manual', discovered_at: new Date(),
  })
}

function openCreateFinding() {
  editingFindingID.value = ''
  resetFindingForm()
  findingDialogVisible.value = true
}

function openEditFinding(item: Finding) {
  editingFindingID.value = item.id
  editingFindingUpdatedAt.value = item.updated_at
  Object.assign(findingForm, {
    title: item.title, severity: item.severity, status: item.status, assignee_organization_id: item.assignee_organization_id,
    asset_id: item.asset_id ?? '', business_system_id: item.business_system_id ?? '', test_batch_id: item.test_batch_id ?? '', vulnerability_type: item.vulnerability_type ?? '',
    description: item.description, evidence: item.evidence ?? '', remediation_advice: item.remediation_advice ?? '', source: item.source,
    discovered_at: new Date(item.discovered_at),
  })
  findingDialogVisible.value = true
}

async function saveFinding() {
  if (!findingFormRef.value || !(await findingFormRef.value.validate())) return
  if (!findingForm.asset_id && !findingForm.business_system_id) {
    ElMessage.warning('请至少关联一项网络资产或业务系统')
    return
  }
  submitting.value = true
  try {
    const payload: Record<string, unknown> = { ...findingForm, discovered_at: new Date(findingForm.discovered_at).toISOString() }
    if (editingFindingID.value) {
      payload.expected_updated_at = editingFindingUpdatedAt.value
      await apiClient.patch(`/security-findings/${editingFindingID.value}`, payload)
    }
    else await apiClient.post('/security-findings', payload)
    ElMessage.success(editingFindingID.value ? '安全发现已更新' : '安全发现已登记')
    findingDialogVisible.value = false
    await refreshAfterMutation()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, editingFindingID.value ? '更新安全发现失败' : '登记安全发现失败'))
  } finally {
    submitting.value = false
  }
}

function openFindingImport() {
  importTestBatchID.value = ''
  importFile.value = undefined
  importResult.value = undefined
  if (importFileInput.value) importFileInput.value.value = ''
  importDialogVisible.value = true
}

function selectFindingImportFile(event: Event) {
  const input = event.target as HTMLInputElement
  importFile.value = input.files?.[0]
}

async function downloadFindingTemplate(format: 'csv' | 'xlsx') {
  try {
    const response = await apiClient.get(`/security-findings/import-template?format=${format}`, { responseType: 'blob' })
    const url = URL.createObjectURL(response.data)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = `漏洞导入模板.${format}`
    anchor.click()
    URL.revokeObjectURL(url)
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '下载漏洞导入模板失败'))
  }
}

async function submitFindingImport() {
  if (!importFile.value) {
    ElMessage.warning('请选择 CSV、XLSX 或 JSON 文件')
    return
  }
  const formData = new FormData()
  if (importTestBatchID.value) formData.append('test_batch_id', importTestBatchID.value)
  formData.append('file', importFile.value)
  importSubmitting.value = true
  try {
    const response = await apiClient.post<ItemResponse<FindingImportResult>>('/security-findings/import', formData, { headers: { 'Content-Type': 'multipart/form-data' }, timeout: 120_000 })
    importResult.value = response.data.item
    ElMessage.success(`导入完成：成功 ${response.data.item.success_count} 条，待关联 ${response.data.item.pending_count} 条，失败 ${response.data.item.failure_count} 条，重复 ${response.data.item.duplicate_count} 条`)
    await refreshAfterMutation()
    importDialogVisible.value = true
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '批量导入安全发现失败'))
  } finally {
    importSubmitting.value = false
  }
}

function resetWorkOrderForm() {
  workOrderForm.execution_mode = 'organization'
  Object.assign(workOrderForm, {
    title: '', assignee_organization_id: defaultOrganizationID(), description: '', remediation_requirement: '', due_at: defaultDueAt(), finding_ids: [], issue_immediately: true,
  })
}

async function openFindingDetail(item: Finding) {
  findingDetailVisible.value = true
  findingDetailLoading.value = true
  try {
    const response = await apiClient.get<ItemResponse<Finding>>(`/security-findings/${item.id}`)
    selectedFinding.value = response.data.item
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取安全发现详情失败'))
  } finally {
    findingDetailLoading.value = false
  }
}

function openRiskDialog(item: Finding, mode: 'accept' | 'review') {
  selectedFinding.value = item
  riskMode.value = mode
  Object.assign(riskForm, {
    reason: mode === 'accept' ? '' : (item.risk_acceptance_reason ?? ''),
    comment: mode === 'review' ? (item.risk_review_comment ?? '') : '',
    accepted_until: item.risk_accepted_until ? new Date(item.risk_accepted_until) : defaultRiskUntil(),
  })
  riskDialogVisible.value = true
}

async function submitRiskAction() {
  const item = selectedFinding.value
  if (!item) return
  if (riskMode.value === 'accept' && !riskForm.reason.trim()) return ElMessage.warning('请填写风险接受原因')
  if (riskMode.value === 'review' && !riskForm.comment.trim()) return ElMessage.warning('请填写复核意见')
  const acceptedUntil = new Date(riskForm.accepted_until)
  if (Number.isNaN(acceptedUntil.getTime()) || acceptedUntil <= new Date()) return ElMessage.warning('风险有效期必须晚于当前时间')
  riskSubmitting.value = true
  try {
    const endpoint = riskMode.value === 'accept' ? 'accept-risk' : 'review-risk'
    // Carry the row version the dialog was opened from so a concurrent edit is
    // reported instead of silently overwritten.
    const payload = riskMode.value === 'accept'
      ? { reason: riskForm.reason, accepted_until: acceptedUntil.toISOString(), expected_updated_at: item.updated_at }
      : { comment: riskForm.comment, accepted_until: acceptedUntil.toISOString(), expected_updated_at: item.updated_at }
    const response = await apiClient.post<ItemResponse<Finding>>(`/security-findings/${item.id}/${endpoint}`, payload)
    selectedFinding.value = response.data.item
    riskDialogVisible.value = false
    ElMessage.success(riskMode.value === 'accept' ? '风险接受已保存' : '风险复核已完成')
    await refreshAfterMutation()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, riskMode.value === 'accept' ? '保存风险接受失败' : '完成风险复核失败'))
  } finally {
    riskSubmitting.value = false
  }
}

async function revokeFindingRisk(item: Finding) {
  try {
    await ElMessageBox.confirm(`确定撤销“${item.code}”的风险接受吗？撤销后将恢复为待处理状态。`, '撤销风险接受', { type: 'warning', confirmButtonText: '撤销', cancelButtonText: '取消' })
    const response = await apiClient.post<ItemResponse<Finding>>(`/security-findings/${item.id}/revoke-risk`, { expected_updated_at: item.updated_at })
    selectedFinding.value = response.data.item
    ElMessage.success('风险接受已撤销')
    await refreshAfterMutation()
  } catch (error) {
    if (error === 'cancel' || error === 'close') return
    ElMessage.error(getAPIErrorMessage(error, '撤销风险接受失败'))
  }
}

function openCreateWorkOrder(finding?: Finding) {
  resetWorkOrderForm()
  if (finding) {
    workOrderForm.assignee_organization_id = finding.assignee_organization_id
    workOrderForm.finding_ids = [finding.id]
    workOrderForm.title = `${severityLabels[finding.severity]}漏洞整改：${finding.title}`
    workOrderForm.remediation_requirement = finding.remediation_advice || '请根据漏洞描述完成修复，开展自查并提交整改结果和验证说明。'
  }
  workOrderDialogVisible.value = true
}

async function saveWorkOrder() {
  if (!workOrderFormRef.value || !(await workOrderFormRef.value.validate())) return
  submitting.value = true
  try {
    const response = await apiClient.post<{ item: WorkOrder }>('/security-work-orders', {
      execution_mode: workOrderForm.execution_mode, title: workOrderForm.title, assignee_organization_id: workOrderForm.assignee_organization_id,
      description: workOrderForm.description, remediation_requirement: workOrderForm.remediation_requirement,
      due_at: new Date(workOrderForm.due_at).toISOString(), finding_ids: workOrderForm.finding_ids,
    })
    if (workOrderForm.issue_immediately) {
      await apiClient.post(`/security-work-orders/${response.data.item.id}/issue`, { comment: '创建后立即下发' })
      ElMessage.success('安全工单已创建并下发')
    } else {
      ElMessage.success('安全工单草稿已创建')
    }
    workOrderDialogVisible.value = false
    activeTab.value = 'orders'
    await refreshAfterMutation()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '创建安全工单失败'))
  } finally {
    submitting.value = false
  }
}

async function loadOrderFindings() {
  if (!selectedOrder.value) return
  orderFindingLoading.value = true
  try {
    const response = await apiClient.get<ListResponse<FindingSummary>>(`/security-work-orders/${selectedOrder.value.id}/findings`, {
      params: { page: orderFindingPage.value, page_size: orderFindingPageSize.value },
    })
    orderDetailFindings.value = response.data.items
    orderFindingTotal.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取工单关联安全发现失败'))
  } finally {
    orderFindingLoading.value = false
  }
}
async function loadOrderEvents() {
  if (!selectedOrder.value) return
  orderEventLoading.value = true
  try {
    const response = await apiClient.get<ListResponse<WorkOrderEvent>>(`/security-work-orders/${selectedOrder.value.id}/events`, {
      params: { page: orderEventPage.value, page_size: orderEventPageSize.value },
    })
    orderEvents.value = response.data.items
    orderEventTotal.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取工单处理记录失败'))
  } finally {
    orderEventLoading.value = false
  }
}
async function loadOrderAttachments() {
  if (!selectedOrder.value) return
  orderAttachmentLoading.value = true
  try {
    const response = await apiClient.get<ListResponse<WorkOrderAttachment>>(`/security-work-orders/${selectedOrder.value.id}/attachments`, {
      params: { page: orderAttachmentPage.value, page_size: orderAttachmentPageSize.value },
    })
    orderAttachments.value = response.data.items
    orderAttachmentTotal.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取工单附件失败'))
  } finally {
    orderAttachmentLoading.value = false
  }
}
async function openDetail(item: WorkOrder) {
  detailVisible.value = true
  detailLoading.value = true
  selectedOrder.value = undefined
  orderDetailFindings.value = []
  orderEvents.value = []
  orderAttachments.value = []
  orderFindingPage.value = 1
  orderEventPage.value = 1
  orderAttachmentPage.value = 1
  orderFindingTotal.value = 0
  orderEventTotal.value = 0
  orderAttachmentTotal.value = 0
  try {
    const response = await apiClient.get<{ item: WorkOrder }>(`/security-work-orders/${item.id}`)
    selectedOrder.value = response.data.item
    await Promise.all([loadOrderFindings(), loadOrderEvents(), loadOrderAttachments()])
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取工单详情失败'))
  } finally {
    detailLoading.value = false
  }
}
function handleOrderFindingPageChange(value: number) { orderFindingPage.value = value; void loadOrderFindings() }
function handleOrderFindingPageSizeChange(value: number) { orderFindingPageSize.value = value; orderFindingPage.value = 1; void loadOrderFindings() }
function handleOrderEventPageChange(value: number) { orderEventPage.value = value; void loadOrderEvents() }
function handleOrderEventPageSizeChange(value: number) { orderEventPageSize.value = value; orderEventPage.value = 1; void loadOrderEvents() }
function handleOrderAttachmentPageChange(value: number) { orderAttachmentPage.value = value; void loadOrderAttachments() }
function handleOrderAttachmentPageSizeChange(value: number) { orderAttachmentPageSize.value = value; orderAttachmentPage.value = 1; void loadOrderAttachments() }
async function refreshOrderAttachmentsAfterMutation() {
  await loadOrderAttachments()
  if (orderAttachments.value.length === 0 && orderAttachmentTotal.value > 0 && orderAttachmentPage.value > 1) {
    orderAttachmentPage.value -= 1
    await loadOrderAttachments()
  }
}

function openAction(item: WorkOrder, mode: ActionMode) {
  actionOrder.value = item
  actionMode.value = mode
  Object.assign(actionForm, { comment: '', progress: item.remediation_progress ?? '', remediation_result: item.remediation_result ?? '', verification_result: '', return_reason: '' })
  actionDialogVisible.value = true
}

async function submitAction() {
  const item = actionOrder.value
  if (!item) return
  let endpoint = ''
  let payload: Record<string, string> = {}
  if (actionMode.value === 'issue') { endpoint = 'issue'; payload = { comment: actionForm.comment } }
  if (actionMode.value === 'accept') { endpoint = 'accept'; payload = { comment: actionForm.comment } }
  if (actionMode.value === 'progress') {
    if (!actionForm.progress.trim()) return ElMessage.warning('请填写整改进度')
    endpoint = 'progress'; payload = { progress: actionForm.progress, comment: actionForm.comment }
  }
  if (actionMode.value === 'submit') {
    if (!actionForm.remediation_result.trim()) return ElMessage.warning('请填写整改结果')
    endpoint = 'submit'; payload = { remediation_result: actionForm.remediation_result, comment: actionForm.comment }
  }
  if (actionMode.value === 'return') {
    if (!actionForm.return_reason.trim()) return ElMessage.warning('请填写退回原因')
    endpoint = 'review'; payload = { decision: 'returned', verification_result: actionForm.verification_result, return_reason: actionForm.return_reason }
  }
  if (actionMode.value === 'close') {
    if (!actionForm.verification_result.trim()) return ElMessage.warning('请填写复测结论')
    endpoint = 'review'; payload = { decision: 'closed', verification_result: actionForm.verification_result, return_reason: '' }
  }
  if (actionMode.value === 'cancel') {
    if (!actionForm.comment.trim()) return ElMessage.warning('请填写取消原因')
    endpoint = 'cancel'; payload = { comment: actionForm.comment }
  }
  submitting.value = true
  try {
    await apiClient.post(`/security-work-orders/${item.id}/${endpoint}`, payload)
    ElMessage.success(actionSuccessLabel(actionMode.value))
    actionDialogVisible.value = false
    if (detailVisible.value) await openDetail(item)
    await refreshAfterMutation()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '处理安全工单失败'))
  } finally {
    submitting.value = false
  }
}

function onAttachmentSelected(event: Event) {
  const input = event.target as HTMLInputElement
  selectedAttachmentFile.value = input.files?.[0]
}

async function uploadAttachment() {
  if (!selectedOrder.value || !selectedAttachmentFile.value) return ElMessage.warning('请先选择附件')
  const data = new FormData()
  data.append('file', selectedAttachmentFile.value)
  data.append('category', auth.canManageSecurityWorkOrders ? attachmentForm.category : 'remediation')
  data.append('description', attachmentForm.description)
  attachmentUploading.value = true
  try {
    await apiClient.post(`/security-work-orders/${selectedOrder.value.id}/attachments`, data, { headers: { 'Content-Type': 'multipart/form-data' } })
    ElMessage.success('工单附件已上传')
    selectedAttachmentFile.value = undefined
    attachmentForm.description = ''
    if (attachmentFileInput.value) attachmentFileInput.value.value = ''
    orderAttachmentPage.value = 1
    orderEventPage.value = 1
    await Promise.all([loadOrderAttachments(), loadOrderEvents()])
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '上传工单附件失败'))
  } finally {
    attachmentUploading.value = false
  }
}

async function downloadAttachment(item: WorkOrderAttachment) {
  if (!selectedOrder.value) return
  try {
    const response = await apiClient.get(`/security-work-orders/${selectedOrder.value.id}/attachments/${item.id}/download`, { responseType: 'blob' })
    const url = URL.createObjectURL(response.data)
    const link = document.createElement('a')
    link.href = url
    link.download = item.file_name
    link.click()
    URL.revokeObjectURL(url)
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '下载工单附件失败'))
  }
}

async function deleteAttachment(item: WorkOrderAttachment) {
  if (!selectedOrder.value) return
  try {
    await ElMessageBox.confirm(`确定删除附件“${item.file_name}”吗？`, '删除工单附件', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' })
    await apiClient.delete(`/security-work-orders/${selectedOrder.value.id}/attachments/${item.id}`)
    ElMessage.success('工单附件已删除')
    orderEventPage.value = 1
    await Promise.all([refreshOrderAttachmentsAfterMutation(), loadOrderEvents()])
  } catch (error) {
    if (error === 'cancel' || error === 'close') return
    ElMessage.error(getAPIErrorMessage(error, '删除工单附件失败'))
  }
}

function canDeleteAttachment(item: WorkOrderAttachment) {
  return auth.canManageSecurityWorkOrders || (item.uploaded_by === auth.user?.id && !['closed', 'cancelled'].includes(selectedOrder.value?.status || ''))
}

function formatFileSize(value: number) {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 / 1024).toFixed(1)} MB`
}

async function openFindingImportCandidate(item: FindingImportCandidate) {
  selectedCandidate.value = item
  selectedCandidateScopeAssetID.value = (item.test_batch_id ? item.suggested_scope_asset_id : item.suggested_asset_id) ?? ''
  candidateReviewNote.value = ''
  candidateDialogVisible.value = true
  try {
    const response = await apiClient.get<ListResponse<FindingImportScopeOption>>(`/security-findings/import-candidates/${item.id}/scope-assets`)
    candidateScopeOptions.value = response.data.items
    if (!candidateScopeOptions.value.some(option => option.id === selectedCandidateScopeAssetID.value)) {
      selectedCandidateScopeAssetID.value = candidateScopeOptions.value[0]?.id ?? ''
    }
  } catch (error) {
    candidateScopeOptions.value = []
    ElMessage.error(getAPIErrorMessage(error, '读取可关联资产失败'))
  }
}

async function submitFindingImportCandidate() {
  if (!selectedCandidate.value || !selectedCandidateScopeAssetID.value) {
    ElMessage.warning('请选择要关联的资产')
    return
  }
  candidateSubmitting.value = true
  try {
    const response = await apiClient.post<ItemResponse<FindingImportCandidate>>(`/security-findings/import-candidates/${selectedCandidate.value.id}/link`, {
      ...(selectedCandidate.value.test_batch_id ? { scope_asset_id: selectedCandidateScopeAssetID.value } : { asset_id: selectedCandidateScopeAssetID.value }),
      review_note: candidateReviewNote.value,
    })
    const item = response.data.item
    ElMessage.success(item.status === 'duplicate' ? `已关联，漏洞与 ${item.imported_finding_code || '现有安全发现'} 重复` : `已生成安全发现 ${item.imported_finding_code}`)
    candidateDialogVisible.value = false
    await refreshAfterMutation()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '关联漏洞导入项失败'))
  } finally {
    candidateSubmitting.value = false
  }
}

async function ignoreFindingImportCandidate(item: FindingImportCandidate) {
  try {
    const result = await ElMessageBox.prompt(`忽略后不会生成安全发现：${item.title}`, '忽略待关联漏洞', {
      confirmButtonText: '确认忽略', cancelButtonText: '取消', inputType: 'textarea',
      inputPlaceholder: '请填写忽略原因', inputValidator: value => Boolean(value?.trim()) || '请填写忽略原因',
    })
    await apiClient.post(`/security-findings/import-candidates/${item.id}/ignore`, { review_note: result.value.trim() })
    ElMessage.success('已忽略该漏洞导入项')
    await refreshAfterMutation()
  } catch (error) {
    if (error === 'cancel' || error === 'close') return
    ElMessage.error(getAPIErrorMessage(error, '忽略漏洞导入项失败'))
  }
}

function showPendingCandidates(batchID?: string) {
  activeTab.value = 'candidates'
  candidateStatusFilter.value = 'pending'
  candidateBatchFilter.value = batchID ?? ''
  candidatePage.value = 1
  importDialogVisible.value = false
  void loadActiveList()
}

function candidateStatusTag(value: string) {
  return value === 'imported' ? 'success' : value === 'ignored' || value === 'invalid' ? 'info' : value === 'duplicate' ? 'warning' : 'danger'
}

function actionTitle(mode: ActionMode) {
  return { issue: '下发安全工单', accept: '接收安全工单', progress: '更新整改进度', submit: '提交集团复测', return: '复测不通过并退回', close: '复测通过并关闭', cancel: '取消安全工单' }[mode]
}
function actionSuccessLabel(mode: ActionMode) {
  return { issue: '安全工单已下发', accept: '安全工单已接收', progress: '整改进度已更新', submit: '已提交集团复测', return: '安全工单已退回', close: '安全工单已关闭', cancel: '安全工单已取消' }[mode]
}
function actionButtonType(mode: ActionMode) { return mode === 'cancel' || mode === 'return' ? 'danger' : 'primary' }
function severityTag(value: string) { return value === 'critical' ? 'danger' : value === 'high' ? 'danger' : value === 'medium' ? 'warning' : value === 'low' ? 'success' : 'info' }
function orderStatusTag(value: string) { return value === 'closed' ? 'success' : value === 'returned' || value === 'cancelled' ? 'danger' : value === 'pending_verification' ? 'warning' : value === 'in_progress' ? 'primary' : 'info' }
function findingStatusTag(value: string) { return findingStatusTagType(value) }
function formatDate(value?: string) { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—' }
// A finding can be linked to a live asset, waiting on an unpublished working
// version, pointing at a retired asset, or never linked at all. All four used to
// render as "未关联对象", which made an expected state look like broken data.
const assetLinkStateLabels: Record<string, string> = {
  live: '已关联资产',
  pending: '待关联资产',
  retired: '资产已下线',
  unlinked: '未关联对象',
}
function assetLinkStateLabel(state?: string) { return assetLinkStateLabels[state ?? ''] ?? '未关联对象' }
// pending and retired are informational, not errors: the finding is fine in both
// cases and a red tag would train reviewers to ignore the tag.
function assetLinkStateTagType(state?: string) {
  return state === 'pending' ? 'warning' : state === 'retired' ? 'info' : 'primary'
}
function findingTarget(item: FindingSummary | Finding) {
  // A retired asset keeps its value, so the reviewer still sees which host was
  // affected; the tag next to it explains that the asset is gone.
  if (item.asset_value) return item.asset_value
  return item.business_system_name || assetLinkStateLabel(item.asset_link_state)
}
function riskAcceptanceLabel(item: Finding) {
  if (item.status !== FINDING_STATUS_RISK_ACCEPTED) return ''
  return item.risk_acceptance_expired ? '已到期，待复核' : '风险接受中'
}
async function onTabChange() {
  if (activeTab.value === 'candidates') {
    candidateStatusFilter.value ||= 'pending'
  } else {
    organizationFilter.value = ''
    statusFilter.value = ''
    assetLinkStateFilter.value = ''
    overdueOnly.value = false
  }
  if (activeTab.value === 'orders') await savedFilterViews.value?.loadViews()
  void loadActiveList()
}
</script>

<template>
  <section class="page-stack" v-loading="loading">
    <div class="page-heading">
      <div>
        <h1>安全工单</h1>
      </div>
      <div v-if="auth.canManageSecurityFindings || auth.canManageSecurityWorkOrders" class="heading-actions">
        <el-button v-if="auth.canManageSecurityFindings" @click="openFindingImport">批量导入漏洞</el-button>
        <el-button v-if="auth.canManageSecurityFindings" @click="openCreateFinding">登记安全发现</el-button>
        <el-button v-if="auth.canManageSecurityWorkOrders" type="primary" @click="openCreateWorkOrder()">新建整改工单</el-button>
      </div>
    </div>

    <details class="work-order-guide"><summary>查看协作说明</summary><p>{{ auth.hasGroupSecurityRole?'本平台记录测试结果和整改过程；专业测试由授权人员在对应环境执行。':'仅展示当前组织有权访问的工单，请按期限接收、反馈修复结果并跟进集团复测。' }}</p></details>

    <div class="panel security-panel">
      <el-tabs v-model="activeTab" @tab-change="onTabChange">
        <el-tab-pane label="安全整改工单" name="orders" />
        <el-tab-pane label="漏洞与安全发现" name="findings" />
        <el-tab-pane v-if="auth.canManageSecurityFindings" name="candidates">
          <template #label>待关联漏洞<el-badge v-if="candidateStatusFilter === 'pending' && candidateTotal" :value="candidateTotal" :max="99" class="candidate-tab-badge" /></template>
        </el-tab-pane>
      </el-tabs>
      <div v-if="activeTab === 'findings'" class="finding-summary-cards" aria-label="漏洞与安全发现汇总">
        <div class="finding-summary-card tone-total"><span>发现总数</span><strong>{{ findingSummary.total }}</strong></div>
        <div class="finding-summary-card tone-critical"><span>严重</span><strong>{{ findingSummary.critical }}</strong></div>
        <div class="finding-summary-card tone-high"><span>高危</span><strong>{{ findingSummary.high }}</strong></div>
        <div class="finding-summary-card tone-medium"><span>中危</span><strong>{{ findingSummary.medium }}</strong></div>
        <div class="finding-summary-card tone-low"><span>低危</span><strong>{{ findingSummary.low }}</strong></div>
      </div>
      <el-collapse v-if="activeTab === 'findings'" class="finding-summary-dimensions">
        <el-collapse-item name="dimensions" title="其他待办口径（不计入「待处理」）">
          <div class="finding-dimension-cards">
            <el-tooltip v-for="dimension in findingSummaryDimensions" :key="dimension.key" :content="dimension.hint" placement="top">
              <div class="finding-summary-card" :class="dimension.tone" :aria-label="`${dimension.label}：${dimension.hint}`">
                <span>{{ dimension.label }}</span>
                <strong>{{ findingSummaryValue(dimension.key) }}</strong>
              </div>
            </el-tooltip>
          </div>
        </el-collapse-item>
      </el-collapse>
      <div v-if="activeTab === 'candidates'" class="filter-bar candidate-filter-bar">
        <el-select v-model="candidateBatchFilter" clearable filterable placeholder="全部测试批次" style="width: 260px">
          <el-option v-for="item in usableTestBatches" :key="item.id" :label="`${item.name}（${item.code}）`" :value="item.id" />
        </el-select>
        <el-select v-model="candidateStatusFilter" clearable placeholder="全部处理状态" style="width: 160px">
          <el-option v-for="(label, value) in candidateStatusLabels" :key="value" :label="label" :value="value" />
        </el-select>
        <el-select v-model="candidateAssetTypeFilter" clearable placeholder="全部资产类型" style="width: 150px">
          <el-option v-for="(label, value) in assetTypeLabels" :key="value" :label="label" :value="value" />
        </el-select>
        <el-input v-model="candidateKeyword" clearable placeholder="资产值或漏洞标题" style="width: 220px" @keyup.enter="applyFilter" />
        <el-button type="primary" plain @click="applyFilter">筛选</el-button>
        <el-button @click="resetFilters">重置</el-button>
      </div>
      <div v-else class="filter-bar">
        <el-select v-if="auth.hasGroupSecurityRole" v-model="organizationFilter" clearable filterable placeholder="全部责任组织" style="width: 220px">
          <el-option v-for="item in enabledOrganizations" :key="item.id" :label="item.name" :value="item.id" />
        </el-select>
        <el-select v-model="statusFilter" clearable placeholder="全部状态" style="width: 170px">
          <template v-if="activeTab === 'orders'">
            <el-option v-for="(label, value) in orderStatusLabels" :key="value" :label="label" :value="value" />
          </template>
          <template v-else>
            <el-option v-for="(label, value) in findingStatusLabels" :key="value" :label="label" :value="value" />
          </template>
        </el-select>
        <el-select v-if="activeTab === 'findings'" v-model="assetLinkStateFilter" clearable placeholder="全部关联状态" style="width: 190px">
          <el-option v-for="(label, value) in assetLinkStateLabels" :key="value" :label="label" :value="value" />
        </el-select>
        <el-checkbox v-if="activeTab === 'orders'" v-model="overdueOnly">仅看逾期</el-checkbox>
        <el-button type="primary" plain @click="applyFilter">筛选</el-button>
        <el-button @click="resetFilters">重置</el-button>
      </div>
      <SavedFilterViews v-show="activeTab === 'orders'" ref="savedFilterViews" page-key="security_work_orders" :current-filters="currentWorkOrderFilters" class="work-order-saved-views" @apply="applySavedWorkOrderFilters" />
      <div v-show="activeTab === 'orders'" class="filter-stat-list work-order-stat-list">
        <div class="filter-stat"><span>符合筛选工单</span><strong>{{ orderTotal }}</strong></div>
        <div class="filter-stat"><span>处理中</span><strong>{{ activeOrderCount }}</strong></div>
        <div class="filter-stat"><span>待复测</span><strong>{{ pendingVerificationCount }}</strong></div>
        <div class="filter-stat danger"><span>逾期</span><strong>{{ overdueCount }}</strong></div>
      </div>

      <el-table v-if="activeTab === 'orders'" :data="workOrders" row-key="id" empty-text="暂无安全整改工单">
        <el-table-column label="工单" min-width="210">
          <template #default="scope"><el-button link type="primary" class="table-link" @click="openDetail(scope.row)">{{ scope.row.title }}</el-button><span class="table-secondary mono-text">{{ scope.row.number }}</span><span class="list-secondary">{{ scope.row.finding_count }} 项关联发现</span></template>
        </el-table-column>
        <el-table-column label="责任组织" min-width="130">
          <template #default="scope">{{ scope.row.assignee_organization_name }}</template>
        </el-table-column>
        <el-table-column label="等级" width="85"><template #default="scope"><el-tag :type="severityTag(scope.row.severity)" effect="dark">{{ severityLabels[scope.row.severity] }}</el-tag></template></el-table-column>
        <el-table-column label="状态" width="115"><template #default="scope"><el-tag :type="orderStatusTag(scope.row.status)" effect="plain">{{ orderStatusLabels[scope.row.status] }}</el-tag></template></el-table-column>
        <el-table-column label="个人修复进度" min-width="175"><template #default="{ row }"><div v-if="row.execution_mode === 'individual'"><span v-for="(label, key) in {unassigned:'未分派',pending_acceptance:'待接收',in_progress:'修复中',pending_org_review:'组织确认',pending_security_review:'集团复测',verified_fixed:'修复通过',risk_accepted:'接受风险',false_positive:'误报',risk_expired:'风险接受失效'}" :key="key" class="inline-tag" v-show="row.remediation_counts?.[key]">{{label}} {{row.remediation_counts?.[key] || 0}} </span></div><span v-else>组织统一整改</span></template></el-table-column>

        <el-table-column label="整改期限" min-width="130">
          <template #default="scope"><span :class="{ 'overdue-text': scope.row.overdue }">{{ formatDate(scope.row.due_at) }}</span><el-tag v-if="scope.row.overdue" type="danger" size="small" class="inline-tag">逾期</el-tag></template>
        </el-table-column>
        <el-table-column label="操作" fixed="right" width="88" align="right" header-align="right" class-name="table-action-column">
          <template #default="scope"><RowActions :label="`操作：${scope.row.name || scope.row.title || scope.row.code || scope.row.display_name || scope.row.id}`">
            <el-button link type="primary" @click="openDetail(scope.row)">详情</el-button>
            <el-button v-if="scope.row.execution_mode === 'individual'" link type="primary" @click="$router.push({ path: '/remediation', query: { work_order_id: scope.row.id } })">修复任务</el-button>
            <template v-if="auth.canManageSecurityWorkOrders">
              <el-button v-if="scope.row.status === 'draft'" link type="primary" @click="openAction(scope.row, 'issue')">下发</el-button>
              <el-button v-if="scope.row.execution_mode !== 'individual' && scope.row.status === 'pending_verification'" link type="danger" @click="openAction(scope.row, 'return')">退回</el-button>
              <el-button v-if="scope.row.execution_mode !== 'individual' && scope.row.status === 'pending_verification'" link type="success" @click="openAction(scope.row, 'close')">复测通过</el-button>
              <el-button v-if="!['closed', 'cancelled'].includes(scope.row.status)" link type="danger" @click="openAction(scope.row, 'cancel')">取消</el-button>
            </template>
            <template v-else>
              <el-button v-if="scope.row.status === 'issued'" link type="primary" @click="openAction(scope.row, 'accept')">接收</el-button>
              <el-button v-if="scope.row.execution_mode !== 'individual' && ['in_progress', 'returned'].includes(scope.row.status)" link type="primary" @click="openAction(scope.row, 'progress')">更新进度</el-button>
              <el-button v-if="scope.row.execution_mode !== 'individual' && ['in_progress', 'returned'].includes(scope.row.status)" link type="success" @click="openAction(scope.row, 'submit')">提交复测</el-button>
            </template>
          </RowActions></template>
        </el-table-column>
      </el-table>

      <div v-else-if="activeTab === 'findings'" class="finding-list-content"><el-table :data="findings" row-key="id" empty-text="暂无安全发现">
        <el-table-column label="安全发现" min-width="200">
          <template #default="scope"><strong>{{ scope.row.title }}</strong><span class="table-secondary mono-text">{{ scope.row.code }} · {{ scope.row.vulnerability_type || '未分类' }}</span><span v-if="scope.row.test_batch_id" class="list-secondary">{{ scope.row.test_batch_name }} · {{ scope.row.test_batch_code }}</span></template>
        </el-table-column>
        <el-table-column label="责任组织" min-width="130"><template #default="scope">{{ scope.row.assignee_organization_name }}</template></el-table-column>
        <el-table-column label="关联资产/系统" min-width="155"><template #default="scope">{{ findingTarget(scope.row) }}<span v-if="scope.row.asset_value && scope.row.business_system_name" class="table-secondary">{{ scope.row.business_system_name }}</span><el-tag v-if="scope.row.asset_link_state && scope.row.asset_link_state !== 'live'" size="small" :type="assetLinkStateTagType(scope.row.asset_link_state)" class="asset-link-tag">{{ assetLinkStateLabel(scope.row.asset_link_state) }}</el-tag></template></el-table-column>

        <el-table-column label="等级" width="85"><template #default="scope"><el-tag :type="severityTag(scope.row.severity)" effect="dark">{{ severityLabels[scope.row.severity] }}</el-tag></template></el-table-column>
        <el-table-column label="状态" width="115"><template #default="scope"><el-tag :type="findingStatusTag(scope.row.status)" effect="plain">{{ findingStatusLabels[scope.row.status] }}</el-tag><span v-if="scope.row.status === FINDING_STATUS_RISK_ACCEPTED" class="table-secondary risk-state" :class="{ expired: scope.row.risk_acceptance_expired }">{{ riskAcceptanceLabel(scope.row) }}</span></template></el-table-column>
        <el-table-column label="发现时间" min-width="135"><template #default="scope">{{ formatDate(scope.row.discovered_at) }}</template></el-table-column>
        <el-table-column label="操作" fixed="right" width="88" align="right" header-align="right" class-name="table-action-column">
          <template #default="scope"><RowActions :label="`操作：${scope.row.name || scope.row.title || scope.row.code || scope.row.display_name || scope.row.id}`">
            <el-button link type="primary" @click="openFindingDetail(scope.row)">详情</el-button>
            <template v-if="auth.canManageSecurityFindings || auth.canManageSecurityWorkOrders">
              <el-button v-if="auth.canManageSecurityFindings" link type="primary" @click="openEditFinding(scope.row)">编辑</el-button>
              <el-button v-if="auth.canManageSecurityWorkOrders && [FINDING_STATUS_PENDING, FINDING_STATUS_CONFIRMED].includes(scope.row.status)" link type="success" @click="openCreateWorkOrder(scope.row)">生成工单</el-button>
              <el-button v-if="auth.canManageSecurityFindings && ['pending', 'in_progress', FINDING_STATUS_CONFIRMED, 'resolved', 'fixed'].includes(scope.row.status)" link type="warning" @click="openRiskDialog(scope.row, 'accept')">接受风险</el-button>
              <el-button v-if="auth.canManageSecurityFindings && scope.row.status === FINDING_STATUS_RISK_ACCEPTED" link type="warning" @click="openRiskDialog(scope.row, 'review')">复核</el-button>
            </template>
          </RowActions></template>
        </el-table-column>
      </el-table>

      </div>
      <el-table v-else :data="findingImportCandidates" row-key="id" empty-text="暂无待关联漏洞">
        <el-table-column label="导入行" width="75"><template #default="scope">第 {{ scope.row.source_row_number }} 行</template></el-table-column>
        <el-table-column label="原始资产标识" min-width="160">
          <template #default="scope"><strong class="mono-text">{{ scope.row.asset_value }}</strong><span class="table-secondary">{{ assetTypeLabels[scope.row.asset_type] || scope.row.asset_type }}</span></template>
        </el-table-column>
        <el-table-column label="漏洞信息" min-width="170">
          <template #default="scope"><strong>{{ scope.row.title }}</strong><span class="table-secondary">{{ scope.row.vulnerability_type || '未分类' }} · {{ formatDate(scope.row.discovered_at) }}</span></template>
        </el-table-column>
        <el-table-column label="测试批次" min-width="135"><template #default="scope">{{ scope.row.test_batch_name || '未关联测试批次' }}<span v-if="scope.row.test_batch_code" class="table-secondary mono-text">{{ scope.row.test_batch_code }}</span></template></el-table-column>
        <el-table-column label="系统建议" min-width="135">
          <template #default="scope">
            <span v-if="scope.row.suggested_scope_asset_id || scope.row.suggested_asset_id">{{ scope.row.suggested_asset_value }}<span class="table-secondary">{{ scope.row.suggested_organization_name }} · {{ matchMethodLabels[scope.row.match_method || ''] || '相似标识' }} {{ scope.row.match_confidence }}%</span></span>
            <span v-else class="table-secondary">未找到唯一建议，请人工选择资产</span>
          </template>
        </el-table-column>
        <el-table-column label="状态 / 结果" width="115"><template #default="scope"><el-tag :type="candidateStatusTag(scope.row.status)" effect="plain">{{ candidateStatusLabels[scope.row.status] || scope.row.status }}</el-tag><span class="list-secondary">{{ scope.row.imported_finding_code || scope.row.review_note || '—' }}</span></template></el-table-column>

        <el-table-column label="操作" fixed="right" width="88" align="right" header-align="right" class-name="table-action-column">
          <template #default="scope"><RowActions :direct="scope.row.status !== 'pending'" :label="`操作：${scope.row.name || scope.row.title || scope.row.code || scope.row.display_name || scope.row.id}`">
            <template v-if="scope.row.status === 'pending'"><el-button link type="primary" @click="openFindingImportCandidate(scope.row)">关联资产</el-button><el-button link type="danger" @click="ignoreFindingImportCandidate(scope.row)">忽略</el-button></template>
            <span v-else class="table-secondary">{{ scope.row.reviewed_by_name || '已处理' }}</span>
          </RowActions></template>
        </el-table-column>
      </el-table>

      <div class="table-pagination">
        <el-pagination
          background
          layout="total, sizes, prev, pager, next"
          :total="currentTotal"
          :current-page="currentPage"
          :page-size="pageSize"
          :page-sizes="[20, 50, 100]"
          @current-change="handlePageChange"
          @size-change="handlePageSizeChange"
        />
      </div>
    </div>

    <el-drawer v-model="findingDetailVisible" title="安全发现详情" size="680px">
      <div v-loading="findingDetailLoading" v-if="selectedFinding" class="finding-detail">
        <div class="detail-title">
          <div><span class="mono-text">{{ selectedFinding.code }}</span><h2>{{ selectedFinding.title }}</h2></div>
          <div class="finding-detail-tags"><el-tag :type="severityTag(selectedFinding.severity)" effect="dark">{{ severityLabels[selectedFinding.severity] }}</el-tag><el-tag :type="findingStatusTag(selectedFinding.status)" effect="plain">{{ findingStatusLabels[selectedFinding.status] }}</el-tag></div>
        </div>
        <el-alert v-if="selectedFinding.status === FINDING_STATUS_RISK_ACCEPTED && selectedFinding.risk_acceptance_expired" title="风险接受已到期，请集团安全运营人员及时复核、延长有效期或撤销风险接受。" type="error" :closable="false" show-icon class="detail-alert" />
        <el-descriptions :column="2" border>
          <el-descriptions-item label="责任组织">{{ selectedFinding.assignee_organization_name }}</el-descriptions-item>
          <el-descriptions-item label="漏洞类型">{{ selectedFinding.vulnerability_type || '未分类' }}</el-descriptions-item>
          <el-descriptions-item label="关联对象">{{ findingTarget(selectedFinding) }}<el-tag v-if="selectedFinding.asset_link_state && selectedFinding.asset_link_state !== 'live'" size="small" :type="assetLinkStateTagType(selectedFinding.asset_link_state)" class="asset-link-tag">{{ assetLinkStateLabel(selectedFinding.asset_link_state) }}</el-tag><div v-if="selectedFinding.asset_link_state === 'retired'" class="table-secondary">该资产已下线，漏洞记录保留用于追溯，无需重新登记资产。</div><div v-else-if="selectedFinding.asset_link_state === 'pending'" class="table-secondary">资产工作版本尚未发布，发布后自动关联。</div></el-descriptions-item>
          <el-descriptions-item label="业务系统">{{ selectedFinding.business_system_name || '—' }}</el-descriptions-item>
          <el-descriptions-item label="测试批次">{{ selectedFinding.test_batch_name || '—' }}<span v-if="selectedFinding.test_batch_code" class="table-secondary mono-text">{{ selectedFinding.test_batch_code }}</span></el-descriptions-item>
          <el-descriptions-item label="发现时间">{{ formatDate(selectedFinding.discovered_at) }}</el-descriptions-item>
        </el-descriptions>
        <div class="detail-block"><h3>漏洞描述</h3><p>{{ selectedFinding.description }}</p></div>
        <div v-if="selectedFinding.evidence" class="detail-block"><h3>验证证据</h3><p>{{ selectedFinding.evidence }}</p></div>
        <div v-if="selectedFinding.remediation_advice" class="detail-block"><h3>整改建议</h3><p>{{ selectedFinding.remediation_advice }}</p></div>
        <div v-if="selectedFinding.risk_accepted_at" class="detail-block risk-acceptance-card" :class="{ expired: selectedFinding.risk_acceptance_expired }">
          <div class="detail-block-heading"><h3>风险接受记录</h3><el-tag v-if="selectedFinding.status === FINDING_STATUS_RISK_ACCEPTED" :type="selectedFinding.risk_acceptance_expired ? 'danger' : 'warning'">{{ riskAcceptanceLabel(selectedFinding) }}</el-tag><el-tag v-else type="info">已撤销</el-tag></div>
          <el-descriptions :column="1" border size="small">
            <el-descriptions-item label="接受原因">{{ selectedFinding.risk_acceptance_reason || '—' }}</el-descriptions-item>
            <el-descriptions-item label="接受人/时间">{{ selectedFinding.risk_accepted_by_name || '—' }} · {{ formatDate(selectedFinding.risk_accepted_at) }}</el-descriptions-item>
            <el-descriptions-item label="有效期至">{{ formatDate(selectedFinding.risk_accepted_until) }}</el-descriptions-item>
            <el-descriptions-item v-if="selectedFinding.risk_reviewed_at" label="最近复核">{{ selectedFinding.risk_reviewed_by_name || '—' }} · {{ formatDate(selectedFinding.risk_reviewed_at) }}<p class="review-comment">{{ selectedFinding.risk_review_comment }}</p></el-descriptions-item>
          </el-descriptions>
        </div>
        <div v-if="auth.canManageSecurityFindings || auth.canManageSecurityWorkOrders" class="finding-detail-actions">
          <el-button v-if="auth.canManageSecurityFindings && ['pending', 'in_progress', FINDING_STATUS_CONFIRMED, 'resolved', 'fixed'].includes(selectedFinding.status)" type="warning" @click="openRiskDialog(selectedFinding, 'accept')">接受风险</el-button>
          <el-button v-if="auth.canManageSecurityFindings && selectedFinding.status === FINDING_STATUS_RISK_ACCEPTED" type="warning" @click="openRiskDialog(selectedFinding, 'review')">复核并延长有效期</el-button>
          <el-button v-if="auth.canManageSecurityFindings && selectedFinding.status === FINDING_STATUS_RISK_ACCEPTED" type="danger" plain @click="revokeFindingRisk(selectedFinding)">撤销风险接受</el-button>
          <el-button v-if="auth.canManageSecurityWorkOrders && ([FINDING_STATUS_PENDING, FINDING_STATUS_CONFIRMED] as string[]).includes(selectedFinding.status)" type="primary" @click="openCreateWorkOrder(selectedFinding)">生成整改工单</el-button>
        </div>
      </div>
    </el-drawer>

    <el-dialog v-model="riskDialogVisible" :title="riskActionLabels[riskMode]" width="560px" destroy-on-close>
      <el-alert v-if="riskMode === 'accept'" type="warning" :closable="false" show-icon title="风险接受不会删除漏洞，仅在明确业务理由和有效期限内暂停整改；到期后系统将标记为待复核。" />
      <el-alert v-else-if="selectedFinding?.risk_acceptance_expired" type="error" :closable="false" show-icon title="当前风险接受已到期，完成复核时必须设置新的未来有效期。" />
      <el-form label-position="top" class="risk-form">
        <el-form-item v-if="riskMode === 'accept'" label="风险接受原因（必填）"><el-input v-model="riskForm.reason" type="textarea" :rows="5" maxlength="5000" show-word-limit placeholder="说明暂不整改的业务原因、补偿措施和责任判断" /></el-form-item>
        <el-form-item v-else label="复核意见（必填）"><el-input v-model="riskForm.comment" type="textarea" :rows="5" maxlength="5000" show-word-limit placeholder="记录本次复核结论、现有补偿措施及继续接受风险的理由" /></el-form-item>
        <el-form-item label="风险有效期至（必填）"><el-date-picker v-model="riskForm.accepted_until" type="datetime" style="width:100%" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="riskDialogVisible = false">取消</el-button><el-button type="warning" :loading="riskSubmitting" @click="submitRiskAction">确认{{ riskActionLabels[riskMode] }}</el-button></template>
    </el-dialog>

    <el-dialog v-model="findingDialogVisible" :title="editingFindingID ? '编辑安全发现' : '登记安全发现'" width="760px" destroy-on-close>
      <el-form ref="findingFormRef" :model="findingForm" :rules="findingRules" label-position="top">
        <div class="form-grid two-columns">
          <el-form-item label="漏洞标题" prop="title"><el-input v-model="findingForm.title" maxlength="255" show-word-limit /></el-form-item>
          <el-form-item label="严重等级" prop="severity"><el-select v-model="findingForm.severity" style="width:100%"><el-option v-for="(label, value) in severityLabels" :key="value" :label="label" :value="value" /></el-select></el-form-item>
          <el-form-item label="测试批次"><el-select v-model="findingForm.test_batch_id" clearable filterable style="width:100%" placeholder="可选；选择后资产限于授权范围"><el-option v-for="item in usableTestBatches" :key="item.id" :label="`${item.name}（${item.code}）`" :value="item.id" /></el-select></el-form-item>
          <el-form-item label="责任组织" prop="assignee_organization_id"><el-select v-model="findingForm.assignee_organization_id" filterable style="width:100%" :disabled="Boolean(findingForm.test_batch_id)"><el-option v-for="item in enabledOrganizations" :key="item.id" :label="item.name" :value="item.id" /></el-select></el-form-item>
          <el-form-item label="漏洞类型"><el-input v-model="findingForm.vulnerability_type" placeholder="例如：越权访问、SQL 注入" /></el-form-item>
          <el-form-item label="关联网络资产"><el-select v-model="findingForm.asset_id" clearable filterable style="width:100%" placeholder="URL、IP、域名等"><el-option v-for="item in scopedAssets" :key="item.id" :label="`${item.name || item.value}（${item.value}）`" :value="item.id" /></el-select></el-form-item>
          <el-form-item label="关联业务系统"><el-select v-model="findingForm.business_system_id" clearable filterable style="width:100%" placeholder="可与网络资产同时选择"><el-option v-for="item in scopedSystems" :key="item.id" :label="`${item.name}（${item.code}）`" :value="item.id" /></el-select></el-form-item>
          <el-form-item label="发现时间" prop="discovered_at"><el-date-picker v-model="findingForm.discovered_at" type="datetime" style="width:100%" /></el-form-item>
          <el-form-item v-if="editingFindingID" label="发现状态"><el-select v-model="findingForm.status" style="width:100%" :disabled="findingForm.status === FINDING_STATUS_RISK_ACCEPTED"><el-option v-for="(label, value) in findingStatusLabels" :key="value" :label="label" :value="value" :disabled="value === FINDING_STATUS_RISK_ACCEPTED" /></el-select><small v-if="findingForm.status === FINDING_STATUS_RISK_ACCEPTED" class="form-help">风险接受状态请通过详情中的风险操作维护</small></el-form-item>
        </div>
        <el-form-item label="漏洞描述" prop="description"><el-input v-model="findingForm.description" type="textarea" :rows="4" placeholder="描述漏洞位置、触发条件和影响" /></el-form-item>
        <el-form-item label="验证证据"><el-input v-model="findingForm.evidence" type="textarea" :rows="3" placeholder="请脱敏填写请求路径、响应现象或证据位置，不要录入明文口令" /></el-form-item>
        <el-form-item label="整改建议"><el-input v-model="findingForm.remediation_advice" type="textarea" :rows="3" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="findingDialogVisible = false">取消</el-button><el-button type="primary" :loading="submitting" @click="saveFinding">保存</el-button></template>
    </el-dialog>

    <el-dialog v-model="importDialogVisible" title="批量导入漏洞与安全发现" width="860px" destroy-on-close>
      <el-alert type="info" :closable="false" show-icon title="测试批次可选：选择后按批次授权范围匹配资产；不选择时按全局资产台账匹配。资产尚未建档的有效漏洞会进入待关联列表。支持 CSV、XLSX 以及安全测试导出的 JSON。" />
      <div class="import-toolbar">
        <el-select v-model="importTestBatchID" clearable filterable placeholder="测试批次（可选）" style="min-width:320px">
          <el-option v-for="item in usableTestBatches" :key="item.id" :label="`${item.name}（${item.code}，${item.asset_count} 项资产）`" :value="item.id" />
        </el-select>
        <input ref="importFileInput" type="file" accept=".csv,.xlsx,.json" @change="selectFindingImportFile" />
        <el-button type="primary" :loading="importSubmitting" @click="submitFindingImport">开始导入</el-button>
      </div>
      <div class="template-links">标准列：资产类型、资产值、漏洞标题、严重等级、漏洞类型、漏洞描述、验证证据、整改建议、发现时间。<el-button link type="primary" @click="downloadFindingTemplate('xlsx')">下载 XLSX 模板</el-button><el-button link type="primary" @click="downloadFindingTemplate('csv')">下载 CSV 模板</el-button></div>
      <div v-if="importResult" class="import-result">
        <div class="import-stat-grid">
          <el-statistic title="总行数" :value="importResult.total_count" />
          <el-statistic title="成功" :value="importResult.success_count" />
          <el-statistic title="待关联" :value="importResult.pending_count" />
          <el-statistic title="重复" :value="importResult.duplicate_count" />
          <el-statistic title="失败" :value="importResult.failure_count" />
        </div>
        <el-button v-if="importResult.pending_count" type="warning" plain class="pending-candidate-entry" @click="showPendingCandidates(importResult.test_batch_id)">处理 {{ importResult.pending_count }} 条待关联漏洞</el-button>
        <el-table v-if="importResult.errors.length" :data="importResult.errors" size="small" max-height="220" class="import-errors">
          <el-table-column label="行号" prop="row" width="75" /><el-table-column label="错误代码" prop="code" width="170" /><el-table-column label="原因" prop="message" min-width="360" />
        </el-table>
      </div>
      <div class="detail-block"><h3>最近导入历史</h3>
        <el-table :data="findingImports.slice(0, 10)" size="small" empty-text="暂无导入记录">
          <el-table-column label="文件" prop="file_name" min-width="160" /><el-table-column label="测试批次" min-width="135"><template #default="scope">{{ scope.row.test_batch_name || '未关联测试批次' }}<span v-if="scope.row.test_batch_code" class="table-secondary mono-text">{{ scope.row.test_batch_code }}</span></template></el-table-column>
          <el-table-column label="结果" min-width="230"><template #default="scope">成功 {{ scope.row.success_count }} / 待关联 {{ scope.row.pending_count }} / 重复 {{ scope.row.duplicate_count }} / 失败 {{ scope.row.failure_count }}</template></el-table-column>
          <el-table-column label="导入时间" width="165"><template #default="scope">{{ formatDate(scope.row.created_at) }}</template></el-table-column>
        </el-table>
      </div>
      <template #footer><el-button @click="importDialogVisible = false">关闭</el-button></template>
    </el-dialog>

    <el-dialog v-model="candidateDialogVisible" title="关联漏洞到资产" width="760px" destroy-on-close>
      <template v-if="selectedCandidate">
        <el-alert type="warning" :closable="false" show-icon :title="selectedCandidate.test_batch_id ? '系统只允许关联当前安全测试批次中的授权资产；相似匹配仅作为建议，不会自动入库。' : '当前导入未关联测试批次，将从全局资产台账中选择资产；系统建议仅供参考，不会自动入库。'" />
        <el-descriptions :column="2" border class="candidate-descriptions">
          <el-descriptions-item label="测试批次">{{ selectedCandidate.test_batch_name || '未关联测试批次' }}<span v-if="selectedCandidate.test_batch_code">（{{ selectedCandidate.test_batch_code }}）</span></el-descriptions-item>
          <el-descriptions-item label="原文件行号">第 {{ selectedCandidate.source_row_number }} 行</el-descriptions-item>
          <el-descriptions-item label="原始资产类型">{{ assetTypeLabels[selectedCandidate.asset_type] || selectedCandidate.asset_type }}</el-descriptions-item>
          <el-descriptions-item label="原始资产值"><span class="mono-text">{{ selectedCandidate.asset_value }}</span></el-descriptions-item>
          <el-descriptions-item label="漏洞标题" :span="2">{{ selectedCandidate.title }}</el-descriptions-item>
          <el-descriptions-item label="漏洞描述" :span="2"><span class="candidate-description-text">{{ selectedCandidate.description }}</span></el-descriptions-item>
        </el-descriptions>
        <el-form label-position="top" class="candidate-link-form">
          <el-form-item label="关联资产（必选）">
            <el-select v-model="selectedCandidateScopeAssetID" filterable style="width:100%" :placeholder="selectedCandidate.test_batch_id ? '请选择当前批次授权资产' : '请选择全局资产台账中的资产'">
              <el-option v-for="item in candidateScopeOptions" :key="item.id" :value="item.id" :label="`${item.asset_value} · ${item.organization_name}${item.business_system_name ? ` · ${item.business_system_name}` : ''}`">
                <div class="candidate-option"><span><el-tag v-if="item.suggested" size="small" type="warning">系统建议</el-tag>{{ item.asset_value }}</span><small>{{ item.organization_name }}{{ item.business_system_name ? ` · ${item.business_system_name}` : '' }}</small></div>
              </el-option>
            </el-select>
          </el-form-item>
          <el-form-item label="关联备注"><el-input v-model="candidateReviewNote" type="textarea" :rows="3" maxlength="2000" show-word-limit placeholder="可记录扫描器标识与台账资产的对应依据" /></el-form-item>
        </el-form>
      </template>
      <template #footer><el-button @click="candidateDialogVisible = false">取消</el-button><el-button type="primary" :loading="candidateSubmitting" @click="submitFindingImportCandidate">确认关联并生成安全发现</el-button></template>
    </el-dialog>

    <el-dialog v-model="workOrderDialogVisible" title="新建安全整改工单" width="760px" destroy-on-close>
      <el-form ref="workOrderFormRef" :model="workOrderForm" :rules="workOrderRules" label-position="top">
        <div class="form-grid two-columns">
          <el-form-item label="工单标题" prop="title"><el-input v-model="workOrderForm.title" maxlength="255" /></el-form-item>
          <el-form-item label="责任组织" prop="assignee_organization_id"><el-select v-model="workOrderForm.assignee_organization_id" filterable style="width:100%"><el-option v-for="item in enabledOrganizations" :key="item.id" :label="item.name" :value="item.id" /></el-select></el-form-item>
          <el-form-item label="整改期限" prop="due_at"><el-date-picker v-model="workOrderForm.due_at" type="datetime" style="width:100%" /></el-form-item>
          <el-form-item label="执行方式"><el-radio-group v-model="workOrderForm.execution_mode"><el-radio value="organization">组织统一整改</el-radio><el-radio value="individual">组织派给具体人员</el-radio></el-radio-group></el-form-item>
          <el-form-item label="下发方式"><el-switch v-model="workOrderForm.issue_immediately" active-text="保存后立即下发" inactive-text="先保存草稿" /></el-form-item>
        </div>
        <el-form-item label="关联安全发现" prop="finding_ids"><el-select v-model="workOrderForm.finding_ids" multiple filterable collapse-tags style="width:100%" placeholder="仅显示该组织待处理的发现"><el-option v-for="item in orderFindings" :key="item.id" :label="`[${severityLabels[item.severity]}] ${item.code} ${item.title} · ${findingTarget(item)}`" :value="item.id" /></el-select></el-form-item>
        <el-form-item label="背景说明"><el-input v-model="workOrderForm.description" type="textarea" :rows="3" /></el-form-item>
        <el-form-item label="整改要求" prop="remediation_requirement"><el-input v-model="workOrderForm.remediation_requirement" type="textarea" :rows="4" placeholder="明确整改要求、验证口径和提交材料" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="workOrderDialogVisible = false">取消</el-button><el-button type="primary" :loading="submitting" @click="saveWorkOrder">{{ workOrderForm.issue_immediately ? '创建并下发' : '保存草稿' }}</el-button></template>
    </el-dialog>

    <el-drawer v-model="detailVisible" title="安全工单详情" size="720px">
      <div v-loading="detailLoading" v-if="selectedOrder" class="work-order-detail">
        <div class="detail-title"><div><span class="mono-text">{{ selectedOrder.number }}</span><h2>{{ selectedOrder.title }}</h2></div><el-tag :type="orderStatusTag(selectedOrder.status)" size="large">{{ orderStatusLabels[selectedOrder.status] }}</el-tag></div>
        <el-descriptions :column="2" border>
          <el-descriptions-item label="责任组织">{{ selectedOrder.assignee_organization_name }}</el-descriptions-item>
          <el-descriptions-item label="严重等级"><el-tag :type="severityTag(selectedOrder.severity)" effect="dark">{{ severityLabels[selectedOrder.severity] }}</el-tag></el-descriptions-item>
          <el-descriptions-item label="整改期限"><span :class="{ 'overdue-text': selectedOrder.overdue }">{{ formatDate(selectedOrder.due_at) }}</span></el-descriptions-item>
          <el-descriptions-item label="下发时间">{{ formatDate(selectedOrder.issued_at) }}</el-descriptions-item>
        </el-descriptions>
        <div class="detail-block"><h3>整改要求</h3><p>{{ selectedOrder.remediation_requirement }}</p></div>
        <div v-if="selectedOrder.return_reason" class="detail-block warning-block"><h3>最近退回原因</h3><p>{{ selectedOrder.return_reason }}</p></div>
        <div v-if="selectedOrder.remediation_progress" class="detail-block"><h3>当前整改进度</h3><p>{{ selectedOrder.remediation_progress }}</p></div>
        <div v-if="selectedOrder.remediation_result" class="detail-block"><h3>整改结果</h3><p>{{ selectedOrder.remediation_result }}</p></div>
        <div v-if="selectedOrder.verification_result" class="detail-block"><h3>集团复测结论</h3><p>{{ selectedOrder.verification_result }}</p></div>
        <div v-loading="orderAttachmentLoading" class="detail-block">
          <div class="detail-block-heading"><h3>整改与复测附件（{{ orderAttachmentTotal }}）</h3><span>支持 PDF、图片、TXT、CSV、XLSX、DOCX、ZIP，单文件不超过 20MB</span></div>
          <div v-if="auth.canManageSecurityWorkOrders || !['closed', 'cancelled'].includes(selectedOrder.status)" class="attachment-upload-box">
            <el-select v-if="auth.canManageSecurityWorkOrders" v-model="attachmentForm.category" size="small" style="width: 130px">
              <el-option v-for="(label, value) in attachmentCategoryLabels" :key="value" :label="label" :value="value" />
            </el-select>
            <input ref="attachmentFileInput" type="file" accept=".pdf,.png,.jpg,.jpeg,.txt,.csv,.xlsx,.docx,.zip" @change="onAttachmentSelected">
            <el-input v-model="attachmentForm.description" size="small" maxlength="500" placeholder="附件说明（可选）" />
            <el-button type="primary" size="small" :loading="attachmentUploading" @click="uploadAttachment">上传附件</el-button>
          </div>
          <el-table :data="orderAttachments" size="small" empty-text="暂无附件">
            <el-table-column label="类型" width="90"><template #default="scope">{{ attachmentCategoryLabels[scope.row.category] }}</template></el-table-column>
            <el-table-column label="文件" min-width="190"><template #default="scope"><el-button link type="primary" @click="downloadAttachment(scope.row)">{{ scope.row.file_name }}</el-button><small v-if="scope.row.description" class="attachment-description">{{ scope.row.description }}</small></template></el-table-column>
            <el-table-column label="大小" width="90"><template #default="scope">{{ formatFileSize(scope.row.size_bytes) }}</template></el-table-column>
            <el-table-column label="上传人" width="130"><template #default="scope">{{ scope.row.uploader_organization_name || '—' }}<br><small>{{ scope.row.uploaded_by_name || '—' }}</small></template></el-table-column>
            <el-table-column label="时间" width="155"><template #default="scope">{{ formatDate(scope.row.created_at) }}</template></el-table-column>
            <el-table-column label="操作" width="70" align="right" header-align="right" class-name="table-action-column"><template #default="scope"><el-button v-if="canDeleteAttachment(scope.row)" link type="danger" @click="deleteAttachment(scope.row)">删除</el-button></template></el-table-column>
          </el-table>
          <div v-if="orderAttachmentTotal > 0" class="table-pagination detail-pagination">
            <el-pagination background small layout="total, sizes, prev, pager, next" :total="orderAttachmentTotal" :current-page="orderAttachmentPage" :page-size="orderAttachmentPageSize" :page-sizes="[20, 50, 100]" @current-change="handleOrderAttachmentPageChange" @size-change="handleOrderAttachmentPageSizeChange" />
          </div>
        </div>
        <div v-loading="orderFindingLoading" class="detail-block">
          <h3>关联安全发现（{{ orderFindingTotal }}）</h3>
          <el-table :data="orderDetailFindings" size="small" empty-text="暂无关联安全发现"><el-table-column label="编号" prop="code" width="150" /><el-table-column label="标题" prop="title" min-width="220" /><el-table-column label="对象" min-width="180"><template #default="scope">{{ findingTarget(scope.row) }}</template></el-table-column><el-table-column label="等级" width="75"><template #default="scope">{{ severityLabels[scope.row.severity] }}</template></el-table-column></el-table>
          <div v-if="orderFindingTotal > 0" class="table-pagination detail-pagination"><el-pagination background small layout="total, sizes, prev, pager, next" :total="orderFindingTotal" :current-page="orderFindingPage" :page-size="orderFindingPageSize" :page-sizes="[20, 50, 100]" @current-change="handleOrderFindingPageChange" @size-change="handleOrderFindingPageSizeChange" /></div>
        </div>
        <div v-loading="orderEventLoading" class="detail-block">
          <h3>处理时间线（{{ orderEventTotal }}）</h3>
          <el-timeline v-if="orderEvents.length"><el-timeline-item v-for="event in orderEvents" :key="event.id" :timestamp="formatDate(event.created_at)" placement="top"><strong>{{ eventLabels[event.action] || event.action }}</strong><span class="event-actor">{{ event.actor_organization_name }} · {{ event.actor_name }}</span><p v-if="event.comment">{{ event.comment }}</p></el-timeline-item></el-timeline>
          <div v-else-if="!orderEventLoading" class="empty-state compact-empty">暂无处理记录</div>
          <div v-if="orderEventTotal > 0" class="table-pagination detail-pagination"><el-pagination background small layout="total, sizes, prev, pager, next" :total="orderEventTotal" :current-page="orderEventPage" :page-size="orderEventPageSize" :page-sizes="[20, 50, 100]" @current-change="handleOrderEventPageChange" @size-change="handleOrderEventPageSizeChange" /></div>
        </div>
      </div>
    </el-drawer>

    <el-dialog v-model="actionDialogVisible" :title="actionTitle(actionMode)" width="560px" destroy-on-close>
      <p class="action-order-name">{{ actionOrder?.number }} · {{ actionOrder?.title }}</p>
      <el-form label-position="top">
        <el-form-item v-if="actionMode === 'progress'" label="当前整改进度（必填）"><el-input v-model="actionForm.progress" type="textarea" :rows="5" /></el-form-item>
        <el-form-item v-if="actionMode === 'submit'" label="整改结果与自测情况（必填）"><el-input v-model="actionForm.remediation_result" type="textarea" :rows="6" /></el-form-item>
        <el-form-item v-if="actionMode === 'return'" label="退回原因（必填）"><el-input v-model="actionForm.return_reason" type="textarea" :rows="4" /></el-form-item>
        <el-form-item v-if="actionMode === 'return'" label="本次复测记录"><el-input v-model="actionForm.verification_result" type="textarea" :rows="3" /></el-form-item>
        <el-form-item v-if="actionMode === 'close'" label="复测通过结论（必填）"><el-input v-model="actionForm.verification_result" type="textarea" :rows="5" /></el-form-item>
        <el-form-item v-if="['issue', 'accept', 'progress', 'submit', 'cancel'].includes(actionMode)" :label="actionMode === 'cancel' ? '取消原因（必填）' : '处理说明'"><el-input v-model="actionForm.comment" type="textarea" :rows="3" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="actionDialogVisible = false">取消</el-button><el-button :type="actionButtonType(actionMode)" :loading="submitting" @click="submitAction">确认{{ actionTitle(actionMode) }}</el-button></template>
    </el-dialog>
  </section>
</template>

<style scoped>
.security-panel { overflow: hidden; }
.finding-summary-cards { display:grid; grid-template-columns:repeat(5, minmax(110px, 1fr)); gap:12px; margin:14px 0 10px; }
.finding-summary-dimensions { margin:0 0 18px; }.finding-summary-dimensions :deep(.el-collapse-item__header) { font-size:13px;color:var(--muted); }
.finding-dimension-cards { display:grid; grid-template-columns:repeat(auto-fit, minmax(150px, 1fr)); gap:12px; padding:4px 0 6px; }
.finding-summary-card { min-height:78px; padding:14px 16px; border:1px solid var(--line); border-radius:12px; background:#fff; display:flex; flex-direction:column; justify-content:space-between; box-shadow:0 2px 8px rgba(15,23,42,.03); }
.finding-summary-card span { color:var(--muted); font-size:13px; }
.finding-summary-card strong { color:var(--navy); font-size:27px; line-height:1; }
.finding-summary-card.tone-pending { border-color:#fed7aa; background:#fffaf3; }.finding-summary-card.tone-pending strong,.finding-summary-card.tone-high strong { color:#c2410c; }
.finding-summary-card.tone-critical { border-color:#fecaca; background:#fff5f5; }.finding-summary-card.tone-critical strong { color:#b91c1c; }
.finding-summary-card.tone-medium { border-color:#fde68a; background:#fffdf2; }.finding-summary-card.tone-medium strong { color:#b45309; }
.finding-summary-card.tone-low { border-color:#bbf7d0; background:#f5fff8; }.finding-summary-card.tone-low strong { color:#15803d; }
.work-order-saved-views { margin:-8px 0 0; padding-top:12px; border-top:1px dashed var(--line); }
.work-order-stat-list { justify-content: flex-start; margin: 12px 0 18px; padding-top: 12px; border-top: 1px dashed var(--line); }
.filter-bar { display: flex; flex-wrap: wrap; gap: 10px; margin: 4px 0 18px; }
.table-pagination { display: flex; justify-content: flex-end; margin-top: 18px; }
.detail-pagination { overflow-x: auto; padding-bottom: 2px; }
.heading-actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 10px; }
.import-toolbar { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; margin: 18px 0 10px; }
.import-toolbar input { max-width: 270px; }
.template-links { color: #64748b; font-size: 13px; line-height: 32px; }
.import-result { margin-top: 16px; padding: 16px; border-radius: 10px; background: #f8fafc; }
.import-errors { margin-top: 14px; }
.import-stat-grid { display: grid; grid-template-columns: repeat(5, minmax(90px, 1fr)); gap: 12px; }
.pending-candidate-entry { margin-top: 14px; }
.candidate-tab-badge { margin-left: 8px; }
.candidate-descriptions { margin-top: 18px; }
.candidate-description-text { white-space: pre-wrap; }
.candidate-link-form { margin-top: 18px; }
.candidate-option { display: flex; align-items: center; justify-content: space-between; gap: 16px; }
.candidate-option span { display: flex; align-items: center; gap: 6px; min-width: 0; }
.candidate-option small { color: #94a3b8; }
.mono-text { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.inline-tag { margin-left: 8px; }
.overdue-text, .danger-number { color: var(--el-color-danger); font-weight: 700; }
.action-order-name { margin: -4px 0 18px; padding: 12px 14px; border-radius: 10px; background: #f5f7fa; color: #475569; }
.work-order-detail { padding: 0 8px 28px; }
.detail-title { display: flex; align-items: flex-start; justify-content: space-between; gap: 20px; margin-bottom: 20px; }
.detail-title h2 { margin: 5px 0 0; font-size: 22px; }
.detail-block { margin-top: 24px; }
.detail-block h3 { margin: 0 0 10px; font-size: 15px; }
.detail-block-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
.detail-block-heading span { color: #94a3b8; font-size: 12px; }
.attachment-upload-box { display: grid; grid-template-columns: auto minmax(170px, 1fr) minmax(180px, 1.2fr) auto; align-items: center; gap: 8px; margin-bottom: 12px; padding: 12px; border-radius: 10px; background: #f8fafc; }
.attachment-upload-box input { min-width: 0; color: #475569; }
.attachment-description { display: block; margin-top: 3px; color: #94a3b8; }
.compact-empty { padding: 16px; }
.detail-block p { margin: 0; line-height: 1.75; white-space: pre-wrap; color: #475569; }
.warning-block { padding: 14px 16px; border: 1px solid #fed7aa; border-radius: 10px; background: #fff7ed; }
.event-actor { margin-left: 10px; color: #94a3b8; font-size: 12px; }
.risk-state { margin-top: 5px; color: #b45309; }
.risk-state.expired { color: var(--el-color-danger); font-weight: 700; }
.finding-detail { padding: 0 8px 28px; }
.finding-detail-tags { display: flex; gap: 8px; }
.detail-alert { margin-bottom: 18px; }
.risk-acceptance-card { padding: 16px; border: 1px solid #fde68a; border-radius: 12px; background: #fffbeb; }
.risk-acceptance-card.expired { border-color: #fecaca; background: #fef2f2; }
.review-comment { margin-top: 6px !important; color: #64748b !important; }
.finding-detail-actions { display: flex; flex-wrap: wrap; gap: 10px; margin-top: 24px; }
.risk-form { margin-top: 18px; }
.asset-link-tag { margin-left: 6px; }
@media (max-width: 900px) { .import-stat-grid { grid-template-columns: repeat(2, 1fr); } .heading-actions { width: 100%; } .attachment-upload-box { grid-template-columns: 1fr; } }
@media (max-width: 620px) { .finding-summary-cards { grid-template-columns:repeat(2, 1fr); } }
</style>

<style scoped>
.page-stack>.page-heading { margin-bottom:0; }.work-order-guide { color:var(--muted);font-size:12px; }.work-order-guide summary { cursor:pointer; }.work-order-guide p { line-height:1.7; }.security-panel { padding:8px 20px 20px; }.work-order-stat-list { margin:12px 0;padding:10px 0; }.work-order-saved-views { margin:0;padding-top:8px; }
</style>
