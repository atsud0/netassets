import { describe, expect, it, vi } from 'vitest'
import { contactPhoneRule, isValidContactPhone, normalizeContactPhone } from './contact-phone'

describe('contact phone validation', () => {
  it('accepts the numbers a group ledger actually stores', () => {
    for (const value of ['13800000000', '138-0000-0000', '021 8888 6666', '0755-88886666-8001', '400-820-8820', '8008208820', '6688', '+86 (139) 0000-0018']) {
      expect(isValidContactPhone(value), value).toBe(true)
    }
  })

  it('rejects text that can never reach an owner', () => {
    for (const value of ['abc', '张三', '1380000000', '12345678901', '138000000001234', '转接行政', '12345678901234567890', '12800000000', '12', '13800000000转1']) {
      expect(isValidContactPhone(value), value).toBe(false)
    }
  })

  it('treats an empty value as "not provided"', () => {
    expect(isValidContactPhone('')).toBe(true)
    expect(isValidContactPhone('   ')).toBe(true)
  })

  it('normalizes separators so stored values compare consistently', () => {
    expect(normalizeContactPhone(' 138-0000-0000 ')).toBe('13800000000')
    expect(normalizeContactPhone('(021) 8888 6666')).toBe('02188886666')
    expect(normalizeContactPhone('+86 (139) 0000-0018')).toBe('+8613900000018')
    expect(normalizeContactPhone('0755-88886666-8001')).toBe('0755888866668001')
    expect(normalizeContactPhone('   ')).toBe('')
  })

  it.each(['错误电话', 'invalid-phone', '1380000000', 'x'.repeat(64)])('returns a field-specific validation error for %s', value => {
    const callback = vi.fn()
    expect(() => contactPhoneRule.validator({}, value, callback)).not.toThrow()
    expect(callback).toHaveBeenCalledExactlyOnceWith(new Error('请填写手机号码、座机号码或分机号'))
  })

  it.each(['138-0000-0018', '0755-88886666-8001', '+86 (139) 0000-0018', '400-123-4567', '8001', '   '])('allows %s through the form validator', value => {
    const callback = vi.fn()
    contactPhoneRule.validator({}, value, callback)
    expect(callback).toHaveBeenCalledExactlyOnceWith()
  })
})
