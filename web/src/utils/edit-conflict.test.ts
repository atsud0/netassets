import { describe, expect, it, vi } from 'vitest'
import { AxiosError } from 'axios'

vi.mock('element-plus', () => ({
  ElMessage: { warning: vi.fn(), error: vi.fn() },
  ElMessageBox: { confirm: vi.fn().mockResolvedValue('confirm') },
}))

import { reportEditConflict } from './edit-conflict'

function axiosError(status: number) {
  const error = new AxiosError('failed')
  ;(error as unknown as { response?: unknown }).response = { status, data: { error: { code: 'working_version_conflict' } } }
  return error
}

describe('reportEditConflict', () => {
  it('reloads after a 409 so the user retries against fresh data', async () => {
    const reload = vi.fn().mockResolvedValue(undefined)
    await expect(reportEditConflict(axiosError(409), '草稿已被其他人更新', reload)).resolves.toBe(true)
    expect(reload).toHaveBeenCalledTimes(1)
  })

  it('keeps the user input when the reload is declined', async () => {
    const { ElMessageBox } = await import('element-plus')
    ;(ElMessageBox.confirm as unknown as ReturnType<typeof vi.fn>).mockRejectedValueOnce('cancel')
    const reload = vi.fn()
    await expect(reportEditConflict(axiosError(409), '草稿已被其他人更新', reload)).resolves.toBe(true)
    expect(reload).not.toHaveBeenCalled()
  })

  it('lets non-conflict errors fall through to the normal error handling', async () => {
    const reload = vi.fn()
    await expect(reportEditConflict(new Error('boom'), '标题', reload)).resolves.toBe(false)
    expect(reload).not.toHaveBeenCalled()
  })
})
