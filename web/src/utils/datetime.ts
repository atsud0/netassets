// One shared, locale-aware formatter for every timestamp the API returns.
//
// The backend serializes Go time.Time as RFC 3339 in UTC ("2026-10-14T01:41:29Z").
// Screens that printed that raw string showed a UTC wall clock eight hours behind
// the user's local time, and screens that only printed the date part showed
// "10/14/26" for an event that happened on the 15th. Both mistakes are exactly
// the "查看操作下拉历史，时间显示错误" reports.
//
// Formatting in one place also means the history drawers, tables and detail
// cards can no longer drift apart from each other.

const DATE_TIME = new Intl.DateTimeFormat('zh-CN', {
  year: 'numeric', month: '2-digit', day: '2-digit',
  hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
})

const DATE_TIME_SHORT = new Intl.DateTimeFormat('zh-CN', { dateStyle: 'short', timeStyle: 'short' })

export function formatDateTime(value?: string | null): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return DATE_TIME.format(date)
}

export function formatDateTimeShort(value?: string | null): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return DATE_TIME_SHORT.format(date)
}
