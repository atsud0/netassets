<script setup lang="ts">
import RowActions from '../components/RowActions.vue'
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'
import { useAuthStore } from '../stores/auth'

const auth = useAuthStore()

interface Organization { id: string; code: string; name: string; status: string }
interface BusinessSystem { id: string; code: string; name: string; organization_id: string }
interface NetworkArea { id: string; name: string; organization_id: string; status: string }
interface Candidate {
  id: string; asset_type: string; asset_value: string; normalized_value: string; name?: string
  organization_id?: string; organization_code?: string; organization_name?: string; source: string; source_reference?: string
  suggested_data: Record<string, unknown>; status: string; matched_asset_id?: string; review_note?: string
  created_at: string; reviewed_at?: string; finding_count?: number; highest_severity?: string
}
interface Asset { id: string; type: string; value: string; name?: string; organization_name: string }
interface ListResponse<T> { items: T[]; total: number; page: number; page_size: number }
interface BatchAcceptItemResult {
  candidate_id?: string; succeeded: boolean; already_settled?: boolean
  working_version_id?: string; organization_id?: string; code?: string; message?: string
}
interface BatchAcceptResult {
  total: number; succeeded: number; failed: number; skipped: number; items: BatchAcceptItemResult[]
}
interface SelfServicePolicy {
  organization_id: string; organization_name: string; allow_self_service: boolean
  allowed_asset_types: string[]; require_enabled_organization: boolean; updated_at?: string
}

const loading = ref(false)
const creating = ref(false)
const accepting = ref(false)
const createVisible = ref(false)
const acceptVisible = ref(false)
const duplicateVisible = ref(false)
const duplicateSubmitting = ref(false)
const duplicateTarget = ref<Candidate>()
const duplicateForm = reactive({ matched_asset_id: '', review_note: '' })
const selected = ref<Candidate>()
const filterStatus = ref('pending')
const candidates = ref<Candidate[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const organizations = ref<Organization[]>([])
const systems = ref<BusinessSystem[]>([])
const areas = ref<NetworkArea[]>([])
const assets = ref<Asset[]>([])
const createRef = ref<FormInstance>()
const acceptRef = ref<FormInstance>()
const createForm = reactive({ asset_type: 'domain', asset_value: '', name: '', organization_id: '', source: 'manual', source_reference: '', notes: '' })
const acceptForm = reactive({ organization_id: '', name: '', business_system_id: '', network_area_id: '', environment: 'production', network_scope: 'public', status: 'active', importance: 'normal', owner_name: '', notes: '', review_note: '' })
const createRules: FormRules = { asset_value: [{ required: true, message: '请输入资产值', trigger: 'blur' }], organization_id: [{ required: true, message: '请选择建议所属组织', trigger: 'change' }] }
const acceptRules: FormRules = { organization_id: [{ required: true, message: '请选择资产所属组织', trigger: 'change' }], environment: [{ required: true, message: '请选择环境', trigger: 'change' }], network_scope: [{ required: true, message: '请选择网络暴露范围', trigger: 'change' }] }
const enabledOrganizations = computed(() => organizations.value.filter((item) => item.status === 'enabled'))
const selectedOrganizationID = computed(() => acceptForm.organization_id || selected.value?.organization_id || '')
const selectedSystems = computed(() => systems.value.filter((item) => item.organization_id === selectedOrganizationID.value))
const selectedAreas = computed(() => areas.value.filter((item) => item.organization_id === selectedOrganizationID.value && item.status === 'enabled'))
const pendingCount = computed(() => candidates.value.filter((item) => item.status === 'pending').length)

// Batch confirmation: a scan often reports dozens of unknown addresses at once and
// reviewing them one dialog at a time is the slowest part of the queue.
const batchSubmitting = ref(false)
const batchVisible = ref(false)
const batchForm = reactive({ environment: 'production', network_scope: 'public', status: 'active', importance: 'normal', review_note: '' })
const batchResult = ref<BatchAcceptResult>()
const selectedRows = ref<Candidate[]>([])
const batchCandidates = computed(() => candidates.value.filter((item) => item.status === 'pending'))
function toggleRowSelection(rows: Candidate[]) { selectedRows.value = rows }
function clearRowSelection() { selectedRows.value = [] }
function openBatchAccept() {
  if (selectedRows.value.length === 0) return
  batchResult.value = undefined
  batchVisible.value = true
}
async function submitBatchAccept() {
  const items = selectedRows.value.filter((item) => item.organization_id).map((item) => ({
    candidate_id: item.id,
    input: {
      organization_id: item.organization_id,
      environment: batchForm.environment,
      network_scope: batchForm.network_scope,
      status: batchForm.status,
      importance: batchForm.importance,
      review_note: batchForm.review_note,
      name: item.name ?? '',
      notes: typeof item.suggested_data.notes === 'string' ? item.suggested_data.notes : '',
    },
  }))
  if (items.length === 0) { ElMessage.warning('选中的待确认资产没有归属组织，请先在单条确认中补充组织'); return }
  batchSubmitting.value = true
  try {
    const response = await apiClient.post<{ result: BatchAcceptResult }>('/asset-candidates/batch-accept', { items })
    batchResult.value = response.data.result
    const { succeeded, failed, skipped } = response.data.result
    if (failed === 0) { ElMessage.success(`批量确认完成：成功 ${succeeded} 条${skipped ? `，跳过 ${skipped} 条` : ''}`); batchVisible.value = false }
    else ElMessage.warning(`批量确认完成：成功 ${succeeded} 条，失败 ${failed} 条`)
    clearRowSelection()
    await refreshAfterMutation()
  } catch (error) { ElMessage.error(getAPIErrorMessage(error, '批量确认失败')) } finally { batchSubmitting.value = false }
}

const statusLabel: Record<string, string> = { pending: '待处理', accepted: '已入库', ignored: '已忽略', duplicate: '重复资产', invalid: '无效线索' }
const severityLabel: Record<string, string> = { critical: '严重', high: '高危', medium: '中危', low: '低危' }
const typeLabel: Record<string, string> = { domain: '域名', ip: 'IP 地址', url: 'URL', service: '网络服务', other: '其他' }

async function loadSupportingData() {
  try {
    const params = { page: 1, page_size: 100 }
    const [organizationResponse, systemResponse, areaResponse, assetResponse] = await Promise.all([
      apiClient.get<ListResponse<Organization>>('/organizations', { params }),
      apiClient.get<ListResponse<BusinessSystem>>('/business-systems', { params }),
      apiClient.get<ListResponse<NetworkArea>>('/network-areas', { params }),
      apiClient.get<ListResponse<Asset>>('/assets', { params }),
    ])
    organizations.value = organizationResponse.data.items
    systems.value = systemResponse.data.items
    areas.value = areaResponse.data.items
    assets.value = assetResponse.data.items
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取待确认资产基础数据失败'))
  }
}

async function load() {
  loading.value = true
  try {
    const params: Record<string, string | number> = { page: page.value, page_size: pageSize.value }
    if (filterStatus.value) params.status = filterStatus.value
    const response = await apiClient.get<ListResponse<Candidate>>('/asset-candidates', { params })
    candidates.value = response.data.items
    total.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取待确认资产失败'))
  } finally {
    loading.value = false
  }
}

async function refreshAfterMutation() {
  await Promise.all([loadSupportingData(), load()])
  if (candidates.value.length === 0 && total.value > 0 && page.value > 1) {
    page.value -= 1
    await load()
  }
}

function changePage(nextPage: number) {
  page.value = nextPage
  void load()
}

function changePageSize(nextPageSize: number) {
  pageSize.value = nextPageSize
  page.value = 1
  void load()
}
function resetCreate() { Object.assign(createForm, { asset_type: 'domain', asset_value: '', name: '', organization_id: enabledOrganizations.value[0]?.id ?? '', source: 'manual', source_reference: '', notes: '' }) }
function openCreate() { resetCreate(); createVisible.value = true }
async function submitCreate() {
  if (!createRef.value || !(await createRef.value.validate())) return
  creating.value = true
  try { await apiClient.post('/asset-candidates', { ...createForm, suggested_data: {} }); ElMessage.success('待确认资产已新增'); createVisible.value = false; await refreshAfterMutation() } catch (error) { ElMessage.error(getAPIErrorMessage(error, '新增待确认资产失败')) } finally { creating.value = false }
}
function openAccept(item: Candidate) {
  selected.value = item
  Object.assign(acceptForm, { organization_id: item.organization_id ?? '', name: item.name ?? '', business_system_id: '', network_area_id: '', environment: 'production', network_scope: 'public', status: 'active', importance: 'normal', owner_name: '', notes: typeof item.suggested_data.notes === 'string' ? item.suggested_data.notes : '', review_note: '' })
  acceptVisible.value = true
}
async function submitAccept() {
  if (!selected.value || !acceptRef.value || !(await acceptRef.value.validate())) return
  accepting.value = true
  try { await apiClient.post(`/asset-candidates/${selected.value.id}/accept`, acceptForm); ElMessage.success('已确认归属并生成未上报工作版本'); acceptVisible.value = false; await refreshAfterMutation() } catch (error) { ElMessage.error(getAPIErrorMessage(error, '确认入库失败')) } finally { accepting.value = false }
}
async function review(item: Candidate, action: 'ignore' | 'invalidate') {
  try {
    const { value } = await ElMessageBox.prompt(action === 'ignore' ? '可填写忽略原因（选填）' : '请填写无效原因（选填）', action === 'ignore' ? '忽略线索' : '标记无效', { inputPlaceholder: '处理说明', confirmButtonText: '确认', cancelButtonText: '取消' })
    await apiClient.post(`/asset-candidates/${item.id}/${action}`, { review_note: value ?? '' })
    ElMessage.success('处理完成'); await refreshAfterMutation()
  } catch (error: unknown) { if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error, '处理失败')) }
}
function openDuplicate(item: Candidate) {
  duplicateTarget.value = item
  duplicateForm.matched_asset_id = assets.value.find((asset) => asset.type === item.asset_type && asset.value.toLowerCase() === item.normalized_value.toLowerCase())?.id ?? ''
  duplicateForm.review_note = ''
  duplicateVisible.value = true
}
const duplicateAssets = computed(() => assets.value.filter((asset) => duplicateTarget.value && asset.type === duplicateTarget.value.asset_type && asset.value.toLowerCase() === duplicateTarget.value.normalized_value.toLowerCase()))
async function markDuplicate() {
  if (!duplicateTarget.value || !duplicateForm.matched_asset_id) { ElMessage.warning('请选择对应的正式资产'); return }
  duplicateSubmitting.value = true
  try { await apiClient.post(`/asset-candidates/${duplicateTarget.value.id}/mark-duplicate`, duplicateForm); ElMessage.success('已标记为重复资产'); duplicateVisible.value = false; await refreshAfterMutation() } catch (error) { ElMessage.error(getAPIErrorMessage(error, '处理失败')) } finally { duplicateSubmitting.value = false }
}
function formatDate(value?: string) { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—' }
watch(filterStatus, () => {
  page.value = 1
  void load()
})
onMounted(async () => {
  await loadSupportingData()
  await Promise.all([load(), loadPolicies()])
})

// Self-service delegation: headquarters decides which organizations may confirm
// their own discoveries. Organization users read the same list to see whether the
// queue is theirs to work on or still waiting for headquarters.
const policies = ref<SelfServicePolicy[]>([])
const policySaving = ref(false)
async function loadPolicies() {
  try {
    const response = await apiClient.get<ListResponse<SelfServicePolicy>>('/asset-candidates/self-service-policies')
    policies.value = response.data.items
  } catch {
    policies.value = []
  }
}
function policyFor(organizationID?: string) {
  return policies.value.find((item) => item.organization_id === organizationID)
}
async function savePolicy(row: SelfServicePolicy) {
  policySaving.value = true
  try {
    const response = await apiClient.put<{ item: SelfServicePolicy }>('/asset-candidates/self-service-policies', {
      organization_id: row.organization_id,
      allow_self_service: row.allow_self_service,
      allowed_asset_types: row.allowed_asset_types,
      require_enabled_organization: row.require_enabled_organization,
    })
    const index = policies.value.findIndex((item) => item.organization_id === row.organization_id)
    if (index >= 0) policies.value.splice(index, 1, response.data.item)
    ElMessage.success('自助确认策略已保存')
  } catch (error) { ElMessage.error(getAPIErrorMessage(error, '保存自助确认策略失败')) } finally { policySaving.value = false }
}
</script>

<template>
  <section>
    <div class="page-heading">
      <div>
      <h1>待确认资产</h1>
      <p>漏洞导入发现的未建档资产会先进入这里；确认归属后生成该组织的未上报工作版本，发布时自动入库并释放关联漏洞。</p>
      </div>
      <div class="button-row"><el-button @click="load">刷新</el-button><el-button type="primary" @click="openCreate">新增待确认资产</el-button></div>
    </div>
    <div class="panel candidate-filter-panel"><el-radio-group v-model="filterStatus"><el-radio-button label="pending">待处理</el-radio-button><el-radio-button label="">全部</el-radio-button><el-radio-button label="accepted">已入库</el-radio-button><el-radio-button label="ignored">已忽略</el-radio-button><el-radio-button label="duplicate">重复</el-radio-button><el-radio-button label="invalid">无效</el-radio-button></el-radio-group><div class="filter-stat-list"><div class="filter-stat"><span>待处理</span><strong>{{ pendingCount }}</strong></div></div></div>
    <div class="panel data-table-panel"><div v-if="batchCandidates.length" class="table-panel-heading"><span class="table-subtext">已选 {{ selectedRows.length }} 条待确认资产</span><el-button type="primary" :disabled="selectedRows.length === 0" @click="openBatchAccept">批量确认入库</el-button></div><el-table v-loading="loading" :data="candidates" stripe max-height="620" @selection-change="toggleRowSelection"><el-table-column type="selection" width="44" :selectable="(row: Candidate) => row.status === 'pending'"/><el-table-column label="资产线索" min-width="230"><template #default="{ row }"><strong>{{ row.asset_value }}</strong><div class="table-subtext">{{ typeLabel[row.asset_type] ?? row.asset_type }} · 标准化：{{ row.normalized_value }}</div></template></el-table-column><el-table-column label="建议归属" min-width="155"><template #default="{ row }">{{ row.organization_name || '待确认' }}<div class="table-subtext">{{ row.finding_count ? `关联漏洞 ${row.finding_count} 个` : '无关联漏洞' }}<span v-if="row.finding_count && row.highest_severity"> · 最高{{ severityLabel[row.highest_severity] || row.highest_severity }}</span></div><div v-if="row.organization_id && !policyFor(row.organization_id)?.allow_self_service" class="table-subtext">需集团确认</div></template></el-table-column><el-table-column label="来源" min-width="145"><template #default="{ row }">{{ row.source === 'api' ? 'API' : row.source === 'finding_import' ? '漏洞导入' : '手工录入' }}<div v-if="row.source_reference" class="table-subtext">{{ row.source_reference }}</div></template></el-table-column><el-table-column label="状态" width="120"><template #default="{ row }"><el-tag :type="row.status === 'pending' ? 'warning' : row.status === 'accepted' ? 'success' : 'info'">{{ statusLabel[row.status] ?? row.status }}</el-tag><span v-if="row.review_note" class="list-secondary">{{ row.review_note }}</span></template></el-table-column><el-table-column label="登记时间" width="180"><template #default="{ row }">{{ formatDate(row.created_at) }}</template></el-table-column><el-table-column label="操作" width="88" fixed="right" align="right" header-align="right" class-name="table-action-column"><template #default="{ row }"><RowActions :direct="row.status !== 'pending'" :label="`操作：${row.name || row.title || row.code || row.display_name || row.id}`"><template v-if="row.status === 'pending'"><el-button v-if="auth.isGroupAdmin || policyFor(row.organization_id)?.allow_self_service" link type="primary" @click="openAccept(row)">确认入库</el-button><el-button link @click="openDuplicate(row)">标记重复</el-button><el-button link @click="review(row, 'ignore')">忽略</el-button><el-button link type="danger" @click="review(row, 'invalidate')">无效</el-button></template><span v-else class="table-subtext">—</span></RowActions></template></el-table-column></el-table><div class="table-pagination"><el-pagination background layout="total, sizes, prev, pager, next" :total="total" :current-page="page" :page-size="pageSize" :page-sizes="[20, 50, 100]" @current-change="changePage" @size-change="changePageSize"/></div></div>

    <el-dialog v-model="createVisible" title="新增待确认资产" width="620px" destroy-on-close><el-alert type="info" :closable="false" title="待确认资产不会直接进入正式台账；请在核实后补充环境、范围、重要程度等信息后入库。" show-icon class="dialog-alert" /><el-form ref="createRef" :model="createForm" :rules="createRules" label-width="110px"><el-form-item label="资产类型" prop="asset_type"><el-select v-model="createForm.asset_type"><el-option label="域名" value="domain"/><el-option label="IP 地址" value="ip"/><el-option label="URL" value="url"/><el-option label="网络服务" value="service"/><el-option label="其他" value="other"/></el-select></el-form-item><el-form-item label="资产值" prop="asset_value"><el-input v-model="createForm.asset_value" placeholder="如 portal.example.com 或 https://portal.example.com"/></el-form-item><el-form-item label="资产名称"><el-input v-model="createForm.name" placeholder="选填"/></el-form-item><el-form-item label="建议所属组织" prop="organization_id"><el-select v-model="createForm.organization_id" filterable><el-option v-for="org in enabledOrganizations" :key="org.id" :label="org.name" :value="org.id"/></el-select></el-form-item><el-form-item label="来源"><el-select v-model="createForm.source"><el-option label="手工录入" value="manual"/><el-option label="API 导入" value="api"/></el-select></el-form-item><el-form-item label="来源参考"><el-input v-model="createForm.source_reference" placeholder="工单、邮件或外部线索编号（选填）"/></el-form-item><el-form-item label="备注"><el-input v-model="createForm.notes" type="textarea" :rows="3"/></el-form-item></el-form><template #footer><el-button @click="createVisible=false">取消</el-button><el-button type="primary" :loading="creating" @click="submitCreate">保存线索</el-button></template></el-dialog>

    <el-dialog v-model="duplicateVisible" title="标记重复资产" width="560px" destroy-on-close>
      <el-alert type="warning" :closable="false" show-icon class="dialog-alert" title="请选择网络资产台账中已存在的对应资产。仅展示类型和标准化值均一致的资产。"/>
      <el-form label-width="110px"><el-form-item label="待确认资产"><span>{{ duplicateTarget?.asset_value }}</span></el-form-item><el-form-item label="对应正式资产" required><el-select v-model="duplicateForm.matched_asset_id" filterable placeholder="请选择正式资产"><el-option v-for="asset in duplicateAssets" :key="asset.id" :value="asset.id" :label="`${asset.value}${asset.name ? `（${asset.name}）` : ''} - ${asset.organization_name}`"/></el-select><div v-if="!duplicateAssets.length" class="form-tip">未找到同类型、同标准化值的正式资产，请先核实后再处理。</div></el-form-item><el-form-item label="处理说明"><el-input v-model="duplicateForm.review_note" type="textarea" :rows="2" placeholder="选填"/></el-form-item></el-form>
      <template #footer><el-button @click="duplicateVisible=false">取消</el-button><el-button type="primary" :loading="duplicateSubmitting" :disabled="!duplicateAssets.length" @click="markDuplicate">确认标记</el-button></template>
    </el-dialog>

    <div v-if="auth.isGroupAdmin" class="panel">
      <div class="table-panel-heading"><div><strong>组织自助确认策略</strong><span>开启后该组织运维人员可自行确认本组织资产线索，默认关闭</span></div></div>
      <el-table :data="policies" stripe size="small"><el-table-column label="组织" min-width="200" prop="organization_name"/><el-table-column label="自助确认" width="120"><template #default="{ row }"><el-switch v-model="row.allow_self_service"/></template></el-table-column><el-table-column label="允许资产类型" min-width="240"><template #default="{ row }"><el-select v-model="row.allowed_asset_types" multiple clearable placeholder="留空表示全部类型"><el-option label="IP 地址" value="ip"/><el-option label="域名" value="domain"/><el-option label="URL" value="url"/><el-option label="主机名" value="hostname"/></el-select></template></el-table-column><el-table-column label="要求组织启用" width="140"><template #default="{ row }"><el-switch v-model="row.require_enabled_organization"/></template></el-table-column><el-table-column label="操作" width="90" align="right"><template #default="{ row }"><el-button link type="primary" :loading="policySaving" @click="savePolicy(row)">保存</el-button></template></el-table-column></el-table>
    </div>
    <el-dialog v-model="batchVisible" title="批量确认入库" width="720px" destroy-on-close>
      <el-alert type="info" :closable="false" show-icon class="dialog-alert" title="批量确认将为每条选中的待确认资产生成未上报工作版本；缺少归属组织的线索会被跳过，请单独处理。"/>
      <el-form :model="batchForm" label-width="118px"><el-form-item label="环境"><el-select v-model="batchForm.environment"><el-option label="生产" value="production"/><el-option label="预发布" value="staging"/><el-option label="测试" value="testing"/><el-option label="开发" value="development"/><el-option label="办公" value="office"/><el-option label="其他" value="other"/></el-select></el-form-item><el-form-item label="网络暴露范围"><el-select v-model="batchForm.network_scope"><el-option label="公网" value="public"/><el-option label="内网" value="private"/><el-option label="未知" value="unknown"/></el-select></el-form-item><el-form-item label="资产状态"><el-select v-model="batchForm.status"><el-option label="启用" value="active"/><el-option label="待确认" value="pending"/><el-option label="停用" value="disabled"/></el-select></el-form-item><el-form-item label="重要程度"><el-select v-model="batchForm.importance"><el-option label="一般" value="normal"/><el-option label="重要" value="important"/><el-option label="核心" value="core"/></el-select></el-form-item><el-form-item label="审核说明"><el-input v-model="batchForm.review_note" type="textarea" :rows="2" placeholder="选填，写入审计日志"/></el-form-item></el-form>
      <el-table v-if="batchResult" :data="batchResult.items" size="small" max-height="260" class="batch-result-table"><el-table-column label="结果" width="110"><template #default="{ row }"><el-tag :type="row.succeeded ? 'success' : row.already_settled ? 'info' : 'danger'">{{ row.succeeded ? '已确认' : row.already_settled ? '已处理' : '失败' }}</el-tag></template></el-table-column><el-table-column label="说明" min-width="260"><template #default="{ row }">{{ row.message || (row.succeeded ? '已生成未上报工作版本' : '') }}</template></el-table-column></el-table>
      <template #footer><el-button @click="batchVisible = false">关闭</el-button><el-button type="primary" :loading="batchSubmitting" @click="submitBatchAccept">确认 {{ selectedRows.length }} 条</el-button></template>
    </el-dialog>
    <el-dialog v-model="acceptVisible" title="确认并入库" width="700px" destroy-on-close><el-alert v-if="selected" type="success" :closable="false" show-icon class="dialog-alert" :title="`确认 ${selected.asset_value} 归属并生成未上报工作版本，发布后进入正式网络资产台账`"/><el-form ref="acceptRef" :model="acceptForm" :rules="acceptRules" label-width="118px"><el-form-item label="资产所属组织" prop="organization_id"><el-select v-model="acceptForm.organization_id" filterable placeholder="请选择组织"><el-option v-for="org in enabledOrganizations" :key="org.id" :label="org.name" :value="org.id"/></el-select></el-form-item><el-form-item label="资产名称"><el-input v-model="acceptForm.name" placeholder="选填"/></el-form-item><el-form-item label="关联业务系统"><el-select v-model="acceptForm.business_system_id" clearable filterable><el-option v-for="system in selectedSystems" :key="system.id" :label="`${system.name}（${system.code}）`" :value="system.id"/></el-select></el-form-item><el-form-item label="环境" prop="environment"><el-select v-model="acceptForm.environment"><el-option label="生产" value="production"/><el-option label="预发布" value="staging"/><el-option label="测试" value="testing"/><el-option label="开发" value="development"/><el-option label="办公" value="office"/><el-option label="其他" value="other"/></el-select></el-form-item><el-form-item label="网络暴露范围" prop="network_scope"><el-select v-model="acceptForm.network_scope"><el-option label="公网" value="public"/><el-option label="内网" value="private"/><el-option label="未知" value="unknown"/></el-select></el-form-item><el-form-item label="网络区域"><el-select v-model="acceptForm.network_area_id" clearable><el-option v-for="area in selectedAreas" :key="area.id" :label="area.name" :value="area.id"/></el-select></el-form-item><el-form-item label="资产状态"><el-select v-model="acceptForm.status"><el-option label="启用" value="active"/><el-option label="待确认" value="pending"/><el-option label="停用" value="disabled"/></el-select></el-form-item><el-form-item label="重要程度"><el-select v-model="acceptForm.importance"><el-option label="一般" value="normal"/><el-option label="重要" value="important"/><el-option label="核心" value="core"/></el-select></el-form-item><el-form-item label="负责人"><el-input v-model="acceptForm.owner_name" placeholder="选填"/></el-form-item><el-form-item label="资产备注"><el-input v-model="acceptForm.notes" type="textarea" :rows="2"/></el-form-item><el-form-item label="审核说明"><el-input v-model="acceptForm.review_note" type="textarea" :rows="2" placeholder="选填，写入审计日志"/></el-form-item></el-form><template #footer><el-button @click="acceptVisible=false">取消</el-button><el-button type="primary" :loading="accepting" @click="submitAccept">确认入库</el-button></template></el-dialog>
  </section>
</template>

<style scoped>
.candidate-filter-panel { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 12px; margin-bottom: 16px; }
.table-pagination { display:flex; justify-content:flex-end; margin-top:18px; }
</style>
