export type Point = { x: number; y: number }

export const columnWidth = 300
export const rowHeight = 140

/**
 * Places nodes left to right: each node's column is its longest distance from a node with
 * no incoming connections, and nodes keep their declaration order within a column.
 * Connections naming unknown nodes are ignored.
 */
export function layout(
  nodes: readonly { id: string }[],
  connections: readonly { from: string; to: string }[],
): Map<string, Point> {
  const column = new Map(nodes.map((n) => [n.id, 0]))
  const incoming = new Map(nodes.map((n) => [n.id, 0]))
  const outgoing = new Map<string, string[]>()

  for (const c of connections) {
    if (!column.has(c.from) || !column.has(c.to)) continue

    incoming.set(c.to, (incoming.get(c.to) ?? 0) + 1)
    outgoing.set(c.from, [...(outgoing.get(c.from) ?? []), c.to])
  }

  // Kahn's algorithm: a node is placed once everything feeding it is. Nodes in a cycle,
  // which a saved workflow cannot have, simply stay in their column.
  const ready = nodes.filter((n) => incoming.get(n.id) === 0).map((n) => n.id)

  while (ready.length > 0) {
    const id = ready.shift()!

    for (const next of outgoing.get(id) ?? []) {
      column.set(next, Math.max(column.get(next)!, column.get(id)! + 1))
      incoming.set(next, incoming.get(next)! - 1)

      if (incoming.get(next) === 0) ready.push(next)
    }
  }

  const rows = new Map<number, number>()
  const positions = new Map<string, Point>()

  for (const n of nodes) {
    const col = column.get(n.id)!
    const row = rows.get(col) ?? 0

    rows.set(col, row + 1)
    positions.set(n.id, { x: col * columnWidth, y: row * rowHeight })
  }

  return positions
}
