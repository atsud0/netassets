import { flushPromises, shallowMount } from '@vue/test-utils'
import { ElMessage } from 'element-plus'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ResourceSubmissionHistoryDrawer from './ResourceSubmissionHistoryDrawer.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn() }))

vi.mock('../api/client', () => ({
  apiClient: mocks,
  getAPIErrorMessage: (_error: unknown, fallback: string) => fallback,
}))

const response = {
  items: [{
    submission_id: 'submission-1', task_id: 'task-1', task_name: '公网资产专项核查', organization_id: 'org-1',
    version: 2, status: 'returned', operation: 'update', submitted_at: '2026-08-14T08:00:00Z',
    submitted_by_name: '子公司管理员', return_reason: '负责人信息不完整', resource_key: 'domain:example.com',
    resource_name: '示例资产', changes: [{ field: 'asset_name', label: '资产名称', before: '旧名称', after: '新名称' }],
  }],
  total: 1,
  page: 1,
  page_size: 20,
}

function mountComponent(resourceType: 'network_asset' | 'business_system' = 'network_asset') {
  return shallowMount(ResourceSubmissionHistoryDrawer, {
    props: { resourceType },
    global: {
      stubs: {
        ElDrawer: true,
        ElAlert: true,
        ElTag: true,
        ElEmpty: true,
        ElPagination: true,
      },
      directives: { loading: () => undefined },
    },
  })
}

describe('ResourceSubmissionHistoryDrawer', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.get.mockResolvedValue({ data: response })
  })

  it('loads the first page of network asset submission history', async () => {
    const wrapper = mountComponent()
    await (wrapper.vm as unknown as { open: (id: string) => Promise<void> }).open('asset-1')

    expect(mocks.get).toHaveBeenCalledWith('/inventory-tasks/resource-submissions/network_asset/asset-1', {
      params: { page: 1, page_size: 20 },
    })
  })

  it('uses the business system resource path', async () => {
    const wrapper = mountComponent('business_system')
    await (wrapper.vm as unknown as { open: (id: string) => Promise<void> }).open('system-1')

    expect(mocks.get).toHaveBeenCalledWith('/inventory-tasks/resource-submissions/business_system/system-1', {
      params: { page: 1, page_size: 20 },
    })
  })

  it('reloads with real pagination parameters', async () => {
    const wrapper = mountComponent()
    await (wrapper.vm as unknown as { open: (id: string) => Promise<void> }).open('asset-1')
    ;(wrapper.vm as unknown as { handlePageChange: (page: number) => void }).handlePageChange(2)
    await flushPromises()

    expect(mocks.get).toHaveBeenLastCalledWith('/inventory-tasks/resource-submissions/network_asset/asset-1', {
      params: { page: 2, page_size: 20 },
    })
  })

  it('reports a readable API error', async () => {
    mocks.get.mockRejectedValue(new Error('failed'))
    const message = vi.spyOn(ElMessage, 'error').mockImplementation(() => ({ close: () => undefined }))
    const wrapper = mountComponent()

    await (wrapper.vm as unknown as { open: (id: string) => Promise<void> }).open('asset-1')

    expect(message).toHaveBeenCalledWith('读取资源上报历史失败')
    message.mockRestore()
  })
})
