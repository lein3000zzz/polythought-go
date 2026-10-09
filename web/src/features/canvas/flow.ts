import type { Edge, Node } from '@xyflow/react'

import type { Connection, NodeType, Workflow, WorkflowNode } from '@/api/client'

import { layout } from './layout'

/** The port every node can route failed items to when it continues on failure. */
export const errorPort = 'error'
export const defaultPort = 'main'

export type WorkflowNodeData = {
  spec: WorkflowNode
  type?: NodeType // missing when the server no longer knows the node's type
  inputs: string[]
  outputs: string[]
}

export type FlowNode = Node<WorkflowNodeData, 'workflow'>

/**
 * Converts a workflow into React Flow nodes and edges. Workflows saved without a layout,
 * such as ones created through the API, are laid out automatically.
 */
export function toFlow(workflow: Workflow, types: readonly NodeType[]) {
  const byName = new Map(types.map((t) => [t.name, t]))
  const auto = hasLayout(workflow.nodes) ? undefined : layout(workflow.nodes, workflow.connections)

  const nodes: FlowNode[] = workflow.nodes.map((spec) => {
    const type = byName.get(spec.type)
    const outputs = [...(type?.outputs ?? [])]

    if (spec.continueOnFail) outputs.push(errorPort)

    return {
      id: spec.id,
      type: 'workflow',
      position: auto?.get(spec.id) ?? spec.position ?? { x: 0, y: 0 },
      data: {
        spec,
        type,
        inputs: withConnected(type?.inputs ?? [], workflow.connections, (c) =>
          c.to === spec.id ? c.toPort : undefined,
        ),
        outputs: withConnected(outputs, workflow.connections, (c) =>
          c.from === spec.id ? c.fromPort : undefined,
        ),
      },
    }
  })

  const edges: Edge[] = workflow.connections.map((c) => ({
    id: `${c.from}.${c.fromPort}->${c.to}.${c.toPort}`,
    source: c.from,
    sourceHandle: c.fromPort,
    target: c.to,
    targetHandle: c.toPort,
    className: c.fromPort === errorPort ? 'error-edge' : undefined,
  }))

  return { nodes, edges }
}

function hasLayout(nodes: readonly WorkflowNode[]): boolean {
  return nodes.some((n) => n.position && (n.position.x !== 0 || n.position.y !== 0))
}

// withConnected adds ports that connections use but the node type does not declare, so
// every edge has a handle to attach to even when the type changed or is unknown.
function withConnected(
  declared: readonly string[],
  connections: readonly Connection[],
  portOf: (c: Connection) => string | undefined,
): string[] {
  const ports = [...declared]

  for (const c of connections) {
    const port = portOf(c)
    if (port !== undefined && !ports.includes(port)) ports.push(port)
  }

  return ports
}
