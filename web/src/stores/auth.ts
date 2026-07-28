import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { apiClient } from '../api/client'

export interface UserOrganization {
  id: string
  code: string
  name: string
  type: string
  is_primary?: boolean
}

export interface CurrentUser {
  id: string
  username: string
  display_name: string
  email?: string
  mobile_phone?: string
  office_phone?: string
  must_change_password: boolean
  organization?: UserOrganization
  organizations: UserOrganization[]
  roles: string[]
  permissions: string[]
}

interface AuthResponse {
  user: CurrentUser
  expires_at?: string
}

export const useAuthStore = defineStore('auth', () => {
  const user = ref<CurrentUser | null>(null)
  const initialized = ref(false)
  const authenticated = computed(() => user.value !== null)
  const isGroupAdmin = computed(() => user.value?.roles.includes('group_admin') ?? false)
  const isSecurityOperator = computed(() => user.value?.roles.includes('security_operator') ?? false)
  const hasGroupSecurityRole = computed(() => isGroupAdmin.value || isSecurityOperator.value)
  const hasPermission = (permission: string) => user.value?.permissions.includes(permission) ?? false

  const canManageSecurityTests = computed(() => hasGroupSecurityRole.value && hasPermission('security_test.manage'))
  const canManageSecurityFindings = computed(() => hasGroupSecurityRole.value && hasPermission('security_finding.manage'))
  const canManageSecurityWorkOrders = computed(() => hasGroupSecurityRole.value && hasPermission('security_workorder.manage'))

  function setCurrentUser(currentUser: CurrentUser) {
    user.value = currentUser
  }

  async function fetchCurrentUser() {
    try {
      const response = await apiClient.get<AuthResponse>('/auth/me')
      user.value = response.data.user
      return user.value
    } catch {
      user.value = null
      return null
    } finally {
      initialized.value = true
    }
  }

  async function login(username: string, password: string) {
    const response = await apiClient.post<AuthResponse>('/auth/login', { username, password })
    user.value = response.data.user
    initialized.value = true
    return user.value
  }

  async function switchOrganization(organizationID: string) {
    const response = await apiClient.put<AuthResponse>('/auth/current-organization', { organization_id: organizationID })
    user.value = response.data.user
    return user.value
  }

  async function logout() {
    try {
      await apiClient.post('/auth/logout')
    } finally {
      user.value = null
      initialized.value = true
    }
  }

  return { user, initialized, authenticated, isGroupAdmin, isSecurityOperator, hasGroupSecurityRole, canManageSecurityTests, canManageSecurityFindings, canManageSecurityWorkOrders, hasPermission, setCurrentUser, fetchCurrentUser, login, switchOrganization, logout }
})
