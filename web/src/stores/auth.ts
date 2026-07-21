import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { apiClient } from '../api/client'

export interface UserOrganization {
  id: string
  code: string
  name: string
  type: string
}

export interface CurrentUser {
  id: string
  username: string
  display_name: string
  email?: string
  organization?: UserOrganization
  roles: string[]
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

  async function logout() {
    try {
      await apiClient.post('/auth/logout')
    } finally {
      user.value = null
      initialized.value = true
    }
  }

  return { user, initialized, authenticated, isGroupAdmin, fetchCurrentUser, login, logout }
})
