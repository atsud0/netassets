<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox, type FormInstance, type FormItemRule, type FormRules } from 'element-plus'
import { apiClient, getAPIErrorMessage, isEditConflictError, describeEditConflict } from '../api/client'
import { loadOptions, severityLabels } from '../api/remediation'
import { useAuthStore } from '../stores/auth'
import { FINDING_STATUS_RISK_ACCEPTED, findingStatusLabels } from '../constants/findingStatus'
import MarkdownEditor from '../components/MarkdownEditor.vue'

interface Organization { id: string; name: string; status: string; type: string }
interface Asset { id: string; name?: string; value: string; organization_id: string; business_system_id?: string; status: string }
interface BusinessSystem { id: string; code: string; name: string; organization_id: string; status: string }
interface TestBatch { id: string; code: string; name: string; status: string; scope_assets?: { asset_id?: string }[] }
interface Finding {
  id: string; code: string; title: string; severity: string; status: string; assignee_organization_id: string
  assignee_organization_name?: string; asset_id?: string; asset_value?: string; business_system_id?: string; business_system_name?: string
  test_batch_id?: string; test_batch_name?: string; vulnerability_type?: string; description: string; evidence?: string
  remediation_advice?: string; source: string; discovered_at: string; updated_at: string
}
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const formRef = ref<FormInstance>()
const loading = ref(false)
const saving = ref(false)
const loadError = ref('')
const saveError = ref('')
const scopeError = ref('')
const scopeLoading = ref(false)
const finding = ref<Finding>()
const organizations = ref<Organization[]>([])
const assets = ref<Asset[]>([])
const systems = ref<BusinessSystem[]>([])
const batches = ref<TestBatch[]>([])
const scopeAssetIDs = ref<string[]>([])
const baseline = ref('')
const form = reactive({
  title: '', severity: '', status: '', assignee_organization_id: '', asset_id: '', business_system_id: '', test_batch_id: '',
  vulnerability_type: '', description: '', evidence: '', remediation_advice: '', source: '', discovered_at: '' as Date | string | null,
})
const dirty = computed(() => Boolean(baseline.value) && JSON.stringify(form) !== baseline.value)
const returnPath = computed(() => {
  const value = route.query.returnTo
  if (typeof value !== 'string') return '/security-work-orders?tab=findings'
  try {
    const target = new URL(value, 'https://netassets.invalid')
    if (value.startsWith('/') && target.origin === 'https://netassets.invalid' && target.pathname === '/security-work-orders') {
      return target.pathname + target.search + target.hash
    }
  } catch {}
  return '/security-work-orders?tab=findings'
})
const frozenBatchScope = computed(() => Boolean(form.test_batch_id) && !auth.canManageSecurityTests)
const organizationOptions = computed(() => {
  const options = organizations.value.filter(item => item.status === 'enabled' && item.type !== 'group').map(item => ({ value: item.id, label: item.name }))
  if (form.assignee_organization_id && !options.some(item => item.value === form.assignee_organization_id)) {
    options.push({ value: form.assignee_organization_id, label: finding.value?.assignee_organization_name || form.assignee_organization_id })
  }
  return options
})
const scopedAssets = computed(() => assets.value.filter(item => item.status !== 'archived' && (form.test_batch_id
  ? scopeAssetIDs.value.includes(item.id)
  : item.organization_id === form.assignee_organization_id)))
const scopedSystems = computed(() => systems.value.filter(item => item.status !== 'archived' && item.organization_id === form.assignee_organization_id))
const usableBatches = computed(() => batches.value.filter(item => ['authorized', 'in_progress', 'completed'].includes(item.status)))

function textRule(limit: number, label: string, required = false): FormItemRule {
  return { trigger: 'blur', required, validator: (_rule, value, callback) => {
    const text = String(value ?? '').trim()
    if (required && !text) return callback(new Error(`请输入${label}`))
    if (Array.from(text).length > limit) return callback(new Error(`${label}最多 ${limit} 字`))
    callback()
  } }
}
const rules: FormRules = {
  title: [textRule(255, '漏洞标题', true)], vulnerability_type: [textRule(128, '漏洞类型')],
  description: [textRule(20000, '漏洞描述', true)], evidence: [textRule(30000, '漏洞证据')], remediation_advice: [textRule(20000, '修复建议')],
  severity: [{ required: true, message: '请选择严重等级', trigger: 'change' }],
  assignee_organization_id: [{ required: true, message: '请选择责任组织', trigger: 'change' }],
  discovered_at: [{ required: true, message: '请选择发现时间', trigger: 'change' }],
}
let loadSequence = 0
let scopeSequence = 0
async function loadFinding() {
  const request = ++loadSequence
  ++scopeSequence
  finding.value = undefined
  baseline.value = ''
  loadError.value = ''
  saveError.value = ''
  scopeError.value = ''
  scopeLoading.value = false
  scopeAssetIDs.value = []
  if (!auth.canManageSecurityFindings) {
    loadError.value = '当前账号没有编辑安全发现的权限'
    loading.value = false
    return
  }
  loading.value = true
  try {
    const id = String(route.params.id ?? '')
    const [response, organizationOptions, assetOptions, systemOptions, batchOptions] = await Promise.all([
      apiClient.get<{ item: Finding }>(`/security-findings/${encodeURIComponent(id)}`),
      loadOptions<Organization>('/security-reference/organizations'),
      loadOptions<Asset>('/security-reference/assets'),
      loadOptions<BusinessSystem>('/security-reference/business-systems'),
      auth.canManageSecurityTests ? loadOptions<TestBatch>('/security-test-batches') : Promise.resolve([] as TestBatch[]),
    ])
    if (request !== loadSequence) return
    const item = response.data.item
    let initialScope: string[] = []
    if (item.test_batch_id && auth.canManageSecurityTests) {
      const { data } = await apiClient.get<{ item: TestBatch }>(`/security-test-batches/${item.test_batch_id}`)
      initialScope = (data.item.scope_assets ?? []).flatMap(asset => asset.asset_id ? [asset.asset_id] : [])
    }
    if (request !== loadSequence) return
    organizations.value = organizationOptions
    assets.value = assetOptions
    systems.value = systemOptions
    batches.value = batchOptions
    scopeAssetIDs.value = initialScope
    Object.assign(form, {
      title: item.title, severity: item.severity, status: item.status, assignee_organization_id: item.assignee_organization_id,
      asset_id: item.asset_id ?? '', business_system_id: item.business_system_id ?? '', test_batch_id: item.test_batch_id ?? '',
      vulnerability_type: item.vulnerability_type ?? '', description: item.description, evidence: item.evidence ?? '',
      remediation_advice: item.remediation_advice ?? '', source: item.source, discovered_at: new Date(item.discovered_at),
    })
    finding.value = item
    baseline.value = JSON.stringify(form)
  } catch (error) {
    if (request === loadSequence) loadError.value = getAPIErrorMessage(error, '无法加载安全发现，请确认记录存在且有访问权限后重试')
  } finally {
    if (request === loadSequence) loading.value = false
  }
}
function changeOrganization() {
  if (!scopedAssets.value.some(item => item.id === form.asset_id)) form.asset_id = ''
  if (!scopedSystems.value.some(item => item.id === form.business_system_id)) form.business_system_id = ''
}
function changeAsset() {
  if (!form.test_batch_id || !form.asset_id) return
  const asset = assets.value.find(item => item.id === form.asset_id)
  if (asset) {
    form.assignee_organization_id = asset.organization_id
    form.business_system_id = asset.business_system_id ?? ''
  }
}
async function changeBatch() {
  const request = ++scopeSequence
  const batchID = form.test_batch_id
  scopeAssetIDs.value = []
  scopeError.value = ''
  scopeLoading.value = Boolean(batchID)
  if (!batchID) return
  try {
    const { data } = await apiClient.get<{ item: TestBatch }>(`/security-test-batches/${batchID}`)
    if (request !== scopeSequence) return
    scopeAssetIDs.value = (data.item.scope_assets ?? []).flatMap(item => item.asset_id ? [item.asset_id] : [])
    if (!scopeAssetIDs.value.includes(form.asset_id)) form.asset_id = ''
    changeAsset()
  } catch (error) {
    if (request === scopeSequence) scopeError.value = getAPIErrorMessage(error, '读取批次授权范围失败，请重试或清除批次')
  } finally {
    if (request === scopeSequence) scopeLoading.value = false
  }
}
async function allowLeave() {
  if (saving.value) {
    ElMessage.warning('正在保存，请稍候')
    return false
  }
  if (!dirty.value) return true
  try {
    await ElMessageBox.confirm('有尚未保存的修改，离开后将丢失。确定离开吗？', '未保存的修改', {
      confirmButtonText: '放弃修改并离开', cancelButtonText: '继续编辑', type: 'warning',
    })
    return true
  } catch { return false }
}
onBeforeRouteLeave(allowLeave)
onBeforeRouteUpdate(async (to, from) => to.params.id === from.params.id || await allowLeave())
function beforeUnload(event: BeforeUnloadEvent) {
  if (!dirty.value && !saving.value) return
  event.preventDefault()
  event.returnValue = ''
}
window.addEventListener('beforeunload', beforeUnload)
onBeforeUnmount(() => {
  ++loadSequence
  ++scopeSequence
  window.removeEventListener('beforeunload', beforeUnload)
})
watch(() => route.params.id, () => { void loadFinding() }, { immediate: true })
async function save() {
  if (saving.value || loading.value || !finding.value || !auth.canManageSecurityFindings || scopeLoading.value || scopeError.value) return
  if (!formRef.value || !(await formRef.value.validate().catch(() => false))) return
  if (saving.value) return
  if (!form.asset_id && !form.business_system_id) return ElMessage.warning('请至少关联一项网络资产或业务系统')
  if (form.test_batch_id && !form.asset_id) return ElMessage.warning('关联测试批次时必须选择批次范围内的网络资产')
  const discoveredAt = form.discovered_at ? new Date(form.discovered_at) : null
  if (!discoveredAt || Number.isNaN(discoveredAt.getTime())) return ElMessage.warning('请选择有效的发现时间')
  saving.value = true
  saveError.value = ''
  try {
    await apiClient.patch(`/security-findings/${finding.value.id}`, {
      ...form, discovered_at: discoveredAt.toISOString(), expected_updated_at: finding.value.updated_at,
    })
    baseline.value = JSON.stringify(form)
    saving.value = false
    ElMessage.success('安全发现已更新')
    await router.push(returnPath.value)
  } catch (error) {
    saveError.value = isEditConflictError(error) ? describeEditConflict('安全发现已被其他人更新') : getAPIErrorMessage(error, '更新安全发现失败，输入内容已保留')
    ElMessage.error(saveError.value)
  } finally { saving.value = false }
}
async function reload() {
  if (await allowLeave()) await loadFinding()
}
</script>

<template>
  <section class="page-shell finding-edit-page" aria-labelledby="finding-edit-title" v-loading="loading">
    <header class="page-header">
      <div><h1 id="finding-edit-title">编辑安全发现</h1><p v-if="finding" class="edit-context">{{ finding.code }} · {{ finding.title }}</p></div>
      <div class="edit-actions">
        <el-button :disabled="saving" @click="router.push(returnPath)">取消／返回</el-button>
        <el-button v-if="finding && auth.canManageSecurityFindings" type="primary" :loading="saving" :disabled="loading || scopeLoading || Boolean(scopeError)" @click="save">保存修改</el-button>
      </div>
    </header>
    <el-alert v-if="loadError" :title="loadError" type="error" :closable="false" show-icon />
    <el-button v-if="loadError && auth.canManageSecurityFindings" class="retry-button" @click="loadFinding">重新加载</el-button>
    <template v-if="finding">
      <el-alert v-if="saveError" :title="saveError" type="error" :closable="false" show-icon class="edit-alert"><el-button link type="primary" @click="reload">重新加载最新内容</el-button></el-alert>
      <el-alert v-if="frozenBatchScope" title="当前账号可编辑发现内容；测试批次及其关联范围保持原值，修改范围需要安全测试管理权限。" type="info" :closable="false" class="edit-alert" />
      <el-alert v-if="scopeError" :title="scopeError" type="error" :closable="false" class="edit-alert"><el-button link type="primary" @click="changeBatch">重试读取范围</el-button></el-alert>
      <el-form ref="formRef" :model="form" :rules="rules" label-position="top" :disabled="saving || !auth.canManageSecurityFindings" class="edit-form">
        <section class="panel-card edit-basics" aria-label="安全发现基本信息">
          <div class="edit-grid">
            <el-form-item label="漏洞标题" prop="title"><el-input v-model="form.title" /></el-form-item>
            <el-form-item label="严重等级" prop="severity"><el-select v-model="form.severity"><el-option v-for="(label, value) in severityLabels" :key="value" :label="label" :value="value" /></el-select></el-form-item>
            <el-form-item label="测试批次"><el-select v-model="form.test_batch_id" clearable filterable :disabled="!auth.canManageSecurityTests" @change="changeBatch"><el-option v-for="item in usableBatches" :key="item.id" :label="`${item.name}（${item.code}）`" :value="item.id" /><el-option v-if="form.test_batch_id && !usableBatches.some(item => item.id === form.test_batch_id)" :value="form.test_batch_id" :label="finding.test_batch_name || form.test_batch_id" /></el-select></el-form-item>
            <el-form-item label="责任组织" prop="assignee_organization_id"><el-select-v2 v-model="form.assignee_organization_id" :options="organizationOptions" filterable :disabled="Boolean(form.test_batch_id)" @change="changeOrganization" /></el-form-item>
            <el-form-item label="漏洞类型" prop="vulnerability_type"><el-input v-model="form.vulnerability_type" placeholder="例如：越权访问、SQL 注入" /></el-form-item>
            <el-form-item label="关联网络资产"><el-select v-model="form.asset_id" clearable filterable :disabled="frozenBatchScope || scopeLoading || Boolean(scopeError)" @change="changeAsset"><el-option v-for="item in scopedAssets" :key="item.id" :label="`${item.name || item.value}（${item.value}）`" :value="item.id" /><el-option v-if="form.asset_id && !scopedAssets.some(item => item.id === form.asset_id)" :value="form.asset_id" :label="finding.asset_value || form.asset_id" /></el-select></el-form-item>
            <el-form-item label="关联业务系统"><el-select v-model="form.business_system_id" clearable filterable :disabled="frozenBatchScope"><el-option v-for="item in scopedSystems" :key="item.id" :label="`${item.name}（${item.code}）`" :value="item.id" /><el-option v-if="form.business_system_id && !scopedSystems.some(item => item.id === form.business_system_id)" :value="form.business_system_id" :label="finding.business_system_name || form.business_system_id" /></el-select></el-form-item>
            <el-form-item label="发现时间" prop="discovered_at"><el-date-picker v-model="form.discovered_at" type="datetime" /></el-form-item>
            <el-form-item label="发现状态"><el-select v-model="form.status" :disabled="form.status === FINDING_STATUS_RISK_ACCEPTED"><el-option v-for="(label, value) in findingStatusLabels" :key="value" :label="label" :value="value" :disabled="value === FINDING_STATUS_RISK_ACCEPTED" /></el-select><small v-if="form.status === FINDING_STATUS_RISK_ACCEPTED">风险接受状态请通过详情中的风险操作维护</small></el-form-item>
          </div>
        </section>
        <section class="panel-card edit-content" aria-label="安全发现 Markdown 内容">
          <el-form-item label="漏洞描述" prop="description"><MarkdownEditor v-model="form.description" label="漏洞描述" :disabled="saving || !auth.canManageSecurityFindings" /></el-form-item>
          <el-form-item label="验证证据" prop="evidence"><MarkdownEditor v-model="form.evidence" label="验证证据" :disabled="saving || !auth.canManageSecurityFindings" /><small>请脱敏填写请求路径、响应现象或证据位置，不要录入明文口令。</small></el-form-item>
          <el-form-item label="整改建议" prop="remediation_advice"><MarkdownEditor v-model="form.remediation_advice" label="整改建议" :disabled="saving || !auth.canManageSecurityFindings" /></el-form-item>
        </section>
        <div class="edit-actions edit-footer"><span v-if="dirty" role="status">有未保存的修改</span><el-button :disabled="saving" @click="router.push(returnPath)">取消／返回</el-button><el-button type="primary" :loading="saving" :disabled="scopeLoading || Boolean(scopeError)" @click="save">保存修改</el-button></div>
      </el-form>
    </template>
  </section>
</template>

<style scoped>
.finding-edit-page { max-width: 1600px; margin-inline: auto; min-width: 0; }
.page-header { align-items: flex-start; gap: 16px; }
.edit-context { color: var(--text-secondary, #64748b); overflow-wrap: anywhere; margin: 8px 0 0; }
.edit-actions { display: flex; align-items: center; justify-content: flex-end; gap: 12px; flex-wrap: wrap; }
.edit-actions .el-button + .el-button { margin-left: 0; }
.edit-form { min-width: 0; }
.edit-basics, .edit-content { padding: 24px; margin-top: 20px; min-width: 0; }
.edit-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 0 24px; }
.edit-grid :deep(.el-select), .edit-grid :deep(.el-select-v2), .edit-grid :deep(.el-date-editor) { width: 100%; }
.edit-content :deep(.el-form-item__content) { min-width: 0; display: block; }
.edit-alert { margin-bottom: 16px; }
.retry-button { margin-top: 16px; }
.edit-footer { margin: 20px 0; }
.edit-footer span, small { color: var(--text-secondary, #64748b); }
@media (max-width: 768px) {
  .page-header { flex-direction: column; }
  .edit-grid { grid-template-columns: minmax(0, 1fr); }
  .edit-basics, .edit-content { padding: 16px; }
  .edit-actions { justify-content: flex-start; }
}
</style>
