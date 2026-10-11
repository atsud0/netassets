<script setup lang="ts">
import RowActions from '../components/RowActions.vue'
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'
import { dateText, loadOptions } from '../api/remediation'
import RemediationAdministration from '../components/RemediationAdministration.vue'
import { useAuthStore } from '../stores/auth'
const auth = useAuthStore()
import type { Grant, Page, SystemOption, Vendor } from '../api/remediation'
interface Invitation { id: string; intended_username: string; display_name: string; organization_name: string; system_name: string; expires_at: string; accepted_at?: string; revoked_at?: string }
const invitations = ref<Invitation[]>([])
const tab = ref('people')
const people = ref<Grant[]>([])
const vendors = ref<Vendor[]>([])
const systems = ref<SystemOption[]>([])
const total = ref(0)
const page = ref(1)
const loading = ref(false)
const busy = ref(false)
const visible = ref(false)
const vendorVisible = ref(false)
const inviteLink = ref('')
const mode = ref('create')
const form = reactive({ business_system_id: '', organization_id: '', username: '', display_name: '', password: '', participant_type: 'internal', vendor_id: '', valid_until: '' })
const vendor = reactive({ id: '', organization_id: '', name: '', contact: '', status: 'enabled' })
const grantEdit = ref<Grant>()
const editVisible = ref(false)
const grantUntil = ref('')
const grantReason = ref('')
async function load() {
  loading.value = true
  try { const { data } = await apiClient.get<Page<Grant>>('/remediation-grants', { params: { page: page.value } }); people.value = data.items; total.value = data.total; vendors.value = await loadOptions<Vendor>('/vendors'); invitations.value = await loadOptions<Invitation>('/remediation-invitations') } catch (error) { ElMessage.error(getAPIErrorMessage(error)) } finally { loading.value = false }
}
function openPerson() { Object.assign(form, { business_system_id: '', organization_id: '', username: '', display_name: '', password: '', participant_type: 'internal', vendor_id: '', valid_until: new Date(Date.now() + 90 * 86400000).toISOString() }); inviteLink.value = ''; visible.value = true }
function selectSystem(id: string) { form.organization_id = systems.value.find(s => s.id === id)?.organization_id ?? ''; form.vendor_id = '' }
async function savePerson() {
  if (busy.value) return; busy.value = true
  try {
    const { data } = await apiClient.post<{ token?: string }>(mode.value === 'invite' ? '/remediation-invitations' : '/remediation-grants', { ...form, vendor_id: form.participant_type === 'external' ? form.vendor_id : '', valid_until: form.valid_until ? new Date(form.valid_until).toISOString() : null })
    if (data.token) { inviteLink.value = `${window.location.origin}/repair/invite#${data.token}`; form.password = ''; ElMessage.success('邀请已创建，请将链接交给指定人员') } else { visible.value = false; ElMessage.success('个人账号及系统授权已创建') }
    await load()
  } catch (error) { ElMessage.error(getAPIErrorMessage(error)) } finally { busy.value = false }
}
async function revoke(g: Grant) {
  try { const { value } = await ElMessageBox.prompt('撤销后该人员立即失去相应系统任务访问权限，请填写原因', '撤销协作授权', { inputValidator: value => Boolean(value?.trim()) || '请填写原因' }); await apiClient.patch(`/remediation-grants/${g.id}`, { status: 'revoked', reason: value }); await load(); ElMessage.success('授权已撤销') } catch (error) { if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error)) }
}
async function revokeInvitation(item: Invitation) {
  try { const { value } = await ElMessageBox.prompt('填写撤销邀请的原因', '撤销邀请', { inputValidator: value => Boolean(value?.trim()) || '请填写原因' }); await apiClient.post(`/remediation-invitations/${item.id}/revoke`, { reason: value }); await load(); ElMessage.success('邀请已撤销') } catch (error) { if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error)) }
}
function extend(g: Grant) { grantEdit.value = g; grantUntil.value = g.valid_until; grantReason.value = ''; editVisible.value = true }
async function saveGrant() {
  if (!grantEdit.value || busy.value) return; busy.value = true
  try { await apiClient.patch(`/remediation-grants/${grantEdit.value.id}`, { status: 'enabled', valid_until: new Date(grantUntil.value).toISOString(), reason: grantReason.value }); editVisible.value = false; await load(); ElMessage.success('授权已更新') } catch (error) { ElMessage.error(getAPIErrorMessage(error)) } finally { busy.value = false }
}
function openVendor(item?: Vendor) { Object.assign(vendor, item ?? { id: '', organization_id: '', name: '', contact: '', status: 'enabled' }); vendorVisible.value = true }
async function saveVendor() {
  if (busy.value) return; busy.value = true
  try { await apiClient.request({ method: vendor.id ? 'PATCH' : 'POST', url: vendor.id ? `/vendors/${vendor.id}` : '/vendors', data: vendor }); vendorVisible.value = false; await load(); ElMessage.success('厂商已保存') } catch (error) { ElMessage.error(getAPIErrorMessage(error)) } finally { busy.value = false }
}
onMounted(async () => { await load(); try { systems.value = await loadOptions<SystemOption>('/remediation-options') } catch (error) { ElMessage.error(getAPIErrorMessage(error)) } })
</script>
<template>
  <section class="people-page"><div class="page-heading"><div><h1>人员与厂商</h1></div><el-button @click="$router.push('/remediation')">返回修复任务</el-button></div>
    <div class="collaboration-note"><strong>责任落实到具体人员</strong><span>按业务系统授权 · 到期自动失效 · 派单后开放共享材料</span></div><el-card shadow="never"><el-tabs v-model="tab"><el-tab-pane label="人员协作授权" name="people"><div class="toolbar"><el-button type="primary" @click="openPerson">添加人员 / 邀请</el-button><el-button :loading="loading" @click="load">刷新</el-button></div>
      <el-table :data="people" v-loading="loading"><template #empty><el-empty description="尚未配置人员的系统协作授权" :image-size="64"><p class="muted">先添加内部开发或邀请外包人员，再按业务系统授予访问期限。</p><el-button type="primary" @click="openPerson">添加人员 / 邀请</el-button></el-empty></template><el-table-column label="人员" min-width="150"><template #default="{ row }">{{ row.display_name }}<div class="muted">{{ row.username }}</div></template></el-table-column><el-table-column label="组织 / 系统" min-width="210"><template #default="{ row }">{{ row.system_name }}<div class="muted">{{ row.organization_name }}</div></template></el-table-column><el-table-column label="身份" min-width="150"><template #default="{ row }">{{ row.participant_type === 'external' ? row.vendor_name : '内部人员' }}</template></el-table-column><el-table-column label="授权有效期" min-width="180"><template #default="{ row }">{{ dateText(row.valid_until) }}<el-tag :type="row.available && new Date(row.valid_until).getTime()-Date.now()>=7*86400000?'success':'warning'">{{ !row.available?'已失效':new Date(row.valid_until).getTime()-Date.now()<7*86400000?'即将到期':'有效' }}</el-tag></template></el-table-column><el-table-column prop="active_task_count" label="未完成" width="90" /><el-table-column label="操作" width="88"><template #default="{ row }"><RowActions :label="`操作：${row.name || row.display_name || row.id}`"><el-button link type="primary" @click="extend(row)">续期</el-button><el-button v-if="row.status === 'enabled'" link type="danger" @click="revoke(row)">撤销</el-button></RowActions></template></el-table-column></el-table><el-pagination v-model:current-page="page" :total="total" :page-size="20" layout="total, prev, pager, next" @current-change="load" />
    </el-tab-pane><el-tab-pane label="邀请记录" name="invitations"><el-table :data="invitations"><el-table-column prop="display_name" label="受邀人" /><el-table-column prop="intended_username" label="用户名" /><el-table-column prop="organization_name" label="组织" /><el-table-column prop="system_name" label="业务系统" /><el-table-column label="状态"><template #default="{ row }">{{ row.accepted_at ? '已接受' : row.revoked_at ? '已撤销' : new Date(row.expires_at).getTime() <= Date.now() ? '已过期' : '待接受' }}</template></el-table-column><el-table-column label="操作" width="100"><template #default="{ row }"><el-button v-if="!row.accepted_at && !row.revoked_at" link type="danger" @click="revokeInvitation(row)">撤销邀请</el-button></template></el-table-column></el-table></el-tab-pane><el-tab-pane label="合作厂商" name="vendors"><el-button v-if="auth.isGroupAdmin" type="primary" @click="openVendor()">登记厂商</el-button><el-table :data="vendors"><el-table-column prop="name" label="厂商名称" /><el-table-column prop="organization_name" label="合作组织" /><el-table-column prop="contact" label="联系方式" /><el-table-column label="状态"><template #default="{ row }">{{ row.global_status !== 'enabled' ? '集团已停用' : row.status === 'enabled' ? '合作中' : '合作已停用' }}</template></el-table-column><el-table-column label="操作" width="100"><template #default="{ row }"><el-button link @click="openVendor(row)">编辑</el-button></template></el-table-column></el-table></el-tab-pane><el-tab-pane v-if="auth.isGroupAdmin" label="厂商目录与规则" name="administration"><RemediationAdministration @changed="load" /></el-tab-pane></el-tabs></el-card>
    <el-dialog v-model="visible" title="添加修复人员" width="620px" destroy-on-close><el-form label-position="top"><el-form-item label="开通方式"><el-radio-group v-model="mode"><el-radio value="create">创建新账号</el-radio><el-radio value="invite">邀请激活 / 关联已有账号</el-radio></el-radio-group></el-form-item><el-form-item label="授权业务系统"><el-select v-model="form.business_system_id" filterable style="width:100%" @change="selectSystem"><el-option v-for="s in systems" :key="s.id" :value="s.id" :label="`${s.organization_name} / ${s.system_name}`" /></el-select></el-form-item><el-form-item label="用户名"><el-input v-model="form.username" autocomplete="off" placeholder="3–64 位字母、数字、下划线、点或连字符" /></el-form-item><el-form-item label="姓名"><el-input v-model="form.display_name" /></el-form-item><el-form-item v-if="mode === 'create'" label="临时密码（首次登录须修改）"><el-input v-model="form.password" type="password" show-password autocomplete="new-password" placeholder="至少 10 位" /></el-form-item><el-form-item label="人员类型"><el-radio-group v-model="form.participant_type"><el-radio value="internal">内部人员</el-radio><el-radio value="external">外包人员</el-radio></el-radio-group></el-form-item><el-form-item v-if="form.participant_type === 'external'" label="所属厂商"><el-select v-model="form.vendor_id" filterable><el-option v-for="v in vendors.filter(v => v.organization_id === form.organization_id && v.status === 'enabled' && v.global_status === 'enabled')" :key="v.id" :value="v.id" :label="v.name" /></el-select></el-form-item><el-form-item label="授权有效期"><el-date-picker v-model="form.valid_until" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" /></el-form-item></el-form><el-alert v-if="inviteLink" title="邀请有效期最长 48 小时，仅能使用一次；已有账号需本人登录后接受。请复制下方链接交给指定人员。" type="success" :closable="false" /><el-input v-if="inviteLink" :model-value="inviteLink" readonly aria-label="邀请链接" /><template #footer><el-button @click="visible = false">关闭</el-button><el-button v-if="!inviteLink" type="primary" :loading="busy" @click="savePerson">{{ mode === 'invite' ? '创建邀请' : '创建账号与授权' }}</el-button></template></el-dialog>
    <el-dialog v-model="vendorVisible" title="合作厂商" width="520px"><el-form label-position="top"><el-form-item label="合作组织"><el-select v-model="vendor.organization_id" :disabled="Boolean(vendor.id)"><el-option v-for="s in systems.filter((item, index, all) => all.findIndex(v => v.organization_id === item.organization_id) === index)" :key="s.organization_id" :value="s.organization_id" :label="s.organization_name" /></el-select></el-form-item><el-form-item label="厂商名称"><el-input v-model="vendor.name" :disabled="Boolean(vendor.id)" placeholder="集团统一厂商名称，同名自动关联已有档案" /></el-form-item><el-form-item label="联系方式"><el-input v-model="vendor.contact" /></el-form-item><el-form-item label="状态"><el-radio-group v-model="vendor.status"><el-radio value="enabled">启用</el-radio><el-radio value="disabled">停用</el-radio></el-radio-group></el-form-item></el-form><template #footer><el-button type="primary" :loading="busy" @click="saveVendor">保存</el-button></template></el-dialog>
    <el-dialog v-model="editVisible" title="更新协作授权" width="520px"><el-form label-position="top"><el-form-item label="新的有效期"><el-date-picker v-model="grantUntil" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" /></el-form-item><el-form-item label="变更原因"><el-input v-model="grantReason" type="textarea" /></el-form-item></el-form><template #footer><el-button type="primary" :loading="busy" @click="saveGrant">保存授权</el-button></template></el-dialog>
  </section>
</template>
<style scoped>
.people-page { display:grid;gap:18px; }.page-heading,.toolbar { display:flex;align-items:center;justify-content:space-between;gap:12px; }.toolbar { justify-content:flex-start;margin-bottom:16px; }.page-heading h1 { margin:0 0 8px; }.page-heading p,.muted { color:var(--el-text-color-secondary); }.el-pagination { margin-top:18px; }.el-tag { margin-left:6px; }
</style>

<style scoped>
.people-page .page-heading { margin-bottom:0; }.collaboration-note { display:flex;gap:20px;align-items:center;padding:15px 20px;background:#eaf5f2;border:1px solid #d0e7e3;border-radius:8px;color:var(--cyan);font-size:13px; }.collaboration-note strong { font-weight:500; }.collaboration-note span { color:var(--muted); }.toolbar { padding:8px 0 16px; }.people-page :deep(.el-card__body) { padding-top:8px; }@media(max-width:760px) { .collaboration-note { flex-wrap:wrap;gap:8px; }.page-heading { flex-wrap:wrap; } }
</style>
