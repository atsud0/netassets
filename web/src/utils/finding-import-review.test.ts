import { describe, expect, it } from 'vitest'
import { validateFindingImportIgnoreReason } from './finding-import-review'

describe('待关联漏洞忽略原因', () => {
  it.each([undefined, null, '', ' \n\t　'])('拒绝空白值 %j', value => {
    expect(validateFindingImportIgnoreReason(value)).toBe('请填写忽略原因')
  })

  it.each(['略', 'a', '🔒'])('按字符接受 2000 字并拒绝 2001 字：%s', character => {
    expect(validateFindingImportIgnoreReason(character.repeat(2000))).toBe(true)
    expect(validateFindingImportIgnoreReason(character.repeat(2001))).toBe('忽略原因最多 2000 字')
  })

  it('与后端一致，计数和提交都使用去除首尾空白后的内容', () => {
    expect(validateFindingImportIgnoreReason(` \n${'原'.repeat(2000)}　`)).toBe(true)
  })
})
