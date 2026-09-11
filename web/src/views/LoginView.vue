<script setup lang="ts">
import { reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useRoute, useRouter } from 'vue-router'
import { getAPIErrorMessage } from '../api/client'
import { useAuthStore } from '../stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const loading = ref(false)
const form = reactive({ username: '', password: '' })

async function submit() {
  if (loading.value) {
    return
  }
  if (!form.username.trim() || !form.password) {
    ElMessage.warning('请输入用户名和密码')
    return
  }
  loading.value = true
  try {
    await auth.login(form.username.trim(), form.password)
    const redirect = typeof route.query.redirect === 'string' && route.query.redirect.startsWith('/')
      ? route.query.redirect
      : '/dashboard'
    await router.replace(redirect)
    ElMessage.success('登录成功')
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '用户名或密码错误'))
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="login-page">
    <section class="login-intro">
      <span class="eyebrow">GROUP ASSET GOVERNANCE</span>
      <h1>让集团网络资产<br />统一、清晰、可追溯</h1>
      <p>集团制定标准，子公司维护数据，系统完成校验、审核与汇总。</p>
      <div class="intro-grid">
        <div><strong>组织</strong><span>集团与多级子公司</span></div>
        <div><strong>系统</strong><span>业务系统统一登记</span></div>
        <div><strong>资产</strong><span>域名、IP、URL与服务</span></div>
      </div>
    </section>
    <section class="login-card">
      <div class="login-card-header">
        <div class="brand-mark large">NA</div>
        <div><h2>登录 NetAssets</h2><p>使用集团内部账号进入系统</p></div>
      </div>
      <el-form label-position="top" @submit.prevent="submit">
        <el-form-item label="用户名">
          <el-input v-model="form.username" name="username" size="large" autocomplete="username" placeholder="请输入用户名" />
        </el-form-item>
        <el-form-item label="密码">
          <el-input v-model="form.password" name="password" size="large" type="password" show-password autocomplete="current-password" placeholder="请输入密码" />
        </el-form-item>
        <el-button type="primary" native-type="submit" size="large" :loading="loading" class="full-button">登录</el-button>
      </el-form>
      <p class="login-note">登录后按集团或所属子公司的数据范围进行管理。</p>
    </section>
  </div>
</template>
