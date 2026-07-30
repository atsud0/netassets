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
