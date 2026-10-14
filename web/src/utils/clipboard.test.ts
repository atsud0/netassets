import { beforeEach, describe, expect, it, vi } from 'vitest'
import { copyTextToClipboard } from './clipboard'

describe('copyTextToClipboard', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  it('uses the async clipboard API when it is available', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    await expect(copyTextToClipboard('CRM_001')).resolves.toBe(true)
    expect(writeText).toHaveBeenCalledWith('CRM_001')
  })

  it('falls back when the page is served over plain HTTP and navigator.clipboard is undefined', async () => {
    // This is the reported defect: the product is reached at http://192.168.8.6:8080,
    // where the Clipboard API does not exist at all.
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
    const execCommand = vi.fn().mockReturnValue(true)
    Object.defineProperty(document, 'execCommand', { value: execCommand, configurable: true })
    await expect(copyTextToClipboard('CRM_001')).resolves.toBe(true)
    expect(execCommand).toHaveBeenCalledWith('copy')
  })

  it('falls back when the clipboard API exists but rejects the write', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('NotAllowedError'))
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const execCommand = vi.fn().mockReturnValue(true)
    Object.defineProperty(document, 'execCommand', { value: execCommand, configurable: true })
    await expect(copyTextToClipboard('CRM_001')).resolves.toBe(true)
    expect(execCommand).toHaveBeenCalledWith('copy')
  })

  it('reports failure instead of throwing when neither path works', async () => {
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
    Object.defineProperty(document, 'execCommand', { value: vi.fn().mockReturnValue(false), configurable: true })
    await expect(copyTextToClipboard('CRM_001')).resolves.toBe(false)
  })

  it('refuses to copy an empty value', async () => {
    const writeText = vi.fn()
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    await expect(copyTextToClipboard('')).resolves.toBe(false)
    expect(writeText).not.toHaveBeenCalled()
  })

  it('leaves no temporary element behind and restores the selection', async () => {
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
    Object.defineProperty(document, 'execCommand', { value: vi.fn().mockReturnValue(true), configurable: true })
    document.body.innerHTML = '<p id="anchor">x</p>'
    const selection = document.getSelection()!
    const range = document.createRange()
    range.selectNodeContents(document.getElementById('anchor')!)
    selection.removeAllRanges()
    selection.addRange(range)
    await copyTextToClipboard('CRM_001')
    expect(document.querySelectorAll('textarea')).toHaveLength(0)
    expect(selection.rangeCount).toBe(1)
  })
})
