<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'
import { useAuthStore } from '../stores/auth'
import RepairTaskDetail from '../components/RepairTaskDetail.vue'
import { dateText, executionBlockReasons, loadOptions, newKey, outcomes, severityLabels, taskStatuses } from '../api/remediation'
import type { Grant, Material, Page, RepairTask, RepairStatistics, SystemOption } from '../api/remediation'

interface Finding extends Material { id: string; business_system_id?: string; due_at: string; assigned: boolean }
interface OrderSummary { id: string; work_order_no: string; execution_mode: string; status: string; due_at: string; assignee_organization_id: string; total: number; unassigned: number; counts: Record<string, number> }
interface Order { remediation_counts: Record<string,number>; id: string; number: string; title: string; execution_mode: string; status: string }
const route = useRoute()
const auth = useAuthStore()
const executor = computed(() => route.path.startsWith('/repair'))
const base = computed(() => executor.value ? '/repair/tasks' : '/remediation-tasks')
const rows = ref<RepairTask[]>([])
const total = ref(0)
const page = ref(1)
const status = ref(typeof route.query.status === 'string' ? route.query.status : route.path.startsWith('/repair') ? 'active' : 'all')
const systemID = ref('')
const severity = ref('')
const special = ref(typeof route.query.view === 'string' ? route.query.view : '')
const blockReason = ref('')
const revivedOnly = ref(false)
const systems = ref<SystemOption[]>([])
const stats = ref<RepairStatistics>()
const selectedTasks = ref<RepairTask[]>([])
const reviewQueue = ref<string[]>([])
const queueIndex = ref(0)
const findingSystem = ref('')
const findingSeverity = ref('')
const visibleFindings = computed(() => findings.value.filter(f => (!findingSystem.value || f.business_system_id === findingSystem.value) && (!findingSeverity.value || f.severity === findingSeverity.value)))
const overviewStats = ref<RepairStatistics>()
const advanced = ref(Boolean(route.query.work_order_id))
const compact = ref(false)
const errorMessage = ref('')
const statisticsOpen = ref<string[]>([])
const viewKey = computed(() => `netassets:repair-view:${auth.user?.id}:${auth.user?.organization?.id}:${executor.value}`)
const savedView = ref(false)
function syncSavedView() { try { savedView.value = Boolean(localStorage.getItem(viewKey.value)) } catch { savedView.value = false } }
function saveView() {
 try { localStorage.setItem(viewKey.value, JSON.stringify({status:status.value,systemID:systemID.value,severity:severity.value,special:special.value,keyword:keyword.value,overdue:overdue.value,orderID:orderID.value}));savedView.value=true;ElMessage.success('已保存当前浏览器中的筛选视图') } catch { ElMessage.warning('当前浏览器无法保存视图') }
}
function applyView() {
 try { const v = JSON.parse(localStorage.getItem(viewKey.value) || '{}'); status.value=typeof v.status==='string'?v.status:'all';systemID.value=v.systemID||'';severity.value=v.severity||'';special.value=v.special||'';keyword.value=v.keyword||'';overdue.value=Boolean(v.overdue);orderID.value=executor.value?'':v.orderID||'';advanced.value=true;page.value=1;void load() } catch { ElMessage.warning('保存的视图无法读取，请重新保存') }
}
const revivedSelection = ref<RepairTask[]>([])
const acknowledging = ref(false)
const reviveAckReason = ref('')
function revivedSelectable(row: RepairTask) {
  return Boolean(row.revived && !row.revived_ack_at && row.can_assign)
}
function onSelectionChange(rows: RepairTask[]) {
  selectedTasks.value = rows
  revivedSelection.value = rows.filter(revivedSelectable)
}
function showRevivedOnly() { status.value = 'all'; special.value = ''; blockReason.value = ''; revivedOnly.value = true; page.value = 1; void load() }
async function acknowledgeSelected() {
  const ids = revivedSelection.value.map(r => r.id)
  if (!ids.length) return
  try {
    const reason = reviveAckReason.value.trim() || '已知悉资产/系统恢复，重新纳入当前工作'
    const key = `revive-ack-${new Date().toISOString().slice(0,19).replace(/[T:]/g,'')}-${ids.length}`
    const { data } = await apiClient.post<{ items: { id?: string; error?: string }[] }>('/remediation-tasks/acknowledge-revived', { task_ids: ids, reason }, { headers: { 'Idempotency-Key': key } })
    const failed = (data.items ?? []).filter(i => i.error)
    if (failed.length) {
      ElMessage.warning(`已确认 ${ids.length - failed.length} 项；${failed.length} 项未成功（${failed.map(f => f.error).join('、')}），列表已刷新`)
    } else {
      ElMessage.success(`已确认 ${ids.length} 项恢复跟进`)
    }
    revivedSelection.value = []
    await load()
  } catch (e: unknown) {
    ElMessage.error(e instanceof Error ? e.message : '批量确认失败')
  }
}
function resetFilters() { revivedOnly.value = false; revivedSelection.value = []; reviveAckReason.value = ''; blockReason.value = ''; status.value=executor.value?'active':'all';systemID.value='';severity.value='';special.value='';keyword.value='';overdue.value=false;page.value=1;void load() }
const quickViews = computed(() => [
 {key:'all',label:'全部任务'}, ...(!executor.value ? [{key:'review_mine',label:'待我审核'}] : []),
 {key:'overdue',label:'已逾期'}, {key:'returned',label:'被退回'}, {key:'active',label:'未完成'}, {key:'completed',label:'已完成'},
])
const activeView = computed(() => overdue.value ? 'overdue' : ['review_mine','returned'].includes(special.value) ? special.value : ['active','completed'].includes(status.value) ? status.value : status.value==='all'&&!special.value?'all':'')
function selectView(value:string) { status.value=['active','completed'].includes(value)?value:'all';overdue.value=value==='overdue';special.value=['review_mine','returned'].includes(value)?value:'';page.value=1 }
function taskAction(task:RepairTask) { return executor.value ? task.status==='pending_acceptance'?'接收任务':task.status==='in_progress'?'继续修复':'查看进度' : task.status==='pending_org_review'&&auth.hasPermission('remediation.review')?'组织确认':task.status==='pending_security_review'&&auth.canManageSecurityWorkOrders?'开始复测':'查看详情' }
function blockText(reason?:string) { if (!reason) return ''; return executionBlockReasons[reason]?.text || `任务当前不可执行（${reason}）` }
// The group admin triages the blocked queue by cause, not by task. The
// statistics endpoint groups open blocked tasks by execution_blocked_reason, so
// the workbench can offer one click per cause instead of reading 200 rows.
const blockBreakdown = computed(() => Object.entries(stats.value?.block_reasons ?? {})
  .map(([reason, count]) => ({ reason, count, text: executionBlockReasons[reason]?.text || `不可执行（${reason}）`, action: executionBlockReasons[reason]?.action || '请联系集团安全管理员确认任务状态。' }))
  .sort((a, b) => b.count - a.count))
function filterByBlockReason(reason: string) { status.value = 'all'; special.value = 'needs_reassignment'; blockReason.value = reason; page.value = 1; void load() }
function blockAction(reason?:string) { if (!reason) return ''; return executionBlockReasons[reason]?.action || '请联系集团安全管理员确认任务状态。' }
function severityType(value:string) { return value==='critical'?'danger':value==='high'?'warning':value==='medium'?'primary':'info' }
const metricCards = computed(() => {
 const c=overviewStats.value?.counts ?? {}
 return executor.value ? [
  {label:'待修复',value:c.in_progress??0,hint:'优先处理退回项',view:'active'},
  {label:'待接收',value:c.pending_acceptance??0,hint:'确认要求后安排修复',status:'pending_acceptance'},
  {label:'等待审核',value:(c.pending_org_review??0)+(c.pending_security_review??0),hint:'组织确认 / 集团复测',view:'all'},
  {label:'已逾期',value:overviewStats.value?.overdue??0,hint:'期限不会因退回自动顺延',view:'overdue',danger:true},
 ] : [
  {label:'待组织确认',value:c.pending_org_review??0,hint:'核对部署与自测材料',status:'pending_org_review'},
  {label:'待集团复测',value:c.pending_security_review??0,hint:'逐项验证已提交的修复',status:'pending_security_review'},
  {label:'逾期未完成',value:overviewStats.value?.overdue??0,hint:'优先跟进责任人',view:'overdue',danger:true},
  {label:'已完成处置',value:c.completed??0,hint:`其中复测通过 ${overviewStats.value?.verified_fixed??0} 项`,status:'completed'},
 ]
})
function selectMetric(metric:{view?:string;status?:string}) { if(metric.view)selectView(metric.view);else {overdue.value=false;special.value='';status.value=metric.status??'all'} }
let loadSequence=0
const hours = (seconds: number) => (seconds / 3600).toFixed(1) + ' 小时'
function startReview() { reviewQueue.value = selectedTasks.value.map(t => t.id); queueIndex.value = 0; if (reviewQueue.value[0]) open(reviewQueue.value[0],true) }
function nextReview() { queueIndex.value++; const id = reviewQueue.value[queueIndex.value]; if (id) open(id,true); else { drawer.value = false; reviewQueue.value = [] } }

const keyword = ref('')
const overdue = ref(route.query.overdue === 'true')
const loading = ref(false)
const busy = ref(false)
const orderID = ref(typeof route.query.work_order_id === 'string' ? route.query.work_order_id : '')
const orders = ref<Order[]>([])
const onlyUnassigned = ref(false)
const summary = ref<OrderSummary>()
const selectedID = ref('')
const drawer = ref(false)
const assignVisible = ref(false)
const findings = ref<Finding[]>([])
const selected = ref<Finding[]>([])
const grants = ref<Grant[]>([])
const assignee = ref('')
const assignment = reactive({ due_at: '', target_environment: '生产环境', deployment_required: true })
const assignmentKey = ref(newKey())
const resultErrors = ref<{ finding_id: string; error: string }[]>([])
const selectedSystemIDs = computed(() => [...new Set(selected.value.map(f => f.business_system_id ?? ''))])
const people = computed(() => {
  const candidates = grants.value.filter(g => g.available)
  return candidates.filter((g, index) => candidates.findIndex(other => other.user_id === g.user_id) === index && selectedSystemIDs.value.every(id => candidates.some(other => other.user_id === g.user_id && other.business_system_id === id)))
})
async function load() {
  const sequence=++loadSequence
  loading.value = true; errorMessage.value=''
  try {
    const params: Record<string, string | number | boolean> = { page: page.value, status: status.value, keyword: keyword.value, overdue: overdue.value, work_order_id: executor.value ? '' : orderID.value, business_system_id: systemID.value, severity: severity.value, ...(special.value ? { [special.value]: true } : {}) }
    params['execution_blocked_reason'] = blockReason.value
    params['revived'] = revivedOnly.value
    const [list, statistics, overview, orderSummary] = await Promise.all([
      apiClient.get<Page<RepairTask>>(base.value, { params }),
      apiClient.get<RepairStatistics>(`${base.value}/statistics`, { params }),
      apiClient.get<RepairStatistics>(`${base.value}/statistics`, { params:{status:'all',work_order_id:params.work_order_id} }),
      !executor.value && orderID.value ? apiClient.get<{item:OrderSummary}>(`/security-work-orders/${orderID.value}/remediation-summary`) : Promise.resolve(undefined),
    ])
    if(sequence!==loadSequence)return
    stats.value=statistics.data;overviewStats.value=overview.data;rows.value=list.data.items;total.value=list.data.total;summary.value=orderSummary?.data.item;selectedTasks.value=[]
  } catch (error) { if(sequence===loadSequence){errorMessage.value=getAPIErrorMessage(error);rows.value=[];stats.value=undefined;overviewStats.value=undefined;summary.value=undefined;total.value=0} } finally { if(sequence===loadSequence)loading.value=false }
}

function open(id: string, queued=false) { if(!queued){reviewQueue.value=[];queueIndex.value=0} selectedID.value = id; drawer.value = true }
async function refresh() { await load() }
async function openAssign() {
  if (!summary.value) return
  try {
    findings.value = (await loadOptions<Finding>(`/security-work-orders/${orderID.value}/assignable-findings`)).filter(f => !f.assigned)
    grants.value = await loadOptions<Grant>('/remediation-grants', { organization_id: summary.value.assignee_organization_id })
    selected.value = []; findingSystem.value = ''; findingSeverity.value = ''; assignee.value = ''; resultErrors.value = []; assignmentKey.value = newKey(); assignment.due_at = summary.value.due_at; assignVisible.value = true
  } catch (error) { ElMessage.error(getAPIErrorMessage(error)) }
}
async function assign() {
  if (busy.value || !selected.value.length || !assignee.value || !assignment.due_at) { ElMessage.warning('请选择漏洞、具体人员和期限'); return }
  busy.value = true
  try {
    const items = selected.value.map(f => ({ finding_id: f.id, grant_id: grants.value.find(g => g.available && g.user_id === assignee.value && g.business_system_id === f.business_system_id)?.id, ...assignment, due_at: new Date(assignment.due_at).toISOString(), material: { title: f.title, severity: f.severity, affected_location: f.affected_location, description: f.description, evidence: f.evidence, remediation_advice: f.remediation_advice, requirement: f.requirement } }))
    const { data } = await apiClient.post<{ items: { id?: string; finding_id: string; error?: string }[] }>(`/security-work-orders/${orderID.value}/remediation-tasks/batch-assign`, { items }, { headers: { 'Idempotency-Key': assignmentKey.value } })
    resultErrors.value = data.items.filter((item): item is { finding_id: string; error: string } => Boolean(item.error))
    const succeeded = data.items.filter(item => item.id).length
    ElMessage[resultErrors.value.length ? 'warning' : 'success'](`已派发 ${succeeded} 项，失败 ${resultErrors.value.length} 项`)
    assignmentKey.value = newKey()
    if (!resultErrors.value.length) assignVisible.value = false
    else { const successIDs = new Set(data.items.filter(item => item.id).map(item => item.finding_id)); findings.value = findings.value.filter(f => !successIDs.has(f.id)); selected.value = selected.value.filter(f => !successIDs.has(f.id)) }
    await load()
  } catch (error) { ElMessage.error(getAPIErrorMessage(error)) } finally { busy.value = false }
}
async function acceptOrder() {
  if (busy.value) return; busy.value = true
  try { await apiClient.post(`/security-work-orders/${orderID.value}/accept`, { comment: '接收工单，安排个人修复任务' }); await load(); ElMessage.success('工单已接收，可以开始派单') } catch (error) { ElMessage.error(getAPIErrorMessage(error)) } finally { busy.value = false }
}
async function closeOrder() {
  try { const { value } = await ElMessageBox.prompt('所有漏洞必须已有逐项验证或获批处置结果，请填写关闭说明', '关闭主工单', { inputValidator: value => Boolean(value?.trim()) || '请填写说明' }); await apiClient.post(`/security-work-orders/${orderID.value}/close-individual`, { reason: value }); await load(); ElMessage.success('主工单已关闭') } catch (error) { if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error)) }
}
watch([status, overdue, orderID, executor, systemID, severity, special, blockReason, revivedOnly], () => { page.value = 1; void load() })
watch(viewKey, syncSavedView)
watch(() => route.fullPath, () => {
 status.value=typeof route.query.status==='string'?route.query.status:executor.value?'active':'all'
 special.value=typeof route.query.view==='string'?route.query.view:'';overdue.value=route.query.overdue==='true'
 orderID.value=typeof route.query.work_order_id==='string'?route.query.work_order_id:''
 page.value=1; if(typeof route.query.task_id==='string')open(route.query.task_id);else drawer.value=false
 void load()
})
watch(executor, async () => {
 try { systems.value=await loadOptions<SystemOption>(executor.value?'/repair/tasks/systems':'/remediation-options'); if(!executor.value)orders.value=(await loadOptions<Order>('/security-work-orders')).filter(o=>o.execution_mode==='individual') } catch(error) { ElMessage.error(getAPIErrorMessage(error)) }
})
onMounted(async () => {
  syncSavedView()
  if (!executor.value) { try { orders.value = (await loadOptions<Order>('/security-work-orders')).filter(o => o.execution_mode === 'individual'); systems.value = await loadOptions<SystemOption>('/remediation-options') } catch (error) { ElMessage.error(getAPIErrorMessage(error)) } }
  if (executor.value) { try { systems.value = await loadOptions<SystemOption>(`${base.value}/systems`) } catch(error) { ElMessage.error(getAPIErrorMessage(error)) } }
  await load(); if (typeof route.query.task_id === 'string') open(route.query.task_id)
})
</script>

<template>
  <section class="repair-page">
    <div class="page-heading"><div>
    <h1>{{ executor ? '我的修复任务' : '修复任务' }}</h1></div><div class="workspace-actions"><el-button v-if="!executor && auth.hasPermission('remediation.people.manage')" @click="$router.push('/remediation-people')">人员与授权</el-button><el-button v-if="!executor && auth.hasPermission('remediation.assign')" type="primary" @click="advanced = true">选择工单派发</el-button></div></div>
    <el-alert v-if="errorMessage" :title="errorMessage" type="error" :closable="false"><el-button @click="load">重新加载</el-button></el-alert>
    <div v-if="overviewStats" class="stat-grid"><button v-for="metric in metricCards" :key="metric.label" class="stat-card" :class="{danger:metric.danger}" @click="selectMetric(metric)"><span>{{ metric.label }}</span><strong>{{ metric.value }}</strong><small>{{ metric.hint }}</small></button></div>
    <section class="panel repair-list-panel">
      <div class="quick-views" role="group" aria-label="修复任务快捷视图"><button v-for="view in quickViews" :key="view.key" :class="{active:activeView===view.key}" :aria-pressed="activeView===view.key" @click="selectView(view.key)">{{ view.label }}</button></div>
      <div class="repair-toolbar"><el-input v-model="keyword" clearable aria-label="搜索修复任务" placeholder="搜索任务编号或漏洞标题" @keyup.enter="page=1;load()" @clear="page=1;load()" /><el-select v-model="severity" clearable aria-label="严重程度" placeholder="全部等级"><el-option v-for="(label,value) in severityLabels" :key="value" :value="value" :label="label" /></el-select><el-button :loading="loading" @click="page=1;load()">查询</el-button><el-button :aria-expanded="advanced" @click="advanced=!advanced">{{ advanced ? '收起筛选' : '更多筛选' }}</el-button><span class="toolbar-spacer" /><el-button v-if="savedView" text @click="applyView">应用已存视图</el-button><el-button @click="saveView">保存视图</el-button><el-button v-if="!executor" :aria-pressed="compact" @click="compact=!compact">{{ compact?'舒适行高':'紧凑行高' }}</el-button></div>
      <div v-if="advanced" class="advanced-filters"><div class="filters"><el-select v-if="!executor" v-model="orderID" clearable filterable placeholder="全部个人派单工单" style="width:300px"><el-option v-for="order in orders.filter(o => !onlyUnassigned || o.remediation_counts?.unassigned > 0)" :key="order.id" :value="order.id" :label="`${order.number} ${order.title}`" /></el-select>
      <el-checkbox v-if="!executor" v-model="onlyUnassigned" @change="orderID = ''">只选含未分派项的工单</el-checkbox>
      <el-select v-model="status" placeholder="全部状态" style="width:160px"><el-option label="全部状态（含已完成）" value="all" /><el-option label="未完成任务" value="active" /><el-option v-for="(label, value) in taskStatuses" :key="value" :label="label" :value="value" /></el-select><el-select v-model="systemID" clearable filterable placeholder="业务系统" style="width:200px"><el-option v-for="s in systems" :key="s.id" :value="s.id" :label="s.system_name" /></el-select><el-select v-model="special" clearable placeholder="快速筛选" style="width:170px"><el-option label="被退回" value="returned" /><el-option label="三天内到期" value="due_soon" /><el-option label="存在阻塞" value="blocked" /><el-option label="申请处理中" value="pending_request" /><el-option label="材料待复核" value="material_needs_review" /><el-option v-if="!executor" label="待改派（不可执行）" value="needs_reassignment" /><el-option v-if="!executor" label="待我审核" value="review_mine" /></el-select><el-checkbox v-model="overdue">仅逾期</el-checkbox><el-button :loading="loading" @click="page = 1; load()">查询</el-button>
<el-button text @click="resetFilters">重置条件</el-button></div><p v-if="!executor && !orderID" class="muted">选择个人派单工单后，可接收主工单、查看待分派漏洞并安排具体人员。</p></div>
      <div v-if="summary" class="summary"><span>{{ summary.work_order_no }} · 共 {{ summary.total }} 项 · 待分派 {{ summary.unassigned }} 项 · 截止 {{ dateText(summary.due_at) }}</span><span v-for="(count, value) in summary.counts" :key="value">{{ taskStatuses[value] }} {{ count }}</span>
      <el-button v-if="auth.hasPermission('remediation.assign') && summary.status === 'issued'" type="primary" :loading="busy" @click="acceptOrder">接收主工单</el-button>
      <el-button v-if="auth.hasPermission('remediation.assign') && summary.status === 'in_progress'" type="primary" @click="openAssign">派发修复任务</el-button>
      <el-button v-if="auth.canManageSecurityWorkOrders && ['in_progress','pending_verification'].includes(summary.status)" @click="closeOrder">关闭主工单</el-button>
    </div>
      <div v-if="selectedTasks.length && !executor" class="selection-bar"><span>已选择 {{ selectedTasks.length }} 项待审核任务</span><el-button type="primary" @click="startReview">进入逐项审核</el-button><small>每项分别填写意见并确认</small></div>
      <div v-if="executor" v-loading="loading" class="executor-layout"><div class="executor-tasks"><article v-for="row in rows" :key="row.id" class="executor-task" :class="{returned:row.return_reason}"><div class="task-card-meta"><el-tag :type="severityType(row.severity)" size="small">{{ severityLabels[row.severity] }}</el-tag><el-tag size="small">{{ outcomes[row.completion_reason ?? ''] || taskStatuses[row.status] }}</el-tag><span>{{ row.task_no }}</span></div><h2><button @click="open(row.id)">{{ row.title }}</button></h2><p class="muted">{{ row.system_name }} · {{ row.organization_name }} · 目标环境：{{ row.target_environment }}</p><div v-if="row.return_reason" class="task-feedback">退回原因：{{ row.return_reason }}</div><div v-if="row.material_needs_review || row.execution_blocked_reason || row.blocked_reason" class="task-feedback"><div v-if="row.material_needs_review">共享材料待复核。</div><div v-if="row.execution_blocked_reason">{{ blockText(row.execution_blocked_reason) }}</div><div v-if="row.execution_blocked_reason" class="muted">{{ blockAction(row.execution_blocked_reason) }}</div><div v-if="row.blocked_reason">{{ row.blocked_reason }}</div></div><div class="task-card-footer"><span :class="row.overdue?'overdue-text':'muted'">截止 {{ dateText(row.due_at) }}{{ row.overdue?' · 已逾期':'' }}</span><el-button :type="['pending_acceptance','in_progress'].includes(row.status)?'primary':'default'" @click="open(row.id)">{{ taskAction(row) }}</el-button></div></article><el-empty v-if="!loading && !rows.length" description="当前视图没有修复任务"><el-button @click="resetFilters">查看全部未完成任务</el-button></el-empty></div><aside class="executor-guide"><h3>一项任务，三步交付</h3><ol><li><strong>确认修复要求</strong><p>核对共享材料、目标环境与期限。</p></li><li><strong>完成部署与自测</strong><p>保留版本号、自测记录和证明附件。</p></li><li><strong>提交组织确认</strong><p>组织确认后进入集团复测。</p></li></ol><p class="guide-note">遇到依赖阻塞或期限冲突，可在任务内提出申请。延期获批前仍按原期限计时。</p></aside></div>
      <section v-if="!executor && (blockBreakdown.length || stats?.revived)" class="reassign-workbench">
        <div class="reassign-heading"><strong>待改派队列</strong><span class="muted">按不可执行原因分组；先恢复资产或系统，再决定改派或取消</span></div>
        <div class="reassign-grid"><button v-for="item in blockBreakdown" :key="item.reason" type="button" class="reassign-card" :aria-pressed="blockReason === item.reason" @click="filterByBlockReason(item.reason)"><span class="reassign-count">{{ item.count }}</span><span class="reassign-text">{{ item.text }}</span><span class="muted">{{ item.action }}</span></button></div>
        <el-alert v-if="blockReason" type="info" :closable="false" class="reassign-filtered">已按“{{ executionBlockReasons[blockReason]?.text || blockReason }}”筛选 <el-button link type="primary" @click="blockReason = ''; page = 1; load()">查看全部原因</el-button></el-alert>
        <div v-if="stats?.revived" class="reassign-revived"><el-button text type="warning" @click="showRevivedOnly()">{{ stats.revived }} 项任务因资产/系统恢复而重新回到当前工作，请确认是否继续</el-button></div>
      </section>
      <div v-if="revivedSelection.length && !executor" class="selection-bar"><span>已选择 {{ revivedSelection.length }} 项恢复待确认任务</span><el-button type="primary" :loading="acknowledging" @click="acknowledgeSelected">批量确认恢复跟进</el-button><small>确认表示已知悉这些任务重新回到当前工作；确认后可逐项改派、取消或继续执行</small><el-input v-model="reviveAckReason" placeholder="确认说明（可选，将记入任务时间线）" maxlength="200" style="width:280px" /></div>
      <el-table v-loading="loading" :data="rows" :class="{ 'compact-table':compact }" @selection-change="onSelectionChange">
        <template #empty><el-empty description="当前筛选下没有修复任务" :image-size="64"><el-button @click="resetFilters">清除筛选</el-button><el-button v-if="auth.hasPermission('remediation.assign')" type="primary" @click="advanced=true">选择工单安排修复</el-button></el-empty></template>
        <el-table-column type="selection" width="42" :selectable="(row:RepairTask)=>revivedSelectable(row)||(row.status==='pending_org_review'&&auth.hasPermission('remediation.review'))||(row.status==='pending_security_review'&&auth.canManageSecurityWorkOrders)" />
        <el-table-column label="任务 / 漏洞" min-width="210"><template #default="{row}"><el-button link type="primary" @click="open(row.id)">{{ row.title }}</el-button><div class="muted">{{ row.task_no }} <el-tag v-if="row.return_reason" type="warning" size="small">退回修复</el-tag></div></template></el-table-column>
        <el-table-column label="业务归属" min-width="145"><template #default="{row}">{{ row.system_name }}<div class="muted">{{ row.organization_name }}</div></template></el-table-column>
        <el-table-column label="不可执行原因 / 处理建议" min-width="190"><template #default="{row}"><template v-if="row.execution_blocked_reason"><span>{{ blockText(row.execution_blocked_reason) }}</span><div class="muted">{{ blockAction(row.execution_blocked_reason) }}</div></template><span v-else class="muted">—</span></template></el-table-column>
        <el-table-column label="负责人" min-width="115"><template #default="{row}">{{ row.assignee_name }}<div class="muted" :title="row.vendor_name">{{ row.vendor_name || '内部人员' }}</div></template></el-table-column>
        <el-table-column label="等级" width="75"><template #default="{row}"><el-tag :type="severityType(row.severity)" size="small">{{ severityLabels[row.severity] }}</el-tag></template></el-table-column>
        <el-table-column label="当前状态" min-width="135"><template #default="{row}"><el-tag size="small">{{ row.completion_reason==='risk_accepted'&&!row.risk_acceptance_current?'风险接受已失效':outcomes[row.completion_reason]||taskStatuses[row.status] }}</el-tag><div><el-tag v-if="row.material_needs_review" type="warning" size="small">材料待复核</el-tag><el-tag v-if="row.blocked_reason" type="warning" size="small">有阻塞</el-tag><el-tag v-if="row.execution_blocked_reason" :type="row.execution_blocked_reason==='system_unavailable'?'warning':'danger'" size="small">{{ executionBlockReasons[row.execution_blocked_reason]?.text || row.execution_blocked_reason }}</el-tag><el-tag v-if="row.revived && !row.revived_ack_at" type="warning" size="small">恢复待确认</el-tag></div></template></el-table-column>
        <el-table-column label="截止 / 优先级" min-width="135"><template #default="{row}"><span :class="{'overdue-text':row.overdue}">{{ dateText(row.due_at) }}</span><div v-if="row.overdue" class="overdue-text">已逾期</div></template></el-table-column>
        <el-table-column label="下一步" width="100" fixed="right"><template #default="{row}"><el-button link type="primary" @click="open(row.id)">{{ taskAction(row) }}</el-button></template></el-table-column>
      </el-table>
      <el-pagination v-model:current-page="page" :total="total" :page-size="20" layout="total, prev, pager, next" @current-change="load" />
    </section>
    <el-collapse v-if="stats" v-model="statisticsOpen" class="statistics-disclosure"><el-collapse-item title="查看当前筛选统计、耗时与工作量" name="statistics"><div class="summary"><strong>当前筛选统计</strong><span>复测通过 {{ stats.verified_fixed }}</span><span>接受风险 {{ stats.risk_accepted }}</span><span>确认误报 {{ stats.false_positive }}</span><span>风险接受已失效 {{ stats.risk_expired }}</span><span>逾期 {{ stats.overdue }}</span><span>已延期 {{ stats.extended }}</span></div><div class="summary"><span>平均执行耗时（含待接收）：{{ hours(stats.execution_seconds) }}</span><span>组织确认等待：{{ hours(stats.organization_seconds) }}</span><span>集团复测等待：{{ hours(stats.security_seconds) }}</span></div><p class="muted">按当前可见任务及各次状态流转累计；正在处理的阶段统计到当前时间。切换“全部状态”可查看历史完成结果。</p><el-collapse v-if="!executor"><el-collapse-item title="人员与厂商工作量（当前可见范围）"><el-table :data="stats.workload"><el-table-column prop="organization_name" label="组织" /><el-table-column prop="assignee_name" label="人员" /><el-table-column prop="vendor_name" label="厂商" /><el-table-column prop="total" label="全部任务" /><el-table-column prop="active" label="未完成" /></el-table></el-collapse-item></el-collapse></el-collapse-item></el-collapse>

    <el-dialog v-model="drawer" title="修复任务详情" class="repair-workspace-dialog" width="min(1180px, 96vw)" top="3vh" destroy-on-close :close-on-click-modal="false"><div v-if="reviewQueue.length" class="selection-bar"><span>审核队列 {{ queueIndex+1 }} / {{ reviewQueue.length }}</span><el-button @click="nextReview">{{ queueIndex+1<reviewQueue.length?'跳过此项':'结束队列' }}</el-button></div><RepairTaskDetail v-if="selectedID" :key="selectedID" :id="selectedID" :executor="executor" @changed="refresh" @reviewed="reviewQueue.length && nextReview()" /></el-dialog>
    <el-dialog v-model="assignVisible" title="派发修复任务" width="min(96%, 1000px)" destroy-on-close>
      <el-alert title="请逐项确认并编辑接收人员可见的内容。完整内部报告不会自动共享。缺少业务系统关联的漏洞需先补齐。" type="info" :closable="false" />
      <div class="filters"><el-select v-model="findingSystem" clearable placeholder="筛选业务系统"><el-option v-for="s in systems" :key="s.id" :value="s.id" :label="s.system_name" /></el-select><el-select v-model="findingSeverity" clearable placeholder="筛选严重程度"><el-option v-for="(label, value) in severityLabels" :key="value" :value="value" :label="label" /></el-select></div>
      <el-table :data="visibleFindings" @selection-change="selected = $event"><el-table-column type="selection" :selectable="(row: Finding) => Boolean(row.business_system_id)" width="45" /><el-table-column prop="title" label="漏洞" /><el-table-column label="严重程度" width="100"><template #default="{ row }">{{ severityLabels[row.severity] }}</template></el-table-column><el-table-column label="状态" width="140"><template #default="{ row }">{{ row.business_system_id ? '可分派' : '缺少业务系统' }}</template></el-table-column></el-table>
      <el-alert v-if="selected.length && !people.length" title="所选系统没有共同的有效负责人，请按系统分组派发或先补充人员授权。" type="warning" :closable="false" /><el-form label-position="top" class="assignment-form"><el-form-item label="具体负责人"><el-select v-model="assignee" filterable style="width:100%"><el-option v-for="g in people" :key="g.user_id" :value="g.user_id" :label="`${g.display_name} · ${g.vendor_name || '内部人员'} · 未完成 ${g.active_task_count} 项 · 授权至 ${dateText(g.valid_until)}`" /></el-select></el-form-item><el-form-item label="修复期限"><el-date-picker v-model="assignment.due_at" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" /></el-form-item><el-form-item label="验收目标环境"><el-input v-model="assignment.target_environment" /></el-form-item><el-checkbox v-model="assignment.deployment_required">需部署到目标环境后才能提交最终修复</el-checkbox>
      <el-collapse><el-collapse-item v-for="f in selected" :key="f.id" :title="`共享材料预览：${f.title}`" :name="f.id"><el-form-item label="受影响位置"><el-input v-model="f.affected_location" /></el-form-item><el-form-item label="漏洞描述"><el-input v-model="f.description" type="textarea" :rows="3" /></el-form-item><el-form-item label="复现步骤与证据"><el-input v-model="f.evidence" type="textarea" :rows="3" /></el-form-item><el-form-item label="修复建议"><el-input v-model="f.remediation_advice" type="textarea" :rows="2" /></el-form-item><el-form-item label="验收要求"><el-input v-model="f.requirement" type="textarea" :rows="2" /></el-form-item></el-collapse-item></el-collapse>
      </el-form><el-alert v-for="item in resultErrors" :key="item.finding_id" :title="`${findings.find(f => f.id === item.finding_id)?.title || item.finding_id}：${item.error}`" type="error" :closable="false" />
      <template #footer><el-button @click="assignVisible = false">取消</el-button><el-button type="primary" :loading="busy" :disabled="!selected.length || !assignee" @click="assign">确认共享并派发 {{ selected.length }} 项</el-button></template>
    </el-dialog>
  </section>
</template>

<style scoped>
.repair-page { display:grid;gap:20px; }.repair-page .page-heading { margin-bottom:0; }.muted { color:var(--muted);font-size:12px;line-height:1.7; }.filters,.summary { display:flex;gap:12px;align-items:center;flex-wrap:wrap; }.summary { margin:16px 0;padding:12px;background:var(--el-fill-color-light);border-radius:8px;font-size:13px; }.repair-list-panel { padding:0;overflow:hidden;min-width:0; }
.quick-views { display:flex;gap:24px;padding:0 20px;border-bottom:1px solid var(--line);overflow-x:auto; }.quick-views button { border:0;background:none;color:var(--muted);padding:17px 0;white-space:nowrap;cursor:pointer;font-size:13px;border-bottom:2px solid transparent; }.quick-views button.active { color:var(--cyan);border-bottom-color:var(--cyan); }
.repair-toolbar { display:flex;gap:8px;padding:16px 20px;align-items:center;flex-wrap:wrap; }.repair-toolbar>.el-input { width:260px; }.repair-toolbar>.el-select { width:120px; }.repair-toolbar .el-button { margin:0; }.toolbar-spacer { flex:1; }.advanced-filters { padding:16px 20px;border-top:1px solid var(--line);background:#f8fafc; }.advanced-filters .el-select { max-width:100%; }.repair-list-panel>.summary { margin:0 20px 16px; }
.selection-bar { display:flex;gap:14px;align-items:center;flex-wrap:wrap;background:#eaf5f2;padding:12px 20px;color:var(--cyan); }.selection-bar small { color:var(--muted); }.overdue-text { color:var(--el-color-danger);font-size:12px; }.el-pagination { padding:18px 20px; }.assignment-form { margin-top:20px; }.el-tag { margin:2px; }.statistics-disclosure { border:1px solid var(--line);border-radius:10px;padding:0 20px;background:white; }.statistics-disclosure :deep(.el-collapse-item__wrap),.statistics-disclosure :deep(.el-collapse-item__header) { border:0; }.compact-table :deep(td.el-table__cell) { padding:7px 0; }
.reassign-workbench { margin:16px 0;padding:16px;border:1px solid var(--line);border-radius:9px; }.reassign-heading { display:flex;align-items:baseline;gap:12px;flex-wrap:wrap;margin-bottom:12px; }.reassign-heading strong { font-size:15px;font-weight:500; }.reassign-grid { display:grid;grid-template-columns:repeat(auto-fill,minmax(220px,1fr));gap:10px; }.reassign-card { display:grid;gap:4px;text-align:left;padding:12px;border:1px solid var(--line);border-radius:8px;background:#fff;cursor:pointer;font:inherit;color:inherit; }.reassign-card:hover { border-color:var(--cyan); }.reassign-card[aria-pressed='true'] { border-color:var(--cyan);background:#f0f7f6; }.reassign-count { font-size:22px;font-weight:500; }.reassign-text { font-size:13px; }.reassign-card .muted { font-size:12px;line-height:1.6; }.reassign-filtered { margin-top:12px; }
.executor-layout { display:grid;grid-template-columns:minmax(0,1fr) 270px;gap:20px;padding:20px; }.executor-tasks { display:grid;gap:14px;min-width:0; }.executor-task { padding:20px;border:1px solid var(--line);border-radius:9px; }.executor-task.returned { border-left:3px solid #c88935; }.task-card-meta { display:flex;align-items:center;gap:7px; }.task-card-meta>span:last-child { margin-left:auto;color:var(--muted);font-size:12px; }.executor-task h2 { font-size:16px;font-weight:500;margin:16px 0 8px; }.executor-task h2 button { font:inherit;color:var(--ink);background:none;border:0;padding:0;text-align:left;cursor:pointer; }.task-feedback { background:#fff5e6;color:#895a21;padding:12px;margin-top:16px;border-radius:6px;font-size:13px;line-height:1.7; }.task-card-footer { display:flex;justify-content:space-between;align-items:center;gap:12px;margin-top:20px; }.executor-guide { align-self:start;padding:20px;border:1px solid var(--line);border-radius:9px;font-size:13px; }.executor-guide h3 { margin:0 0 24px;font-size:15px;font-weight:500; }.executor-guide ol { padding-left:18px; }.executor-guide li { margin-bottom:24px; }.executor-guide strong { font-weight:500; }.executor-guide p { color:var(--muted);line-height:1.7;font-size:12px; }.guide-note { background:#f3f6f8;border-radius:6px;padding:12px; }
@media(max-width:1100px) { .executor-layout { grid-template-columns:1fr; }.executor-guide { display:none; } }
@media(max-width:760px) { .repair-toolbar { padding:14px; }.repair-toolbar>.el-input { width:100%; }.toolbar-spacer { display:none; }.quick-views { gap:20px;padding:0 14px; }.executor-layout { padding:12px; }.executor-task { padding:15px; }.task-card-footer { flex-wrap:wrap; }.filters>* { max-width:100%; }.stat-grid { gap:10px; } }
</style>

<style scoped>
.repair-list-panel :deep(.el-table .muted) { white-space:nowrap;overflow:hidden;text-overflow:ellipsis; }.repair-list-panel :deep(.el-table td.el-table__cell) { padding-top:12px;padding-bottom:12px; }.repair-list-panel :deep(.compact-table td.el-table__cell) { padding-top:7px;padding-bottom:7px; }
</style>
