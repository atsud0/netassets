export interface ImportColumn {
  index: number
  name: string
  examples: string[]
}

export interface ImportField {
  key: string
  name: string
  required: boolean
  custom: boolean
  suggested_column?: number | null
}

export type ImportColumnMapping = Record<string, number | null | undefined>

export function createSuggestedColumnMapping(fields: ImportField[]): ImportColumnMapping {
  return Object.fromEntries(fields.map((field) => [field.key, field.suggested_column ?? null]))
}

export function getImportColumnMappingError(fields: ImportField[], mapping: ImportColumnMapping): string {
  const missing = fields.filter((field) => field.required && (!Number.isInteger(mapping[field.key]) || (mapping[field.key] as number) < 0))
  if (missing.length) return `请映射必填字段：${missing.map((field) => field.name).join('、')}`

  const used = new Map<number, string>()
  for (const field of fields) {
    const index = mapping[field.key]
    if (!Number.isInteger(index) || (index as number) < 0) continue
    const numericIndex = index as number
    const previous = used.get(numericIndex)
    if (previous) return `文件列不能重复映射给“${previous}”和“${field.name}”`
    used.set(numericIndex, field.name)
  }
  return ''
}

export function isImportColumnUsed(mapping: ImportColumnMapping, index: number, currentKey: string): boolean {
  return Object.entries(mapping).some(([key, value]) => key !== currentKey && value === index)
}

export function serializeImportColumnMapping(fields: ImportField[], mapping: ImportColumnMapping): string {
  const payload: Record<string, number> = {}
  for (const field of fields) {
    const index = mapping[field.key]
    if (Number.isInteger(index)) payload[field.key] = index as number
  }
  return JSON.stringify(payload)
}

export function importColumnLabel(column: ImportColumn): string {
  const name = column.name || `第 ${column.index + 1} 列`
  if (!column.examples.length) return name
  return `${name}（示例：${column.examples.join('、')}）`
}
