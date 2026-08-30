import { createRouter, createWebHistory } from 'vue-router'
const AppLayout = () => import('../layouts/AppLayout.vue')
const LoginView = () => import('../views/LoginView.vue')
const DashboardView = () => import('../views/DashboardView.vue')
const OrganizationsView = () => import('../views/OrganizationsView.vue')
const UsersView = () => import('../views/UsersView.vue')
const ProfileView = () => import('../views/ProfileView.vue')
const BusinessSystemsView = () => import('../views/BusinessSystemsView.vue')
const AssetsView = () => import('../views/AssetsView.vue')
const AssetCandidatesView = () => import('../views/AssetCandidatesView.vue')
const DataGovernanceView = () => import('../views/DataGovernanceView.vue')
const DataQualityIssuesView = () => import('../views/DataQualityIssuesView.vue')
const DataQualityScanHistoryView = () => import('../views/DataQualityScanHistoryView.vue')
const InventoryTasksView = () => import('../views/InventoryTasksView.vue')
const ExportJobsView = () => import('../views/ExportJobsView.vue')
const AuditLogsView = () => import('../views/AuditLogsView.vue')
const SecurityWorkOrdersView = () => import('../views/SecurityWorkOrdersView.vue')
const SecurityTestBatchesView = () => import('../views/SecurityTestBatchesView.vue')
const NotificationsView = () => import('../views/NotificationsView.vue')
const RecycleBinView = () => import('../views/RecycleBinView.vue')
const BackupsView = () => import('../views/BackupsView.vue')
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
        { path: 'organizations', name: 'organizations', component: OrganizationsView, meta: { permission: 'organization.read' } },
        { path: 'users', name: 'users', component: UsersView, meta: { permission: 'user.manage' } },
        { path: 'profile', name: 'profile', component: ProfileView },
        { path: 'systems', name: 'systems', component: BusinessSystemsView, meta: { permission: 'system.manage' } },
        { path: 'assets', name: 'assets', component: AssetsView, meta: { permission: 'asset.manage' } },
        { path: 'asset-candidates', name: 'asset-candidates', component: AssetCandidatesView, meta: { groupAdminOnly: true } },
        { path: 'data-governance', name: 'data-governance', component: DataGovernanceView, meta: { groupAdminOnly: true } },
        { path: 'data-quality-issues', name: 'data-quality-issues', component: DataQualityIssuesView, meta: { permission: 'data_quality.manage' } },
        { path: 'data-quality-scan-history', name: 'data-quality-scan-history', component: DataQualityScanHistoryView, meta: { permission: 'data_quality.manage', groupAdminOnly: true } },
        { path: 'inventory-tasks', name: 'inventory-tasks', component: InventoryTasksView, meta: { permission: 'inventory.manage' } },
        { path: 'export-jobs', name: 'export-jobs', component: ExportJobsView },
        { path: 'security-test-batches', name: 'security-test-batches', component: SecurityTestBatchesView, meta: { permission: 'security_test.manage', groupSecurityOnly: true } },
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
  if (to.meta.groupSecurityOnly && !auth.hasGroupSecurityRole) {
    return { name: 'dashboard' }
  }
  if (to.meta.permission && !auth.hasPermission(String(to.meta.permission))) {
    return { name: 'dashboard' }
  }
  return true
})

export default router
