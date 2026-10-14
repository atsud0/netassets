<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiClient, getAPIErrorMessage, getDownloadAPIErrorMessage } from '../api/client'
import { dateText, eventLabels, executionBlockReasons, loadOptions, newKey, outcomes, requestLabels, severityLabels, taskStatuses } from '../api/remediation'
import type { Attachment, Comment, Grant, Material, MaterialVersion, Page, RepairRequest, RepairTask, Submission, TaskEvent } from '../api/remediation'

const props = defineProps<{ id: string; executor: boolean }>()
const emit = defineEmits<{ changed: []; reviewed: [] }>()
const task = ref<RepairTask>()
const busy = ref(false)
const loading = ref(false)
const tab = ref('materials')
const reviewConfirmed = ref(false)
const latestSubmissions = ref<Submission[]>([])
const currentSubmission = computed(() => latestSubmissions.value.find(item => item.id === task.value?.current_submission_id))
const reviewable = computed(() => task.value && ((task.value.can_review && task.value.status==='pending_org_review') || (task.value.can_verify && task.value.status==='pending_security_review')))
const stageIndex = computed(() => ['pending_acceptance','in_progress','pending_org_review','pending_security_review','completed'].indexOf(task.value?.status ?? ''))
const fileOptions = ref<Attachment[]>([])
async function sendReview(decision:'pass'|'return') {
 if(!review.reason.trim()) { ElMessage.warning('请填写审核意见，退回时说明需要补充的内容');return }
 if(decision==='pass'&&!reviewConfirmed.value) { ElMessage.warning('请先确认已核对提交版本、目标环境与证明材料');return }
 if(decision==='pass'&&task.value?.status==='pending_security_review'&&(!review.verified_environment.trim()||!review.verified_at)) { ElMessage.warning('请填写实际验证环境与时间');return }
 await action(task.value?.status==='pending_org_review'?'organization-review':'security-review',{...review,decision,submission_id:task.value?.current_submission_id})
}

const submissions = ref<Submission[]>([])
const comments = ref<Comment[]>([])
const attachments = ref<Attachment[]>([])
const requests = ref<RepairRequest[]>([])
const events = ref<TaskEvent[]>([])
const childPage = ref(1)
const childTotal = ref(0)
const path = computed(() => `${props.executor ? '/repair/tasks' : '/remediation-tasks'}/${props.id}`)
const writable = computed(() => task.value && ['pending_acceptance', 'in_progress'].includes(task.value.status))
const editable = computed(() => task.value?.can_execute && task.value.status === 'in_progress')
// The backend answers "can you act" and "why not" with one predicate. Rendering the
// reason is what turns a refusal into something the operator can act on.
const executionBlock = computed<{ text: string; action: string } | undefined>(() => {
  const reason = task.value?.execution_blocked_reason
  if (!reason) return undefined
  return executionBlockReasons[reason] ?? { text: `任务当前不可执行（${reason}）`, action: '请联系集团安全管理员确认任务状态。' }
})
const form = reactive({ remediation_summary: '', deployment_status: 'target', change_reference: '', deployed_at: '', self_test_result: '', no_deployment_reason: '', attachment_ids: [] as string[] })
const progress = ref('')
const blocked = ref('')
const plan = reactive({ expected_completion_at: '', blocked_dependency: '', blocked_until: '' })
const materialHistory = ref<MaterialVersion[]>([])
const compareOlder = ref('')
const compareNewer = ref('')
const compareSubmissions = ref<Submission[]>([])
const diffRows = computed(() => {
 const before = compareSubmissions.value.find(s => s.id === compareOlder.value)
 const after = compareSubmissions.value.find(s => s.id === compareNewer.value)
 if (!before || !after) return []
 const fields: [string, string, string][] = [
 ['修复说明', before.remediation_summary, after.remediation_summary], ['变更编号', before.change_reference, after.change_reference],
 ['部署状态', before.deployment_status, after.deployment_status], ['目标环境', before.target_environment, after.target_environment],
 ['部署时间', dateText(before.deployed_at), dateText(after.deployed_at)], ['自测结果', before.self_test_result, after.self_test_result],
 ['不涉及部署的原因', before.no_deployment_reason ?? '', after.no_deployment_reason ?? ''],
 ['验收要求', before.material?.requirement ?? '', after.material?.requirement ?? ''],
 ['漏洞描述', before.material?.description ?? '', after.material?.description ?? ''],
 ['证明附件', before.attachments.map(a => a.original_name + ' (' + a.id + ')').join('\n'), after.attachments.map(a => a.original_name + ' (' + a.id + ')').join('\n')],
 ]
 return fields.map(([field, oldValue, newValue]) => ({ field, oldValue, newValue, changed: oldValue !== newValue }))
})
const sourceVisible = ref(false)
const source = reactive<Material>({ title: '', severity: '', affected_location: '', description: '', evidence: '', remediation_advice: '', requirement: '' })
const sourceReason = ref('')
function editSource() { if (task.value?.source_material) Object.assign(source, task.value.source_material); sourceReason.value = ''; sourceVisible.value = true }
const invalidateSubmission = ref(false)
const materialTarget = ref('')
const materialDeployment = ref(true)

const comment = ref('')
const visibility = ref('participants')
const review = reactive({ reason: '', internal_note: '', verified_environment: '', verified_at: '' })
const request = reactive({ type: 'extension', reason: '', mitigation: '', proposed_due_at: '' })
const requestVisible = ref(false)
const reassignVisible = ref(false)
const grants = ref<Grant[]>([])
const selectedGrant = ref('')
const reassignReason = ref('')
const activeRequest = ref<RepairRequest>()
const decisionVisible = ref(false)
const decision = reactive({ decision: 'approved', reason: '', risk_accepted_until: '' })
const materialVisible = ref(false)
const materialReason = ref('')
const material = reactive<Material>({ title: '', severity: '', affected_location: '', description: '', evidence: '', remediation_advice: '', requirement: '' })
async function editMaterial() {
 if (!task.value) return
 try { attachments.value = await loadOptions<Attachment>(`${path.value}/attachments`) } catch (error) { ElMessage.error(getAPIErrorMessage(error)); return }
 Object.assign(material, task.value, { attachment_ids: task.value.material_attachments.map(a => a.id) })
 if (task.value.material_needs_review && task.value.source_material) Object.assign(material, task.value.source_material)
 materialTarget.value = task.value.target_environment; materialDeployment.value = task.value.deployment_required
 materialReason.value = ''; invalidateSubmission.value = false; materialVisible.value = true
}
async function reopenRisk() {
  try { const { value } = await ElMessageBox.prompt('填写撤销风险接受并恢复整改的原因', '恢复整改', { inputValidator: value => Boolean(value?.trim()) || '请填写原因' }); await action('reopen-risk', { reason: value }) } catch { /* cancelled */ }
}
const submissionKey = ref(newKey())
const fileInput = ref<HTMLInputElement>()

async function load() {
  loading.value = true
  try {
    task.value = (await apiClient.get<{ item: RepairTask }>(path.value)).data.item
    progress.value = task.value.progress; blocked.value = task.value.blocked_reason
    plan.expected_completion_at = task.value.expected_completion_at ?? new Date(Math.max(new Date(task.value.due_at).getTime(), Date.now() + 3600000)).toISOString()
    plan.blocked_dependency = task.value.blocked_dependency; plan.blocked_until = task.value.blocked_until ?? ''
    review.verified_environment = task.value.target_environment; review.verified_at = new Date().toISOString()
    const [versions, files] = await Promise.all([
      apiClient.get<Page<Submission>>(`${path.value}/submissions`, {params:{page:1,page_size:20}}),
      loadOptions<Attachment>(`${path.value}/attachments`),
    ])
    latestSubmissions.value = versions.data.items; fileOptions.value=files
    await loadTab()
  } catch (error) { task.value = undefined; ElMessage.error(getAPIErrorMessage(error)) }
  finally { loading.value = false }
}
async function loadTab() {
  const kind = ['materials', 'repair'].includes(tab.value) ? 'attachments' : tab.value === 'material-history' ? 'materials' : tab.value
  const { data } = await apiClient.get<Page<unknown>>(`${path.value}/${kind}`, { params: { page: childPage.value, page_size: 20 } })
  childTotal.value = data.total
  if (kind === 'attachments') attachments.value = data.items as Attachment[]
  if (kind === 'submissions') {
    submissions.value = data.items as Submission[]
    compareSubmissions.value = await loadOptions<Submission>(`${path.value}/submissions`)
    if (!compareNewer.value) compareNewer.value = compareSubmissions.value[0]?.id ?? ''
    if (!compareOlder.value) compareOlder.value = compareSubmissions.value[1]?.id ?? ''
  }
  if (kind === 'materials') materialHistory.value = data.items as MaterialVersion[]
  if (kind === 'comments') comments.value = data.items as Comment[]
  if (kind === 'requests') requests.value = data.items as RepairRequest[]
  if (kind === 'events') events.value = data.items as TaskEvent[]
}
watch(() => props.id, () => { compareOlder.value = ''; compareNewer.value = ''; reviewConfirmed.value=false; review.reason='';review.internal_note=''; latestSubmissions.value=[]; Object.assign(form, {deployment_status:'target',remediation_summary: '', self_test_result: '', change_reference: '', deployed_at: '', attachment_ids: [], no_deployment_reason: ''}); tab.value = 'materials'; childPage.value = 1; submissionKey.value = newKey(); void load() }, { immediate: true })
watch(tab, () => { childPage.value = 1; void loadTab().catch(error => ElMessage.error(getAPIErrorMessage(error))) })
async function action(name: string, body: Record<string, unknown>) {
  if (!task.value || busy.value) return
  busy.value = true
  try {
    await apiClient.post(`${path.value}/actions/${name}`, { expected_version: task.value.version, ...body }, { headers: { 'Idempotency-Key': submissionKey.value } })
    submissionKey.value = newKey(); await load(); emit('changed'); ElMessage.success('已保存')
    reviewConfirmed.value=false
    if(['organization-review','security-review'].includes(name))emit('reviewed')
    comment.value = ''; review.reason = ''; review.internal_note = ''; reassignVisible.value = false; materialVisible.value = false; sourceVisible.value = false
  } catch (error) { ElMessage.error(getAPIErrorMessage(error)) }
  finally { busy.value = false }
}
async function submit() {
  if (!form.remediation_summary.trim() || !form.self_test_result.trim()) { ElMessage.warning('请填写修复说明和自测结果'); return }
  if(task.value?.deployment_required && !['target','not_applicable'].includes(form.deployment_status)) { ElMessage.warning('请部署至目标环境后再提交修复');return }
  await action('submit', { ...form, deployed_at: form.deployed_at ? new Date(form.deployed_at).toISOString() : null })
}
async function withdraw() {
  try { const { value } = await ElMessageBox.prompt('说明撤回原因', '撤回提交', { inputValidator: value => Boolean(value?.trim()) || '请填写原因' }); await action('withdraw', { reason: value, submission_id: task.value?.current_submission_id }) } catch { /* cancelled */ }
}
async function cancel() {
  try { const { value } = await ElMessageBox.prompt('取消后漏洞仍需重新分派，请填写原因', '取消执行任务', { inputValidator: value => Boolean(value?.trim()) || '请填写原因' }); await action('cancel', { reason: value }) } catch { /* cancelled */ }
}
async function openReassign() {
  if (!task.value) return
  try { grants.value = (await loadOptions<Grant>('/remediation-grants', { organization_id: task.value.organization_id, business_system_id: task.value.business_system_id })).filter(g => g.available); selectedGrant.value = ''; reassignReason.value = ''; reassignVisible.value = true } catch (error) { ElMessage.error(getAPIErrorMessage(error)) }
}
async function upload(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0]
  if (!file || !task.value || busy.value) return
  busy.value = true
  try {
    const body = new FormData(); body.append('file', file); body.append('visibility', visibility.value); body.append('expected_version', String(task.value.version))
    await apiClient.post(`${path.value}/attachments`, body, { headers: { 'Content-Type': 'multipart/form-data' } }); await loadTab(); fileOptions.value=await loadOptions<Attachment>(`${path.value}/attachments`); ElMessage.success('附件已上传')
  } catch (error) { ElMessage.error(getAPIErrorMessage(error)) }
  finally { busy.value = false; if (fileInput.value) fileInput.value.value = '' }
}
async function download(file: Attachment) {
  try { const { data } = await apiClient.get<Blob>(`${path.value}/attachments/${file.id}/download`, { responseType: 'blob' }); const url = URL.createObjectURL(data); const a = document.createElement('a'); a.href = url; a.download = file.original_name; a.click(); URL.revokeObjectURL(url) } catch (error) { ElMessage.error(await getDownloadAPIErrorMessage(error)) }
}
async function sendRequest() {
  if (!task.value || busy.value) return
  busy.value = true
  try { await apiClient.post(`${path.value}/requests`, { ...request, expected_version: task.value.version, proposed_due_at: request.type === 'extension' && request.proposed_due_at ? new Date(request.proposed_due_at).toISOString() : null }); requestVisible.value = false; emit('changed'); await load(); ElMessage.success('申请已提交') } catch (error) { ElMessage.error(getAPIErrorMessage(error)) } finally { busy.value = false }
}
async function openDecision(item: RepairRequest) {
  activeRequest.value = item; decision.reason = ''; decision.decision = 'approved'; decision.risk_accepted_until = ''; selectedGrant.value = ''
  if (item.type === 'reassign' && task.value) {
    try { grants.value = (await loadOptions<Grant>('/remediation-grants', { organization_id: task.value.organization_id, business_system_id: task.value.business_system_id })).filter(g => g.available) } catch (error) { ElMessage.error(getAPIErrorMessage(error)); return }
  }
  decisionVisible.value = true
}
async function decideRequest() {
  if (!task.value || !activeRequest.value || busy.value) return
  busy.value = true
  try { await apiClient.post(`/remediation-requests/${activeRequest.value.id}/decide`, { ...decision, expected_version: task.value.version, grant_id: selectedGrant.value, risk_accepted_until: decision.risk_accepted_until ? new Date(decision.risk_accepted_until).toISOString() : null }); decisionVisible.value = false; emit('changed'); await load(); ElMessage.success('已处理申请') } catch (error) { ElMessage.error(getAPIErrorMessage(error)) } finally { busy.value = false }
}
</script>

<template>
  <div v-loading="loading" class="repair-detail">
    <template v-if="task">
      <div class="detail-heading"><div><span class="muted">{{ task.task_no }} · {{ task.work_order_no }}</span><h2>{{ task.title }}</h2></div><el-tag>{{ outcomes[task.completion_reason ?? ''] || taskStatuses[task.status] }}</el-tag></div>
      <div class="repair-context"><span>{{ task.organization_name }} / {{ task.system_name }}</span><span>{{ task.assignee_name }} · {{ task.vendor_name || '内部人员' }}</span><span>{{ severityLabels[task.severity] }}</span><span :class="{'overdue-text':task.overdue}">截止 {{ dateText(task.due_at) }}{{ task.overdue?' · 已逾期':'' }}</span></div>
      <ol v-if="task.status!=='cancelled' && (task.status!=='completed' || task.completion_reason==='verified_fixed')" class="repair-stages" aria-label="任务进度"><li v-for="(label,index) in ['待接收','修复中','组织确认','集团复测',task.completion_reason==='verified_fixed'?'复测通过':'已处置']" :key="index" :class="{done:index<stageIndex,current:index===stageIndex}" :aria-current="index===stageIndex?'step':undefined"><b>{{ index<stageIndex?'✓':index+1 }}</b>{{ label }}</li></ol>
      <p v-else class="muted">{{ task.status==='cancelled'?'执行任务已取消，请在主工单中跟进后续安排。':'已按批准的处置结果结束执行，具体依据见申请和操作记录。' }}</p>
      <el-alert v-if="task.material_needs_review" title="共享材料待管理员复核，暂不能提交或通过审核。原有材料保留供查阅。" type="warning" :closable="false" />
      <el-alert v-if="task.return_reason" :title="`退回原因：${task.return_reason}`" type="warning" :closable="false" />
      <el-alert v-if="executionBlock" type="warning" :closable="false" class="execution-blocked-alert">
        <template #title>{{ executionBlock?.text }}</template>
        <p class="blocked-action">{{ executionBlock?.action }}</p>
        <p v-if="task.asset_retired_at" class="muted">资产于 {{ dateText(task.asset_retired_at) }} 删除；恢复资产后任务自动回到可执行状态。</p>
        <p v-if="task.system_retired_at" class="muted">业务系统于 {{ dateText(task.system_retired_at) }} 删除；恢复业务系统后需重新启用才能继续整改。</p>
      </el-alert>
      <div class="repair-workspace"><section class="repair-evidence">
      <el-tabs v-model="tab">
        <el-tab-pane label="漏洞与验收要求" name="materials">
          <div class="evidence-heading"><h3>当前共享材料</h3><el-tag size="small" effect="plain">第 {{ task.material_revision }} 版</el-tag></div>
          <div class="evidence-card"><p class="pre">{{ task.description || '暂无漏洞描述' }}</p><dl><dt>目标环境</dt><dd>{{ task.target_environment }}</dd><dt>部署要求</dt><dd>{{ task.deployment_required?'需部署到目标环境后提交':'按验收要求验证' }}</dd><dt>验收要求</dt><dd class="pre">{{ task.requirement || '未提供' }}</dd></dl></div>
          <el-collapse class="material-more"><el-collapse-item title="查看受影响位置、复现步骤与修复建议">          <h3>受影响位置</h3><p class="pre">{{ task.affected_location || '见漏洞描述' }}</p><h3>漏洞描述</h3><p class="pre">{{ task.description }}</p>
          <h3>复现步骤与证据</h3><p class="pre">{{ task.evidence || '未提供' }}</p><h3>修复建议</h3><p class="pre">{{ task.remediation_advice || '未提供' }}</p><h3>验收要求</h3><p class="pre">{{ task.requirement }}</p>
</el-collapse-item></el-collapse>
          <template v-if="currentSubmission"><div class="evidence-heading"><h3>第 {{ currentSubmission.revision }} 次修复提交</h3><el-tag size="small" effect="plain">提交时冻结</el-tag></div><div class="evidence-card"><dl><dt>修复说明</dt><dd class="pre">{{ currentSubmission.remediation_summary }}</dd><dt>部署版本</dt><dd>{{ currentSubmission.change_reference || '未提供' }}</dd><dt>部署环境</dt><dd>{{ currentSubmission.target_environment }} · {{ dateText(currentSubmission.deployed_at) }}</dd><dt>自测结果</dt><dd class="pre">{{ currentSubmission.self_test_result }}</dd></dl><div v-if="currentSubmission.attachments.length" class="proof-list"><h4>本次提交的证明附件</h4><el-button v-for="file in currentSubmission.attachments" :key="file.id" link type="primary" @click="download(file)">{{ file.original_name }}</el-button></div><p v-else class="muted">本次提交未引用证明附件</p></div><el-button text type="primary" @click="tab='submissions'">查看历次提交、版本差异与审核记录</el-button></template>
          <div v-else class="evidence-card muted">{{ task.status==='pending_acceptance'?'等待执行人员接收任务。':'当前没有待验收的有效提交，可在“提交与审核”中查看历史记录。' }}</div>
        </el-tab-pane>
        <el-tab-pane label="修复进展" name="repair">
          <el-form label-position="top">
            <el-form-item label="当前进展"><el-input v-model="progress" type="textarea" :rows="3" :disabled="!editable" /></el-form-item>
            <el-form-item label="预计完成时间"><el-date-picker v-model="plan.expected_completion_at" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" :disabled="!editable" /></el-form-item>
            <el-form-item label="阻塞情况"><el-input v-model="blocked" type="textarea" :rows="2" :disabled="!editable" placeholder="例如等待发布窗口；填写后仍按原期限计时" /></el-form-item>
            <template v-if="blocked.trim()"><el-form-item label="阻塞依赖方"><el-input v-model="plan.blocked_dependency" :disabled="!editable" /></el-form-item><el-form-item label="预计解除时间"><el-date-picker v-model="plan.blocked_until" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" :disabled="!editable" /></el-form-item></template>
            <el-button v-if="editable" :loading="busy" @click="action('progress', { progress, blocked_reason: blocked, ...plan, expected_completion_at: plan.expected_completion_at || null, blocked_until: plan.blocked_until || null })">保存进展</el-button>

          </el-form>
        </el-tab-pane>
        <el-tab-pane label="提交与审核" name="submissions">
          <el-collapse v-if="compareSubmissions.length > 1"><el-collapse-item title="比较两次提交的差异"><div class="actions"><el-select v-model="compareOlder" aria-label="旧提交版本"><el-option v-for="s in compareSubmissions" :key="s.id" :value="s.id" :label="`第 ${s.revision} 次提交`" /></el-select><span>对比</span><el-select v-model="compareNewer" aria-label="新提交版本"><el-option v-for="s in compareSubmissions" :key="s.id" :value="s.id" :label="`第 ${s.revision} 次提交`" /></el-select></div><el-table :data="diffRows"><el-table-column prop="field" label="字段" width="120" /><el-table-column label="旧版本"><template #default="{ row }"><p class="pre">{{ row.oldValue || '—' }}</p></template></el-table-column><el-table-column label="新版本"><template #default="{ row }"><el-tag v-if="row.changed" type="warning">有变化</el-tag><p class="pre">{{ row.newValue || '—' }}</p></template></el-table-column></el-table></el-collapse-item></el-collapse>
          <el-empty v-if="!submissions.length" description="尚未提交修复结果" />
          <article v-for="submission in submissions" :key="submission.id" class="record">
            <h3>第 {{ submission.revision }} 次提交 <small>{{ dateText(submission.submitted_at) }}</small></h3>
            <el-collapse v-if="submission.material"><el-collapse-item :title="`提交时的共享材料（第 ${submission.material_revision} 版）`"><p class="pre">{{ submission.material.description }}</p><p class="pre">验收要求：{{ submission.material.requirement }}</p></el-collapse-item></el-collapse>
            <p class="pre">{{ submission.remediation_summary }}</p><p>环境：{{ submission.target_environment }} · 版本：{{ submission.change_reference || '—' }} · 部署：{{ dateText(submission.deployed_at) }}</p><p class="pre">自测：{{ submission.self_test_result }}</p><p v-if="submission.no_deployment_reason">无需部署：{{ submission.no_deployment_reason }}</p>
            <el-button v-for="file in submission.attachments" :key="file.id" link type="primary" @click="download(file)">{{ file.original_name }}</el-button>
            <div v-for="(item, index) in submission.reviews" :key="index" class="review-record"><strong>{{ item.stage === 'organization' ? '组织确认' : '集团复测' }}：{{ item.decision === 'pass' ? '通过' : '退回' }}</strong><p>{{ item.reviewer_name }} · {{ dateText(item.reviewed_at) }}</p><p v-if="item.verified_at">实际验证：{{ item.verified_environment }} · {{ dateText(item.verified_at) }}</p><p class="pre">{{ item.public_feedback }}</p><p v-if="item.internal_note" class="pre muted">内部备注：{{ item.internal_note }}</p></div>
          </article>

        </el-tab-pane>
        <el-tab-pane label="材料版本" name="material-history"><article v-for="m in materialHistory" :key="m.revision" class="record"><h3>第 {{ m.revision }} 版 · {{ dateText(m.created_at) }}</h3><p>验收环境：{{ m.target_environment }} · {{ m.deployment_required ? '需要部署' : '无需部署' }}</p><p class="pre">{{ m.description }}</p><p class="pre">验收要求：{{ m.requirement }}</p><el-button v-for="file in m.attachments" :key="file.id" link @click="download(file)">{{ file.original_name }}</el-button></article></el-tab-pane>
        <el-tab-pane label="沟通记录" name="comments">
          <article v-for="item in comments" :key="item.id" class="record"><strong>{{ item.author_name }}</strong> <small>{{ dateText(item.created_at) }} · {{ item.visibility === 'internal' ? '内部可见' : '任务参与者可见' }}</small><p class="pre">{{ item.body }}</p></article>
          <template v-if="!['completed','cancelled'].includes(task.status)"><el-input v-model="comment" type="textarea" :rows="3" placeholder="填写沟通内容" /><div class="actions"><el-select v-if="!executor" v-model="visibility"><el-option label="任务参与者可见" value="participants" /><el-option label="仅内部管理人员可见" value="internal" /></el-select><el-button :loading="busy" @click="action('comment', { reason: comment, visibility })">发送</el-button></div></template>
        </el-tab-pane>
        <el-tab-pane label="申请记录" name="requests"><el-empty v-if="!requests.length" description="暂无申请" /><article v-for="item in requests" :key="item.id" class="record"><strong>{{ requestLabels[item.type] }}</strong><p class="pre">{{ item.reason }}</p><p v-if="item.mitigation" class="pre">临时缓解措施：{{ item.mitigation }}</p><p v-if="item.proposed_due_at">申请期限：{{ dateText(item.proposed_due_at) }}</p><p>{{ { pending: '待处理', approved: '已批准', rejected: '已拒绝' }[item.status] }} {{ item.decision }}</p><el-button v-if="item.status === 'pending' && ((['reassign','extension'].includes(item.type) && (task.can_assign || task.can_verify)) || (['risk_acceptance','false_positive'].includes(item.type) && task.can_verify))" @click="openDecision(item)">处理申请</el-button></article></el-tab-pane>
        <el-tab-pane label="操作记录" name="events"><article v-for="item in events" :key="item.id" class="record"><strong>{{ eventLabels[item.action] || item.action }}</strong> · {{ item.actor_name }} · {{ dateText(item.created_at) }}<p v-if="item.details.reason" class="pre">{{ item.details.reason }}</p></article></el-tab-pane>
      </el-tabs>
      <template v-if="['materials','repair'].includes(tab)">
        <h3>附件</h3><div class="actions" v-if="!editable && (writable || (!executor && !['completed','cancelled'].includes(task.status)))"><el-select v-if="!executor" v-model="visibility"><el-option label="任务参与者可见" value="participants" /><el-option label="仅内部可见" value="internal" /></el-select><input ref="fileInput" type="file" aria-label="上传修复附件" :disabled="busy" accept=".png,.jpg,.jpeg,.pdf,.txt,.log,.csv,.zip,.xlsx,.docx" @change="upload" /><span class="muted">单个文件不超过 20 MB</span></div>
        <el-table :data="attachments"><el-table-column label="文件"><template #default="{ row }"><el-button link type="primary" @click="download(row)">{{ row.original_name }}</el-button></template></el-table-column><el-table-column label="可见范围"><template #default="{ row }">{{ row.visibility === 'internal' ? '内部' : '任务参与者' }}</template></el-table-column></el-table>
      </template>
      <el-pagination v-if="childTotal > 20" v-model:current-page="childPage" :total="childTotal" :page-size="20" layout="total, prev, pager, next" @current-change="loadTab" />
      </section><aside class="repair-decision">
          <el-form v-if="reviewable" label-position="top" class="review-form">
            <h3>{{ task.status==='pending_org_review'?'组织确认':'集团复测' }}</h3><p class="muted">核对当前提交后，填写本次验收结果。</p><el-form-item label="审核 / 复测结果（执行人员可见，必填）"><el-input v-model="review.reason" type="textarea" :rows="4" placeholder="请明确验证环境、版本和结果；退回时说明需要补充或修正的内容" /></el-form-item>
            <template v-if="task.status === 'pending_security_review'"><el-form-item label="实际验证环境（必填）"><el-input v-model="review.verified_environment" /></el-form-item><el-form-item label="实际验证时间（必填）"><el-date-picker v-model="review.verified_at" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" /></el-form-item></template>
            <el-form-item label="内部备注（不向执行人员公开）"><el-input v-model="review.internal_note" type="textarea" :rows="2" /></el-form-item>
            <el-checkbox v-model="reviewConfirmed" class="review-confirm">我已核对目标环境、提交版本及证明材料</el-checkbox><div class="decision-buttons"><el-button type="primary" :loading="busy" :disabled="task.material_needs_review || !currentSubmission" @click="sendReview('pass')">{{ task.status === 'pending_org_review' ? '确认并送集团复测' : '复测通过' }}</el-button>
            <el-button type="warning" :disabled="busy" @click="sendReview('return')">退回修复</el-button></div>
          </el-form>
          <el-form v-if="editable" label-position="top">            <template v-if="editable">
              <h3>提交本次修复</h3><p class="muted">完成部署与自测后，交组织确认。</p>
              <el-form-item label="修复说明（必填）"><el-input v-model="form.remediation_summary" type="textarea" :rows="4" /></el-form-item>
              <el-form-item label="部署状态"><el-select v-model="form.deployment_status"><el-option label="已部署目标环境" value="target" /><el-option label="已部署测试环境" value="test" /><el-option label="仅完成代码修改" value="code_only" /><el-option label="不涉及部署" value="not_applicable" /></el-select></el-form-item>
              <el-form-item v-if="form.deployment_status === 'not_applicable'" label="不涉及部署的原因（必填）"><el-input v-model="form.no_deployment_reason" type="textarea" /></el-form-item>
              <el-form-item label="版本 / 变更编号"><el-input v-model="form.change_reference" /></el-form-item>
              <el-form-item label="部署时间"><el-date-picker v-model="form.deployed_at" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" /></el-form-item>
              <el-form-item label="自测步骤与结果（必填）"><el-input v-model="form.self_test_result" type="textarea" :rows="4" /></el-form-item>
              <el-form-item label="本次提交引用的证明附件"><el-select v-model="form.attachment_ids" multiple style="width:100%"><el-option v-for="file in fileOptions.filter(a => a.visibility === 'participants')" :key="file.id" :label="file.original_name" :value="file.id" /></el-select></el-form-item>
              <el-button type="primary" :loading="busy" :disabled="task.material_needs_review" @click="submit">提交修复，交组织确认</el-button>
            </template>
            <el-button text @click="tab='repair'">记录进展与阻塞</el-button>
            <div class="upload-proof"><input ref="fileInput" type="file" aria-label="上传修复附件" class="sr-only" :disabled="busy" accept=".png,.jpg,.jpeg,.pdf,.txt,.log,.csv,.zip,.xlsx,.docx" @change="upload" /><el-button :disabled="busy" @click="visibility='participants';fileInput?.click()">上传证明附件</el-button><p class="muted">单个文件不超过 20 MB；上传后选择本次提交引用的附件。</p></div>
          </el-form>
          <div v-if="!editable && !reviewable" class="task-next-step"><h3>{{ task.status==='pending_acceptance'?'接收并安排修复':'当前处理进度' }}</h3><p>{{ outcomes[task.completion_reason ?? ''] || taskStatuses[task.status] }}</p><p class="muted">{{ task.status==='pending_acceptance'?'确认修复要求后，填写预计完成时间。':task.status==='completed'?'处置结果及历史证据已保留。':'当前进展与反馈请查看左侧记录。' }}</p></div>
      <el-form v-if="task.can_execute && task.status === 'pending_acceptance'" label-position="top"><el-form-item label="预计完成时间（接收时必填）"><el-date-picker v-model="plan.expected_completion_at" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" /></el-form-item></el-form>
      <div class="actions">
        <el-button v-if="task.can_execute && task.status === 'pending_acceptance'" type="primary" :loading="busy" @click="action('accept', { expected_completion_at: plan.expected_completion_at ? new Date(plan.expected_completion_at).toISOString() : null })">接收任务</el-button>
        <el-button v-if="task.can_execute && task.status === 'pending_org_review'" :disabled="busy" @click="withdraw">撤回提交</el-button>
        <el-button v-if="task.can_execute && writable" :disabled="busy" @click="requestVisible = true">提出申请</el-button>
        <el-button v-if="task.can_assign && !['completed','cancelled'].includes(task.status)" :disabled="busy" @click="editMaterial">更新共享材料</el-button>
        <el-button v-if="task.can_verify && !['completed','cancelled'].includes(task.status)" :disabled="busy" @click="editSource">更新原始漏洞材料</el-button>
        <el-button v-if="task.can_verify && task.status === 'completed' && task.completion_reason === 'risk_accepted' && !['closed', 'cancelled'].includes(task.work_order_status)" :disabled="busy" @click="reopenRisk">撤销风险接受，恢复整改</el-button>
        <el-button v-if="task.can_assign && writable" :disabled="busy" @click="openReassign">改派任务</el-button>
        <el-button v-if="task.can_assign && writable" type="danger" plain :disabled="busy" @click="cancel">取消执行任务</el-button>
      </div>

      </aside></div>
    </template>
    <el-dialog v-model="materialVisible" title="更新共享材料" width="680px" append-to-body><el-form label-position="top"><el-form-item label="标题"><el-input v-model="material.title" /></el-form-item><el-form-item label="受影响位置"><el-input v-model="material.affected_location" /></el-form-item><el-form-item label="漏洞描述"><el-input v-model="material.description" type="textarea" :rows="3" /></el-form-item><el-form-item label="复现步骤与证据"><el-input v-model="material.evidence" type="textarea" :rows="3" /></el-form-item><el-form-item label="修复建议"><el-input v-model="material.remediation_advice" type="textarea" /></el-form-item><el-form-item label="验收要求"><el-input v-model="material.requirement" type="textarea" /></el-form-item><el-form-item label="验收环境"><el-input v-model="materialTarget" /></el-form-item><el-checkbox v-model="materialDeployment">需部署到目标环境</el-checkbox><el-form-item label="此材料版本的共享附件"><el-select v-model="material.attachment_ids" multiple><el-option v-for="file in attachments.filter(a => a.visibility === 'participants')" :key="file.id" :value="file.id" :label="file.original_name" /></el-select></el-form-item><el-checkbox v-if="task && ['pending_org_review','pending_security_review'].includes(task.status)" v-model="invalidateSubmission">本次修改改变验收材料，将当前提交退回重新修复</el-checkbox><el-form-item label="更新原因"><el-input v-model="materialReason" /></el-form-item></el-form><template #footer><el-button type="primary" :loading="busy" @click="action('materials', { material, reason: materialReason, target_environment: materialTarget, deployment_required: materialDeployment, invalidate_submission: invalidateSubmission })">确认共享并保存新版本</el-button></template></el-dialog>
    <el-dialog v-model="sourceVisible" title="更新原始漏洞材料" width="680px" append-to-body><el-alert title="内容变化会使共享材料进入待复核，并将待审核提交退回。内部原始证据不会自动公开给执行人员。" type="warning" :closable="false" /><el-form label-position="top"><el-form-item label="原始标题"><el-input v-model="source.title" /></el-form-item><el-form-item label="严重程度"><el-select v-model="source.severity"><el-option v-for="(label, value) in severityLabels" :key="value" :value="value" :label="label" /></el-select></el-form-item><el-form-item label="原始描述"><el-input v-model="source.description" type="textarea" /></el-form-item><el-form-item label="内部原始证据"><el-input v-model="source.evidence" type="textarea" /></el-form-item><el-form-item label="修复建议"><el-input v-model="source.remediation_advice" type="textarea" /></el-form-item><el-form-item label="变更原因"><el-input v-model="sourceReason" /></el-form-item></el-form><template #footer><el-button type="primary" :loading="busy" @click="action('source-update', { material: source, reason: sourceReason })">更新并通知材料复核</el-button></template></el-dialog>
    <el-dialog v-model="requestVisible" title="提出申请" width="540px" append-to-body><el-form label-position="top"><el-form-item label="申请类型"><el-select v-model="request.type"><el-option v-for="(label, value) in requestLabels" :key="value" :value="value" :label="label" /></el-select></el-form-item><el-form-item label="申请原因"><el-input v-model="request.reason" type="textarea" :rows="4" /></el-form-item><el-form-item v-if="request.type === 'extension'" label="临时缓解措施（必填）"><el-input v-model="request.mitigation" type="textarea" /></el-form-item><el-form-item v-if="request.type === 'extension'" label="希望延期至"><el-date-picker v-model="request.proposed_due_at" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" /></el-form-item></el-form><template #footer><el-button type="primary" :loading="busy" @click="sendRequest">提交申请</el-button></template></el-dialog>
    <el-dialog v-model="reassignVisible" title="改派给具体人员" width="540px" append-to-body><el-form label-position="top"><el-form-item label="新负责人"><el-select v-model="selectedGrant" filterable style="width:100%"><el-option v-for="g in grants" :key="g.id" :value="g.id" :label="`${g.display_name} · ${g.vendor_name || '内部人员'}`" /></el-select></el-form-item><el-form-item label="改派原因"><el-input v-model="reassignReason" type="textarea" /></el-form-item></el-form><template #footer><el-button type="primary" :loading="busy" @click="action('reassign', { grant_id: selectedGrant, reason: reassignReason })">确认改派</el-button></template></el-dialog>
    <el-dialog v-model="decisionVisible" title="处理申请" width="540px" append-to-body><el-form label-position="top"><el-form-item label="处理结果"><el-radio-group v-model="decision.decision"><el-radio value="approved">批准</el-radio><el-radio value="rejected">拒绝</el-radio></el-radio-group></el-form-item><el-form-item label="说明"><el-input v-model="decision.reason" type="textarea" :rows="3" /></el-form-item><el-form-item v-if="activeRequest?.type === 'reassign' && decision.decision === 'approved'" label="新负责人"><el-select v-model="selectedGrant"><el-option v-for="g in grants" :key="g.id" :value="g.id" :label="g.display_name" /></el-select></el-form-item><el-form-item v-if="activeRequest?.type === 'risk_acceptance' && decision.decision === 'approved'" label="风险接受有效期"><el-date-picker v-model="decision.risk_accepted_until" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" /></el-form-item></el-form><template #footer><el-button type="primary" :loading="busy" @click="decideRequest">确认处理</el-button></template></el-dialog>
  </div>
</template>

<style scoped>
.repair-detail { min-height: 240px; }.detail-heading,.actions { display:flex;align-items:center;gap:12px;flex-wrap:wrap;margin:16px 0; }.detail-heading { justify-content:space-between; }.detail-heading h2 { margin:8px 0; }.pre { white-space:pre-wrap;overflow-wrap:anywhere;line-height:1.7; }.muted,small { color:var(--el-text-color-secondary); }.record { padding:16px 0;border-bottom:1px solid var(--el-border-color-lighter); }.review-record { padding:12px;background:var(--el-fill-color-light);margin-top:12px; }.el-alert { margin-top:12px; }.execution-blocked-alert :deep(.el-alert__content) { line-height:1.7; }.blocked-action { margin:4px 0 0;font-size:13px; }.execution-blocked-alert .muted { margin:4px 0 0;font-size:12px; }.el-pagination { margin-top:16px; }.el-select { min-width:180px; }
</style>

<style scoped>
.detail-heading { margin:0 0 10px; }.detail-heading h2 { font-size:22px;font-weight:500; }.repair-context { display:flex;gap:10px 24px;flex-wrap:wrap;color:var(--muted);font-size:13px;line-height:1.7; }.overdue-text { color:var(--el-color-danger); }
.repair-stages { padding:0;margin:22px 0;display:flex;gap:18px;list-style:none; }.repair-stages li { display:flex;align-items:center;gap:7px;color:var(--muted);font-size:12px; }.repair-stages b { display:grid;place-items:center;border-radius:50%;width:22px;height:22px;background:#edf2f6;font-weight:500; }.repair-stages .done,.repair-stages .current { color:var(--cyan); }.repair-stages .done b { background:#eaf5f2; }.repair-stages .current b { color:#fff;background:var(--cyan); }
.repair-workspace { display:grid;grid-template-columns:minmax(0,1fr) 335px;margin-top:20px;border-top:1px solid var(--line); }.repair-evidence { min-width:0;padding:14px 24px 10px 0; }.repair-decision { position:sticky;top:0;max-height:calc(94dvh - 100px);overflow-y:auto;min-width:0;background:#f7fafc;border-left:1px solid var(--line);padding:22px;align-self:start; }.repair-decision h3 { margin:0 0 10px;font-size:16px;font-weight:500; }.repair-decision .muted { margin:0 0 18px;font-size:12px;line-height:1.7; }.repair-decision .el-select { min-width:0;width:100%; }.repair-decision :deep(.el-date-editor.el-input) { width:100%; }.repair-decision .actions { gap:8px; }.repair-decision .actions .el-button { margin:0; }.review-confirm { height:auto;white-space:normal;margin-bottom:18px; }.review-confirm :deep(.el-checkbox__label) { white-space:normal;font-size:12px;line-height:1.6; }.decision-buttons { display:flex;gap:8px;flex-wrap:wrap; }.decision-buttons .el-button { margin:0; }
.evidence-heading { display:flex;align-items:center;justify-content:space-between;gap:12px;margin:18px 0 12px; }.evidence-heading h3 { font-weight:500;font-size:15px;margin:0; }.evidence-card { border:1px solid var(--line);border-radius:8px;padding:18px;font-size:13px;line-height:1.7; }.evidence-card p:first-child { margin-top:0; }.evidence-card dl { display:grid;grid-template-columns:78px minmax(0,1fr);gap:14px 8px;margin:0; }.evidence-card dt { color:var(--muted); }.evidence-card dd { margin:0;overflow-wrap:anywhere; }.proof-list { border-top:1px solid var(--line);margin-top:16px; }.proof-list .el-button { display:flex;margin:8px 0; }.material-more { margin:12px 0; }.task-next-step { margin-bottom:18px; }.upload-proof { margin:16px 0; }.sr-only { position:absolute;width:1px;height:1px;padding:0;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap; }.sr-only:focus-visible+button { outline:2px solid var(--cyan);outline-offset:3px; }
@media(max-width:850px) { .repair-workspace { grid-template-columns:1fr; }.repair-evidence { padding-right:0; }.repair-decision { position:static;max-height:none;overflow:visible;border-left:0;border-top:1px solid var(--line);padding:18px; }.repair-stages { gap:10px;overflow-x:auto; }.repair-stages li { flex:none; }.repair-context { gap:6px 16px; }.detail-heading h2 { font-size:20px; } }
</style>
