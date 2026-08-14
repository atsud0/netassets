import { shallowMount, flushPromises } from '@vue/test-utils'
import { ElMessage } from 'element-plus'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import WorkingVersionDiffDrawer from './WorkingVersionDiffDrawer.vue'

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
}))

vi.mock('../api/client', () => ({
  apiClient: mocks,
  getAPIErrorMessage: (_error: unknown, fallback: string) => fallback,
}))

function mountComponent(resourceType: 'network_asset' | 'business_system' = 'network_asset') {
  return shallowMount(WorkingVersionDiffDrawer, {
    props: { resourceType },
    global: {
      stubs: {
        ElDrawer: true,
        ElTag: true,
        ElAlert: true,
        ElEmpty: true,
      },
      directives: { loading: () => undefined },
    },
  })
}

const response = {
  working_version_diff: {
    resource_type: 'network_asset',
    draft_id: 'draft-1',
    source_id: 'asset-1',
    organization_id: 'org-1',
    operation: 'update',
    draft_status: 'editing',
    resource_key: 'domain:example.com',
    resource_name: '示例资产',
    changes: [{ field: 'asset_name', label: '资产名称', before: '旧名称', after: '新名称' }],
  },
}

describe('WorkingVersionDiffDrawer', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.get.mockResolvedValue({ data: response })
  })

  it('loads the selected network asset working version through the exposed open method', async () => {
    const wrapper = mountComponent()
    await (wrapper.vm as unknown as { open: (draftID: string) => Promise<void> }).open('draft-1')

    expect(mocks.get).toHaveBeenCalledWith('/inventory-tasks/working-versions/network_asset/draft-1/diff')
  })

  it('uses the business system resource path', async () => {
    const wrapper = mountComponent('business_system')
    await (wrapper.vm as unknown as { open: (draftID: string) => Promise<void> }).open('system-draft-1')

    expect(mocks.get).toHaveBeenCalledWith('/inventory-tasks/working-versions/business_system/system-draft-1/diff')
  })

  it('closes the drawer and reports a readable error when loading fails', async () => {
    mocks.get.mockRejectedValue(new Error('failed'))
    const message = vi.spyOn(ElMessage, 'error').mockImplementation(() => ({ close: () => undefined }))
    const wrapper = mountComponent()

    await (wrapper.vm as unknown as { open: (draftID: string) => Promise<void> }).open('draft-1')
    await flushPromises()

    expect(message).toHaveBeenCalledWith('读取工作版本变更失败')
    message.mockRestore()
  })
})
