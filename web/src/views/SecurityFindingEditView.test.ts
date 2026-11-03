import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h, reactive } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ElMessageBox } from 'element-plus'
import { apiClient } from '../api/client'
import { loadOptions } from '../api/remediation'
import SecurityFindingEditView from './SecurityFindingEditView.vue'

const harness = vi.hoisted(() => ({ route: {} as any, push: vi.fn(), leave: undefined as any, update: undefined as any,
  auth: { canManageSecurityFindings: true, canManageSecurityTests: true } }))
vi.mock('vue-router', () => ({
  useRoute: () => harness.route, useRouter: () => ({ push: harness.push }),
  onBeforeRouteLeave: (guard: any) => { harness.leave = guard }, onBeforeRouteUpdate: (guard: any) => { harness.update = guard },
}))
vi.mock('../stores/auth', () => ({ useAuthStore: () => harness.auth }))
vi.mock('../api/client', () => ({
  apiClient: { get: vi.fn(), patch: vi.fn() },
  getAPIErrorMessage: (error: any, fallback: string) => error?.message || fallback,
  isEditConflictError: (error: any) => error?.status === 409,
  describeEditConflict: () => '安全发现已被其他人更新，本次修改未覆盖服务器上的最新内容。请先刷新查看最新数据，再重新提交。',
}))
vi.mock('../api/remediation', async original => ({ ...await original<typeof import('../api/remediation')>(), loadOptions: vi.fn() }))
vi.mock('element-plus', async original => ({ ...await original<typeof import('element-plus')>(),
  ElMessage: { error: vi.fn(), warning: vi.fn(), success: vi.fn() }, ElMessageBox: { confirm: vi.fn() },
}))
const Empty = defineComponent({ setup: () => () => h('div') })
const Markdown = defineComponent({ props: ['modelValue', 'label'], emits: ['update:modelValue'], setup: (props, { emit }) => () => h('textarea', {
  'aria-label': props.label, value: props.modelValue, onInput: (event: Event) => emit('update:modelValue', (event.target as HTMLTextAreaElement).value),
}) })
const originalFinding = {
  id: 'f1', code: 'SF-001', title: '原始标题', severity: 'high', status: 'pending',
  assignee_organization_id: 'org1', asset_id: 'a1', business_system_id: 's1', test_batch_id: '',
  description: '# 原描述\n正文', evidence: '原证据', remediation_advice: '原建议', source: 'manual',
  discovered_at: '2026-11-03T00:00:00Z', updated_at: '2026-11-03T01:00:00Z',
}
const wrappers: ReturnType<typeof mount>[] = []
function state(wrapper: ReturnType<typeof mount>) { return (wrapper.vm.$ as unknown as { setupState: Record<string, any> }).setupState }
async function create() {
  const wrapper = mount(SecurityFindingEditView, { global: {
    stubs: { ElSelect: Empty, ElSelectV2: Empty, ElDatePicker: Empty, MarkdownEditor: Markdown },
    directives: { loading: () => {} },
  } })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}
beforeEach(() => {
  harness.route = reactive({ params: { id: 'f1' }, query: { returnTo: '/security-work-orders?tab=findings&page=3&page_size=50&severity=high' } })
  harness.auth.canManageSecurityFindings = true
  harness.auth.canManageSecurityTests = true
  vi.mocked(apiClient.get).mockResolvedValue({ data: { item: { ...originalFinding } } })
  vi.mocked(apiClient.patch).mockResolvedValue({ data: { item: { ...originalFinding } } })
  vi.mocked(loadOptions).mockImplementation(async path => {
    if (path.endsWith('/organizations')) return [{ id: 'org1', name: '组织一', status: 'enabled', type: 'subsidiary' }]
    if (path.endsWith('/assets')) return [{ id: 'a1', value: 'example.test', organization_id: 'org1', business_system_id: 's1', status: 'active' }]
    if (path.endsWith('/business-systems')) return [{ id: 's1', code: 'SYS-1', name: '系统一', organization_id: 'org1', status: 'active' }]
    return []
  })
})
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.resetAllMocks() })

describe('独立安全发现编辑页面', () => {
  it('直接访问加载完整编辑对象，初始化不会清空关联或产生未保存提示', async () => {
    const wrapper = await create()
    expect(apiClient.get).toHaveBeenCalledWith('/security-findings/f1')
    expect(wrapper.find('h1').text()).toBe('编辑安全发现')
    expect(state(wrapper).form).toMatchObject({ asset_id: 'a1', business_system_id: 's1', description: originalFinding.description })
    expect(state(wrapper).dirty).toBe(false)
    expect(await harness.leave()).toBe(true)
    expect(ElMessageBox.confirm).not.toHaveBeenCalled()
  })
  it('保存 Markdown 原文、所有字段和原始版本，成功后恢复来源列表地址', async () => {
    const wrapper = await create()
    const markdown = '# 验证\n\n```http\nGET /example\n```\n\n|标题|值|\n|---|---|\n|a|b|'
    await wrapper.find('textarea[aria-label="漏洞描述"]').setValue(markdown)
    await wrapper.find('textarea[aria-label="验证证据"]').setValue('> 证据\n`code`')
    await wrapper.find('textarea[aria-label="整改建议"]').setValue('- 建议一\n- 建议二')
    expect(state(wrapper).dirty).toBe(true)
    await state(wrapper).save()
    expect(apiClient.patch).toHaveBeenCalledWith('/security-findings/f1', expect.objectContaining({
      title: '原始标题', description: markdown, evidence: '> 证据\n`code`', remediation_advice: '- 建议一\n- 建议二',
      asset_id: 'a1', business_system_id: 's1', expected_updated_at: originalFinding.updated_at, discovered_at: new Date(originalFinding.discovered_at).toISOString(),
    }))
    expect(harness.push).toHaveBeenCalledWith('/security-work-orders?tab=findings&page=3&page_size=50&severity=high')
    expect(state(wrapper).dirty).toBe(false)
  })
  it('冲突和网络失败保留编辑内容及版本，不跳转、不覆盖服务器', async () => {
    const wrapper = await create()
    state(wrapper).form.description = '# 未保存的新内容'
    vi.mocked(apiClient.patch).mockRejectedValueOnce({ status: 409 })
    await state(wrapper).save()
    expect(state(wrapper).saveError).toContain('已被其他人更新')
    expect(state(wrapper).form.description).toBe('# 未保存的新内容')
    expect(state(wrapper).finding.updated_at).toBe(originalFinding.updated_at)
    expect(state(wrapper).dirty).toBe(true)
    expect(harness.push).not.toHaveBeenCalled()
    vi.mocked(apiClient.patch).mockRejectedValueOnce(new Error('网络失败'))
    await state(wrapper).save()
    expect(state(wrapper).saveError).toBe('网络失败')
    expect(state(wrapper).form.description).toBe('# 未保存的新内容')
  })
  it('验证必填、长度和至少一项关联，非法输入不发送保存请求', async () => {
    const wrapper = await create()
    state(wrapper).form.title = ''
    await state(wrapper).save()
    expect(apiClient.patch).not.toHaveBeenCalled()
    state(wrapper).form.title = '长'.repeat(256)
    await state(wrapper).save()
    expect(apiClient.patch).not.toHaveBeenCalled()
    state(wrapper).form.title = '合法标题'
    state(wrapper).form.asset_id = ''
    state(wrapper).form.business_system_id = ''
    await state(wrapper).save()
    expect(apiClient.patch).not.toHaveBeenCalled()
  })
  it('未保存离开、切换对象与刷新均有保护；取消保留输入', async () => {
    const wrapper = await create()
    state(wrapper).form.description = '尚未保存'
    vi.mocked(ElMessageBox.confirm).mockRejectedValueOnce('cancel')
    expect(await harness.leave()).toBe(false)
    expect(state(wrapper).form.description).toBe('尚未保存')
    vi.mocked(ElMessageBox.confirm).mockRejectedValueOnce('cancel')
    expect(await harness.update({ params: { id: 'f2' } }, { params: { id: 'f1' } })).toBe(false)
    const event = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(true)
    vi.mocked(ElMessageBox.confirm).mockResolvedValueOnce('confirm')
    expect(await harness.leave()).toBe(true)
  })
  it.each(['https://evil.test/security-work-orders', '//evil.test/security-work-orders', '/security-work-orders-evil', '/dashboard'])('不接受外部或非列表返回地址 %s', async returnTo => {
    harness.route.query.returnTo = returnTo
    const wrapper = await create()
    expect(state(wrapper).returnPath).toBe('/security-work-orders?tab=findings')
  })
  it('无权限不读取发现或参考数据；不存在时显示错误并可重试', async () => {
    harness.auth.canManageSecurityFindings = false
    const forbidden = await create()
    expect(forbidden.text()).toContain('没有编辑安全发现的权限')
    expect(apiClient.get).not.toHaveBeenCalled()
    expect(loadOptions).not.toHaveBeenCalled()
    harness.auth.canManageSecurityFindings = true
    vi.mocked(apiClient.get).mockRejectedValueOnce(new Error('安全发现不存在'))
    const missing = await create()
    expect(missing.text()).toContain('安全发现不存在')
    expect(missing.text()).toContain('重新加载')
    await state(missing).loadFinding()
    expect(state(missing).finding.id).toBe('f1')
  })
  it('只有发现管理权限时保留已有测试批次，不读取无权限接口', async () => {
    harness.auth.canManageSecurityTests = false
    vi.mocked(apiClient.get).mockResolvedValue({ data: { item: { ...originalFinding, test_batch_id: 'batch1', test_batch_name: '授权批次' } } })
    const wrapper = await create()
    expect(state(wrapper).form).toMatchObject({ test_batch_id: 'batch1', asset_id: 'a1', business_system_id: 's1' })
    expect(state(wrapper).frozenBatchScope).toBe(true)
    expect(apiClient.get).not.toHaveBeenCalledWith('/security-test-batches/batch1')
    expect(loadOptions).not.toHaveBeenCalledWith('/security-test-batches')
    state(wrapper).form.description = '修改内容'
    await state(wrapper).save()
    expect(apiClient.patch).toHaveBeenCalledWith('/security-findings/f1', expect.objectContaining({ test_batch_id: 'batch1', asset_id: 'a1' }))
  })
  it('快速切换批次只应用最新范围，范围失败时禁止保存且保留当前输入', async () => {
    const wrapper = await create()
    let resolveOld!: (value: unknown) => void
    vi.mocked(apiClient.get).mockImplementation(path => path === '/security-test-batches/old'
      ? new Promise(resolve => { resolveOld = resolve })
      : Promise.resolve({ data: { item: { scope_assets: [{ asset_id: 'a1' }] } } }))
    state(wrapper).form.test_batch_id = 'old'
    const old = state(wrapper).changeBatch()
    state(wrapper).form.test_batch_id = 'new'
    await state(wrapper).changeBatch()
    resolveOld({ data: { item: { scope_assets: [{ asset_id: 'old-asset' }] } } })
    await old
    expect(state(wrapper).scopeAssetIDs).toEqual(['a1'])
    expect(state(wrapper).form.asset_id).toBe('a1')
    vi.mocked(apiClient.get).mockRejectedValueOnce(new Error('读取失败'))
    state(wrapper).form.test_batch_id = 'failed'
    await state(wrapper).changeBatch()
    expect(state(wrapper).scopeError).toBe('读取失败')
    await state(wrapper).save()
    expect(apiClient.patch).not.toHaveBeenCalled()
  })
  it('同组件路由切换时旧响应不能覆盖新发现', async () => {
    let resolveOld!: (value: unknown) => void
    vi.mocked(apiClient.get).mockImplementation(path => path === '/security-findings/f1'
      ? new Promise(resolve => { resolveOld = resolve })
      : Promise.resolve({ data: { item: { ...originalFinding, id: 'f2', title: '第二条' } } }))
    const wrapper = await create()
    harness.route.params.id = 'f2'
    await flushPromises()
    expect(state(wrapper).finding.id).toBe('f2')
    resolveOld({ data: { item: { ...originalFinding } } })
    await flushPromises()
    expect(state(wrapper).finding.id).toBe('f2')
    expect(state(wrapper).form.title).toBe('第二条')
  })
})
