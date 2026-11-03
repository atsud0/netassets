<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'
import type { RepairStatistics } from '../api/remediation'
import { useAuthStore } from '../stores/auth'
import { findingStatusTagType } from '../constants/findingStatus'

interface TaskProgress {
  id: string
  name: string
  status: string
  deadline_at: string
  target_count: number
  submitted_count: number
  approved_count: number
  completion_rate: number
}
interface DashboardSummary {
  organization_count: number
  system_count: number
  asset_count: number
  public_asset_count: number
  unlinked_asset_count: number
  sensitive_public_system_count: number
  active_task_count: number
  pending_review_count: number
  pending_task_count: number
  returned_task_count: number
  overdue_count: number
  active_work_order_count: number
  pending_verification_work_order_count: number
  overdue_work_order_count: number
  due_soon_work_order_count: number
  latest_task?: TaskProgress
}
interface SecurityBreakdown { key: string; label: string; count: number }
interface SecurityTrendPoint { date: string; new_finding_count: number; closed_finding_count: number; dispatched_finding_count: number }
interface OrganizationPerformance {
  organization_id: string; organization_code: string; organization_name: string
  active_work_order_count: number; overdue_work_order_count: number
  closed_work_order_count: number; closed_on_time_count: number; timely_rate: number
  undispatched_finding_count: number; active_finding_count: number; overdue_finding_count: number
  pending_verification_work_order_count: number; pending_verification_finding_count: number; closed_finding_count: number
}
interface TestCoverage {
  public_asset_count: number; tested_public_asset_count: number; public_asset_coverage_rate: number
  active_batch_count: number; completed_batch_count: number
  test_finding_count: number; closed_test_finding_count: number; test_finding_closure_rate: number
}
interface SecurityOverview {
  can_view_undispatched: boolean
  closure_funnel: Record<string, number>
  undispatched_findings: { finding_count: number; critical_count: number; high_count: number; organization_count: number; business_system_count: number; oldest_discovered_at?: string }
  work_order_findings: Record<string, number>
  pending_asset_finding_count: number
  retired_asset_finding_count: number
  finding_severity: SecurityBreakdown[]
  finding_status: SecurityBreakdown[]
  finding_trend: SecurityTrendPoint[]
  organization_performance: OrganizationPerformance[]
  test_coverage: TestCoverage
}

const auth = useAuthStore()
const router = useRouter()
const loading = ref(false)
const repairStats = ref<RepairStatistics>()
const repairError = ref(false)
const canUseRepair = computed(() => auth.hasPermission('remediation.assign') || auth.hasPermission('remediation.review') || auth.canManageSecurityWorkOrders)
const pendingRepairReview = computed(() => (auth.hasPermission('remediation.review')?(repairStats.value?.counts.pending_org_review??0):0)+(auth.canManageSecurityWorkOrders?(repairStats.value?.counts.pending_security_review??0):0))
async function loadRepair() {
 if(!canUseRepair.value)return
 try {repairStats.value=(await apiClient.get<RepairStatistics>('/remediation-tasks/statistics',{params:{status:'all'}})).data;repairError.value=false} catch {repairError.value=true}
}
function toRepair(view?:string) { void router.push({path:'/remediation',query:view==='overdue'?{overdue:'true'}:view?{view}:{}}) }

const summary = ref<DashboardSummary>({
  organization_count: 0, system_count: 0, asset_count: 0, public_asset_count: 0,
  unlinked_asset_count: 0, sensitive_public_system_count: 0, active_task_count: 0,
  pending_review_count: 0, pending_task_count: 0, returned_task_count: 0, overdue_count: 0,
  active_work_order_count: 0, pending_verification_work_order_count: 0,
  overdue_work_order_count: 0, due_soon_work_order_count: 0,
})
const overview = ref<SecurityOverview>({
  can_view_undispatched: false, closure_funnel: {}, work_order_findings: {},
  undispatched_findings: { finding_count: 0, critical_count: 0, high_count: 0, organization_count: 0, business_system_count: 0 },
  pending_asset_finding_count: 0,
  retired_asset_finding_count: 0,
  finding_severity: [], finding_status: [], finding_trend: [], organization_performance: [],
  test_coverage: {
    public_asset_count: 0, tested_public_asset_count: 0, public_asset_coverage_rate: 0,
    active_batch_count: 0, completed_batch_count: 0,
    test_finding_count: 0, closed_test_finding_count: 0, test_finding_closure_rate: 0,
  },
})

const stats = computed(() => [
 {label:canUseRepair.value?'待我审核的修复任务':'待复测工单',value:canUseRepair.value?(repairStats.value?pendingRepairReview.value:'—'):(overview.value.work_order_findings.pending_verification_work_order_count ?? summary.value.pending_verification_work_order_count),hint:canUseRepair.value?'核对当前提交版本与证明材料':`涉及漏洞 ${overview.value.work_order_findings.pending_verification_finding_count ?? '—'} 条`,action:()=>canUseRepair.value?toRepair('review_mine'):toWorkOrders('pending_verification')},
 {label:'逾期整改工单',value:overview.value.work_order_findings.overdue_work_order_count ?? summary.value.overdue_work_order_count,hint:`涉及漏洞 ${overview.value.work_order_findings.overdue_finding_count ?? '—'} 条`,danger:true,action:()=>toWorkOrders(undefined,true)},
 {label:'处理中工单',value:overview.value.work_order_findings.active_work_order_count ?? summary.value.active_work_order_count,hint:`涉及漏洞 ${overview.value.work_order_findings.active_finding_count ?? '—'} 条`,action:()=>toWorkOrders('active')},
 auth.hasPermission('inventory.manage')?{label:auth.isGroupAdmin?'待审核填报':'待填报任务',value:auth.isGroupAdmin?summary.value.pending_review_count:summary.value.pending_task_count,hint:`${summary.value.overdue_count} 项填报已逾期`,action:()=>router.push('/inventory-tasks')}:{label:'已关闭工单',value:overview.value.work_order_findings.closed_work_order_count ?? '—',hint:`涉及漏洞 ${overview.value.work_order_findings.closed_finding_count ?? '—'} 条`,action:()=>toWorkOrders('closed')},
])
const trendMax = computed(() => Math.max(1, ...overview.value.finding_trend.flatMap(item => [item.new_finding_count, item.closed_finding_count, item.dispatched_finding_count || 0])))
const latestTrend = computed(() => overview.value.finding_trend.slice(-14))
const severityTotal = computed(() => overview.value.finding_severity.reduce((total, item) => total + item.count, 0))
const closureStages = computed(() => [
  { label: '已发现漏洞', key: 'discovered', query: {} },
  { label: '已关联资产', key: 'asset_linked', query: { asset_link_state: 'linked' } },
  { label: '已派发工单', key: 'dispatched', query: { dispatch_state: 'dispatched' } },
  { label: '整改中', key: 'remediating', query: { dispatch_state: 'remediating' } },
  { label: '待复测', key: 'pending_verification', query: { dispatch_state: 'pending_verification' } },
  { label: '已闭环', key: 'closed', query: { dispatch_state: 'closed' } },
].map(stage => ({ ...stage, count: overview.value.closure_funnel[`${stage.key}_finding_count`] || 0, orders: overview.value.closure_funnel[`${stage.key}_work_order_count`] })))
const orderCoverage = computed(() => [
  { label: '处理中工单', key: 'active', status: 'active', overdue: false },
  { label: '待复测工单', key: 'pending_verification', status: 'pending_verification', overdue: false },
  { label: '逾期工单', key: 'overdue', status: undefined, overdue: true },
  { label: '已关闭工单', key: 'closed', status: 'closed', overdue: false },
])
const undispatchedRiskRate = computed(() => {
  const gap = overview.value.undispatched_findings
  return gap.finding_count ? Math.round((gap.critical_count + gap.high_count) * 100 / gap.finding_count) : 0
})

async function load() {
  loading.value = true
  try {
    await Promise.all([
      apiClient.get<{ summary: DashboardSummary }>('/dashboard/summary').then(response => {
        summary.value = response.data.summary
      }),
      apiClient.get<{ overview: SecurityOverview }>('/dashboard/security-overview').then(response => {
        overview.value = { ...overview.value, ...response.data.overview }
      }),
    ])
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取安全治理统计失败'))
  } finally {
    loading.value = false
  }
}

function formatDate(value?: string) {
  if (!value) return '—'
  return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}
function findingStatusType(status: string) {
  return findingStatusTagType(status)
}
function severityType(severity: string) {
  return severity === 'critical' ? 'danger' : severity === 'high' ? 'warning' : severity === 'medium' ? 'primary' : 'info'
}
function toFindings(status?: string, assetLinkState?: string, filters: Record<string, string | undefined> = {}) {
  void router.push({ path: '/security-work-orders', query: { tab: 'findings', dashboard_scope: '1', ...(status ? { status } : {}), ...(assetLinkState ? { asset_link_state: assetLinkState } : {}), ...filters } })
}
function toWorkOrders(status?: string, overdueOnly = false, organizationID?: string) {
  void router.push({ path: '/security-work-orders', query: { tab: 'orders', dashboard_scope: '1', ...(status ? { status } : {}), ...(overdueOnly ? { overdue: '1' } : {}), ...(organizationID ? { organization_id: organizationID } : {}) } })
}
function toOrganizationOrders(organizationID: string) {
  void router.push({ path: '/security-work-orders', query: { tab: 'orders', dashboard_scope: '1', organization_id: organizationID } })
}
function onOrganizationRowClick(row: OrganizationPerformance) {
  toOrganizationOrders(row.organization_id)
}

onMounted(() => { void load();void loadRepair() })
</script>

<template>
  <section v-loading="loading">
    <div class="page-heading"><h1>今天，先处理关键事项</h1>
      <div class="heading-actions">
        <el-button v-if="auth.hasPermission('inventory.manage')" @click="router.push('/inventory-tasks')">{{ auth.isGroupAdmin ? '创建填报任务' : '处理填报任务' }}</el-button>
        <el-button type="primary" @click="canUseRepair ? toRepair() : toWorkOrders()">{{ canUseRepair ? '查看修复任务' : '处理安全工单' }}</el-button>
      </div>
    </div>

    <div class="stat-grid">
      <button v-for="stat in stats" :key="stat.label" class="stat-card" :class="{danger:stat.danger}" @click="stat.action()">
        <span>{{ stat.label }}</span><strong>{{ stat.value }}</strong><small>{{ stat.hint }}</small>
      </button>
    </div>

    <div v-if="summary.overdue_work_order_count" class="workspace-notice"><span>有 {{ summary.overdue_work_order_count }} 张整改工单已逾期，请优先跟进责任组织。</span><el-button size="small" @click="toWorkOrders(undefined,true)">查看逾期工单</el-button></div>
    <div class="dashboard-priority-grid">
      <article class="panel"><div class="panel-header"><div><h2>需要你处理</h2><p>按当前职责查看待办，并进入对应处理队列。</p></div><el-tag effect="plain">今日优先</el-tag></div>
        <div v-if="canUseRepair" class="dashboard-todo"><div><strong>核验修复提交</strong><p>{{ repairError?'修复待办暂时读取失败':`待我审核 ${repairStats?pendingRepairReview:'—'} 项 · 逐项确认版本与验证结果` }}</p></div><el-button v-if="repairError" @click="loadRepair">重试</el-button><el-button v-else @click="toRepair('review_mine')">进入审核</el-button></div>
        <div v-if="canUseRepair" class="dashboard-todo"><div><strong>跟进逾期修复</strong><p>逾期任务 {{ repairStats?.overdue ?? '—' }} 项，联系负责人排除阻塞</p></div><el-button @click="toRepair('overdue')">查看任务</el-button></div>
        <div v-if="auth.hasPermission('remediation.assign')" class="dashboard-todo"><div><strong>安排修复负责人</strong><p>选择已下发的个人派单工单，确认材料后派给具体人员</p></div><el-button @click="toRepair()">安排人员</el-button></div>
        <div v-if="auth.hasPermission('inventory.manage')" class="dashboard-todo"><div><strong>{{ auth.isGroupAdmin?'审核资产填报':'完成资产填报' }}</strong><p>填报进度和整改反馈集中处理</p></div><el-button @click="router.push('/inventory-tasks')">查看填报</el-button></div>
      </article>
      <article class="panel"><div class="panel-header"><div><h2>资产治理</h2><p>补齐业务归属与责任人，支撑后续派单。</p></div></div><div class="dashboard-asset-metrics"><div><strong>{{ summary.asset_count }}</strong><span>网络资产</span></div><div><strong>{{ summary.system_count }}</strong><span>业务系统</span></div><div><strong>{{ summary.unlinked_asset_count }}</strong><span>待关联资产</span></div></div><p>公网资产 {{ summary.public_asset_count }} 项 · 公网敏感系统 {{ summary.sensitive_public_system_count }} 个</p><div class="dashboard-actions"><el-button v-if="auth.hasPermission('asset.manage')" @click="router.push('/assets')">查看资产台账</el-button><el-button v-if="auth.hasPermission('system.manage')" @click="router.push('/systems')">业务系统</el-button></div></article>
    </div>
    <section class="security-overview-section">
      <div class="section-heading">
        <div><span class="eyebrow">SECURITY GOVERNANCE</span><h2>安全漏洞整改闭环</h2><p>发现、关联资产、派发、整改、复测与闭环。</p></div>
        <el-button text type="primary" @click="toFindings()">查看安全发现</el-button>
      </div>
      <details class="closure-definition"><summary>统计口径</summary><p>漏洞按发现 ID 去重；只统计已下发且未取消的有效工单。存在多张工单时优先使用当前未关闭工单，再按最近下发时间选择。整改中包括待接收、执行中和退回整改；已闭环要求工单关闭且漏洞完成集团确认或有权限的其他处置。工单数按工单 ID 统计，历史工单可保留，涉及漏洞数按当前有效工单归属计算。</p></details>

      <div class="closure-stage-grid">
        <button v-for="stage in closureStages" :key="stage.key" class="security-highlight-card clickable" type="button" :data-stage="stage.key" @click="toFindings(undefined, undefined, stage.query)">
          <span>{{ stage.label }}</span><strong>{{ stage.count }}<em> 条</em></strong>
          <small>占发现 {{ overview.closure_funnel.discovered_finding_count ? Math.round(stage.count * 100 / overview.closure_funnel.discovered_finding_count) : 0 }}%<template v-if="stage.orders !== undefined"> · {{ stage.orders }} 张工单</template></small>
        </button>
      </div>
      <button v-if="overview.can_view_undispatched" class="undispatched-gap security-highlight-card clickable" type="button" data-testid="undispatched-gap" @click="toFindings(undefined, undefined, { dispatch_state: 'undispatched' })">
        <span>已发现但未派发</span><strong>{{ overview.undispatched_findings.finding_count }}<em> 条漏洞</em></strong>
        <small>严重 {{ overview.undispatched_findings.critical_count }} · 高危 {{ overview.undispatched_findings.high_count }}（合计 {{ undispatchedRiskRate }}%） · {{ overview.undispatched_findings.organization_count }} 个组织 · {{ overview.undispatched_findings.business_system_count }} 个业务系统</small>
        <small>最早发现 {{ formatDate(overview.undispatched_findings.oldest_discovered_at) }}</small>
      </button>
      <div class="work-order-coverage-grid">
        <button v-for="card in orderCoverage" :key="card.key" type="button" class="security-highlight-card clickable" :data-coverage="card.key" @click="toWorkOrders(card.status, card.overdue)">
          <span>{{ card.label }}</span><strong>{{ overview.work_order_findings[`${card.key}_work_order_count`] || 0 }}<em> 张</em></strong>
          <small>涉及漏洞 {{ overview.work_order_findings[`${card.key}_finding_count`] || 0 }} 条</small>
        </button>
      </div>

      <div class="security-content-grid">
        <article class="panel security-panel">
          <div class="panel-header"><div><h2>漏洞风险分布</h2><p>当前可见安全发现共 {{ severityTotal }} 项。</p></div><el-button text type="primary" @click="toFindings()">查看明细</el-button></div>
          <div class="breakdown-list">
            <button v-for="item in overview.finding_severity" :key="item.key" type="button" class="breakdown-row" @click="toFindings(undefined, undefined, { severity: item.key })">
              <span><el-tag :type="severityType(item.key) as any" size="small">{{ item.label }}</el-tag></span>
              <el-progress :percentage="severityTotal ? Math.round(item.count * 100 / severityTotal) : 0" :show-text="false" :stroke-width="9" />
              <strong>{{ item.count }}</strong>
            </button>
          </div>
          <div class="status-tag-list">
            <button v-for="item in overview.finding_status" :key="item.key" type="button" @click="toFindings(item.key)"><el-tag :type="findingStatusType(item.key) as any" effect="plain">{{ item.label }} {{ item.count }}</el-tag></button>
          </div>
        </article>

        <article class="panel security-panel">
          <div class="panel-header"><div><h2>近 14 天闭环趋势</h2><p>以漏洞条数对比新增发现、派发和复测闭环。</p></div></div>
          <div class="trend-chart" aria-label="近14天漏洞趋势">
            <div v-for="item in latestTrend" :key="item.date" class="trend-column" :title="`${item.date}：新增发现 ${item.new_finding_count} 条，派发 ${item.dispatched_finding_count || 0} 条，闭环 ${item.closed_finding_count} 条漏洞`">
              <div class="trend-bars">
                <button class="trend-bar new" :aria-label="`${item.date} 新增发现 ${item.new_finding_count} 条`" :style="{ height: item.new_finding_count ? `${Math.max(3, item.new_finding_count * 100 / trendMax)}%` : '0' }" @click="toFindings(undefined, undefined, { discovered_date: item.date })" />
                <button class="trend-bar dispatched" :aria-label="`${item.date} 派发 ${item.dispatched_finding_count || 0} 条`" :style="{ height: item.dispatched_finding_count ? `${Math.max(3, item.dispatched_finding_count * 100 / trendMax)}%` : '0' }" @click="toFindings(undefined, undefined, { dispatched_date: item.date })" />
                <button class="trend-bar closed" :aria-label="`${item.date} 复测闭环 ${item.closed_finding_count} 条`" :style="{ height: item.closed_finding_count ? `${Math.max(3, item.closed_finding_count * 100 / trendMax)}%` : '0' }" @click="toFindings(undefined, undefined, { closed_date: item.date })" />
              </div>
              <small>{{ item.date.slice(5) }}</small>
            </div>
          </div>
          <div class="trend-legend"><span><i class="legend-dot new" />新增发现</span><span><i class="legend-dot dispatched" />派发漏洞</span><span><i class="legend-dot closed" />复测闭环</span></div>
        </article>
      </div>

      <div class="security-content-grid">
        <article class="panel security-panel test-coverage-panel">
          <div class="panel-header"><div><h2>安全测试覆盖</h2><p>以已完成批次统计公网资产覆盖与测试发现闭环。</p></div><el-button v-if="auth.canManageSecurityTests" text type="primary" @click="router.push('/security-test-batches')">查看测试批次</el-button></div>
          <div class="coverage-progress-row"><div><span>公网资产已测试</span><strong>{{ overview.test_coverage.public_asset_coverage_rate }}%</strong></div><el-progress :percentage="overview.test_coverage.public_asset_coverage_rate" :stroke-width="12" :show-text="false" /></div>
          <div class="coverage-progress-row"><div><span>测试发现已关闭</span><strong>{{ overview.test_coverage.test_finding_closure_rate }}%</strong></div><el-progress :status="overview.test_coverage.test_finding_count > 0 && overview.test_coverage.test_finding_closure_rate === 100 ? 'success' : undefined" :percentage="overview.test_coverage.test_finding_closure_rate" :stroke-width="12" :show-text="false" /></div>
          <div class="coverage-metrics"><span>执行中批次 <strong>{{ overview.test_coverage.active_batch_count }}</strong></span><span>已完成批次 <strong>{{ overview.test_coverage.completed_batch_count }}</strong></span><button type="button" @click="toFindings(undefined, undefined, { test_batch_only: '1' })">测试发现 <strong>{{ overview.test_coverage.test_finding_count }}</strong></button></div>
        </article>

        <article class="panel security-panel">
          <div class="panel-header"><div><h2>{{ auth.hasGroupSecurityRole ? '子公司整改效能' : '本组织整改效能' }}</h2><p>按工单关闭时间计算按期整改率，优先关注逾期项。</p></div><el-button text type="primary" @click="toWorkOrders()">查看工单</el-button></div>
          <el-table :data="overview.organization_performance" size="small" max-height="220" empty-text="暂无工单数据" @row-click="onOrganizationRowClick">
            <el-table-column prop="organization_name" label="组织" min-width="145" />
            <el-table-column v-if="overview.can_view_undispatched" label="未派发漏洞" min-width="100"><template #default="{ row }"><el-button link @click.stop="toFindings(undefined, undefined, { organization_id: row.organization_id, dispatch_state: 'undispatched' })">{{ row.undispatched_finding_count }} 条</el-button></template></el-table-column>
            <el-table-column label="处理中 / 涉及漏洞" min-width="155"><template #default="{ row }"><el-button link @click.stop="toWorkOrders('active', false, row.organization_id)">{{ row.active_work_order_count }} 张 / {{ row.active_finding_count }} 条</el-button></template></el-table-column>
            <el-table-column label="待复测 / 涉及漏洞" min-width="155"><template #default="{ row }"><el-button link @click.stop="toWorkOrders('pending_verification', false, row.organization_id)">{{ row.pending_verification_work_order_count }} 张 / {{ row.pending_verification_finding_count }} 条</el-button></template></el-table-column>
            <el-table-column label="逾期 / 涉及漏洞" min-width="150"><template #default="{ row }"><el-button link :type="row.overdue_work_order_count ? 'danger' : 'default'" @click.stop="toWorkOrders(undefined, true, row.organization_id)">{{ row.overdue_work_order_count }} 张 / {{ row.overdue_finding_count }} 条</el-button></template></el-table-column>
            <el-table-column label="按期整改率" width="110" align="right"><template #default="{ row }"><el-button link @click.stop="toWorkOrders('closed', false, row.organization_id)">{{ row.closed_work_order_count ? `${row.timely_rate}%` : '—' }}</el-button></template></el-table-column>
          </el-table>
        </article>
      </div>
    </section>
    <div v-if="!auth.isSecurityOperator" class="content-grid">
      <article class="panel dashboard-panel">
        <div class="panel-header">
          <div><h2>填报进度</h2><p>{{ auth.isGroupAdmin ? '最近一项进行中或已完成的集团任务。' : '最近一项与本组织相关的填报任务。' }}</p></div>
          <el-button text type="primary" @click="router.push('/inventory-tasks')">查看全部</el-button>
        </div>
        <template v-if="summary.latest_task">
          <div class="latest-task-heading">
            <div><strong>{{ summary.latest_task.name }}</strong><span>截止 {{ formatDate(summary.latest_task.deadline_at) }}</span></div>
            <el-tag :type="summary.latest_task.status === 'completed' ? 'success' : 'primary'" effect="plain">{{ summary.latest_task.status === 'completed' ? '已完成' : '进行中' }}</el-tag>
          </div>
          <el-progress :percentage="summary.latest_task.completion_rate" :stroke-width="12" />
          <div class="task-progress-metrics">
            <span>目标组织 <strong>{{ summary.latest_task.target_count }}</strong></span>
            <span>已提交 <strong>{{ summary.latest_task.submitted_count }}</strong></span>
            <span>已审核 <strong>{{ summary.latest_task.approved_count }}</strong></span>
          </div>
        </template>
        <el-empty v-else description="暂无可展示的填报任务" :image-size="70" />
      </article>

      <article class="panel dashboard-panel">
        <div class="panel-header">
          <div><h2>资产治理提示</h2><p>优先补全公网暴露、未关联业务系统的资产信息。</p></div>
          <el-button text type="primary" @click="router.push('/assets')">查看资产</el-button>
        </div>
        <ul class="metric-list dashboard-metrics">
          <li><span>未关联业务系统资产</span><strong>{{ summary.unlinked_asset_count }}</strong></li>
          <li><span>公网网络资产</span><strong>{{ summary.public_asset_count }}</strong></li>
          <li><span>公网且涉及敏感个人信息系统</span><strong>{{ summary.sensitive_public_system_count }}</strong></li>
          <li v-if="auth.isGroupAdmin"><span>进行中填报任务</span><strong>{{ summary.active_task_count }}</strong></li>
        </ul>
        <div class="dashboard-actions"><el-button size="small" @click="router.push('/systems')">查看业务系统</el-button><el-button size="small" @click="router.push('/assets')">查看网络资产</el-button></div>
      </article>
    </div>

  </section>
</template>

<style scoped>
.heading-actions { display: flex; gap: 10px; flex-wrap: wrap; }
.panel-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.panel-header > div { min-width: 0; }
.panel-header > .el-button { flex: 0 0 auto; margin-left: auto; }
.security-overview-section { margin-top: 28px; display: grid; gap: 16px; }
.closure-stage-grid { display:grid; grid-template-columns:repeat(6,minmax(0,1fr)); gap:12px; }
.work-order-coverage-grid { display:grid; grid-template-columns:repeat(4,minmax(0,1fr)); gap:12px; }
.undispatched-gap { border-left:4px solid var(--el-color-warning); width:100%; }
.closure-definition { color:var(--el-text-color-secondary); font-size:12px; }.closure-definition summary { cursor:pointer; }.closure-definition p { line-height:1.7; margin:8px 0; }
.trend-bar.dispatched, .legend-dot.dispatched { background:var(--el-color-warning); }
.trend-bars button { border:0; padding:0; cursor:pointer; }
.section-heading { display: flex; justify-content: space-between; align-items: end; gap: 18px; }
.section-heading h2 { margin: 4px 0 6px; font-size: 22px; }
.section-heading p { margin: 0; color: var(--el-text-color-secondary); }
.security-highlight-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 14px; }
.security-highlight-card { appearance: none; border: 1px solid var(--el-border-color-lighter); border-radius: 12px; background: var(--el-bg-color); padding: 18px; text-align: left; color: inherit; display: grid; gap: 7px; min-width: 0; }
.security-highlight-card.clickable { cursor: pointer; transition: border-color .2s, transform .2s, box-shadow .2s; }
.security-highlight-card.clickable:hover { border-color: var(--el-color-primary-light-5); transform: translateY(-2px); box-shadow: var(--el-box-shadow-light); }
.security-highlight-card span { color: var(--el-text-color-secondary); font-size: 13px; }
.security-highlight-card strong { font-size: 30px; line-height: 1; color: var(--el-text-color-primary); }
.security-highlight-card strong em { font-size: 17px; font-style: normal; margin-left: 2px; }
.security-highlight-card small { color: var(--el-text-color-secondary); line-height: 1.4; }
.security-highlight-card.danger strong { color: var(--el-color-danger); }
.security-content-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }
.security-panel { min-width: 0; }
.breakdown-list { display: grid; gap: 11px; }
.breakdown-row { border: 0; background: transparent; padding: 0; display: grid; grid-template-columns: 76px minmax(80px, 1fr) 34px; align-items: center; gap: 10px; cursor: pointer; text-align: left; }
.breakdown-row:hover strong { color: var(--el-color-primary); }
.breakdown-row strong { text-align: right; }
.status-tag-list { margin-top: 18px; display: flex; flex-wrap: wrap; gap: 8px; }
.status-tag-list button { border: 0; padding: 0; background: transparent; cursor: pointer; }
.trend-chart { height: 166px; display: grid; grid-template-columns: repeat(14, minmax(16px, 1fr)); gap: 5px; align-items: end; padding-top: 8px; border-bottom: 1px solid var(--el-border-color-lighter); }
.trend-column { height: 100%; min-width: 0; display: grid; grid-template-rows: 1fr 18px; align-items: end; }
.trend-bars { display: flex; align-items: end; justify-content: center; gap: 3px; height: 100%; }
.trend-bar { width: min(10px, 42%); border-radius: 3px 3px 0 0; }
.trend-bar.new, .legend-dot.new { background: var(--el-color-primary); }
.trend-bar.closed, .legend-dot.closed { background: var(--el-color-success); }
.trend-column small { overflow: hidden; font-size: 10px; white-space: nowrap; color: var(--el-text-color-secondary); transform: scale(.86); transform-origin: center; }
.trend-legend { display: flex; gap: 16px; margin-top: 12px; font-size: 12px; color: var(--el-text-color-secondary); }
.legend-dot { display: inline-block; width: 8px; height: 8px; margin-right: 5px; border-radius: 50%; }
.coverage-progress-row { display: grid; gap: 8px; margin: 5px 0 19px; }
.coverage-progress-row > div { display: flex; justify-content: space-between; color: var(--el-text-color-secondary); font-size: 13px; }
.coverage-progress-row strong { min-width: 52px; color: var(--el-text-color-primary); text-align: right; font-variant-numeric: tabular-nums; }
.coverage-progress-row :deep(.el-progress-bar) { padding-right: 0; margin-right: 0; }
.coverage-metrics { display: flex; flex-wrap: wrap; gap: 10px 18px; color: var(--el-text-color-secondary); font-size: 13px; }
.coverage-metrics strong { color: var(--el-text-color-primary); margin-left: 4px; }
.coverage-metrics button { border: 0; padding: 0; cursor: pointer; color: var(--el-color-primary); background: transparent; }
:deep(.el-table__row) { cursor: pointer; }
@media (max-width: 1100px) {
  .closure-stage-grid { grid-template-columns:repeat(3,minmax(0,1fr)); }
  .work-order-coverage-grid { grid-template-columns:repeat(2,minmax(0,1fr)); }
  .stat-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .content-grid, .security-content-grid { grid-template-columns: 1fr; }
  .security-highlight-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
@media (max-width: 900px) {
  .page-heading, .section-heading { align-items: flex-start; flex-direction: column; }
  .heading-actions { width: 100%; }
}
@media (max-width: 720px) {
  .stat-grid, .security-highlight-grid { grid-template-columns: 1fr; }
  .panel-header { flex-wrap: wrap; }
  .panel-header > .el-button { margin-left: 0; }
}
</style>

<style scoped>
.dashboard-priority-grid { display:grid;grid-template-columns:minmax(0,1.5fr) minmax(0,1fr);gap:20px;margin-top:20px; }.dashboard-todo { display:flex;justify-content:space-between;align-items:center;gap:16px;border-bottom:1px solid var(--line);padding:20px 0; }.dashboard-todo:last-child { border-bottom:0;padding-bottom:0; }.dashboard-todo strong { font-weight:500;font-size:14px; }.dashboard-todo p { margin-top:6px; }.dashboard-todo .el-button { flex:none; }.dashboard-asset-metrics { display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px;margin:32px 0; }.dashboard-asset-metrics>div { display:grid;gap:10px;border-right:1px solid var(--line); }.dashboard-asset-metrics>div:last-child { border:0; }.dashboard-asset-metrics strong { font-size:27px;font-weight:500; }.dashboard-asset-metrics span { font-size:12px;color:var(--muted); }
@media(max-width:980px) { .dashboard-priority-grid { grid-template-columns:1fr; } }
@media(max-width:760px) { .security-content-grid,.security-highlight-grid { grid-template-columns:1fr; }.section-heading { align-items:flex-start;flex-wrap:wrap; }.trend-chart { gap:2px; }.trend-column small { font-size:9px; } }
</style>

<style scoped>
@media(max-width:760px) { .stat-grid { grid-template-columns:repeat(2,minmax(0,1fr)); }.stat-card { gap:8px; }.stat-card span { font-size:12px; }.stat-card small { font-size:11px; } }
@media(max-width:540px) { .closure-stage-grid,.work-order-coverage-grid { grid-template-columns:repeat(2,minmax(0,1fr)); }.security-highlight-card { padding:12px; }.security-highlight-card strong { font-size:24px; } }
</style>
