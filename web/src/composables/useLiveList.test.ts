import { effectScope, nextTick, reactive, ref } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useLiveList } from './useLiveList'

const scopes: ReturnType<typeof effectScope>[] = []
afterEach(() => { scopes.splice(0).forEach(scope => scope.stop()); vi.useRealTimers() })

function setup() {
  vi.useFakeTimers()
  const scope = effectScope()
  scopes.push(scope)
  const filters = reactive({ status: '', keyword: '' })
  const page = ref(4)
  const loading = ref(false)
  const result = ref('initial')
  const requests: { finish: (value: string) => void; fail: () => void }[] = []
  const errors = vi.fn()
  const search = vi.fn(() => {
    page.value = 1
    const isCurrent = liveList.begin()
    loading.value = true
    const response = new Promise<string>((resolve, reject) => requests.push({ finish: resolve, fail: () => reject(new Error('failure')) }))
    void response.then(value => { if (isCurrent()) result.value = value })
      .catch(() => { if (isCurrent()) errors() })
      .finally(() => { if (isCurrent()) loading.value = false })
  })
  const liveList = scope.run(() => useLiveList(() => filters, search, () => filters.keyword))!
  return { filters, page, loading, result, requests, errors, search, scope }
}

describe('live list filtering', () => {
  it('queries status selection and clearing once and resets pagination', async () => {
    const state = setup()
    state.filters.status = 'open'
    await nextTick()
    expect(state.search).toHaveBeenCalledTimes(1)
    expect(state.page.value).toBe(1)
    expect(state.loading.value).toBe(true)
    state.page.value = 3
    state.filters.status = ''
    await nextTick()
    expect(state.search).toHaveBeenCalledTimes(2)
    expect(state.page.value).toBe(1)
  })

  it('debounces typing and deletion, clears immediately, and leaves state intact', async () => {
    const state = setup()
    state.filters.keyword = 'abc'
    await nextTick()
    await vi.advanceTimersByTimeAsync(200)
    state.filters.keyword = 'ab'
    await nextTick()
    await vi.advanceTimersByTimeAsync(249)
    expect(state.search).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(state.search).toHaveBeenCalledTimes(1)
    expect(state.result.value).toBe('initial')
    state.filters.keyword = ''
    await nextTick()
    expect(state.search).toHaveBeenCalledTimes(2)
  })

  it('applies discrete filters immediately even with nonempty text', async () => {
    const state = setup()
    state.filters.keyword = 'abc'
    await nextTick()
    state.filters.status = 'open'
    await nextTick()
    expect(state.search).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(300)
    expect(state.search).toHaveBeenCalledTimes(1)
  })

  it('ignores stale responses during debounce and keeps the latest loading state', async () => {
    const state = setup()
    state.search()
    state.filters.keyword = 'new'
    state.requests[0]!.finish('stale')
    await vi.advanceTimersByTimeAsync(0)
    expect(state.result.value).toBe('initial')
    expect(state.loading.value).toBe(true)
    await vi.advanceTimersByTimeAsync(250)
    state.requests[1]!.finish('latest')
    await vi.advanceTimersByTimeAsync(0)
    expect(state.result.value).toBe('latest')
    expect(state.loading.value).toBe(false)
  })

  it('ignores out-of-order failures and completion', async () => {
    const state = setup()
    state.search()
    state.filters.status = 'closed'
    await nextTick()
    state.requests[1]!.finish('latest')
    await vi.advanceTimersByTimeAsync(0)
    state.requests[0]!.fail()
    await vi.advanceTimersByTimeAsync(0)
    expect(state.errors).not.toHaveBeenCalled()
    expect(state.result.value).toBe('latest')
    expect(state.loading.value).toBe(false)
  })

  it('reloads when filters return to an already discarded request', async () => {
    const state = setup()
    state.search()
    state.filters.keyword = 'temporary'
    await nextTick()
    state.requests[0]!.finish('discarded')
    await vi.advanceTimersByTimeAsync(0)
    state.filters.keyword = ''
    await nextTick()
    expect(state.search).toHaveBeenCalledTimes(2)
    state.requests[1]!.finish('restored')
    await vi.advanceTimersByTimeAsync(0)
    expect(state.result.value).toBe('restored')
    expect(state.loading.value).toBe(false)
  })

  it('coalesces saved-view and reset changes with explicit reloads', async () => {
    const state = setup()
    Object.assign(state.filters, { keyword: 'saved', status: 'open' })
    state.search()
    await nextTick()
    await vi.advanceTimersByTimeAsync(300)
    expect(state.search).toHaveBeenCalledTimes(1)
    Object.assign(state.filters, { keyword: '', status: '' })
    state.search()
    await nextTick()
    expect(state.search).toHaveBeenCalledTimes(2)
  })

  it('cancels pending work and invalidates requests on unmount', async () => {
    const state = setup()
    state.search()
    state.filters.keyword = 'new'
    await nextTick()
    state.scope.stop()
    state.requests[0]!.finish('disposed')
    await vi.advanceTimersByTimeAsync(300)
    expect(state.search).toHaveBeenCalledTimes(1)
    expect(state.result.value).toBe('initial')
  })
})
