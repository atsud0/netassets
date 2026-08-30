<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage, type FormInstance } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'

interface Field {
  id: string
  resource_type: string
  field_key: string
  field_name: string
  description?: string
  field_type: string
  options: string[]
  is_required: boolean
  is_enabled: boolean
  sort_order: number
}
type ConditionOperator = 'eq' | 'ne' | 'in' | 'not_in' | 'empty' | 'not_empty' | 'is_true' | 'is_false'
interface RuleCondition { field: string; operator: ConditionOperator; values: string[] }
interface QualityRule { id: string; rule_name: string; description?: string; resource_type: string; rule_type: 'required' | 'stale' | 'expiry'; target_field: string; severity: string; stale_after_days?: number; expiry_warning_days?: number; conditions: RuleCondition[]; is_enabled: boolean; sort_order: number }
interface ConditionFieldOption { value: string; label: string; kind: 'enum' | 'text' | 'uuid' | 'presence' | 'boolean' | 'multiselect'; options?: Array<[string, string]> }
interface Organization { id: string; code: string; name: string; status: string }
interface NetworkArea { id: string; organization_code: string; organization_name: string; name: string; code?: string; status: string }
interface ScanIssue {
  rule_id: string
  rule_name: string
  rule_type: 'required' | 'stale' | 'expiry'
  severity: 'error' | 'warning'
  stale_after_days?: number
  expiry_warning_days?: number
  resource_type: 'asset' | 'business_system'
  resource_id: string
  resource_name: string
  organization_id: string
  organization_code: string
  organization_name: string
  target_field: string
}
interface ScanSummary {
  asset_count: number
  system_count: number
  enabled_rule_count: number
  total_issue_count: number
  error_count: number
  warning_count: number
  returned_count: number
  truncated: boolean
  external_observation_candidates: number
  external_observation_processed: number
  external_observation_successes: number
  external_observation_failures: number
  external_observation_unsupported: number
  external_observation_deferred: number
}
interface ScanResult { summary: ScanSummary; issues: ScanIssue[] }
interface ListResponse<T> { items: T[]; total: number; page: number; page_size: number }

type QualityTarget = Pick<ScanIssue, 'resource_type' | 'target_field'> & { rule_type?: 'required' | 'stale' | 'expiry'; stale_after_days?: number; expiry_warning_days?: number }

const activeTab = ref('asset')
const resourceType = ref('asset')
const loading = ref(false)
const saving = ref(false)
const visible = ref(false)
const editingID = ref('')
const formRef = ref<FormInstance>()
const items = ref<Field[]>([])
const fieldPage = ref(1)
const fieldPageSize = ref(20)
const fieldTotal = ref(0)
const form = reactive({
  resource_type: 'asset', field_key: '', field_name: '', description: '', field_type: 'text', optionsText: '',
  is_required: false, is_enabled: true, sort_order: 0,
})
const types = [['text', '单行文本'], ['textarea', '多行文本'], ['number', '数字'], ['date', '日期'], ['select', '单选'], ['multiselect', '多选'], ['boolean', '是/否']]
const ruleItems = ref<QualityRule[]>([])
const rulePage = ref(1)
const rulePageSize = ref(20)
const ruleTotal = ref(0)
const ruleLoading = ref(false)
const ruleVisible = ref(false)
const editingRuleID = ref('')
const ruleFormRef = ref<FormInstance>()
const ruleForm = reactive({ rule_name: '', description: '', resource_type: 'asset', rule_type: 'required' as 'required' | 'stale' | 'expiry', target_field: 'owner_name', severity: 'error', stale_after_days: 0, expiry_warning_days: 30, conditions: [] as RuleCondition[], is_enabled: true, sort_order: 0 })
const qualityCustomFields = ref<Field[]>([])
const ruleOptionsLoaded = ref(false)
const organizations = ref<Organization[]>([])
const networkAreas = ref<NetworkArea[]>([])
const scanLoading = ref(false)
const scanResult = ref<ScanResult | null>(null)
const scanFilters = reactive({ organization_id: '', resource_type: '', severity: '' })
const builtinTargets: Record<string, Array<[string, string]>> = {
  asset: [['value', '资产值'], ['name', '资产名称'], ['network_scope', '网络范围'], ['network_area_id', '网络区域'], ['owner_name', '负责人'], ['business_system_id', '关联业务系统'], ['importance', '重要性']],
  business_system: [['code', '系统编码'], ['name', '系统名称'], ['owner_name', '负责人'], ['owner_contact', '负责人联系方式'], ['system_level', '系统等级'], ['internet_exposure', '互联网暴露情况'], ['contains_personal_information', '是否含个人信息'], ['contains_sensitive_personal_information', '是否含敏感个人信息'], ['data_volume_over_100k', '数据量是否超过10万条'], ['contains_important_data', '是否含重要数据'], ['contains_core_data', '是否含核心数据']],
}
const ruleTargetOptions = computed<Array<[string, string]>>(() => {
  if (ruleForm.rule_type === 'stale') return [['updated_at', '最后更新时间']]
  if (ruleForm.rule_type === 'expiry') return [
    ['expires_at', '资产生命周期到期时间'],
    ['domain_registration_expires_at', '域名注册到期时间'],
    ['tls_certificate_expires_at', 'TLS 证书到期时间'],
  ]
  return [
    ...(builtinTargets[ruleForm.resource_type] ?? []),
    ...qualityCustomFields.value.filter((field) => field.resource_type === ruleForm.resource_type && field.is_enabled).map((field) => [`custom:${field.field_key}`, `自定义字段：${field.field_name}`] as [string, string]),
  ]
})
const enumOptions = {
  asset_type: [['domain', '域名'], ['ip', 'IP 地址'], ['url', 'URL'], ['service', '服务'], ['other', '其他']],
  asset_status: [['active', '启用'], ['pending', '待确认'], ['disabled', '停用'], ['archived', '已归档']],
  environment: [['production', '生产'], ['staging', '预发布'], ['testing', '测试'], ['development', '开发'], ['office', '办公'], ['other', '其他']],
  importance: [['normal', '一般'], ['important', '重要'], ['core', '核心']],
  network_scope: [['public', '公网'], ['private', '内网'], ['unknown', '未知']],
  system_status: [['planning', '规划中'], ['running', '运行中'], ['suspended', '已暂停'], ['offline', '已下线'], ['archived', '已归档']],
  system_level: [['normal', '一般'], ['important', '重要'], ['core', '核心']],
  internet_exposure: [['yes', '是'], ['no', '否'], ['partial', '部分暴露'], ['unknown', '未知']],
  yes_no_unknown: [['yes', '是'], ['no', '否'], ['unknown', '未知']],
  classification: [['yes', '是'], ['no', '否'], ['pending', '待确认'], ['unknown', '未知']],
} satisfies Record<string, Array<[string, string]>>
const conditionOperatorLabels: Record<ConditionOperator, string> = { eq: '等于', ne: '不等于', in: '属于多个值之一', not_in: '不属于多个值', empty: '为空', not_empty: '不为空', is_true: '为真', is_false: '为假' }
function buildConditionFieldOptions(resourceType: string): ConditionFieldOption[] {
  const organizationOptions = organizations.value.map((item) => [item.id, item.name] as [string, string])
  const networkAreaOptions = networkAreas.value.map((item) => [
    item.id,
    `${item.organization_name} · ${item.name}${item.code ? `（${item.code}）` : ''}${item.status === 'enabled' ? '' : '（已停用）'}`,
  ] as [string, string])
  const builtins: Record<string, ConditionFieldOption[]> = {
    asset: [
      { value: 'asset_type', label: '资产类型', kind: 'enum', options: enumOptions.asset_type },
      { value: 'status', label: '资产状态', kind: 'enum', options: enumOptions.asset_status },
      { value: 'environment', label: '部署环境', kind: 'enum', options: enumOptions.environment },
      { value: 'importance', label: '重要程度', kind: 'enum', options: enumOptions.importance },
      { value: 'network_scope', label: '网络范围', kind: 'enum', options: enumOptions.network_scope },
      { value: 'organization_id', label: '所属组织', kind: 'uuid', options: organizationOptions },
      { value: 'network_area_id', label: '网络区域', kind: 'uuid', options: networkAreaOptions },
      { value: 'has_business_system', label: '是否关联业务系统', kind: 'presence' },
    ],
    business_system: [
      { value: 'status', label: '系统状态', kind: 'enum', options: enumOptions.system_status },
      { value: 'system_level', label: '系统等级', kind: 'enum', options: enumOptions.system_level },
      { value: 'deployment_type', label: '部署方式', kind: 'text' },
      { value: 'internet_exposure', label: '互联网暴露情况', kind: 'enum', options: enumOptions.internet_exposure },
      { value: 'contains_personal_information', label: '是否含个人信息', kind: 'enum', options: enumOptions.yes_no_unknown },
      { value: 'contains_sensitive_personal_information', label: '是否含敏感个人信息', kind: 'enum', options: enumOptions.yes_no_unknown },
      { value: 'data_volume_over_100k', label: '数据量是否超过10万条', kind: 'enum', options: enumOptions.yes_no_unknown },
      { value: 'contains_important_data', label: '是否含重要数据', kind: 'enum', options: enumOptions.classification },
      { value: 'contains_core_data', label: '是否含核心数据', kind: 'enum', options: enumOptions.classification },
      { value: 'organization_id', label: '所属组织', kind: 'uuid', options: organizationOptions },
    ],
  }
  const custom = qualityCustomFields.value
    .filter((field) => field.resource_type === resourceType && field.is_enabled)
    .map<ConditionFieldOption>((field) => ({
      value: `custom:${field.field_key}`,
      label: `自定义字段：${field.field_name}`,
      kind: field.field_type === 'boolean' ? 'boolean' : field.field_type === 'multiselect' ? 'multiselect' : field.field_type === 'select' ? 'enum' : 'text',
      options: ['select', 'multiselect'].includes(field.field_type) ? field.options.map((option) => [option, option] as [string, string]) : undefined,
    }))
  return [...(builtins[resourceType] ?? []), ...custom]
}
const conditionFieldOptions = computed<ConditionFieldOption[]>(() => buildConditionFieldOptions(ruleForm.resource_type))
const scanSummaryCards = computed(() => {
  const summary = scanResult.value?.summary
  return [
    { label: '扫描网络资产', value: summary?.asset_count ?? 0, tone: 'primary' },
    { label: '扫描业务系统', value: summary?.system_count ?? 0, tone: 'primary' },
    { label: '启用规则', value: summary?.enabled_rule_count ?? 0, tone: 'info' },
    { label: '错误问题', value: summary?.error_count ?? 0, tone: 'danger' },
    { label: '警告问题', value: summary?.warning_count ?? 0, tone: 'warning' },
    { label: '问题总数', value: summary?.total_issue_count ?? 0, tone: 'total' },
    { label: '外部刷新资产', value: summary?.external_observation_processed ?? 0, tone: 'info' },
    { label: '外部采集成功', value: summary?.external_observation_successes ?? 0, tone: 'info' },
    { label: '外部采集失败', value: summary?.external_observation_failures ?? 0, tone: 'danger' },
    { label: '外部不支持', value: summary?.external_observation_unsupported ?? 0, tone: 'warning' },
    { label: '批次延后', value: summary?.external_observation_deferred ?? 0, tone: 'warning' },
  ]
})

async function load() {
  loading.value = true
  try {
    const response = await apiClient.get<ListResponse<Field>>('/custom-fields', {
      params: { resource_type: resourceType.value, page: fieldPage.value, page_size: fieldPageSize.value },
    })
    items.value = response.data.items
    fieldTotal.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取自定义字段失败'))
  } finally {
    loading.value = false
  }
}

async function refreshFields() {
  await load()
  if (items.value.length === 0 && fieldTotal.value > 0 && fieldPage.value > 1) {
    fieldPage.value -= 1
    await load()
  }
}

function handleFieldPageChange(value: number) {
  fieldPage.value = value
  void load()
}

function handleFieldPageSizeChange(value: number) {
  fieldPageSize.value = value
  fieldPage.value = 1
  void load()
}

async function loadRules() {
  ruleLoading.value = true
  try {
    const response = await apiClient.get<ListResponse<QualityRule>>('/data-quality-rules', {
      params: { page: rulePage.value, page_size: rulePageSize.value },
    })
    ruleItems.value = response.data.items
    ruleTotal.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取数据质量规则失败'))
  } finally {
    ruleLoading.value = false
  }
}

async function loadAllPages<T>(url: string, params: Record<string, string | number> = {}) {
  const collected: T[] = []
  let page = 1
  while (true) {
    const response = await apiClient.get<ListResponse<T>>(url, { params: { ...params, page, page_size: 100 } })
    collected.push(...response.data.items)
    if (collected.length >= response.data.total || response.data.items.length === 0) return collected
    page += 1
  }
}

async function loadRuleOptions() {
  try {
    const [assetFields, systemFields, organizationItems, networkAreaItems] = await Promise.all([
      loadAllPages<Field>('/custom-fields', { resource_type: 'asset' }),
      loadAllPages<Field>('/custom-fields', { resource_type: 'business_system' }),
      loadAllPages<Organization>('/organizations'),
      loadAllPages<NetworkArea>('/network-areas'),
    ])
    qualityCustomFields.value = [...assetFields, ...systemFields]
    organizations.value = organizationItems
    networkAreas.value = networkAreaItems
    ruleOptionsLoaded.value = true
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取质量规则候选数据失败'))
  }
}

async function refreshRules() {
  await loadRules()
  if (ruleItems.value.length === 0 && ruleTotal.value > 0 && rulePage.value > 1) {
    rulePage.value -= 1
    await loadRules()
  }
}

function handleRulePageChange(value: number) {
  rulePage.value = value
  void loadRules()
}

function handleRulePageSizeChange(value: number) {
  rulePageSize.value = value
  rulePage.value = 1
  void loadRules()
}
async function runScan() {
  scanLoading.value = true
  try {
    const params: Record<string, string> = {}
    if (scanFilters.organization_id) params.organization_id = scanFilters.organization_id
    if (scanFilters.resource_type) params.resource_type = scanFilters.resource_type
    if (scanFilters.severity) params.severity = scanFilters.severity
    const response = await apiClient.get<{ result: ScanResult }>('/data-quality-rules/scan', { params })
    scanResult.value = response.data.result
    const count = response.data.result.summary.total_issue_count
    ElMessage.success(count ? `检测完成，共发现 ${count} 项问题` : '检测完成，当前台账未发现问题')
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '执行当前台账数据质量检测失败'))
  } finally {
    scanLoading.value = false
  }
}
function onTabChange(tab: string | number) {
  if (tab === 'quality') {
    void Promise.all([loadRules(), loadRuleOptions()])
    return
  }
  fieldPage.value = 1
  resourceType.value = String(tab)
}
function conditionField(field: string, resourceType = ruleForm.resource_type) {
  return buildConditionFieldOptions(resourceType).find((item) => item.value === field)
}
function conditionOperators(condition: RuleCondition): Array<[ConditionOperator, string]> {
  const kind = conditionField(condition.field)?.kind
  if (kind === 'presence') return [['is_true', '为真'], ['is_false', '为假']]
  if (kind === 'boolean') return [['is_true', '为真'], ['is_false', '为假'], ['empty', '为空'], ['not_empty', '不为空']]
  if (kind === 'multiselect') return [['in', '包含任一值'], ['not_in', '不包含这些值'], ['empty', '为空'], ['not_empty', '不为空']]
  return [['eq', '等于'], ['ne', '不等于'], ['in', '属于多个值之一'], ['not_in', '不属于多个值'], ['empty', '为空'], ['not_empty', '不为空']]
}
function conditionNeedsValue(operator: ConditionOperator) {
  return !['empty', 'not_empty', 'is_true', 'is_false'].includes(operator)
}
function conditionUsesMultiple(operator: ConditionOperator) {
  return ['in', 'not_in'].includes(operator)
}
function resetCondition(condition: RuleCondition) {
  const operators = conditionOperators(condition)
  condition.operator = operators[0]?.[0] ?? 'eq'
  condition.values = []
}
function addCondition() {
  if (ruleForm.conditions.length >= 10) {
    ElMessage.warning('每条规则最多配置 10 个生效条件')
    return
  }
  const field = conditionFieldOptions.value[0]
  if (!field) return
  const condition: RuleCondition = { field: field.value, operator: 'eq', values: [] }
  resetCondition(condition)
  ruleForm.conditions.push(condition)
}
function removeCondition(index: number) {
  ruleForm.conditions.splice(index, 1)
}
function conditionsValid() {
  return ruleForm.conditions.every((condition) => condition.field && condition.operator && (!conditionNeedsValue(condition.operator) || condition.values.length > 0))
}
function conditionValueLabel(condition: RuleCondition, value: string, resourceType = ruleForm.resource_type) {
  return conditionField(condition.field, resourceType)?.options?.find(([option]) => option === value)?.[1] ?? value
}
function conditionSummary(condition: RuleCondition, resourceType = ruleForm.resource_type) {
  const field = conditionField(condition.field, resourceType)
  const operator = conditionOperatorLabels[condition.operator] ?? condition.operator
  if (!conditionNeedsValue(condition.operator)) return `${field?.label ?? condition.field}${operator}`
  return `${field?.label ?? condition.field}${operator}${condition.values.map((value) => conditionValueLabel(condition, value, resourceType)).join('、')}`
}
function ruleConditionSummary(rule: QualityRule) {
  if (!rule.conditions?.length) return '全部资源'
  return rule.conditions.map((condition) => conditionSummary(condition, rule.resource_type)).join(' 且 ')
}

function openRuleCreate() {
  editingRuleID.value = ''
  Object.assign(ruleForm, { rule_name: '', description: '', resource_type: 'asset', rule_type: 'required', target_field: 'owner_name', severity: 'error', stale_after_days: 0, expiry_warning_days: 30, conditions: [], is_enabled: true, sort_order: ruleTotal.value * 10 })
  ruleVisible.value = true
}
function openRuleEdit(item: QualityRule) {
  editingRuleID.value = item.id
  Object.assign(ruleForm, { rule_name: item.rule_name, description: item.description ?? '', resource_type: item.resource_type, rule_type: item.rule_type, target_field: item.target_field, severity: item.severity, stale_after_days: item.stale_after_days ?? 0, expiry_warning_days: item.expiry_warning_days ?? 0, conditions: (item.conditions ?? []).map((condition) => ({ ...condition, values: [...(condition.values ?? [])] })), is_enabled: item.is_enabled, sort_order: item.sort_order })
  ruleVisible.value = true
}
async function saveRule() {
  if (!ruleFormRef.value || !(await ruleFormRef.value.validate())) return
  if (!conditionsValid()) {
    ElMessage.warning('请完整填写生效条件的字段、运算符和值')
    return
  }
  saving.value = true
  try {
    const payload = {
      ...ruleForm,
      stale_after_days: ruleForm.rule_type === 'stale' ? ruleForm.stale_after_days : 0,
      expiry_warning_days: ruleForm.rule_type === 'expiry' ? ruleForm.expiry_warning_days : 0,
    }
    if (editingRuleID.value) await apiClient.patch(`/data-quality-rules/${editingRuleID.value}`, payload)
    else await apiClient.post('/data-quality-rules', payload)
    ElMessage.success(editingRuleID.value ? '数据质量规则已更新' : '数据质量规则已创建')
    ruleVisible.value = false
    scanResult.value = null
    await refreshRules()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, editingRuleID.value ? '更新数据质量规则失败' : '创建数据质量规则失败'))
  } finally { saving.value = false }
}
async function toggleRule(item: QualityRule) {
  saving.value = true
  try {
    await apiClient.patch(`/data-quality-rules/${item.id}`, { ...item, is_enabled: !item.is_enabled })
    ElMessage.success(item.is_enabled ? '规则已停用' : '规则已启用')
    scanResult.value = null
    await refreshRules()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '更新规则状态失败'))
  } finally { saving.value = false }
}
function openCreate() {
  editingID.value = ''
  Object.assign(form, { resource_type: resourceType.value, field_key: '', field_name: '', description: '', field_type: 'text', optionsText: '', is_required: false, is_enabled: true, sort_order: fieldTotal.value * 10 })
  visible.value = true
}
function openEdit(item: Field) {
  editingID.value = item.id
  Object.assign(form, { resource_type: item.resource_type, field_key: item.field_key, field_name: item.field_name, description: item.description ?? '', field_type: item.field_type, optionsText: item.options?.join('\n') ?? '', is_required: item.is_required, is_enabled: item.is_enabled, sort_order: item.sort_order })
  visible.value = true
}
function payload() {
  return { ...form, options: form.optionsText.split(/\n|,/).map((item) => item.trim()).filter(Boolean) }
}
async function save() {
  if (!formRef.value || !(await formRef.value.validate())) return
  saving.value = true
  try {
    if (editingID.value) await apiClient.patch(`/custom-fields/${editingID.value}`, payload())
    else await apiClient.post('/custom-fields', payload())
    ElMessage.success(editingID.value ? '自定义字段已更新' : '自定义字段已创建')
    visible.value = false
    await refreshFields()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, editingID.value ? '更新自定义字段失败' : '创建自定义字段失败'))
  } finally { saving.value = false }
}
async function toggle(item: Field) {
  saving.value = true
  try {
    await apiClient.patch(`/custom-fields/${item.id}`, { ...item, is_enabled: !item.is_enabled })
    ElMessage.success(item.is_enabled ? '字段已停用' : '字段已启用')
    await refreshFields()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '更新字段状态失败'))
  } finally { saving.value = false }
}
function qualityTargetLabel(issue: QualityTarget) {
  if (issue.rule_type === 'stale' || issue.target_field === 'updated_at') {
    return issue.stale_after_days ? `最后更新时间（超过 ${issue.stale_after_days} 天未维护）` : '最后更新时间'
  }
  if (issue.target_field === 'domain_registration_expires_at') {
    return `域名注册到期时间（提前 ${issue.expiry_warning_days ?? 0} 天提醒）`
  }
  if (issue.target_field === 'tls_certificate_expires_at') {
    return `TLS 证书到期时间（提前 ${issue.expiry_warning_days ?? 0} 天提醒）`
  }
  if (issue.rule_type === 'expiry' || issue.target_field === 'expires_at') {
    return `有效期至（提前 ${issue.expiry_warning_days ?? 0} 天提醒）`
  }
  if (issue.target_field.startsWith('custom:')) {
    const key = issue.target_field.slice('custom:'.length)
    const field = qualityCustomFields.value.find((item) => item.resource_type === issue.resource_type && item.field_key === key)
    return `自定义字段：${field?.field_name ?? key}`
  }
  return builtinTargets[issue.resource_type]?.find(([value]) => value === issue.target_field)?.[1] ?? issue.target_field
}
function resourceRoute(issue: ScanIssue) {
  return { name: issue.resource_type === 'asset' ? 'assets' : 'systems' }
}

watch(resourceType, () => { void load() })
watch([() => ruleForm.resource_type, () => ruleForm.rule_type], () => {
  if (ruleOptionsLoaded.value) {
    const allowedConditionFields = new Set(conditionFieldOptions.value.map((item) => item.value))
    ruleForm.conditions = ruleForm.conditions.filter((condition) => allowedConditionFields.has(condition.field))
  }
  if (ruleForm.rule_type === 'stale') {
    ruleForm.target_field = 'updated_at'
    if (!ruleForm.stale_after_days) ruleForm.stale_after_days = 90
    return
  }
  if (ruleForm.rule_type === 'expiry') {
    ruleForm.resource_type = 'asset'
    ruleForm.target_field = 'expires_at'
    return
  }
  if (!ruleTargetOptions.value.some(([value]) => value === ruleForm.target_field)) ruleForm.target_field = 'owner_name'
})
onMounted(load)
</script>

<template>
  <section>
    <div class="page-heading">
      <div><span class="eyebrow">DATA GOVERNANCE</span><h1>数据治理</h1><p>集团统一定义补充字段和质量规则，并可主动检查当前网络资产与业务系统台账。</p></div>
      <div class="button-row"><el-button v-if="activeTab === 'quality'" type="primary" @click="openRuleCreate">新增质量规则</el-button><el-button v-else type="primary" @click="openCreate">新增自定义字段</el-button></div>
    </div>
    <div class="panel governance-filter-panel"><el-tabs v-model="activeTab" @tab-change="onTabChange"><el-tab-pane label="网络资产字段" name="asset" /><el-tab-pane label="业务系统字段" name="business_system" /><el-tab-pane label="数据质量规则" name="quality" /></el-tabs></div>

    <div v-if="activeTab !== 'quality'" class="panel data-table-panel">
      <el-table v-loading="loading" :data="items" stripe max-height="620" empty-text="暂未配置自定义字段">
        <el-table-column prop="field_name" label="字段名称" min-width="180"><template #default="{ row }"><strong>{{ row.field_name }}</strong><div class="table-subtext">{{ row.field_key }}</div></template></el-table-column>
        <el-table-column label="字段类型" width="130"><template #default="{ row }">{{ types.find((item) => item[0] === row.field_type)?.[1] || row.field_type }}</template></el-table-column>
        <el-table-column label="选项" min-width="180"><template #default="{ row }">{{ row.options?.join('、') || '—' }}</template></el-table-column>
        <el-table-column label="规则" width="130"><template #default="{ row }"><el-tag v-if="row.is_required" type="warning">必填</el-tag><span v-else>选填</span></template></el-table-column>
        <el-table-column label="排序" prop="sort_order" width="90" />
        <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="row.is_enabled ? 'success' : 'info'">{{ row.is_enabled ? '启用' : '停用' }}</el-tag></template></el-table-column>
        <el-table-column label="操作" width="120" fixed="right" align="right" header-align="right" class-name="table-action-column"><template #default="{ row }"><el-button link type="primary" @click="openEdit(row)">编辑</el-button><el-button link :type="row.is_enabled ? 'warning' : 'success'" :loading="saving" @click="toggle(row)">{{ row.is_enabled ? '停用' : '启用' }}</el-button></template></el-table-column>
      </el-table>
      <div class="table-pagination">
        <el-pagination background layout="total, sizes, prev, pager, next" :total="fieldTotal" :current-page="fieldPage" :page-size="fieldPageSize" :page-sizes="[20, 50, 100]" @current-change="handleFieldPageChange" @size-change="handleFieldPageSizeChange" />
      </div>
    </div>

    <template v-else>
      <div class="panel quality-scan-panel" v-loading="scanLoading">
        <div class="quality-section-heading">
          <div><h2>当前台账质量检测</h2><p>使用当前已启用规则检查存量台账；检测不会修改数据，执行记录会写入审计日志。</p></div>
          <el-button type="primary" :loading="scanLoading" @click="runScan">执行当前台账检测</el-button>
        </div>
        <div class="quality-scan-filters">
          <el-select v-model="scanFilters.organization_id" filterable clearable placeholder="全部组织"><el-option v-for="item in organizations" :key="item.id" :label="item.name" :value="item.id" /></el-select>
          <el-select v-model="scanFilters.resource_type" clearable placeholder="全部检查对象"><el-option label="网络资产" value="asset" /><el-option label="业务系统" value="business_system" /></el-select>
          <el-select v-model="scanFilters.severity" clearable placeholder="全部严重程度"><el-option label="错误" value="error" /><el-option label="警告" value="warning" /></el-select>
        </div>
        <el-alert v-if="!scanResult" title="请选择筛选范围后手动执行检测。检测结果反映执行当时的台账状态，规则或台账变更后请重新检测。" type="info" :closable="false" show-icon />
        <template v-else>
          <div class="quality-summary-grid"><div v-for="card in scanSummaryCards" :key="card.label" class="quality-summary-card" :class="`tone-${card.tone}`"><span>{{ card.label }}</span><strong>{{ card.value }}</strong></div></div>
          <el-alert
            v-if="scanResult.summary.external_observation_processed || scanResult.summary.external_observation_failures || scanResult.summary.external_observation_unsupported || scanResult.summary.external_observation_deferred"
            :title="`本次外部采集：处理 ${scanResult.summary.external_observation_processed} 项，成功 ${scanResult.summary.external_observation_successes} 项，失败 ${scanResult.summary.external_observation_failures} 项，不支持 ${scanResult.summary.external_observation_unsupported} 项，延后 ${scanResult.summary.external_observation_deferred} 项。`"
            :type="scanResult.summary.external_observation_failures ? 'warning' : 'info'"
            :closable="false"
            show-icon
          />
          <el-alert v-if="scanResult.summary.truncated" :title="`共发现 ${scanResult.summary.total_issue_count} 项问题，当前仅展示前 ${scanResult.summary.returned_count} 项，请缩小组织、对象或严重程度筛选范围。`" type="warning" :closable="false" show-icon />
          <div class="quality-issue-heading"><div><h3>问题清单</h3><span>已返回 {{ scanResult.summary.returned_count }} 项</span></div><span v-if="scanResult.summary.total_issue_count === 0" class="quality-passed">当前筛选范围检查通过</span></div>
          <el-table :data="scanResult.issues" stripe max-height="440" empty-text="当前筛选范围未发现数据质量问题">
            <el-table-column label="严重程度" width="105"><template #default="{ row }"><el-tag :type="row.severity === 'error' ? 'danger' : 'warning'">{{ row.severity === 'error' ? '错误' : '警告' }}</el-tag></template></el-table-column>
            <el-table-column prop="rule_name" label="规则名称" min-width="190" show-overflow-tooltip />
            <el-table-column label="对象类型" width="110"><template #default="{ row }">{{ row.resource_type === 'asset' ? '网络资产' : '业务系统' }}</template></el-table-column>
            <el-table-column label="资源" min-width="210"><template #default="{ row }"><strong>{{ row.resource_name || '未命名资源' }}</strong><div class="table-subtext">{{ row.resource_id }}</div></template></el-table-column>
            <el-table-column prop="organization_name" label="所属组织" min-width="190" />
            <el-table-column label="检查项" min-width="210"><template #default="{ row }">{{ qualityTargetLabel(row) }}</template></el-table-column>
            <el-table-column label="操作" width="100" fixed="right" align="right" header-align="right" class-name="table-action-column"><template #default="{ row }"><router-link :to="resourceRoute(row)"><el-button link type="primary">前往台账</el-button></router-link></template></el-table-column>
          </el-table>
        </template>
      </div>

      <div class="panel data-table-panel quality-rule-panel">
        <div class="quality-section-heading compact"><div><h2>规则配置</h2><p>集团可配置字段必填、长期未更新和资产到期规则；错误用于重点整改，警告用于一般提醒。</p></div></div>
        <el-table v-loading="ruleLoading" :data="ruleItems" stripe max-height="620" empty-text="暂未配置数据质量规则">
          <el-table-column prop="rule_name" label="规则名称" min-width="200"><template #default="{ row }"><strong>{{ row.rule_name }}</strong><div class="table-subtext">{{ row.description || qualityTargetLabel(row) }}</div></template></el-table-column>
          <el-table-column label="对象" width="120"><template #default="{ row }">{{ row.resource_type === 'asset' ? '网络资产' : '业务系统' }}</template></el-table-column>
          <el-table-column label="类型" width="120"><template #default="{ row }">{{ row.rule_type === 'stale' ? '长期未更新' : row.rule_type === 'expiry' ? '资产到期' : '字段必填' }}</template></el-table-column>
          <el-table-column label="检查项" min-width="190"><template #default="{ row }">{{ qualityTargetLabel(row) }}</template></el-table-column>
          <el-table-column label="生效条件" min-width="260"><template #default="{ row }"><span class="condition-summary">{{ ruleConditionSummary(row) }}</span></template></el-table-column>
          <el-table-column label="级别" width="100"><template #default="{ row }"><el-tag :type="row.severity === 'error' ? 'danger' : 'warning'">{{ row.severity === 'error' ? '错误' : '警告' }}</el-tag></template></el-table-column>
          <el-table-column prop="sort_order" label="排序" width="90" />
          <el-table-column label="状态" width="100"><template #default="{ row }"><el-tag :type="row.is_enabled ? 'success' : 'info'">{{ row.is_enabled ? '启用' : '停用' }}</el-tag></template></el-table-column>
          <el-table-column label="操作" width="120" fixed="right" align="right" header-align="right" class-name="table-action-column"><template #default="{ row }"><el-button link type="primary" @click="openRuleEdit(row)">编辑</el-button><el-button link :type="row.is_enabled ? 'warning' : 'success'" :loading="saving" @click="toggleRule(row)">{{ row.is_enabled ? '停用' : '启用' }}</el-button></template></el-table-column>
        </el-table>
        <div class="table-pagination">
          <el-pagination background layout="total, sizes, prev, pager, next" :total="ruleTotal" :current-page="rulePage" :page-size="rulePageSize" :page-sizes="[20, 50, 100]" @current-change="handleRulePageChange" @size-change="handleRulePageSizeChange" />
        </div>
      </div>
    </template>

    <el-dialog v-model="visible" :title="editingID ? '编辑集团自定义字段' : '新增集团自定义字段'" width="620px" destroy-on-close>
      <el-form ref="formRef" :model="form" label-width="110px" :rules="{ field_key: [{ required: true, message: '请输入字段编码', trigger: 'blur' }], field_name: [{ required: true, message: '请输入字段名称', trigger: 'blur' }] }">
        <el-form-item label="字段编码" prop="field_key"><el-input v-model="form.field_key" :disabled="Boolean(editingID)" placeholder="如 data_retention_period；仅小写字母、数字、下划线" /></el-form-item>
        <el-form-item label="字段名称" prop="field_name"><el-input v-model="form.field_name" /></el-form-item>
        <el-form-item label="字段类型"><el-select v-model="form.field_type" :disabled="Boolean(editingID)"><el-option v-for="item in types" :key="item[0]" :value="item[0]" :label="item[1]" /></el-select></el-form-item>
        <el-form-item v-if="['select', 'multiselect'].includes(form.field_type)" label="可选项"><el-input v-model="form.optionsText" type="textarea" :rows="3" placeholder="每行一个选项，也可用逗号分隔" /></el-form-item>
        <el-form-item label="填写要求"><el-switch v-model="form.is_required" active-text="必填" inactive-text="选填" /></el-form-item>
        <el-form-item label="状态"><el-switch v-model="form.is_enabled" active-text="启用" inactive-text="停用" /></el-form-item>
        <el-form-item label="排序"><el-input-number v-model="form.sort_order" :min="0" /></el-form-item>
        <el-form-item label="说明"><el-input v-model="form.description" type="textarea" :rows="2" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="visible = false">取消</el-button><el-button type="primary" :loading="saving" @click="save">保存</el-button></template>
    </el-dialog>
    <el-dialog v-model="ruleVisible" :title="editingRuleID ? '编辑数据质量规则' : '新增数据质量规则'" width="860px" destroy-on-close>
      <el-form ref="ruleFormRef" :model="ruleForm" label-width="110px" :rules="{ rule_name: [{ required: true, message: '请输入规则名称', trigger: 'blur' }], target_field: [{ required: true, message: '请选择目标字段', trigger: 'change' }], stale_after_days: [{ required: ruleForm.rule_type === 'stale', message: '请设置未维护天数', trigger: 'change' }], expiry_warning_days: [{ required: ruleForm.rule_type === 'expiry', message: '请设置提前提醒天数', trigger: 'change' }] }">
        <el-form-item label="规则名称" prop="rule_name"><el-input v-model="ruleForm.rule_name" placeholder="如：资产责任人必填" /></el-form-item>
        <el-form-item label="检查对象"><el-select v-model="ruleForm.resource_type" :disabled="ruleForm.rule_type === 'expiry'" style="width:100%"><el-option label="网络资产" value="asset" /><el-option label="业务系统" value="business_system" /></el-select></el-form-item>
        <el-form-item label="规则类型"><el-select v-model="ruleForm.rule_type" style="width:100%"><el-option label="字段必填" value="required" /><el-option label="长期未更新" value="stale" /><el-option label="资产到期" value="expiry" /></el-select></el-form-item>
        <el-form-item label="目标字段" prop="target_field"><el-select v-model="ruleForm.target_field" :disabled="ruleForm.rule_type === 'stale'" filterable style="width:100%"><el-option v-for="item in ruleTargetOptions" :key="item[0]" :label="item[1]" :value="item[0]" /></el-select></el-form-item>
        <el-form-item v-if="ruleForm.rule_type === 'stale'" label="未维护天数" prop="stale_after_days"><el-input-number v-model="ruleForm.stale_after_days" :min="1" :max="3650" :step="30" /><span class="form-help-inline">超过该天数未更新即产生问题</span></el-form-item>
        <el-form-item v-if="ruleForm.rule_type === 'expiry'" label="提前提醒天数" prop="expiry_warning_days"><el-input-number v-model="ruleForm.expiry_warning_days" :min="0" :max="3650" :step="1" /><span class="form-help-inline">到期前 N 天及已过期资产都会产生问题；0 表示仅在到期时提醒</span></el-form-item>
        <el-form-item label="生效条件">
          <div class="condition-editor">
            <div class="condition-editor-heading"><span>不配置时检查全部资源；多个条件按“且（AND）”同时满足。</span><el-button type="primary" plain size="small" :disabled="ruleForm.conditions.length >= 10" @click="addCondition">添加条件</el-button></div>
            <div v-if="ruleForm.conditions.length === 0" class="condition-empty">当前规则对全部{{ ruleForm.resource_type === 'asset' ? '网络资产' : '业务系统' }}生效</div>
            <div v-for="(condition, index) in ruleForm.conditions" :key="index" class="condition-row">
              <span class="condition-join">{{ index === 0 ? '当' : '且' }}</span>
              <el-select v-model="condition.field" filterable placeholder="选择字段" @change="resetCondition(condition)"><el-option v-for="item in conditionFieldOptions" :key="item.value" :label="item.label" :value="item.value" /></el-select>
              <el-select v-model="condition.operator" placeholder="选择关系" @change="condition.values = []"><el-option v-for="item in conditionOperators(condition)" :key="item[0]" :label="item[1]" :value="item[0]" /></el-select>
              <template v-if="conditionNeedsValue(condition.operator)">
                <el-select v-if="conditionField(condition.field)?.options?.length && conditionUsesMultiple(condition.operator)" v-model="condition.values" multiple filterable collapse-tags collapse-tags-tooltip placeholder="选择一个或多个条件值" class="condition-value"><el-option v-for="item in conditionField(condition.field)?.options" :key="item[0]" :label="item[1]" :value="item[0]" /></el-select>
                <el-select v-else-if="conditionField(condition.field)?.options?.length" v-model="condition.values[0]" filterable placeholder="选择条件值" class="condition-value"><el-option v-for="item in conditionField(condition.field)?.options" :key="item[0]" :label="item[1]" :value="item[0]" /></el-select>
                <el-select v-else-if="conditionUsesMultiple(condition.operator)" v-model="condition.values" multiple filterable allow-create default-first-option collapse-tags collapse-tags-tooltip placeholder="输入后回车，可添加多个值" class="condition-value" />
                <el-input v-else v-model="condition.values[0]" maxlength="256" placeholder="输入条件值" class="condition-value" />
              </template>
              <div v-else class="condition-value condition-no-value">无需填写值</div>
              <el-button link type="danger" @click="removeCondition(index)">删除</el-button>
            </div>
          </div>
        </el-form-item>
        <el-form-item label="严重程度"><el-radio-group v-model="ruleForm.severity"><el-radio value="error">错误：重点整改</el-radio><el-radio value="warning">警告：一般提醒</el-radio></el-radio-group></el-form-item>
        <el-form-item label="状态"><el-switch v-model="ruleForm.is_enabled" active-text="启用" inactive-text="停用" /></el-form-item>
        <el-form-item label="排序"><el-input-number v-model="ruleForm.sort_order" :min="0" /></el-form-item>
        <el-form-item label="说明"><el-input v-model="ruleForm.description" type="textarea" :rows="2" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="ruleVisible = false">取消</el-button><el-button type="primary" :loading="saving" @click="saveRule">保存</el-button></template>
    </el-dialog>
  </section>
</template>

<style scoped>
.governance-filter-panel { margin-bottom: 16px; }
.quality-scan-panel { margin-bottom: 16px; }
.quality-rule-panel { padding-top: 18px; }
.table-pagination { display:flex; justify-content:flex-end; margin-top:18px; }
.quality-section-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 20px; margin-bottom: 18px; }
.quality-section-heading.compact { margin-bottom: 12px; }
.quality-section-heading h2 { margin: 0 0 6px; color: var(--navy); font-size: 18px; }
.quality-section-heading p { margin: 0; color: var(--muted); font-size: 13px; line-height: 1.6; }
.quality-scan-filters { display: grid; grid-template-columns: minmax(260px, 1.4fr) minmax(180px, 1fr) minmax(180px, 1fr); gap: 12px; margin-bottom: 18px; }
.quality-scan-filters .el-select { width: 100%; }
.quality-summary-grid { display: grid; grid-template-columns: repeat(6, minmax(0, 1fr)); gap: 12px; margin: 18px 0; }
.quality-summary-card { min-width: 0; padding: 14px 16px; border: 1px solid var(--line); border-left: 4px solid #5b8def; border-radius: 10px; background: #f8fafc; }
.quality-summary-card span { display: block; margin-bottom: 8px; color: var(--muted); font-size: 12px; white-space: nowrap; }
.quality-summary-card strong { color: var(--navy); font-size: 25px; line-height: 1; }
.quality-summary-card.tone-info { border-left-color: #909399; }
.quality-summary-card.tone-danger { border-left-color: #f56c6c; }
.quality-summary-card.tone-warning { border-left-color: #e6a23c; }
.quality-summary-card.tone-total { border-left-color: var(--cyan); }
.quality-issue-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; margin: 20px 0 10px; }
.quality-issue-heading > div { display: flex; align-items: baseline; gap: 10px; }
.quality-issue-heading h3 { margin: 0; color: var(--navy); font-size: 16px; }
.quality-issue-heading span { color: var(--muted); font-size: 12px; }
.quality-issue-heading .quality-passed { color: #529b2e; font-weight: 650; }
.form-help-inline { margin-left: 12px; color: var(--muted); font-size: 12px; }
.condition-summary { color: #475569; font-size: 13px; line-height: 1.55; }
.condition-editor { width: 100%; padding: 12px; border: 1px solid var(--line); border-radius: 10px; background: #f8fafc; }
.condition-editor-heading { display: flex; align-items: center; justify-content: space-between; gap: 16px; margin-bottom: 10px; color: var(--muted); font-size: 12px; }
.condition-empty { padding: 12px; border: 1px dashed #cbd5e1; border-radius: 8px; color: var(--muted); text-align: center; }
.condition-row { display: grid; grid-template-columns: 28px minmax(150px, 1.1fr) minmax(130px, .8fr) minmax(190px, 1.4fr) 40px; align-items: center; gap: 8px; margin-top: 8px; }
.condition-row:first-of-type { margin-top: 0; }
.condition-join { color: var(--navy); font-weight: 650; text-align: center; }
.condition-value { width: 100%; }
.condition-no-value { box-sizing: border-box; height: 32px; padding: 0 11px; border: 1px dashed #d7dce3; border-radius: 4px; color: var(--muted); font-size: 12px; line-height: 30px; }
@media (max-width: 1300px) { .quality-summary-grid { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
</style>
