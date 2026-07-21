<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'
import { useAuthStore } from '../stores/auth'

interface Organization {
  id: string
  code: string
  name: string
  type: string
  status: string
}
interface OrganizationProgress {
  organization_id: string
  organization_code: string
  organization_name: string
  status: string
  started_at?: string
  submitted_at?: string
  reviewed_at?: string
  submission_note?: string
  submitted_system_count: number
  submitted_asset_count: number
  submitted_by_name?: string
  reviewed_by_name?: string
  return_reason?: string
}
interface InventoryTask {
  id: string
  name: string
  description?: string
  start_at: string
  deadline_at: string
  status: string
  required_fields: string[]
  quality_rules: string[]
  require_review: boolean
  allow_late_submission: boolean
  target_count: number
  submitted_count: number
  approved_count: number
  created_by_name?: string
  created_at: string
  organizations: OrganizationProgress[]
}
interface ListResponse<T> { items: T[]; total: number }

const auth = useAuthStore()
const router = useRouter()
const loading = ref(false)
const submitting = ref(false)
const actionLoading = ref('')
const createDialogVisible = ref(false)
const submissionDialogVisible = ref(false)
const returnDialogVisible = ref(false)
const formRef = ref<FormInstance>()
const tasks = ref<InventoryTask[]>([])
const organizations = ref<Organization[]>([])
const selectedTask = ref<InventoryTask | null>(null)
const selectedProgress = ref<OrganizationProgress | null>(null)
const submissionForm = reactive({ note: '' })
const returnForm = reactive({ reason: '' })

function initialStart() {
  const value = new Date()
  value.setSeconds(0, 0)
  return value
}
function initialDeadline(start: Date) {
  return new Date(start.getTime() + 7 * 24 * 60 * 60 * 1000)
}
const defaultStart = initialStart()
const form = reactive({
  name: '',
  description: '',
  start_at: defaultStart,
  deadline_at: initialDeadline(defaultStart),
  status: 'active',
  organization_ids: [] as string[],
  required_fields: ['business_systems', 'network_assets'] as string[],
  require_review: true,
  allow_late_submission: false,
})
const rules: FormRules = {
  name: [{ required: true, message: '请输入任务名称', trigger: 'blur' }],
  start_at: [{ required: true, message: '请选择开始时间', trigger: 'change' }],
  deadline_at: [{ required: true, message: '请选择截止时间', trigger: 'change' }],
  organization_ids: [{ type: 'array', required: true, min: 1, message: '请选择至少一个填报组织', trigger: 'change' }],
}

const eligibleOrganizations = computed(() => organizations.value.filter((item) => item.status === 'enabled' && item.type !== 'group'))
const activeCount = computed(() => tasks.value.filter((item) => item.status === 'active').length)
const draftCount = computed(() => tasks.value.filter((item) => item.status === 'draft').length)
const overdueCount = computed(() => tasks.value.filter((item) => item.status === 'active' && new Date(item.deadline_at).getTime() < Date.now()).length)
const taskStatusLabels: Record<string, string> = { draft: '草稿', active: '进行中', closed: '已关闭', completed: '已完成', archived: '已归档' }
const progressStatusLabels: Record<string, string> = {
  not_started: '未开始', in_progress: '填报中', submitted: '待审核', returned: '已退回', approved: '已通过', overdue: '已逾期',
}
const requiredFieldLabels: Record<string, string> = {
  business_systems: '业务系统信息', data_security: '数据安全属性', network_assets: '网络资产信息', owner_contacts: '负责人及联系方式',
}

async function load() {
  loading.value = true
  try {
    if (auth.isGroupAdmin) {
      const [taskResponse, organizationResponse] = await Promise.all([
        apiClient.get<ListResponse<InventoryTask>>('/inventory-tasks'),
        apiClient.get<ListResponse<Organization>>('/organizations'),
      ])
      tasks.value = taskResponse.data.items
      organizations.value = organizationResponse.data.items
    } else {
      const taskResponse = await apiClient.get<ListResponse<InventoryTask>>('/inventory-tasks')
      tasks.value = taskResponse.data.items
      organizations.value = []
    }
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取填报任务失败'))
  } finally {
    loading.value = false
  }
}

function openCreateDialog() {
  const start = initialStart()
  Object.assign(form, {
    name: '', description: '', start_at: start, deadline_at: initialDeadline(start), status: 'active',
    organization_ids: [], required_fields: ['business_systems', 'network_assets'], require_review: true, allow_late_submission: false,
  })
  createDialogVisible.value = true
}

async function createTask() {
  if (!formRef.value || !(await formRef.value.validate())) return
  if (!(form.deadline_at > form.start_at)) {
    ElMessage.warning('截止时间必须晚于开始时间')
    return
  }
  submitting.value = true
  try {
    await apiClient.post('/inventory-tasks', { ...form, quality_rules: [] })
    createDialogVisible.value = false
    ElMessage.success(form.status === 'active' ? '填报任务已创建并下发' : '填报任务草稿已保存')
    await load()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '创建填报任务失败'))
  } finally {
    submitting.value = false
  }
}

function operationKey(task: InventoryTask, progress?: OrganizationProgress) {
  return `${task.id}:${progress?.organization_id ?? 'task'}`
}

function isTaskOpen(task: InventoryTask) {
  const now = Date.now()
  return task.status === 'active' && now >= new Date(task.start_at).getTime() &&
    (task.allow_late_submission || now <= new Date(task.deadline_at).getTime())
}

function effectiveProgressStatus(task: InventoryTask, progress: OrganizationProgress) {
  if (task.status === 'active' && new Date(task.deadline_at).getTime() < Date.now() &&
      !['submitted', 'approved'].includes(progress.status)) return 'overdue'
  return progress.status
}

function canStart(task: InventoryTask, progress: OrganizationProgress) {
  return isTaskOpen(task) && ['not_started', 'returned', 'overdue'].includes(progress.status)
}

function canSubmit(task: InventoryTask, progress: OrganizationProgress) {
  return isTaskOpen(task) && ['in_progress', 'returned', 'overdue'].includes(progress.status)
}

async function startProgress(task: InventoryTask, progress: OrganizationProgress) {
  const key = operationKey(task, progress)
  actionLoading.value = key
  try {
    await apiClient.post(`/inventory-tasks/${task.id}/organizations/${progress.organization_id}/start`)
    ElMessage.success('已开始填报，请完善业务系统和网络资产后提交')
    await load()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '开始填报失败'))
  } finally {
    actionLoading.value = ''
  }
}

function openSubmissionDialog(task: InventoryTask, progress: OrganizationProgress) {
  selectedTask.value = task
  selectedProgress.value = progress
  submissionForm.note = progress.submission_note || ''
  submissionDialogVisible.value = true
}

async function submitProgress() {
  if (!selectedTask.value || !selectedProgress.value) return
  const key = operationKey(selectedTask.value, selectedProgress.value)
  actionLoading.value = key
  try {
    await apiClient.post(`/inventory-tasks/${selectedTask.value.id}/organizations/${selectedProgress.value.organization_id}/submit`, {
      note: submissionForm.note,
    })
    submissionDialogVisible.value = false
    ElMessage.success(selectedTask.value.require_review ? '填报结果已提交，等待集团审核' : '填报结果已提交并自动完成')
    await load()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '提交填报任务失败'))
  } finally {
    actionLoading.value = ''
  }
}

async function approveProgress(task: InventoryTask, progress: OrganizationProgress) {
  try {
    await ElMessageBox.confirm(`确认通过“${progress.organization_name}”提交的填报结果吗？`, '审核通过', { type: 'success', confirmButtonText: '通过', cancelButtonText: '取消' })
  } catch {
    return
  }
  const key = operationKey(task, progress)
  actionLoading.value = key
  try {
    await apiClient.post(`/inventory-tasks/${task.id}/organizations/${progress.organization_id}/review`, { decision: 'approved', return_reason: '' })
    ElMessage.success('填报结果已审核通过')
    await load()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '审核填报任务失败'))
  } finally {
    actionLoading.value = ''
  }
}

function openReturnDialog(task: InventoryTask, progress: OrganizationProgress) {
  selectedTask.value = task
  selectedProgress.value = progress
  returnForm.reason = ''
  returnDialogVisible.value = true
}

async function returnProgress() {
  if (!selectedTask.value || !selectedProgress.value) return
  if (!returnForm.reason.trim()) {
    ElMessage.warning('请填写退回原因')
    return
  }
  const key = operationKey(selectedTask.value, selectedProgress.value)
  actionLoading.value = key
  try {
    await apiClient.post(`/inventory-tasks/${selectedTask.value.id}/organizations/${selectedProgress.value.organization_id}/review`, {
      decision: 'returned', return_reason: returnForm.reason,
    })
    returnDialogVisible.value = false
    ElMessage.success('填报结果已退回修改')
    await load()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '退回填报任务失败'))
  } finally {
    actionLoading.value = ''
  }
}

async function updateTaskStatus(task: InventoryTask, status: string) {
  const labels: Record<string, string> = { active: '下发', closed: '关闭', archived: '归档' }
  try {
    await ElMessageBox.confirm(`确认${labels[status]}任务“${task.name}”吗？`, `${labels[status]}任务`, {
      type: status === 'closed' ? 'warning' : 'info', confirmButtonText: labels[status], cancelButtonText: '取消',
    })
  } catch {
    return
  }
  const key = operationKey(task)
  actionLoading.value = key
  try {
    await apiClient.patch(`/inventory-tasks/${task.id}/status`, { status })
    ElMessage.success(`任务已${labels[status]}`)
    await load()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, `${labels[status]}任务失败`))
  } finally {
    actionLoading.value = ''
  }
}

function formatDate(value?: string) {
  if (!value) return '—'
  return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}
function taskStatusTag(value: string) {
  return value === 'active' ? 'success' : value === 'draft' ? 'info' : value === 'completed' ? 'primary' : 'warning'
}
function progressStatusTag(value: string) {
  return value === 'approved' ? 'success' : value === 'submitted' ? 'primary' : value === 'returned' || value === 'overdue' ? 'danger' : value === 'in_progress' ? 'warning' : 'info'
}

onMounted(load)
</script>

<template>
  <section>
    <div class="page-heading">
      <div>
        <span class="eyebrow">INVENTORY</span>
        <h1>填报任务</h1>
        <p>{{ auth.isGroupAdmin ? '集团选择下级组织并下发填报任务，统一跟踪提交和审核进度。' : '维护本组织的业务系统和网络资产，并在任务中提交集团审核。' }}</p>
      </div>
      <el-button v-if="auth.isGroupAdmin" type="primary" :disabled="eligibleOrganizations.length === 0" @click="openCreateDialog">创建任务</el-button>
    </div>

    <el-alert v-if="auth.isGroupAdmin && eligibleOrganizations.length === 0" title="请先在“组织机构”中新增并启用至少一个子公司或部门，之后才能创建填报任务。" type="warning" :closable="false" show-icon class="page-alert" />
    <el-alert v-if="!auth.isGroupAdmin" title="填报任务以系统中当前的业务系统和网络资产为准。请先维护数据，再回到本页面提交。" type="info" :closable="false" show-icon class="page-alert">
      <template #default>
        <div class="inventory-guide-actions">
          <span>填报任务以系统中当前的业务系统和网络资产为准。请先维护数据，再回到本页面提交。</span>
          <el-button size="small" @click="router.push('/systems')">维护业务系统</el-button>
          <el-button size="small" @click="router.push('/assets')">维护网络资产</el-button>
        </div>
      </template>
    </el-alert>

    <div class="organization-summary">
      <div><span>任务总数</span><strong>{{ tasks.length }}</strong></div>
      <div><span>进行中</span><strong>{{ activeCount }}</strong></div>
      <div><span>草稿</span><strong>{{ draftCount }}</strong></div>
      <div><span>已逾期</span><strong>{{ overdueCount }}</strong></div>
    </div>

    <div class="panel data-table-panel" v-loading="loading">
      <el-table :data="tasks" row-key="id" empty-text="暂无填报任务">
        <el-table-column type="expand">
          <template #default="scope">
            <div class="task-detail">
              <div class="task-detail-description"><strong>任务说明</strong><span>{{ scope.row.description || '未填写' }}</span></div>
              <div><strong>必填范围</strong><span>{{ scope.row.required_fields.map((item: string) => requiredFieldLabels[item] || item).join('、') || '未指定' }}</span></div>
              <div><strong>审核要求</strong><span>{{ scope.row.require_review ? '需要集团审核' : '提交后直接完成' }}</span></div>
              <div><strong>逾期提交</strong><span>{{ scope.row.allow_late_submission ? '允许' : '不允许' }}</span></div>
              <el-table :data="scope.row.organizations" size="small" class="task-organization-table" empty-text="暂无组织">
                <el-table-column label="填报组织" min-width="190">
                  <template #default="itemScope"><strong>{{ itemScope.row.organization_name }}</strong><span class="table-secondary">{{ itemScope.row.organization_code }}</span></template>
                </el-table-column>
                <el-table-column label="状态" width="100">
                  <template #default="itemScope"><el-tag :type="progressStatusTag(effectiveProgressStatus(scope.row, itemScope.row))" effect="plain">{{ progressStatusLabels[effectiveProgressStatus(scope.row, itemScope.row)] || itemScope.row.status }}</el-tag></template>
                </el-table-column>
                <el-table-column label="提交快照" min-width="150">
                  <template #default="itemScope">
                    <template v-if="itemScope.row.submitted_at"><strong>{{ itemScope.row.submitted_system_count }}</strong> 个系统 / <strong>{{ itemScope.row.submitted_asset_count }}</strong> 项资产<span class="table-secondary">{{ itemScope.row.submitted_by_name }} · {{ formatDate(itemScope.row.submitted_at) }}</span></template>
                    <span v-else>—</span>
                  </template>
                </el-table-column>
                <el-table-column label="填报说明/退回原因" min-width="190">
                  <template #default="itemScope">
                    <span v-if="itemScope.row.return_reason" class="return-reason">退回：{{ itemScope.row.return_reason }}</span>
                    <span v-else>{{ itemScope.row.submission_note || '—' }}</span>
                  </template>
                </el-table-column>
                <el-table-column label="操作" width="190" fixed="right">
                  <template #default="itemScope">
                    <template v-if="auth.isGroupAdmin">
                      <el-button v-if="scope.row.status === 'active' && itemScope.row.status === 'submitted'" type="success" link :loading="actionLoading === operationKey(scope.row, itemScope.row)" @click="approveProgress(scope.row, itemScope.row)">通过</el-button>
                      <el-button v-if="scope.row.status === 'active' && itemScope.row.status === 'submitted'" type="danger" link :disabled="actionLoading === operationKey(scope.row, itemScope.row)" @click="openReturnDialog(scope.row, itemScope.row)">退回</el-button>
                      <span v-else-if="itemScope.row.reviewed_at" class="table-secondary">审核：{{ itemScope.row.reviewed_by_name || '系统自动' }}<br>{{ formatDate(itemScope.row.reviewed_at) }}</span>
                    </template>
                    <template v-else>
                      <el-button v-if="canStart(scope.row, itemScope.row)" type="primary" link :loading="actionLoading === operationKey(scope.row, itemScope.row)" @click="startProgress(scope.row, itemScope.row)">开始填报</el-button>
                      <el-button v-if="canSubmit(scope.row, itemScope.row)" type="success" link :disabled="actionLoading === operationKey(scope.row, itemScope.row)" @click="openSubmissionDialog(scope.row, itemScope.row)">提交</el-button>
                      <span v-if="scope.row.status === 'active' && new Date(scope.row.start_at).getTime() > Date.now()" class="table-secondary">尚未到开始时间</span>
                      <span v-else-if="scope.row.status === 'active' && !scope.row.allow_late_submission && new Date(scope.row.deadline_at).getTime() < Date.now() && !['submitted', 'approved'].includes(itemScope.row.status)" class="table-secondary">已截止，不允许逾期提交</span>
                    </template>
                  </template>
                </el-table-column>
              </el-table>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="任务名称" min-width="220"><template #default="scope"><div class="organization-name-cell"><strong>{{ scope.row.name }}</strong><span>创建人：{{ scope.row.created_by_name || '—' }}</span></div></template></el-table-column>
        <el-table-column label="状态" width="105"><template #default="scope"><el-tag :type="taskStatusTag(scope.row.status)" effect="plain">{{ taskStatusLabels[scope.row.status] || scope.row.status }}</el-tag></template></el-table-column>
        <el-table-column label="开始时间" min-width="165"><template #default="scope">{{ formatDate(scope.row.start_at) }}</template></el-table-column>
        <el-table-column label="截止时间" min-width="165"><template #default="scope">{{ formatDate(scope.row.deadline_at) }}</template></el-table-column>
        <el-table-column label="填报进度" width="155"><template #default="scope"><strong>{{ scope.row.submitted_count }}</strong> / {{ scope.row.target_count }} 已提交，{{ scope.row.approved_count }} 已通过</template></el-table-column>
        <el-table-column v-if="auth.isGroupAdmin" label="任务操作" width="105" fixed="right">
          <template #default="scope">
            <el-button v-if="scope.row.status === 'draft'" type="primary" link :loading="actionLoading === operationKey(scope.row)" @click="updateTaskStatus(scope.row, 'active')">下发</el-button>
            <el-button v-else-if="scope.row.status === 'active'" type="warning" link :loading="actionLoading === operationKey(scope.row)" @click="updateTaskStatus(scope.row, 'closed')">关闭</el-button>
            <el-button v-else-if="['closed', 'completed'].includes(scope.row.status)" type="info" link :loading="actionLoading === operationKey(scope.row)" @click="updateTaskStatus(scope.row, 'archived')">归档</el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <el-dialog v-model="createDialogVisible" title="创建填报任务" width="760px" destroy-on-close>
      <el-alert title="任务创建后将关联到所选组织；选择“立即下发”后，子公司登录即可看到该任务。" type="info" :closable="false" show-icon />
      <el-form ref="formRef" :model="form" :rules="rules" label-position="top" class="dialog-form-with-alert">
        <div class="form-grid two-columns">
          <el-form-item label="任务名称" prop="name" class="full-span"><el-input v-model="form.name" maxlength="255" placeholder="如：2026 年第三季度网络资产填报" /></el-form-item>
          <el-form-item label="开始时间" prop="start_at"><el-date-picker v-model="form.start_at" type="datetime" placeholder="选择开始时间" style="width:100%" /></el-form-item>
          <el-form-item label="截止时间" prop="deadline_at"><el-date-picker v-model="form.deadline_at" type="datetime" placeholder="选择截止时间" style="width:100%" /></el-form-item>
          <el-form-item label="任务状态"><el-select v-model="form.status" style="width:100%"><el-option label="立即下发" value="active" /><el-option label="保存草稿" value="draft" /></el-select></el-form-item>
          <el-form-item label="填报组织" prop="organization_ids"><el-select v-model="form.organization_ids" multiple filterable collapse-tags collapse-tags-tooltip placeholder="请选择子公司或部门" style="width:100%"><el-option v-for="item in eligibleOrganizations" :key="item.id" :label="`${item.name}（${item.code}）`" :value="item.id" /></el-select></el-form-item>
          <el-form-item label="填报内容" class="full-span"><el-checkbox-group v-model="form.required_fields"><el-checkbox label="business_systems">业务系统信息</el-checkbox><el-checkbox label="data_security">数据安全属性</el-checkbox><el-checkbox label="network_assets">网络资产信息</el-checkbox><el-checkbox label="owner_contacts">负责人及联系方式</el-checkbox></el-checkbox-group></el-form-item>
          <el-form-item label="任务说明" class="full-span"><el-input v-model="form.description" type="textarea" :rows="3" maxlength="5000" show-word-limit placeholder="填写本次填报范围、口径和注意事项" /></el-form-item>
          <el-form-item label="需要集团审核"><el-switch v-model="form.require_review" /></el-form-item>
          <el-form-item label="允许逾期提交"><el-switch v-model="form.allow_late_submission" /></el-form-item>
        </div>
      </el-form>
      <template #footer><el-button @click="createDialogVisible = false">取消</el-button><el-button type="primary" :loading="submitting" @click="createTask">{{ form.status === 'active' ? '创建并下发' : '保存草稿' }}</el-button></template>
    </el-dialog>

    <el-dialog v-model="submissionDialogVisible" title="提交填报结果" width="580px" destroy-on-close>
      <el-alert title="系统将记录提交时本组织的业务系统和网络资产数量快照。提交后如需修改，请等待集团退回。" type="info" :closable="false" show-icon />
      <el-form label-position="top" class="dialog-form-with-alert">
        <el-form-item label="任务"><el-input :model-value="selectedTask?.name" disabled /></el-form-item>
        <el-form-item label="填报组织"><el-input :model-value="selectedProgress?.organization_name" disabled /></el-form-item>
        <el-form-item label="填报说明"><el-input v-model="submissionForm.note" type="textarea" :rows="4" maxlength="5000" show-word-limit placeholder="可填写数据范围、统计口径或需要集团关注的事项" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="submissionDialogVisible = false">取消</el-button><el-button type="primary" :loading="actionLoading !== ''" @click="submitProgress">确认提交</el-button></template>
    </el-dialog>

    <el-dialog v-model="returnDialogVisible" title="退回填报结果" width="540px" destroy-on-close>
      <el-form label-position="top">
        <el-form-item label="填报组织"><el-input :model-value="selectedProgress?.organization_name" disabled /></el-form-item>
        <el-form-item label="退回原因" required><el-input v-model="returnForm.reason" type="textarea" :rows="4" maxlength="2000" show-word-limit placeholder="请明确说明需要补充或修正的内容" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="returnDialogVisible = false">取消</el-button><el-button type="danger" :loading="actionLoading !== ''" @click="returnProgress">确认退回</el-button></template>
    </el-dialog>
  </section>
</template>
