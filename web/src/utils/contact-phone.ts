// One shared rule for every "phone" input in the product.
//
// The backend already rejects unusable contact numbers (see
// backend/internal/contactphone), so the form has to state the same rule
// instead of letting the user fill something in and only then receive a
// generic rejection. Keeping the pattern here identical to the Go one means the
// two can never drift apart in a user-visible way.

const PHONE_PATTERN = /^(?:\+?86[- ]?)?(?:1[3-9]\d{9}|0\d{2,3}[- ]?\d{7,8}(?:[- ]?\d{1,6})?|[48]00[- ]?\d{3}[- ]?\d{4}|\d{3,6})$/

export function normalizeContactPhone(value: string): string {
  return (value ?? '').trim().replace(/[\s()-]/g, '')
}

export function isValidContactPhone(value: string): boolean {
  const trimmed = (value ?? '').trim()
  if (trimmed === '') return true
  // Separators are removed first so that "138-0000-0000" and "13800000000"
  // are judged by the same rule, and "0755-88886666-8001" is read as area code
  // 0755 plus number 88886666 plus extension 8001. This mirrors the Go
  // validator's Normalize-then-match order exactly.
  return PHONE_PATTERN.test(trimmed.replace(/[\s()-]/g, ''))
}

export const contactPhoneRule = {
  validator: (_rule: unknown, value: string, callback: (error?: Error) => void) => {
    if (isValidContactPhone(value)) return callback()
    return callback(new Error('请填写手机号码、座机号码或分机号'))
  },
}
