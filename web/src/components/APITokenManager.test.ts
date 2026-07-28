import { shallowMount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import APITokenManager from './APITokenManager.vue'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  delete: vi.fn(),
}))

vi.mock('../api/client', () => ({
  apiClient: mocks,
  getAPIErrorMessage: (_error: unknown, fallback: string) => fallback,
}))

const item = {
  id: '2cf486e9-71c8-4a48-82e4-17e3ab76b53d',
  name: '资产同步脚本',
  token_prefix: 'nat_abcdefgh',
  organization_id: '507921d8-6d9e-4e22-8517-684f269c817b',
  organization_name: '集团总部',
  expires_at: '2026-10-26T00:00:00Z',
  created_at: '2026-07-28T00:00:00Z',
}

function mountComponent() {
  return shallowMount(APITokenManager, {
    global: {
      stubs: {
        ElButton: true,
        ElTable: true,
        ElTableColumn: true,
        ElTag: true,
        ElPagination: true,
        ElDialog: true,
        ElForm: true,
        ElFormItem: true,
        ElInput: true,
        ElSelect: true,
        ElOption: true,
        ElAlert: true,
      },
      directives: { loading: () => undefined },
    },
  })
}

describe('APITokenManager', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.get.mockResolvedValue({ data: { items: [item], total: 1, page: 1, page_size: 20 } })
  })

  it('loads only the current user token metadata with bounded pagination', async () => {
    mountComponent()
    await flushPromises()

    expect(mocks.get).toHaveBeenCalledWith('/auth/api-tokens', {
      params: { page: 1, page_size: 20 },
    })
  })

  it('shows the newly created secret once and reloads metadata', async () => {
    mocks.post.mockResolvedValue({ data: { item, token: 'nat_full_secret_value' } })
    const wrapper = mountComponent()
    await flushPromises()
    const vm = wrapper.vm as unknown as {
      createForm: { name: string; expires_in_days: number }
      createToken: () => Promise<void>
      createdToken: string
      secretDialogVisible: boolean
    }
    vm.createForm.name = ' 资产同步脚本 '
    vm.createForm.expires_in_days = 90

    await vm.createToken()

    expect(mocks.post).toHaveBeenCalledWith('/auth/api-tokens', { name: '资产同步脚本', expires_in_days: 90 })
    expect(vm.createdToken).toBe('nat_full_secret_value')
    expect(vm.secretDialogVisible).toBe(true)
    expect(mocks.get).toHaveBeenCalledTimes(2)
  })
})
