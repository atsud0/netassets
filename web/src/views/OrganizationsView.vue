<script setup lang="ts">
import RowActions from '../components/RowActions.vue'
import { computed, h, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'
import { useAuthStore } from '../stores/auth'

type OrganizationType = 'group' | 'subsidiary' | 'department' | 'other'
interface Organization {
  id: string; parent_id?: string; parent_name?: string; code: string; name: string; short_name?: string
  type: OrganizationType; manager?: string; contact?: string; status: 'enabled' | 'disabled'; notes?: string
  child_count: number; business_system_count: number; asset_count: number; created_at: string; children?: Organization[]
  can_update: boolean; can_create_child: boolean
}
interface OrganizationTreeResponse { items: Organization[]; total: number }
interface OrganizationImpact {
  organization_id: string; organization_name: string; organization_type: OrganizationType; status: 'enabled' | 'disabled'
  user_count: number; role_assignment_count: number; asset_count: number; business_system_count: number
  child_count: number; enabled_child_count: number; unfinished_inventory_task_count: number
  open_work_order_count: number; open_quality_issue_count: number; network_area_count: number; active_api_token_count: number
  requires_resource_confirmation: boolean; can_disable: boolean; disable_blockers: string[]; can_delete: boolean; delete_blockers: string[]
}

const auth = useAuthStore()
const loading = ref(false)
const submitting = ref(false)
const groupSubmitting = ref(false)
const editSubmitting = ref(false)
const structureSubmitting = ref(false)
const operatingID = ref('')
const createDialogVisible = ref(false)
const groupDialogVisible = ref(false)
const editDialogVisible = ref(false)
const structureDialogVisible = ref(false)
const organizations = ref<Organization[]>([])
const total = ref(0)
const editingOrganization = ref<Organization | null>(null)
const formRef = ref<FormInstance>()
const groupFormRef = ref<FormInstance>()
const editFormRef = ref<FormInstance>()
const structureFormRef = ref<FormInstance>()
const form = reactive({ parent_id: '', code: '', name: '', short_name: '', type: 'subsidiary' as Exclude<OrganizationType, 'group'>, manager: '', contact: '', notes: '' })
const groupForm = reactive({ name: '', short_name: '', manager: '', contact: '', notes: '' })
const editForm = reactive({ name: '', short_name: '', manager: '', contact: '', notes: '' })
const structureForm = reactive({ parent_id: '', type: 'subsidiary' as Exclude<OrganizationType, 'group'> })

const typeLabels: Record<OrganizationType, string> = { group: '集团', subsidiary: '子公司', department: '部门', other: '其他' }
const groupRules: FormRules = { name: [{ required: true, message: '请输入集团名称', trigger: 'blur' }] }
const editRules: FormRules = { name: [{ required: true, message: '请输入组织名称', trigger: 'blur' }] }
const rules: FormRules = {
  parent_id: [{ required: true, message: '请选择上级组织', trigger: 'change' }],
  code: [
    { required: true, message: '请输入组织编码', trigger: 'blur' },
    { pattern: /^[A-Za-z0-9][A-Za-z0-9_-]{1,63}$/, message: '使用 2-64 位字母、数字、下划线或横线', trigger: 'blur' },
  ],
  name: [{ required: true, message: '请输入组织名称', trigger: 'blur' }],
  type: [{ required: true, message: '请选择组织类型', trigger: 'change' }],
}
const structureRules: FormRules = {
  parent_id: [{ required: true, message: '请选择新的上级组织', trigger: 'change' }],
  type: [{ required: true, message: '请选择组织类型', trigger: 'change' }],
}

function flattenOrganizations(items: Organization[]): Organization[] {
  return items.flatMap((item) => [item, ...flattenOrganizations(item.children ?? [])])
}
function collectDescendantIDs(item: Organization): string[] {
  return (item.children ?? []).flatMap((child) => [child.id, ...collectDescendantIDs(child)])
}
const flatOrganizations = computed(() => flattenOrganizations(organizations.value))
const rootOrganization = computed(() => flatOrganizations.value.find((item) => item.type === 'group') ?? null)
const enabledParents = computed(() => flatOrganizations.value.filter((item) => item.status === 'enabled' && item.can_create_child))
const canManageOrganizations = computed(() => flatOrganizations.value.some((item) => item.can_update || item.can_create_child))
const structureParentOptions = computed(() => {
  const current = editingOrganization.value
  if (!current) return []
  const excluded = new Set([current.id, ...collectDescendantIDs(current)])
  return enabledParents.value.filter((item) => !excluded.has(item.id))
})
const editEntityLabel = computed(() => editingOrganization.value?.type === 'subsidiary' ? '子公司' : editingOrganization.value?.type === 'department' ? '部门' : '组织')
const editDialogTitle = computed(() => `编辑${editEntityLabel.value}基本信息`)
const parentOptionLabel = (item: Organization) => `${item.name}（${typeLabels[item.type]}）`

async function loadOrganizations() {
  loading.value = true
  try {
    const response = await apiClient.get<OrganizationTreeResponse>('/organizations/tree')
    organizations.value = response.data.items
    total.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取组织架构失败'))
  } finally { loading.value = false }
}
function openCreateDialog(parent?: Organization) {
  const defaultParentID = parent?.status === 'enabled' && parent.can_create_child ? parent.id : (enabledParents.value[0]?.id ?? '')
  if (!defaultParentID) { ElMessage.warning('当前账号没有可新增下级组织的授权范围'); return }
  Object.assign(form, { parent_id: defaultParentID, code: '', name: '', short_name: '', type: 'subsidiary', manager: '', contact: '', notes: '' })
  createDialogVisible.value = true
}
function openGroupEditDialog() {
  const group = rootOrganization.value
  if (!group) return
  editingOrganization.value = group
  Object.assign(groupForm, { name: group.name, short_name: group.short_name ?? '', manager: group.manager ?? '', contact: group.contact ?? '', notes: group.notes ?? '' })
  groupDialogVisible.value = true
}
function openEditDialog(item: Organization) {
  if (item.type === 'group') return openGroupEditDialog()
  editingOrganization.value = item
  Object.assign(editForm, { name: item.name, short_name: item.short_name ?? '', manager: item.manager ?? '', contact: item.contact ?? '', notes: item.notes ?? '' })
  editDialogVisible.value = true
}
function openStructureDialog(item: Organization) {
  editingOrganization.value = item
  Object.assign(structureForm, { parent_id: item.parent_id ?? '', type: item.type as Exclude<OrganizationType, 'group'> })
  structureDialogVisible.value = true
}
async function submit() {
  if (!formRef.value || !await formRef.value.validate()) return
  submitting.value = true
  try { await apiClient.post('/organizations', form); ElMessage.success('组织机构已新增'); createDialogVisible.value = false; await loadOrganizations() }
  catch (error) { ElMessage.error(getAPIErrorMessage(error, '新增组织失败')) }
  finally { submitting.value = false }
}
async function submitGroupInfo() {
  if (!groupFormRef.value || !editingOrganization.value || !await groupFormRef.value.validate()) return
  groupSubmitting.value = true
  try { await apiClient.patch(`/organizations/${editingOrganization.value.id}`, groupForm); ElMessage.success('集团基本信息已更新'); groupDialogVisible.value = false; await loadOrganizations() }
  catch (error) { ElMessage.error(getAPIErrorMessage(error, '保存集团信息失败')) }
  finally { groupSubmitting.value = false }
}
async function submitOrganizationInfo() {
  if (!editFormRef.value || !editingOrganization.value || !await editFormRef.value.validate()) return
  editSubmitting.value = true
  try { await apiClient.patch(`/organizations/${editingOrganization.value.id}`, editForm); ElMessage.success(`${editEntityLabel.value}基本信息已更新`); editDialogVisible.value = false; await loadOrganizations() }
  catch (error) { ElMessage.error(getAPIErrorMessage(error, '保存组织信息失败')) }
  finally { editSubmitting.value = false }
}
async function submitStructure() {
  if (!structureFormRef.value || !editingOrganization.value || !await structureFormRef.value.validate()) return
  structureSubmitting.value = true
  try { await apiClient.patch(`/organizations/${editingOrganization.value.id}/structure`, structureForm); ElMessage.success('组织层级与类型已调整，所属子树已整体迁移'); structureDialogVisible.value = false; await loadOrganizations() }
  catch (error) { ElMessage.error(getAPIErrorMessage(error, '调整组织结构失败')) }
  finally { structureSubmitting.value = false }
}
async function loadImpact(item: Organization) {
  const response = await apiClient.get<{ item: OrganizationImpact }>(`/organizations/${item.id}/impact`)
  return response.data.item
}
function impactContent(impact: OrganizationImpact, blockers: string[]) {
  const rows = [
    `用户任职：${impact.user_count} 人`, `网络资产：${impact.asset_count} 项`, `业务系统：${impact.business_system_count} 个`,
    `下级组织：${impact.child_count} 个（启用 ${impact.enabled_child_count} 个）`, `未完成填报任务：${impact.unfinished_inventory_task_count} 个`,
    `未关闭安全工单：${impact.open_work_order_count} 个`, `未完成质量整改：${impact.open_quality_issue_count} 个`,
  ]
  return h('div', rows.map((row) => h('p', { style: 'margin:4px 0' }, row)).concat(blockers.length ? [
    h('strong', { style: 'display:block;margin-top:12px;color:var(--el-color-danger)' }, '当前阻断项'),
    h('ul', { style: 'margin:6px 0 0;padding-left:20px' }, blockers.map((text) => h('li', text))),
  ] : []))
}
async function showImpact(item: Organization) {
  operatingID.value = item.id
  try { const impact = await loadImpact(item); await ElMessageBox.alert(impactContent(impact, impact.disable_blockers), `“${item.name}”影响检查`, { confirmButtonText: '知道了' }) }
  catch (error) { if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error, '读取组织影响失败')) }
  finally { operatingID.value = '' }
}
async function disableOrganization(item: Organization) {
  operatingID.value = item.id
  try {
    const impact = await loadImpact(item)
    if (!impact.can_disable) { await ElMessageBox.alert(impactContent(impact, impact.disable_blockers), '暂时不能停用', { type: 'error', confirmButtonText: '知道了' }); return }
    await ElMessageBox.confirm(impactContent(impact, []), `确认停用“${item.name}”`, { type: 'warning', confirmButtonText: '确认停用', cancelButtonText: '取消' })
    await apiClient.post(`/organizations/${item.id}/disable`, { confirm_resources: impact.requires_resource_confirmation })
    ElMessage.success('组织已停用，其用户不能再切换到该工作组织'); await loadOrganizations()
  } catch (error) { if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error, '停用组织失败')) }
  finally { operatingID.value = '' }
}
async function enableOrganization(item: Organization) {
  operatingID.value = item.id
  try {
    await ElMessageBox.confirm(`确认恢复启用“${item.name}”吗？`, '恢复启用组织', { type: 'warning', confirmButtonText: '恢复启用', cancelButtonText: '取消' })
    await apiClient.post(`/organizations/${item.id}/enable`); ElMessage.success('组织已恢复启用'); await loadOrganizations()
  } catch (error) { if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error, '恢复启用失败')) }
  finally { operatingID.value = '' }
}
async function deleteOrganization(item: Organization) {
  operatingID.value = item.id
  try {
    const impact = await loadImpact(item)
    if (!impact.can_delete) { await ElMessageBox.alert(impactContent(impact, impact.delete_blockers), '暂时不能移入回收站', { type: 'error', confirmButtonText: '知道了' }); return }
    await ElMessageBox.confirm(`确认将空组织“${item.name}”移入回收站吗？恢复后将保持停用状态。`, '移入回收站', { type: 'warning', confirmButtonText: '移入回收站', cancelButtonText: '取消' })
    await apiClient.delete(`/organizations/${item.id}`); ElMessage.success('组织已移入回收站'); await loadOrganizations()
  } catch (error) { if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error, '删除组织失败')) }
  finally { operatingID.value = '' }
}

onMounted(loadOrganizations)
</script>

<template>
  <section>
    <div class="page-heading">
      <div>
        <h1>组织架构</h1>
      </div>
      <div class="page-heading-actions">
        <el-button :loading="loading" @click="loadOrganizations">刷新</el-button>
        <el-button v-if="enabledParents.length" type="primary" @click="openCreateDialog()">新增组织</el-button>
      </div>
    </div>

    <div v-if="rootOrganization" class="panel group-overview">
      <div><span class="panel-kicker">集团基本信息</span><h2>{{ rootOrganization.name }}</h2><p>{{ rootOrganization.short_name || '未设置集团简称' }} · {{ rootOrganization.manager || '未设置负责人' }} · {{ rootOrganization.contact || '未设置联系方式' }}</p></div>
      <div class="group-overview-actions"><el-tag type="success" effect="plain">{{ total }} 个组织节点</el-tag><el-button v-if="auth.isGroupAdmin" @click="openGroupEditDialog">编辑集团信息</el-button></div>
    </div>

    <div class="panel data-table-panel organization-tree-panel" v-loading="loading">
      <div class="panel-title-row"><div><h2>集团组织树</h2><p>展开节点查看完整上下级关系；迁移组织时，其全部下级节点会随组织一起移动。</p></div></div>
      <el-table :data="organizations" row-key="id" default-expand-all :tree-props="{ children: 'children' }" empty-text="暂无组织机构">
        <el-table-column label="组织" min-width="250"><template #default="scope"><div class="organization-name-cell"><strong>{{ scope.row.name }}</strong><span v-if="scope.row.short_name">{{ scope.row.short_name }}</span></div></template></el-table-column>

        <el-table-column label="类型" width="100"><template #default="scope"><el-tag effect="plain">{{ typeLabels[scope.row.type as OrganizationType] }}</el-tag></template></el-table-column>
        <el-table-column label="负责人 / 联系方式" min-width="180"><template #default="scope"><div class="organization-contact-cell"><span>{{ scope.row.manager || '未填写负责人' }}</span><small>{{ scope.row.contact || '未填写联系方式' }}</small></div></template></el-table-column>
        <el-table-column label="系统 / 资产" width="105" align="center"><template #default="scope">{{ scope.row.business_system_count }} / {{ scope.row.asset_count }}</template></el-table-column>
        <el-table-column label="状态" width="96"><template #default="scope"><el-tag :type="scope.row.status === 'enabled' ? 'success' : 'info'" effect="plain">{{ scope.row.status === 'enabled' ? '启用' : '停用' }}</el-tag></template></el-table-column>
        <el-table-column v-if="canManageOrganizations || auth.isGroupAdmin" label="操作" width="88" fixed="right" align="right" header-align="right" class-name="table-action-column"><template #default="scope"><RowActions :label="`操作：${scope.row.name || scope.row.display_name || scope.row.id}`">
          <div class="table-row-actions organization-actions">
            <el-button v-if="scope.row.can_update" link type="primary" @click="openEditDialog(scope.row)">编辑信息</el-button>
            <el-button v-if="!auth.isGroupAdmin && scope.row.can_create_child" link type="primary" @click="openCreateDialog(scope.row)">新增下级组织</el-button>
            <el-button v-if="auth.isGroupAdmin && scope.row.type !== 'group'" link type="primary" @click="openStructureDialog(scope.row)">调整结构</el-button>
            <template v-if="auth.isGroupAdmin && scope.row.type !== 'group'">
              <el-button link type="primary" @click="showImpact(scope.row)" :disabled="operatingID === scope.row.id">查看影响</el-button>
              <el-button link type="primary" v-if="scope.row.status === 'enabled'" @click="disableOrganization(scope.row)" :disabled="operatingID === scope.row.id">停用组织</el-button>
              <el-button link type="primary" v-else @click="enableOrganization(scope.row)" :disabled="operatingID === scope.row.id">恢复启用</el-button>
              <el-button link type="danger" :disabled="operatingID === scope.row.id || scope.row.status !== 'disabled'" @click="deleteOrganization(scope.row)">移入回收站</el-button>
              <el-button link type="primary" v-if="scope.row.can_create_child" @click="openCreateDialog(scope.row)" :disabled="operatingID === scope.row.id">新增下级组织</el-button>
            </template>
          </div>
        </RowActions></template></el-table-column>
      </el-table>
    </div>

    <el-dialog v-model="groupDialogVisible" title="编辑集团基本信息" width="620px" destroy-on-close @closed="editingOrganization = null">
      <el-form ref="groupFormRef" :model="groupForm" :rules="groupRules" label-position="top"><div class="form-grid two-columns">
        <el-form-item label="集团名称" prop="name"><el-input v-model="groupForm.name" maxlength="255" placeholder="请输入集团完整名称" /></el-form-item><el-form-item label="集团简称"><el-input v-model="groupForm.short_name" maxlength="128" /></el-form-item>
        <el-form-item label="集团负责人"><el-input v-model="groupForm.manager" maxlength="128" /></el-form-item><el-form-item label="联系方式"><el-input v-model="groupForm.contact" maxlength="255" /></el-form-item>
        <el-form-item label="集团说明" class="full-span"><el-input v-model="groupForm.notes" type="textarea" :rows="3" maxlength="5000" show-word-limit /></el-form-item>
      </div></el-form><template #footer><el-button @click="groupDialogVisible = false">取消</el-button><el-button type="primary" :loading="groupSubmitting" @click="submitGroupInfo">保存集团信息</el-button></template>
    </el-dialog>

    <el-dialog v-model="editDialogVisible" :title="editDialogTitle" width="620px" destroy-on-close @closed="editingOrganization = null">
      <el-alert :title="auth.isGroupAdmin ? '组织编码不随基本信息修改；上级组织和组织类型请使用“调整结构”。' : '可维护组织名称、简称、负责人、联系方式和备注；组织编码、上级组织和组织类型由集团管理员维护。'" type="info" :closable="false" show-icon />
      <div v-if="editingOrganization" class="organization-system-field"><span>组织编码（系统字段）</span><code>{{ editingOrganization.code }}</code></div>
      <el-form ref="editFormRef" :model="editForm" :rules="editRules" label-position="top" class="dialog-form-with-alert"><div class="form-grid two-columns">
        <el-form-item label="组织名称" prop="name"><el-input v-model="editForm.name" maxlength="255" /></el-form-item><el-form-item label="组织简称"><el-input v-model="editForm.short_name" maxlength="128" /></el-form-item>
        <el-form-item label="负责人"><el-input v-model="editForm.manager" maxlength="128" /></el-form-item><el-form-item label="联系方式"><el-input v-model="editForm.contact" maxlength="255" /></el-form-item>
        <el-form-item label="备注" class="full-span"><el-input v-model="editForm.notes" type="textarea" :rows="3" maxlength="5000" show-word-limit /></el-form-item>
      </div></el-form><template #footer><el-button @click="editDialogVisible = false">取消</el-button><el-button type="primary" :loading="editSubmitting" @click="submitOrganizationInfo">保存基本信息</el-button></template>
    </el-dialog>

    <el-dialog v-model="structureDialogVisible" title="调整组织层级与类型" width="560px" destroy-on-close @closed="editingOrganization = null">
      <el-alert title="迁移当前组织时，其全部下级组织会随节点整体迁移；资产、系统和用户任职仍归属于原组织节点。" type="warning" :closable="false" show-icon />
      <el-form ref="structureFormRef" :model="structureForm" :rules="structureRules" label-position="top" class="dialog-form-with-alert">
        <el-form-item label="当前组织"><el-input :model-value="editingOrganization?.name" disabled /></el-form-item>
        <el-form-item label="新的上级组织" prop="parent_id"><el-select v-model="structureForm.parent_id" filterable style="width:100%" placeholder="请选择启用中的上级组织"><el-option v-for="item in structureParentOptions" :key="item.id" :label="parentOptionLabel(item)" :value="item.id" /></el-select></el-form-item>
        <el-form-item label="组织类型" prop="type"><el-select v-model="structureForm.type" style="width:100%"><el-option label="子公司" value="subsidiary" /><el-option label="部门" value="department" /><el-option label="其他" value="other" /></el-select></el-form-item>
      </el-form><template #footer><el-button @click="structureDialogVisible = false">取消</el-button><el-button type="primary" :loading="structureSubmitting" @click="submitStructure">确认调整</el-button></template>
    </el-dialog>

    <el-dialog v-model="createDialogVisible" title="新增组织机构" width="620px" destroy-on-close>
      <el-form ref="formRef" :model="form" :rules="rules" label-position="top"><div class="form-grid two-columns">
        <el-form-item label="上级组织" prop="parent_id"><el-select v-model="form.parent_id" filterable style="width:100%" placeholder="请选择"><el-option v-for="item in enabledParents" :key="item.id" :label="parentOptionLabel(item)" :value="item.id" /></el-select></el-form-item>
        <el-form-item label="组织类型" prop="type"><el-select v-model="form.type" style="width:100%"><el-option label="子公司" value="subsidiary" /><el-option label="部门" value="department" /><el-option label="其他" value="other" /></el-select></el-form-item>
        <el-form-item label="组织编码" prop="code"><el-input v-model="form.code" maxlength="64" placeholder="如：SUBSIDIARY_01" /><div class="muted-text">系统字段，用于接口关联和数据去重；组织列表不显示。</div></el-form-item><el-form-item label="组织名称" prop="name"><el-input v-model="form.name" maxlength="255" placeholder="请输入完整名称" /></el-form-item>
        <el-form-item label="组织简称"><el-input v-model="form.short_name" maxlength="128" /></el-form-item><el-form-item label="负责人"><el-input v-model="form.manager" maxlength="128" /></el-form-item>
        <el-form-item label="联系方式" class="full-span"><el-input v-model="form.contact" maxlength="255" /></el-form-item><el-form-item label="备注" class="full-span"><el-input v-model="form.notes" type="textarea" :rows="3" maxlength="5000" show-word-limit /></el-form-item>
      </div></el-form><template #footer><el-button @click="createDialogVisible = false">取消</el-button><el-button type="primary" :loading="submitting" @click="submit">确认新增</el-button></template>
    </el-dialog>
  </section>
</template>

<style scoped>
.group-overview { display:flex; align-items:center; justify-content:space-between; gap:24px; margin-bottom:16px; }
.group-overview h2 { margin:4px 0 6px; }.group-overview p { margin:0; color:var(--text-muted); }
.group-overview-actions { display:flex; align-items:center; gap:12px; flex-wrap:wrap; }.panel-kicker { color:var(--brand); font-size:12px; font-weight:700; letter-spacing:.08em; }
.organization-tree-panel { overflow:hidden; }.panel-title-row { display:flex; align-items:flex-start; justify-content:space-between; gap:16px; margin-bottom:14px; }
.panel-title-row h2 { margin:0 0 6px; font-size:18px; }.panel-title-row p { margin:0; color:var(--text-muted); }
.organization-name-cell,.organization-contact-cell { display:flex; flex-direction:column; gap:4px; min-width:0; }
.organization-name-cell span,.organization-contact-cell small { color:var(--text-muted); overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
.organization-system-field { display:flex; align-items:center; gap:10px; margin-top:16px; color:var(--text-muted); font-size:13px; }
.organization-system-field code { color:var(--el-text-color-primary); font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace; }
.organization-actions { display:flex; align-items:center; justify-content:flex-end; gap:12px; min-height:32px; }
.organization-actions > :deep(.el-button),.organization-actions > :deep(.el-dropdown) { display:inline-flex; align-items:center; height:32px; margin-left:0; }
.organization-actions :deep(.el-dropdown .el-button) { display:inline-flex; align-items:center; height:32px; margin:0; }
.dialog-form-with-alert { margin-top:18px; }
@media (max-width:900px) { .group-overview { align-items:flex-start; flex-direction:column; } }
</style>
