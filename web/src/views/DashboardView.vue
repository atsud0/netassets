<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'
import type { RepairStatistics } from '../api/remediation'
import { useAuthStore } from '../stores/auth'
import { FINDING_STATUS_PENDING, findingStatusTagType } from '../constants/findingStatus'

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
interface SecurityTrendPoint { date: string; new_finding_count: number; closed_finding_count: number }
interface OrganizationPerformance {
  organization_id: string; organization_code: string; organization_name: string
  active_work_order_count: number; overdue_work_order_count: number
  closed_work_order_count: number; closed_on_time_count: number; timely_rate: number
}
interface TestCoverage {
  public_asset_count: number; tested_public_asset_count: number; public_asset_coverage_rate: number
  active_batch_count: number; completed_batch_count: number
  test_finding_count: number; closed_test_finding_count: number; test_finding_closure_rate: number
}
interface SecurityOverview {
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
 {label:canUseRepair.value?'待我审核的修复任务':'待复测工单',value:canUseRepair.value?(repairStats.value?pendingRepairReview.value:'—'):summary.value.pending_verification_work_order_count,hint:'核对当前提交版本与证明材料',action:()=>canUseRepair.value?toRepair('review_mine'):toWorkOrders('pending_verification')},
 {label:'逾期整改工单',value:summary.value.overdue_work_order_count,hint:'需优先督办整改',danger:true,action:()=>toWorkOrders(undefined,true)},
 {label:'处理中工单',value:summary.value.active_work_order_count,hint:'当前权限范围内的未关闭工单',action:()=>toWorkOrders()},
 auth.hasPermission('inventory.manage')?{label:auth.isGroupAdmin?'待审核填报':'待填报任务',value:auth.isGroupAdmin?summary.value.pending_review_count:summary.value.pending_task_count,hint:`${summary.value.overdue_count} 项填报已逾期`,action:()=>router.push('/inventory-tasks')}:{label:'三日内到期工单',value:summary.value.due_soon_work_order_count,hint:'尽早协调验证与发布安排',action:()=>toWorkOrders()},
])
const trendMax = computed(() => Math.max(1, ...overview.value.finding_trend.flatMap(item => [item.new_finding_count, item.closed_finding_count])))
const latestTrend = computed(() => overview.value.finding_trend.slice(-14))
const severityTotal = computed(() => overview.value.finding_severity.reduce((total, item) => total + item.count, 0))

async function load() {
  loading.value = true
  try {
    await Promise.all([
      apiClient.get<{ summary: DashboardSummary }>('/dashboard/summary').then(response => {
        summary.value = response.data.summary
      }),
      apiClient.get<{ overview: SecurityOverview }>('/dashboard/security-overview').then(response => {
        overview.value = response.data.overview
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
function toFindings(status?: string, assetLinkState?: string) {
  void router.push({ path: '/security-work-orders', query: { tab: 'findings', ...(status ? { status } : {}), ...(assetLinkState ? { asset_link_state: assetLinkState } : {}) } })
}
function toWorkOrders(status?: string, overdueOnly = false) {
  void router.push({ path: '/security-work-orders', query: { tab: 'orders', ...(status ? { status } : {}), ...(overdueOnly ? { overdue: '1' } : {}) } })
}
function toOrganizationOrders(organizationID: string) {
  void router.push({ path: '/security-work-orders', query: { tab: 'orders', organization_id: organizationID } })
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
        <div><span class="eyebrow">SECURITY GOVERNANCE</span><h2>安全治理概览</h2><p>统计范围会根据当前账号的数据权限自动收敛；子公司仅看到已经下发的相关漏洞与工单。</p></div>
        <el-button text type="primary" @click="toFindings()">查看安全发现</el-button>
      </div>

      <div class="security-highlight-grid">
        <button class="security-highlight-card clickable" type="button" @click="toFindings(FINDING_STATUS_PENDING)">
          <span>待处理漏洞</span><strong>{{ overview.finding_status.find(item => item.key === FINDING_STATUS_PENDING)?.count ?? 0 }}</strong><small>点击查看待处理发现</small>
        </button>
        <button class="security-highlight-card clickable" type="button" @click="toWorkOrders('pending_verification')">
          <span>待集团复测</span><strong>{{ summary.pending_verification_work_order_count }}</strong><small>点击处理待复测工单</small>
        </button>
        <button class="security-highlight-card clickable" type="button" @click="toFindings(FINDING_STATUS_PENDING)">
          <span>待关联资产</span><strong>{{ overview.pending_asset_finding_count }}</strong><small>资产仍在工作版本待发布</small>
        </button>
        <button v-if="overview.retired_asset_finding_count > 0" class="security-highlight-card clickable" type="button" @click="toFindings(undefined, 'retired')">
          <span>资产已下线</span><strong>{{ overview.retired_asset_finding_count }}</strong><small>漏洞保留追溯，无需重建资产</small>
        </button>
        <button class="security-highlight-card danger clickable" type="button" @click="toWorkOrders(undefined, true)">
          <span>逾期整改工单</span><strong>{{ summary.overdue_work_order_count }}</strong><small>点击查看已逾期工单</small>
        </button>
        <article class="security-highlight-card">
          <span>测试资产覆盖率</span><strong>{{ overview.test_coverage.public_asset_coverage_rate }}<em>%</em></strong><small>{{ overview.test_coverage.tested_public_asset_count }} / {{ overview.test_coverage.public_asset_count }} 项公网资产已完成测试</small>
        </article>
      </div>

      <div class="security-content-grid">
        <article class="panel security-panel">
          <div class="panel-header"><div><h2>漏洞风险分布</h2><p>当前可见安全发现共 {{ severityTotal }} 项。</p></div><el-button text type="primary" @click="toFindings()">查看明细</el-button></div>
          <div class="breakdown-list">
            <button v-for="item in overview.finding_severity" :key="item.key" type="button" class="breakdown-row" @click="toFindings()">
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
          <div class="panel-header"><div><h2>近 14 天风险趋势</h2><p>新增发现与通过复测关闭的漏洞数量。</p></div></div>
          <div class="trend-chart" aria-label="近14天漏洞趋势">
            <div v-for="item in latestTrend" :key="item.date" class="trend-column" :title="`${item.date}：新增 ${item.new_finding_count}，关闭 ${item.closed_finding_count}`">
              <div class="trend-bars">
                <span class="trend-bar new" :style="{ height: item.new_finding_count ? `${Math.max(3, item.new_finding_count * 100 / trendMax)}%` : '0' }" />
                <span class="trend-bar closed" :style="{ height: item.closed_finding_count ? `${Math.max(3, item.closed_finding_count * 100 / trendMax)}%` : '0' }" />
              </div>
              <small>{{ item.date.slice(5) }}</small>
            </div>
          </div>
          <div class="trend-legend"><span><i class="legend-dot new" />新增发现</span><span><i class="legend-dot closed" />复测关闭</span></div>
        </article>
      </div>

      <div class="security-content-grid">
        <article class="panel security-panel test-coverage-panel">
          <div class="panel-header"><div><h2>安全测试覆盖</h2><p>以已完成批次统计公网资产覆盖与测试发现闭环。</p></div><el-button v-if="auth.canManageSecurityTests" text type="primary" @click="router.push('/security-test-batches')">查看测试批次</el-button></div>
          <div class="coverage-progress-row"><div><span>公网资产已测试</span><strong>{{ overview.test_coverage.public_asset_coverage_rate }}%</strong></div><el-progress :percentage="overview.test_coverage.public_asset_coverage_rate" :stroke-width="12" :show-text="false" /></div>
          <div class="coverage-progress-row"><div><span>测试发现已关闭</span><strong>{{ overview.test_coverage.test_finding_closure_rate }}%</strong></div><el-progress :status="overview.test_coverage.test_finding_count > 0 && overview.test_coverage.test_finding_closure_rate === 100 ? 'success' : undefined" :percentage="overview.test_coverage.test_finding_closure_rate" :stroke-width="12" :show-text="false" /></div>
          <div class="coverage-metrics"><span>执行中批次 <strong>{{ overview.test_coverage.active_batch_count }}</strong></span><span>已完成批次 <strong>{{ overview.test_coverage.completed_batch_count }}</strong></span><button type="button" @click="toFindings()">测试发现 <strong>{{ overview.test_coverage.test_finding_count }}</strong></button></div>
        </article>

        <article class="panel security-panel">
          <div class="panel-header"><div><h2>{{ auth.hasGroupSecurityRole ? '子公司整改效能' : '本组织整改效能' }}</h2><p>按工单关闭时间计算按期整改率，优先关注逾期项。</p></div><el-button text type="primary" @click="toWorkOrders()">查看工单</el-button></div>
          <el-table :data="overview.organization_performance" size="small" max-height="220" empty-text="暂无工单数据" @row-click="onOrganizationRowClick">
            <el-table-column prop="organization_name" label="组织" min-width="145" />
            <el-table-column label="处理中" width="82" align="center"><template #default="{ row }">{{ row.active_work_order_count }}</template></el-table-column>
            <el-table-column label="逾期" width="72" align="center"><template #default="{ row }"><span :class="{ 'danger-number': row.overdue_work_order_count }">{{ row.overdue_work_order_count }}</span></template></el-table-column>
            <el-table-column label="按期整改率" width="110" align="right"><template #default="{ row }">{{ row.closed_work_order_count ? `${row.timely_rate}%` : '—' }}</template></el-table-column>
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
</style>
