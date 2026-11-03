export function resetListScroll(pagination: HTMLElement) {
  const container = pagination.closest<HTMLElement>('.el-drawer__body, .el-overlay-dialog, .page-content')
  if (!container) return
  let section = pagination.parentElement
  let target: HTMLElement | null = null
  while (section && section !== container) {
    const candidates = [...section.querySelectorAll<HTMLElement>('.el-table, [data-list-start], .el-timeline, .notification-list, .session-list')]
    target = candidates.filter(item => !candidates.some(other => other !== item && other.contains(item)) && Boolean(item.getClientRects().length) && Boolean(item.compareDocumentPosition(pagination) & Node.DOCUMENT_POSITION_FOLLOWING)).at(-1) ?? null
    if (target) break
    section = section.parentElement
  }
  if (!target) return
  const tableBody = target.querySelector<HTMLElement>('.el-table__body-wrapper .el-scrollbar__wrap')
  if (tableBody) tableBody.scrollTop = 0
  const notificationBody = target.closest<HTMLElement>('.notification-list-scroll')
  if (notificationBody) notificationBody.scrollTop = 0
  const listTop = container.scrollTop + target.getBoundingClientRect().top - container.getBoundingClientRect().top - 12
  container.scrollTop = Math.max(0, Math.min(container.scrollTop, listTop))
}
