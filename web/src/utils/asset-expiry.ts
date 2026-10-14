// One place that decides what an asset's expiry deadline means.
//
// The reported defect was "已到期但没有显示已到期，还显示即将过期". The cause
// was arithmetic, not data: the label counted remaining days with Math.ceil,
// so an asset that expired an hour ago rounded to zero remaining days and
// fell through to the "即将到期" branch. Every deadline within 24 hours in the
// past therefore read as still valid, and the tag was still the neutral green
// "plenty of time" colour instead of the danger colour.
//
// The rule is deliberately about the timestamp itself, not about rounded
// calendar days: an asset is expired the moment now() is past the deadline,
// and it is "expiring soon" only while it is still in the future.

const DAY_MS = 86_400_000

export const EXPIRY_SOON_MS = 30 * DAY_MS

/** Remaining whole days, truncated towards zero, so a past deadline is negative. */
export function remainingDays(expiresAt: string, now: number = Date.now()): number {
  const deadline = new Date(expiresAt).getTime()
  if (Number.isNaN(deadline)) return Number.NaN
  // Math.trunc, not Math.ceil: ceil turned "expired 1 hour ago" into zero
  // remaining days and "expired 30 hours ago" into -0, so any caller branching
  // on a positive count would read an expired asset as still valid. trunc keeps
  // the sign of the interval and never reports a positive count for a deadline
  // that has already passed. Callers that need the boolean should use
  // expiryState, which compares the timestamps directly.
  return Math.trunc((deadline - now) / DAY_MS)
}

export type AssetExpiryState = 'permanent' | 'expired' | 'expiring' | 'active' | 'unknown'

export function expiryState(expiresAt?: string | null, now: number = Date.now()): AssetExpiryState {
  if (!expiresAt) return 'permanent'
  const deadline = new Date(expiresAt).getTime()
  if (Number.isNaN(deadline)) return 'unknown'
  if (deadline <= now) return 'expired'
  if (deadline - now <= EXPIRY_SOON_MS) return 'expiring'
  return 'active'
}

/** Element Plus tag type, matching the label: expired is danger, never warning. */
export function expiryTagType(expiresAt?: string | null, now: number = Date.now()): 'info' | 'danger' | 'warning' | 'success' {
  switch (expiryState(expiresAt, now)) {
    case 'expired': return 'danger'
    case 'expiring': return 'warning'
    case 'active': return 'success'
    default: return 'info'
  }
}

export function isAssetExpired(expiresAt?: string | null, now: number = Date.now()): boolean {
  return expiryState(expiresAt, now) === 'expired'
}
