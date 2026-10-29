import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('./client', () => ({ apiClient: { get: vi.fn() } }))
import { apiClient } from './client'
import { loadOptions } from './remediation'

const get = vi.mocked(apiClient.get)
beforeEach(() => get.mockReset())

describe('paginated select options', () => {
  it('loads all pages including an organization outside the first 100', async () => {
    const organizations = Array.from({ length: 241 }, (_, index) => ({ id: `org-${index}` }))
    for (let page = 0; page < 3; page++) {
      get.mockResolvedValueOnce({ data: { items: organizations.slice(page * 100, (page + 1) * 100), total: organizations.length } })
    }
    await expect(loadOptions('/organizations', { status: 'enabled' })).resolves.toEqual(organizations)
    expect(get).toHaveBeenCalledTimes(3)
    for (let page = 1; page <= 3; page++) {
      expect(get).toHaveBeenNthCalledWith(page, '/organizations', { params: { status: 'enabled', page, page_size: 100 } })
    }
  })

  it('stops on an empty page when rows disappear between requests', async () => {
    get.mockResolvedValueOnce({ data: { items: [{ id: 'org-1' }], total: 101 } })
    get.mockResolvedValueOnce({ data: { items: [], total: 101 } })
    await expect(loadOptions('/organizations')).resolves.toEqual([{ id: 'org-1' }])
    expect(get).toHaveBeenCalledTimes(2)
  })

  it('does not silently present a partial list when a later page fails', async () => {
    get.mockResolvedValueOnce({ data: { items: [{ id: 'org-1' }], total: 101 } })
    get.mockRejectedValueOnce(new Error('读取下一页失败'))
    await expect(loadOptions('/organizations')).rejects.toThrow('读取下一页失败')
    expect(get).toHaveBeenCalledTimes(2)
  })
})
