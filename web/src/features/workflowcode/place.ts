import type { XYPosition } from '@xyflow/react'

import type { Definition } from '@/features/workflow/graph'
import { columnWidth, layout, rowHeight } from '@/features/workflow/layout'

/**
 * Where the nodes of a workflow code built go on the canvas (A4, D66): those it had keep
 * their place, by ID; a new one goes after the node feeding it, else before the one it
 * feeds, else under the rest, moved down past any node already there. With none kept,
 * all stay at 0, 0 for the canvas to lay out.
 */
export function place(built: Definition, before: ReadonlyMap<string, XYPosition>): Definition {
  const at = new Map<string, XYPosition>()
  for (const n of built.nodes) {
    const kept = before.get(n.id)
    if (kept) at.set(n.id, kept)
  }

  if (at.size === 0) {
    return { ...built, nodes: built.nodes.map((n) => ({ ...n, position: { x: 0, y: 0 } })) }
  }

  // Left to right, so a chain of new nodes grows from where it joins the rest.
  const columns = layout(built.nodes, built.connections)
  const fresh = built.nodes
    .filter((n) => !at.has(n.id))
    .sort((a, b) => columns.get(a.id)!.x - columns.get(b.id)!.x)

  for (const n of fresh) {
    const from = built.connections.find((c) => c.to === n.id && at.has(c.from))
    const to = built.connections.find((c) => c.from === n.id && at.has(c.to))

    let p: XYPosition
    if (from) {
      const f = at.get(from.from)!
      p = { x: f.x + columnWidth, y: f.y }
    } else if (to) {
      const t = at.get(to.to)!
      p = { x: t.x - columnWidth, y: t.y }
    } else {
      const all = [...at.values()]
      p = { x: Math.min(...all.map((q) => q.x)), y: Math.max(...all.map((q) => q.y)) + rowHeight }
    }

    while ([...at.values()].some((q) => near(q, p))) p = { x: p.x, y: p.y + rowHeight }
    at.set(n.id, p)
  }

  return { ...built, nodes: built.nodes.map((n) => ({ ...n, position: at.get(n.id)! })) }
}

function near(a: XYPosition, b: XYPosition): boolean {
  return Math.abs(a.x - b.x) < columnWidth / 2 && Math.abs(a.y - b.y) < rowHeight / 2
}
