<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Bell } from '@element-plus/icons-vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { loadNotificationSummary } from '../api/notifications'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const collapsed = ref(false)
const loggingOut = ref(false)
const unreadNotificationCount = ref(0)
let notificationTimer: number | undefined

const activePath = computed(() => route.path)
const organizationName = computed(() => auth.user?.organization?.name ?? '集团资产管理')
const activeOrganizationID = computed({ get: () => auth.user?.organization?.id ?? '', set: (value: string) => { if (value) void switchOrganization(value) } })
const availableOrganizations = computed(() => auth.user?.organizations ?? [])
const switchingOrganization = ref(false)
const perspective = computed(() => auth.isGroupAdmin ? '集团管理视角' : auth.isSecurityOperator ? '集团安全运营视角' : '所属组织视角')
const roleLabel = computed(() => auth.isGroupAdmin ? '集团管理员' : auth.isSecurityOperator ? '集团安全运营' : '组织用户')
const avatarText = computed(() => (auth.user?.display_name || auth.user?.username || '用').slice(0, 1))
type NavigationItem = { path: string; label: string }
type NavigationGroup = { key: string; label: string; shortLabel: string; items: NavigationItem[] }

const primaryItems = computed<NavigationItem[]>(() => [
  { path: '/dashboard', label: '集团工作台' },
])
const navigationGroups = computed<NavigationGroup[]>(() => {
  const assetGovernance: NavigationItem[] = [
    ...(auth.hasPermission('system.manage') ? [{ path: '/systems', label: '业务系统' }] : []),
    ...(auth.hasPermission('asset.manage') ? [{ path: '/assets', label: '网络资产' }] : []),
    ...(auth.isGroupAdmin ? [{ path: '/asset-candidates', label: '待确认资产' }] : []),
    ...(auth.hasPermission('inventory.manage') ? [{ path: '/inventory-tasks', label: '填报任务' }] : []),
    ...(auth.isGroupAdmin ? [{ path: '/data-governance', label: '数据治理' }] : []),
    ...(auth.hasPermission('data_quality.manage') ? [{ path: '/data-quality-issues', label: '质量整改' }] : []),
  ]
  const securityOperations: NavigationItem[] = [
    ...(auth.canManageSecurityTests ? [{ path: '/security-test-batches', label: '安全测试' }] : []),
    ...(auth.canManageSecurityFindings || auth.canManageSecurityWorkOrders || !auth.hasGroupSecurityRole ? [{ path: '/security-work-orders', label: '安全工单' }] : []),
  ]
  const systemSettings: NavigationItem[] = [
    ...(auth.hasPermission('organization.read') ? [{ path: '/organizations', label: '组织架构' }] : []),
    ...(auth.hasPermission('user.manage') ? [{ path: '/users', label: auth.isGroupAdmin ? '用户和权限' : '用户管理' }] : []),
    ...(auth.isGroupAdmin ? [{ path: '/recycle-bin', label: '回收站' }, { path: '/audit-logs', label: '审计日志' }, { path: '/backups', label: '备份管理' }] : []),
  ]
  return [
    { key: 'asset-governance', label: '资产治理', shortLabel: '资', items: assetGovernance },
    { key: 'security-operations', label: '安全运营', shortLabel: '安', items: securityOperations },
    { key: 'system-settings', label: '系统设置', shortLabel: '设', items: systemSettings },
  ].filter((group) => group.items.length > 0)
})
const expandedGroups = ref<Record<string, boolean>>({
  'asset-governance': true,
  'security-operations': true,
  'system-settings': true,
})

async function refreshNotificationSummary() {
  try {
    const summary = await loadNotificationSummary()
    unreadNotificationCount.value = summary.unread_count
  } catch {
    // 消息中心不可用时不影响主业务导航。
  }
}

onMounted(() => {
  void refreshNotificationSummary()
  notificationTimer = window.setInterval(() => { void refreshNotificationSummary() }, 30_000)
})

onBeforeUnmount(() => {
  if (notificationTimer !== undefined) window.clearInterval(notificationTimer)
})

function navigate(path: string) {
  void router.push(path)
}

function isGroupActive(group: NavigationGroup) {
  return group.items.some((item) => item.path === activePath.value)
}

function isGroupExpanded(group: NavigationGroup) {
  return expandedGroups.value[group.key] || isGroupActive(group)
}

function toggleGroup(group: NavigationGroup) {
  if (collapsed.value) {
    collapsed.value = false
    expandedGroups.value[group.key] = true
    return
  }
  expandedGroups.value[group.key] = !isGroupExpanded(group)
}

async function switchOrganization(organizationID: string) {
  if (!organizationID || organizationID === auth.user?.organization?.id) return
  switchingOrganization.value = true
  try {
    await auth.switchOrganization(organizationID)
    ElMessage.success('已切换当前工作组织')
    await router.replace({ path: '/dashboard', query: { organization_changed: String(Date.now()) } })
  } catch {
    ElMessage.error('切换工作组织失败')
  } finally {
    switchingOrganization.value = false
  }
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
      <nav class="nav-list" aria-label="主导航">
        <button
          v-for="item in primaryItems"
          :key="item.path"
          type="button"
          class="nav-item nav-primary-item"
          :class="{ active: activePath === item.path }"
          :aria-label="item.label"
          @click="navigate(item.path)"
        >
          <span class="nav-dot" />
          <span v-if="!collapsed">{{ item.label }}</span>
        </button>
        <section v-for="group in navigationGroups" :key="group.key" class="nav-group" :class="{ active: isGroupActive(group) }">
          <button
            type="button"
            class="nav-group-toggle"
            :class="{ active: isGroupActive(group) }"
            :aria-expanded="!collapsed && isGroupExpanded(group)"
            :aria-label="group.label"
            @click="toggleGroup(group)"
          >
            <span class="nav-group-mark">{{ collapsed ? group.shortLabel : '•' }}</span>
            <span v-if="!collapsed" class="nav-group-label">{{ group.label }}</span>
            <span v-if="!collapsed" class="nav-group-chevron" :class="{ expanded: isGroupExpanded(group) }">⌄</span>
          </button>
          <div v-show="!collapsed && isGroupExpanded(group)" class="nav-group-items">
            <button
              v-for="item in group.items"
              :key="item.path"
              type="button"
              class="nav-item nav-child-item"
              :class="{ active: activePath === item.path }"
              @click="navigate(item.path)"
            >
              <span class="nav-dot" />
              <span>{{ item.label }}</span>
            </button>
          </div>
        </section>
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
          <el-button class="notification-topbar-button" text aria-label="消息中心" @click="navigate('/notifications')">
            <el-badge class="notification-topbar-badge" :value="unreadNotificationCount" :max="99" :hidden="unreadNotificationCount === 0">
              <el-icon class="notification-topbar-icon"><Bell /></el-icon>
            </el-badge>
            <span class="notification-topbar-label">消息中心</span>
          </el-button>
          <el-select v-if="availableOrganizations.length > 1" v-model="activeOrganizationID" size="small" :loading="switchingOrganization" class="work-organization-switcher" aria-label="当前工作组织">
            <el-option v-for="organization in availableOrganizations" :key="organization.id" :value="organization.id" :label="`${organization.name}（${organization.code}）`" />
          </el-select>
          <span class="status-pill">{{ roleLabel }}</span>
          <div class="user-summary">
            <span class="avatar">{{ avatarText }}</span>
            <span>{{ auth.user?.display_name }}</span>
          </div>
          <el-button text @click="navigate('/profile')">账户设置</el-button>
          <el-button text :loading="loggingOut" @click="logout">退出</el-button>
        </div>
      </header>
      <main class="page-content">
        <RouterView />
      </main>
    </div>
  </div>
</template>
