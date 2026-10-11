<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'
import { useAuthStore } from '../stores/auth'
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const password = ref('')
const busy = ref(false)
const accepted = ref(false)
const inviteToken = ref(route.hash.slice(1))
async function accept() {
  if (busy.value) return; busy.value = true
  try {
    await apiClient.post(auth.authenticated ? '/repair-invitations/accept-existing' : '/repair-invitations/accept', { token: inviteToken.value, password: password.value })
    accepted.value = true; password.value = ''; inviteToken.value = ''; await router.replace('/repair/invite'); ElMessage.success('协作授权已激活')
    if (auth.authenticated) { await auth.fetchCurrentUser(); await router.replace('/repair/tasks') }
  } catch (error) { ElMessage.error(getAPIErrorMessage(error)) } finally { busy.value = false }
}
function login() { void router.push({ path: '/login', query: { redirect: `/repair/invite#${inviteToken.value}` } }) }
</script>
<template><main class="invitation"><el-card><h1>加入修复工作台</h1><template v-if="!accepted"><p>邀请仅向指定人员提供业务系统协作授权，任务派发后才能查看相应漏洞。</p><p v-if="auth.authenticated">当前登录：{{ auth.user?.display_name }}（{{ auth.user?.username }}）</p><template v-else><p>首次开通请设置至少 10 位的登录密码；已有账号请先登录再接受邀请。</p><el-input v-model="password" type="password" show-password autocomplete="new-password" placeholder="设置登录密码" aria-label="设置登录密码" /></template><div class="actions"><el-button type="primary" :loading="busy" :disabled="!inviteToken" @click="accept">接受邀请</el-button><el-button v-if="!auth.authenticated" @click="login">已有账号，先登录</el-button></div><el-alert v-if="!inviteToken" title="邀请链接缺少有效凭证，请使用管理员提供的完整链接。" type="warning" :closable="false" /></template><template v-else><p>授权已激活，请使用管理员为你指定的用户名和刚设置的密码登录。</p><el-button type="primary" @click="router.push('/login')">前往登录</el-button></template></el-card></main></template>
<style scoped>.invitation{max-width:580px;margin:10vh auto;padding:24px;line-height:1.8}.actions{display:flex;gap:12px;margin-top:20px}</style>
