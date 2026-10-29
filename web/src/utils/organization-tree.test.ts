import { describe, expect, it } from 'vitest'
import { indexOrganizationTree, visibleOrganizationRows, type OrganizationTreeNode } from './organization-tree'

const tree: OrganizationTreeNode[] = [
  { id: 'a', name: '集团', code: 'GROUP', children: [
    { id: 'b', name: '公司', code: 'SUB', children: [{ id: 'c', name: '研发部门', code: 'DEV', short_name: 'R&D' }] },
    { id: 'd', name: '其他部门', code: 'OTHER' },
  ] },
  { id: 'e', name: '独立授权根', code: 'ROOT' },
]
const ids = (query = '', collapsed = new Set<string>()) => visibleOrganizationRows(indexOrganizationTree(tree), query, collapsed).map(row => row.id)

describe('组织树可见行', () => {
  it('保持多根前序顺序、路径和完整原树，表格行不含 children', () => {
    const before = JSON.stringify(tree)
    const entries = indexOrganizationTree(tree)
    expect(ids()).toEqual(['a', 'b', 'c', 'd', 'e'])
    expect(entries[2]!.row).toMatchObject({ treeDepth: 2, treePath: ['集团', '公司'], treeHasChildren: false })
    expect(entries.every(entry => !('children' in entry.row))).toBe(true)
    expect(JSON.stringify(tree)).toBe(before)
  })
  it('按名称、编码、简称忽略首尾空白及大小写匹配，仅保留匹配节点和祖先', () => {
    for (const query of ['研发', ' dev ', 'r&d']) expect(ids(query)).toEqual(['a', 'b', 'c'])
    expect(ids('GROUP')).toEqual(['a'])
    expect(ids('不存在')).toEqual([])
    expect(ids(' ')).toEqual(ids())
  })
  it('单节点折叠不影响兄弟及其他根，搜索穿透折叠且不改变折叠状态', () => {
    const collapsed = new Set(['b'])
    expect(ids('', collapsed)).toEqual(['a', 'b', 'd', 'e'])
    expect(ids('DEV', collapsed)).toEqual(['a', 'b', 'c'])
    expect(ids('', collapsed)).toEqual(['a', 'b', 'd', 'e'])
    expect([...collapsed]).toEqual(['b'])
    expect(ids('', new Set(['a', 'b']))).toEqual(['a', 'e'])
  })
  it('空树和单节点无特殊情况', () => {
    expect(visibleOrganizationRows(indexOrganizationTree([]), '', new Set())).toEqual([])
    expect(indexOrganizationTree([tree[1]!])[0]!.row).toMatchObject({ treeDepth: 0, treePath: [], treeHasChildren: false })
  })
  it('深层搜索只保留完整祖先路径，迭代遍历不依赖递归栈', () => {
    let node: OrganizationTreeNode = { id: 'last', name: '末级', code: 'TARGET' }
    for (let i = 0; i < 1000; i++) node = { id: `level-${i}`, name: `层级${i}`, code: `NODE_${i}`, children: [node] }
    const entries = indexOrganizationTree([node])
    const rows = visibleOrganizationRows(entries, 'TARGET', new Set(['level-999']))
    expect(rows).toHaveLength(1001)
    expect(rows.at(-1)!.treePath).toHaveLength(1000)
  })
})
