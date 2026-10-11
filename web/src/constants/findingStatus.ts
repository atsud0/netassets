// Frontend mirror of backend/internal/findingstatus. Keep the value and label
// pairs aligned with the backend package; sortOrder drives presentation order.
export interface FindingStatusDefinition {
  value: string
  label: string
  sortOrder: number
}

export const FINDING_STATUSES: FindingStatusDefinition[] = [
  { value: 'pending', label: '待处理', sortOrder: 1 },
  { value: 'in_progress', label: '处理中', sortOrder: 2 },
  { value: 'confirmed', label: '已确认', sortOrder: 3 },
  { value: 'resolved', label: '已处理', sortOrder: 4 },
  { value: 'fixed', label: '已修复', sortOrder: 5 },
  { value: 'false_positive', label: '误报', sortOrder: 6 },
  { value: 'ignored', label: '忽略', sortOrder: 7 },
  { value: 'duplicate', label: '重复', sortOrder: 8 },
  { value: 'risk_accepted', label: '风险接受', sortOrder: 9 },
]

// Frequently referenced values, so callers avoid retyping literals. They are
// typed as string to stay usable inside .includes() and equality comparisons.
export const FINDING_STATUS_PENDING: string = 'pending'
export const FINDING_STATUS_CONFIRMED: string = 'confirmed'
export const FINDING_STATUS_RISK_ACCEPTED: string = 'risk_accepted'

export const findingStatusLabels: Record<string, string> = Object.fromEntries(
  FINDING_STATUSES.map(status => [status.value, status.label]),
)

type TagType = 'primary' | 'success' | 'warning' | 'info' | 'danger'

// Element Plus tag types per status, mirroring the previous per-view mappings.
const FINDING_STATUS_TAG_TYPES: Record<string, TagType> = {
  pending: 'primary',
  in_progress: 'warning',
  confirmed: 'warning',
  resolved: 'success',
  fixed: 'success',
  false_positive: 'info',
  ignored: 'info',
  duplicate: 'info',
  risk_accepted: 'danger',
}

export function findingStatusTagType(value: string): TagType {
  return FINDING_STATUS_TAG_TYPES[value] ?? 'primary'
}

// Closed mirrors backend findingstatus.Closed: no further remediation work is
// required once a finding reaches one of these statuses.
const CLOSED_FINDING_STATUSES = ['resolved', 'fixed', 'false_positive', 'ignored', 'duplicate', 'risk_accepted']

export const CLOSED_FINDING_STATUS_VALUES: readonly string[] = CLOSED_FINDING_STATUSES

export function isFindingStatusClosed(value: string) {
  return CLOSED_FINDING_STATUSES.includes(value)
}

export function isFindingStatusOpen(value: string) {
  return findingStatusLabels[value] !== undefined && !isFindingStatusClosed(value)
}
