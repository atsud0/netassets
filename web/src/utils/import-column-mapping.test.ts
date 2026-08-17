import { describe, expect, it } from 'vitest'
import {
  createSuggestedColumnMapping,
  getImportColumnMappingError,
  importColumnLabel,
  isImportColumnUsed,
  serializeImportColumnMapping,
  type ImportField,
} from './import-column-mapping'

const fields: ImportField[] = [
  { key: 'type', name: '资产类型', required: true, custom: false, suggested_column: 1 },
  { key: 'value', name: '资产值', required: true, custom: false, suggested_column: 0 },
  { key: 'custom:level', name: '安全等级', required: false, custom: true, suggested_column: null },
]

describe('import column mapping', () => {
  it('initializes automatic suggestions and serializes mapped fields', () => {
    const mapping = createSuggestedColumnMapping(fields)
    expect(mapping).toEqual({ type: 1, value: 0, 'custom:level': null })
    expect(serializeImportColumnMapping(fields, mapping)).toBe('{"type":1,"value":0}')
  })

  it('reports missing required and duplicate source columns', () => {
    expect(getImportColumnMappingError(fields, { type: 1, value: null })).toContain('资产值')
    expect(getImportColumnMappingError(fields, { type: 1, value: 1 })).toContain('不能重复映射')
    expect(getImportColumnMappingError(fields, { type: 1, value: 0 })).toBe('')
  })

  it('detects columns used by another field and formats examples', () => {
    expect(isImportColumnUsed({ type: 1, value: 0 }, 1, 'value')).toBe(true)
    expect(isImportColumnUsed({ type: 1, value: 0 }, 1, 'type')).toBe(false)
    expect(importColumnLabel({ index: 0, name: '目标地址', examples: ['1.2.3.4', 'example.com'] }))
      .toBe('目标地址（示例：1.2.3.4、example.com）')
  })
})
