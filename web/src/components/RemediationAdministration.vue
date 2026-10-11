<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'
import { dateText, loadOptions } from '../api/remediation'
interface Catalog { id: string; name: string; status: string; organization_count: number }
interface Membership { vendor_id: string; user_id: string; vendor_name: string; display_name: string; status: string; valid_until: string }
const emit = defineEmits<{ changed: [] }>()
const catalog = ref<Catalog[]>([])
const memberships = ref<Membership[]>([])
const loading = ref(false)
const saving = ref(false)
const catalogVisible = ref(false)
const membershipVisible = ref(false)
const edit = reactive({ id: '', name: '', status: 'enabled', reason: '' })
const member = reactive({ vendor_id: '', user_id: '', display_name: '', vendor_name: '', status: 'enabled', valid_until: '', reason: '' })
const reminderDays = ref(1)
async function load() {
 loading.value = true
 try {
  const [c,m,settings] = await Promise.all([loadOptions<Catalog>('/vendor-catalog'), loadOptions<Membership>('/vendor-memberships'), apiClient.get<{overdue_reminder_days: number}>('/remediation-settings')])
  catalog.value=c; memberships.value=m; reminderDays.value=settings.data.overdue_reminder_days
 } catch(e) { ElMessage.error(getAPIErrorMessage(e)) } finally { loading.value=false }
}
function openCatalog(row?: Catalog) { Object.assign(edit, row ?? {id:'',name:'',status:'enabled'}, {reason:''});catalogVisible.value=true }
function openMember(row: Membership) { Object.assign(member,row,{reason:''});membershipVisible.value=true }
async function saveCatalog() {
 if(saving.value)return;saving.value=true
 try { await apiClient.request({method:edit.id?'PATCH':'POST',url:edit.id?`/vendor-catalog/${edit.id}`:'/vendor-catalog',data:edit});catalogVisible.value=false;await load();emit('changed');ElMessage.success('集团厂商档案已更新') } catch(e){ElMessage.error(getAPIErrorMessage(e))}finally{saving.value=false}
}
async function saveMember() {
 if(saving.value)return;saving.value=true
 try {await apiClient.patch(`/vendor-memberships/${member.vendor_id}/${member.user_id}`,{...member,valid_until:new Date(member.valid_until).toISOString()});membershipVisible.value=false;await load();emit('changed');ElMessage.success('厂商成员关系已更新')}catch(e){ElMessage.error(getAPIErrorMessage(e))}finally{saving.value=false}
}
async function saveSettings() {
 try { const {value}=await ElMessageBox.prompt('填写提醒频率调整原因','保存提醒规则',{inputValidator:v=>Boolean(v?.trim())||'请填写原因'});await apiClient.patch('/remediation-settings',{overdue_reminder_days:reminderDays.value,reason:value});ElMessage.success('提醒规则已保存') }catch(e){if(e!=='cancel'&&e!=='close')ElMessage.error(getAPIErrorMessage(e))}
}
onMounted(load)
</script>
<template>
 <el-tabs v-loading="loading">
  <el-tab-pane label="集团统一厂商档案"><el-alert title="集团统一维护厂商身份；合作组织、联系方式与合作状态分别在“合作厂商”中管理。停用集团厂商将使该厂商的执行授权立即失效。" type="info" :closable="false" /><el-button type="primary" @click="openCatalog()">新增集团厂商</el-button><el-table :data="catalog" empty-text="尚无集团厂商"><el-table-column prop="name" label="厂商名称" /><el-table-column prop="organization_count" label="合作组织数" /><el-table-column label="状态"><template #default="{row}">{{row.status==='enabled'?'启用':'停用'}}</template></el-table-column><el-table-column label="操作" width="100"><template #default="{row}"><el-button link @click="openCatalog(row)">编辑档案</el-button></template></el-table-column></el-table></el-tab-pane>
  <el-tab-pane label="厂商成员关系"><p>开通外包人员协作授权时建立成员关系；集团停用成员后，各组织的授权均不能继续使用。</p><el-table :data="memberships" empty-text="尚无厂商成员"><el-table-column prop="vendor_name" label="集团厂商" /><el-table-column prop="display_name" label="成员" /><el-table-column label="有效期"><template #default="{row}">{{dateText(row.valid_until)}}</template></el-table-column><el-table-column label="状态"><template #default="{row}">{{row.status==='enabled'?'启用':'停用'}}</template></el-table-column><el-table-column label="操作" width="100"><template #default="{row}"><el-button link @click="openMember(row)">管理成员</el-button></template></el-table-column></el-table></el-tab-pane>
  <el-tab-pane label="提醒规则"><el-form label-position="top"><el-form-item label="逾期提醒间隔（天）"><el-input-number v-model="reminderDays" :min="1" :max="30" /></el-form-item><p>临期前三天提醒一次；逾期后按此间隔生成新提醒，同一节点去重。消息始终按当前授权过滤。</p><el-button type="primary" @click="saveSettings">保存提醒规则</el-button></el-form></el-tab-pane>
 </el-tabs>
 <el-dialog v-model="catalogVisible" title="集团厂商档案" width="520px" append-to-body><el-form label-position="top"><el-form-item label="统一厂商名称"><el-input v-model="edit.name" /></el-form-item><el-form-item label="全局状态"><el-radio-group v-model="edit.status"><el-radio value="enabled">启用</el-radio><el-radio value="disabled">停用</el-radio></el-radio-group></el-form-item><el-form-item label="变更原因"><el-input v-model="edit.reason" type="textarea" /></el-form-item></el-form><template #footer><el-button type="primary" :loading="saving" @click="saveCatalog">保存集团档案</el-button></template></el-dialog>
 <el-dialog v-model="membershipVisible" title="厂商成员关系" width="520px" append-to-body><p>{{member.vendor_name}} · {{member.display_name}}</p><el-form label-position="top"><el-form-item label="成员状态"><el-radio-group v-model="member.status"><el-radio value="enabled">启用</el-radio><el-radio value="disabled">停用</el-radio></el-radio-group></el-form-item><el-form-item label="成员关系有效期"><el-date-picker v-model="member.valid_until" type="datetime" value-format="YYYY-MM-DDTHH:mm:ssZ" /></el-form-item><el-form-item label="变更原因"><el-input v-model="member.reason" type="textarea" /></el-form-item></el-form><template #footer><el-button type="primary" :loading="saving" @click="saveMember">保存成员关系</el-button></template></el-dialog>
</template>
<style scoped>.el-alert,.el-button{margin-bottom:12px}</style>
