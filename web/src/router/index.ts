import { createRouter, createWebHistory } from 'vue-router'
import AppLayout from '../layouts/AppLayout.vue'
import LoginView from '../views/LoginView.vue'
import DashboardView from '../views/DashboardView.vue'
import OrganizationsView from '../views/OrganizationsView.vue'
import UsersView from '../views/UsersView.vue'
import BusinessSystemsView from '../views/BusinessSystemsView.vue'
import AssetsView from '../views/AssetsView.vue'
import InventoryTasksView from '../views/InventoryTasksView.vue'
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
        { path: 'organizations', name: 'organizations', component: OrganizationsView },
        { path: 'users', name: 'users', component: UsersView, meta: { groupAdminOnly: true } },
        { path: 'systems', name: 'systems', component: BusinessSystemsView },
        { path: 'assets', name: 'assets', component: AssetsView },
        { path: 'inventory-tasks', name: 'inventory-tasks', component: InventoryTasksView },
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
    return { name: 'dashboard' }
  }
  if (to.meta.groupAdminOnly && !auth.isGroupAdmin) {
    return { name: 'dashboard' }
  }
  return true
})

export default router
