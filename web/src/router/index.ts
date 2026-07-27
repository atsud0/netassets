import { createRouter, createWebHistory } from 'vue-router'
import AppLayout from '../layouts/AppLayout.vue'
import LoginView from '../views/LoginView.vue'
import DashboardView from '../views/DashboardView.vue'
import OrganizationsView from '../views/OrganizationsView.vue'
import UsersView from '../views/UsersView.vue'
import ProfileView from '../views/ProfileView.vue'
import BusinessSystemsView from '../views/BusinessSystemsView.vue'
import AssetsView from '../views/AssetsView.vue'
import AssetCandidatesView from '../views/AssetCandidatesView.vue'
import DataGovernanceView from '../views/DataGovernanceView.vue'
import DataQualityIssuesView from '../views/DataQualityIssuesView.vue'
import InventoryTasksView from '../views/InventoryTasksView.vue'
import AuditLogsView from '../views/AuditLogsView.vue'
import SecurityWorkOrdersView from '../views/SecurityWorkOrdersView.vue'
import SecurityTestBatchesView from '../views/SecurityTestBatchesView.vue'
import NotificationsView from '../views/NotificationsView.vue'
import RecycleBinView from '../views/RecycleBinView.vue'
import BackupsView from '../views/BackupsView.vue'
import { pinia } from '../stores'
import { useAuthStore } from '../stores/auth'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: LoginView, meta: { public: true } },
    {
      path: '/',
      component: AppLayout,
      children: [
        { path: '', redirect: '/dashboard' },
        { path: 'dashboard', name: 'dashboard', component: DashboardView },
        { path: 'notifications', name: 'notifications', component: NotificationsView },
        { path: 'organizations', name: 'organizations', component: OrganizationsView },
        { path: 'users', name: 'users', component: UsersView, meta: { permission: 'user.manage' } },
        { path: 'profile', name: 'profile', component: ProfileView },
        { path: 'systems', name: 'systems', component: BusinessSystemsView },
        { path: 'assets', name: 'assets', component: AssetsView },
        { path: 'asset-candidates', name: 'asset-candidates', component: AssetCandidatesView, meta: { groupAdminOnly: true } },
        { path: 'data-governance', name: 'data-governance', component: DataGovernanceView, meta: { groupAdminOnly: true } },
        { path: 'data-quality-issues', name: 'data-quality-issues', component: DataQualityIssuesView, meta: { permission: 'data_quality.manage' } },
        { path: 'inventory-tasks', name: 'inventory-tasks', component: InventoryTasksView },
        { path: 'security-test-batches', name: 'security-test-batches', component: SecurityTestBatchesView, meta: { groupAdminOnly: true } },
        { path: 'security-work-orders', name: 'security-work-orders', component: SecurityWorkOrdersView },
        { path: 'audit-logs', name: 'audit-logs', component: AuditLogsView, meta: { groupAdminOnly: true } },
        { path: 'recycle-bin', name: 'recycle-bin', component: RecycleBinView, meta: { groupAdminOnly: true } },
        { path: 'backups', name: 'backups', component: BackupsView, meta: { groupAdminOnly: true } },
      ],
    },
  ],
})

router.beforeEach(async (to) => {
  const auth = useAuthStore(pinia)
  if (!auth.initialized) {
    await auth.fetchCurrentUser()
  }
  if (!to.meta.public && !auth.authenticated) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  if (to.name === 'login' && auth.authenticated) {
    return { name: auth.user?.must_change_password ? 'profile' : 'dashboard' }
  }
  if (auth.authenticated && auth.user?.must_change_password && to.name !== 'profile') {
    return { name: 'profile', query: { password_change_required: '1' } }
  }
  if (to.meta.groupAdminOnly && !auth.isGroupAdmin) {
    return { name: 'dashboard' }
  }
  if (to.meta.permission && !auth.hasPermission(String(to.meta.permission))) {
    return { name: 'dashboard' }
  }
  return true
})

export default router
