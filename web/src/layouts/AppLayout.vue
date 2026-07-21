<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const collapsed = ref(false)
const loggingOut = ref(false)

const activePath = computed(() => route.path)
const organizationName = computed(() => auth.user?.organization?.name ?? '集团资产管理')
const perspective = computed(() => auth.isGroupAdmin ? '集团视角' : '所属组织视角')
const avatarText = computed(() => (auth.user?.display_name || auth.user?.username || '用').slice(0, 1))
const menuItems = computed(() => [
  { path: '/dashboard', label: '集团工作台' },
  { path: '/organizations', label: '组织机构' },
  ...(auth.isGroupAdmin ? [{ path: '/users', label: '用户与权限' }] : []),
  { path: '/systems', label: '业务系统' },
  { path: '/assets', label: '网络资产' },
  { path: '/inventory-tasks', label: '填报任务' },
])

function navigate(path: string) {
  void router.push(path)
}

async function logout() {
  loggingOut.value = true
  try {
    await auth.logout()
    await router.replace('/login')
    ElMessage.success('已退出登录')
  } finally {
    loggingOut.value = false
  }
}
</script>

<template>
  <div class="app-shell">
    <aside class="sidebar" :class="{ collapsed }">
      <div class="brand">
        <div class="brand-mark">NA</div>
        <div v-if="!collapsed" class="brand-copy">
          <strong>NetAssets</strong>
          <span>集团资产管理</span>
        </div>
      </div>
      <nav class="nav-list">
        <button
          v-for="item in menuItems"
          :key="item.path"
          type="button"
          class="nav-item"
          :class="{ active: activePath === item.path }"
          @click="navigate(item.path)"
        >
          <span class="nav-dot" />
          <span v-if="!collapsed">{{ item.label }}</span>
        </button>
      </nav>
      <button class="collapse-button" type="button" @click="collapsed = !collapsed">
        {{ collapsed ? '展开' : '收起导航' }}
      </button>
    </aside>

    <div class="main-shell">
      <header class="topbar">
        <div>
          <strong>{{ organizationName }}</strong>
          <span class="topbar-subtitle">{{ perspective }}</span>
        </div>
        <div class="topbar-actions">
          <span class="status-pill">{{ auth.isGroupAdmin ? '集团管理员' : '组织用户' }}</span>
          <div class="user-summary">
            <span class="avatar">{{ avatarText }}</span>
            <span>{{ auth.user?.display_name }}</span>
          </div>
          <el-button text :loading="loggingOut" @click="logout">退出</el-button>
        </div>
      </header>
      <main class="page-content">
        <RouterView />
      </main>
    </div>
  </div>
</template>
