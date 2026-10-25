import type { XYPosition } from '@xyflow/react'

import type { Connection, NodeType, WorkflowNode } from '@/api/client'

import { defaultPort, toFlow, type FlowEdge, type FlowNode } from './graph'

/**
 * Nodes copied from the canvas (D420): the selected ones and the connections between them,
 * as text for the system clipboard, so they paste into another workflow or tab. Personal
 * settings stay behind, as sharing leaves them out; a duplicate, which never leaves the
 * page, keeps them.
 */
export type Copied = { nodes: WorkflowNode[]; connections: Connection[] }

const marker = 'polythought/nodes'

/** The selected nodes and the connections between them; personal settings left out unless kept. */
export function copyOf(
  nodes: readonly FlowNode[],
  edges: readonly FlowEdge[],
  keepPersonal = false,
): Copied {
  const chosen = nodes.filter((n) => n.selected)
  const ids = new Set(chosen.map((n) => n.id))

  return {
    nodes: chosen.map((n) => {
      const personal = new Set(
        keepPersonal
          ? []
          : (n.data.type?.params ?? [])
              .filter((p) => p.sensitive || p.kind === 'wallet' || p.kind === 'credential')
              .map((p) => p.name),
      )
      const params = Object.fromEntries(
        Object.entries(n.data.spec.params ?? {}).filter(
          ([k, v]) => !personal.has(k) && v !== undefined,
        ),
      )

      return {
        ...n.data.spec,
        params: Object.keys(params).length > 0 ? params : undefined,
        position: { x: Math.round(n.position.x), y: Math.round(n.position.y) },
      }
    }),
    connections: edges
      .filter((e) => ids.has(e.source) && ids.has(e.target))
      .map((e) => ({
        from: e.source,
        fromPort: e.sourceHandle ?? defaultPort,
        to: e.target,
        toPort: e.targetHandle ?? defaultPort,
      })),
  }
}

export function clipText(c: Copied): string {
  return JSON.stringify({ [marker]: 1, ...c })
}

/** Nodes from the clipboard's text, or nothing when it holds none of the editor's. */
export function fromClip(text: string): Copied | undefined {
  let v: unknown

  try {
    v = JSON.parse(text)
  } catch {
    return undefined
  }

  if (!v || typeof v !== 'object' || !(marker in v)) return undefined

  const { nodes, connections } = v as Partial<Copied>
  if (!Array.isArray(nodes) || !Array.isArray(connections)) return undefined

  const node = (n: unknown): n is WorkflowNode =>
    !!n &&
    typeof n === 'object' &&
    typeof (n as WorkflowNode).id === 'string' &&
    typeof (n as WorkflowNode).type === 'string'
  const link = (c: unknown): c is Connection =>
    !!c &&
    typeof c === 'object' &&
    ['from', 'fromPort', 'to', 'toPort'].every(
      (k) => typeof (c as Record<string, unknown>)[k] === 'string',
    )

  if (!nodes.every(node) || !connections.every(link)) return undefined

  return { nodes, connections }
}

/**
 * Copied nodes as new ones beside those there, by offset, under IDs no node has, selected;
 * nodes of a type this user does not have are left out, and named in skipped.
 */
export function placed(
  c: Copied,
  existing: readonly FlowNode[],
  types: readonly NodeType[],
  offset: XYPosition,
): { nodes: FlowNode[]; edges: FlowEdge[]; skipped: string[] } {
  const known = new Set(types.map((t) => t.name))
  const taken = new Set(existing.map((n) => n.id))
  const ids = new Map<string, string>()
  const skipped = [...new Set(c.nodes.filter((n) => !known.has(n.type)).map((n) => n.type))]

  const nodes = c.nodes
    .filter((n) => known.has(n.type))
    .map((n) => {
      let id = n.id
      const base = n.id.replace(/\d+$/, '') || 'node'
      for (let i = 2; taken.has(id); i++) id = `${base}${i}`

      taken.add(id)
      ids.set(n.id, id)

      const at = n.position ?? { x: 0, y: 0 }

      return { ...n, id, position: { x: at.x + offset.x, y: at.y + offset.y } }
    })

  const connections = c.connections.flatMap((l) => {
    const from = ids.get(l.from)
    const to = ids.get(l.to)

    return from && to ? [{ ...l, from, to }] : []
  })

  const flow = toFlow({ nodes, connections }, types)

  return { nodes: flow.nodes.map((n) => ({ ...n, selected: true })), edges: flow.edges, skipped }
}
