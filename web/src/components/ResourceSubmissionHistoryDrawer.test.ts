import { flushPromises, shallowMount } from '@vue/test-utils'
import { ElMessage, ElMessageBox } from 'element-plus'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ResourceSubmissionHistoryDrawer from './ResourceSubmissionHistoryDrawer.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))

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

function mountComponent(resourceType: 'network_asset' | 'business_system' = 'network_asset', allowRestore = false) {
  return shallowMount(ResourceSubmissionHistoryDrawer, {
    props: { resourceType, allowRestore },
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
    mocks.post.mockResolvedValue({ data: { result: { working_version_id: 'working-1', warnings: [] } } })
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

  it('restores an approved version through the resource-specific endpoint', async () => {
    mocks.get.mockResolvedValue({
      data: { ...response, items: [{ ...response.items[0], status: 'approved', operation: 'update' }] },
    })
    const confirm = vi.spyOn(ElMessageBox, 'confirm').mockResolvedValue({ action: 'confirm' } as never)
    const success = vi.spyOn(ElMessage, 'success').mockImplementation(() => ({ close: () => undefined }))
    const wrapper = mountComponent('network_asset', true)
    await (wrapper.vm as unknown as { open: (id: string) => Promise<void> }).open('asset-1')

    await (wrapper.vm as unknown as { restoreSubmission: (item: typeof response.items[number]) => Promise<void> }).restoreSubmission({
      ...response.items[0], status: 'approved', operation: 'update',
    })

    expect(mocks.post).toHaveBeenCalledWith('/inventory-tasks/resource-submissions/network_asset/asset-1/submission-1/restore')
    expect(success).toHaveBeenCalledWith('已恢复为未上报工作版本，请在台账页面复核后选择纳入填报任务')
    expect(wrapper.emitted('restored')).toEqual([['working-1']])
    confirm.mockRestore()
    success.mockRestore()
  })

  it('does not restore unapproved, delete, or unauthorized history versions', async () => {
    const confirm = vi.spyOn(ElMessageBox, 'confirm').mockResolvedValue({ action: 'confirm' } as never)
    const wrapper = mountComponent('business_system', true)
    await (wrapper.vm as unknown as { open: (id: string) => Promise<void> }).open('system-1')
    const restore = (wrapper.vm as unknown as { restoreSubmission: (item: typeof response.items[number]) => Promise<void> }).restoreSubmission

    await restore(response.items[0])
    await restore({ ...response.items[0], status: 'approved', operation: 'delete' })
    await wrapper.setProps({ allowRestore: false })
    await restore({ ...response.items[0], status: 'approved', operation: 'update' })

    expect(confirm).not.toHaveBeenCalled()
    expect(mocks.post).not.toHaveBeenCalled()
    confirm.mockRestore()
  })

  it('reports a readable restore API error', async () => {
    mocks.get.mockResolvedValue({
      data: { ...response, items: [{ ...response.items[0], status: 'approved', operation: 'update' }] },
    })
    mocks.post.mockRejectedValue(new Error('failed'))
    const confirm = vi.spyOn(ElMessageBox, 'confirm').mockResolvedValue({ action: 'confirm' } as never)
    const message = vi.spyOn(ElMessage, 'error').mockImplementation(() => ({ close: () => undefined }))
    const wrapper = mountComponent('network_asset', true)
    await (wrapper.vm as unknown as { open: (id: string) => Promise<void> }).open('asset-1')

    await (wrapper.vm as unknown as { restoreSubmission: (item: typeof response.items[number]) => Promise<void> }).restoreSubmission({
      ...response.items[0], status: 'approved', operation: 'update',
    })

    expect(message).toHaveBeenCalledWith('恢复资源历史版本失败')
    confirm.mockRestore()
    message.mockRestore()
  })

})
