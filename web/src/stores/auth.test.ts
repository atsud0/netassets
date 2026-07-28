import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useAuthStore, type CurrentUser } from './auth'

function currentUser(roles: string[], permissions: string[]): CurrentUser {
  return {
    id: '00000000-0000-0000-0000-000000000001',
    username: 'security_user',
    display_name: '安全运营测试用户',
    must_change_password: false,
    organizations: [],
    roles,
    permissions,
  }
}

describe('group security permission boundaries', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('allows the built-in security operator according to independent permissions', () => {
    const auth = useAuthStore()
    auth.setCurrentUser(currentUser(
      ['security_operator'],
      ['security_test.manage', 'security_finding.manage'],
    ))

    expect(auth.hasGroupSecurityRole).toBe(true)
    expect(auth.canManageSecurityTests).toBe(true)
    expect(auth.canManageSecurityFindings).toBe(true)
    expect(auth.canManageSecurityWorkOrders).toBe(false)
  })

  it('does not promote an organization role through same-named permissions', () => {
    const auth = useAuthStore()
    auth.setCurrentUser(currentUser(
      ['organization_security'],
      ['security_test.manage', 'security_finding.manage', 'security_workorder.manage'],
    ))

    expect(auth.hasGroupSecurityRole).toBe(false)
    expect(auth.canManageSecurityTests).toBe(false)
    expect(auth.canManageSecurityFindings).toBe(false)
    expect(auth.canManageSecurityWorkOrders).toBe(false)
  })
})
