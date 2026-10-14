import { describe, expect, it } from 'vitest'
import { getDownloadAPIErrorMessage } from './client'

describe('getDownloadAPIErrorMessage', () => {
  it('decodes API error messages returned as download blobs', async () => {
    const error = {
      isAxiosError: true,
      response: {
        data: new Blob([
          JSON.stringify({ error: { code: 'synchronous_xlsx_limit_exceeded', message: '同步 XLSX 导出最多支持 5000 条数据，请改用 CSV 格式导出' } }),
        ], { type: 'application/json' }),
      },
    }

    await expect(getDownloadAPIErrorMessage(error, '导出失败')).resolves.toBe('同步 XLSX 导出最多支持 5000 条数据，请改用 CSV 格式导出')
  })

  it('keeps normal JSON download errors readable', async () => {
    const error = {
      isAxiosError: true,
      response: { data: { error: { message: '无权导出' } } },
    }

    await expect(getDownloadAPIErrorMessage(error, '导出失败')).resolves.toBe('无权导出')
  })
})

import { AxiosError } from 'axios'
import { describeEditConflict, isEditConflictError } from './client'

function axiosError(status: number) {
  const error = new AxiosError('failed')
  ;(error as unknown as { response?: unknown }).response = { status, data: { error: { code: 'working_version_conflict' } } }
  return error
}

describe('isEditConflictError', () => {
  it('treats HTTP 409 as an optimistic concurrency failure', () => {
    expect(isEditConflictError(axiosError(409))).toBe(true)
  })

  it('leaves other failures to the normal error message path', () => {
    expect(isEditConflictError(axiosError(400))).toBe(false)
    expect(isEditConflictError(new Error('boom'))).toBe(false)
  })
})

describe('describeEditConflict', () => {
  it('states that nothing was overwritten and asks for a reload', () => {
    const message = describeEditConflict()
    expect(message).toContain('未覆盖服务器上的最新内容')
    expect(message).toContain('刷新')
    expect(describeEditConflict('资产已被其他人更新')).toContain('资产已被其他人更新')
  })
})
