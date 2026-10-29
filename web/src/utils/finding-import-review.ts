/** Match the backend's trimmed Unicode code-point limit, not JS UTF-16 length. */
export function validateFindingImportIgnoreReason(value: string | null | undefined): true | string {
  const reason = (value ?? '').trim()
  if (!reason) return '请填写忽略原因'
  if (Array.from(reason).length > 2000) return '忽略原因最多 2000 字'
  return true
}
