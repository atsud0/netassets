export interface OrganizationTreeNode {
  id: string
  name: string
  code: string
  short_name?: string
  children?: OrganizationTreeNode[]
}

export type OrganizationTreeRow<T> = Omit<T, 'children'> & {
  treeDepth: number
  treePath: string[]
  treeHasChildren: boolean
  treeExpanded: boolean
}

export interface OrganizationTreeEntry<T> {
  row: OrganizationTreeRow<T>
  parentIndex: number
}

/** Preorder index built once per API response. Never pass nested children to el-table. */
export function indexOrganizationTree<T extends OrganizationTreeNode>(roots: T[]): OrganizationTreeEntry<T>[] {
  const entries: OrganizationTreeEntry<T>[] = []
  const stack = roots.map(node => ({ node, parentIndex: -1 })).reverse()
  while (stack.length) {
    const { node, parentIndex } = stack.pop()!
    const parent = entries[parentIndex]?.row
    const { children, ...fields } = node
    const index = entries.length
    entries.push({
      parentIndex,
      row: {
        ...fields,
        treeDepth: parent ? parent.treeDepth + 1 : 0,
        treePath: parent ? [...parent.treePath, parent.name] : [],
        treeHasChildren: !!children?.length,
        treeExpanded: true,
      } as OrganizationTreeRow<T>,
    })
    for (let i = (children?.length ?? 0) - 1; i >= 0; i--) {
      stack.push({ node: children![i] as T, parentIndex: index })
    }
  }
  return entries
}

/** Search keeps matches and their ancestors, independently of the saved collapse state. */
export function visibleOrganizationRows<T extends OrganizationTreeNode>(
  entries: OrganizationTreeEntry<T>[], query: string, collapsedIDs: ReadonlySet<string>,
): OrganizationTreeRow<T>[] {
  const keyword = query.trim().toLocaleLowerCase()
  const included = entries.map(({ row }) => !keyword || [row.name, row.code, row.short_name ?? ''].some(value => value.toLocaleLowerCase().includes(keyword)))
  if (keyword) {
    for (let i = entries.length - 1; i >= 0; i--) {
      const parentIndex = entries[i]!.parentIndex
      if (included[i] && parentIndex >= 0) included[parentIndex] = true
    }
  }
  const visible: boolean[] = []
  const rows: OrganizationTreeRow<T>[] = []
  entries.forEach(({ row, parentIndex }, index) => {
    visible[index] = !!included[index] && (keyword !== '' || parentIndex < 0 || (visible[parentIndex] === true && !collapsedIDs.has(entries[parentIndex]!.row.id)))
    if (visible[index]) rows.push({ ...row, treeExpanded: keyword !== '' || !collapsedIDs.has(row.id) })
  })
  return rows
}
