import type { Edge, Node, XYPosition } from '@xyflow/react'

import type { Connection, NodeType, Schemas, WorkflowNode } from '@/api/client'

import { layout } from './layout'

export const defaultPort = 'main'

/** The port a node routes failed items to when it continues on failure. */
export const errorPort = 'error'

/** A node's settings; where it sits is the flow node's position. */
export type NodeSpec = Omit<WorkflowNode, 'position'>

export type WorkflowNodeData = {
  spec: NodeSpec
  type?: NodeType // missing when the server does not know the node's type
  // Ports that saved connections use but the type does not declare, so those edges still
  // have a handle to attach to.
  extraInputs: string[]
  extraOutputs: string[]
}

export type FlowNode = Node<WorkflowNodeData, 'workflow'>
export type FlowEdge = Edge

export type Definition = Schemas['WorkflowDefinition']

export function inputsOf(data: WorkflowNodeData): string[] {
  return unique([...(data.type?.inputs ?? []), ...data.extraInputs])
}

export function outputsOf(data: WorkflowNodeData): string[] {
  const outputs = [...(data.type?.outputs ?? []), ...paramPorts(data)]
  if (data.spec.continueOnFail) outputs.push(errorPort)

  return unique([...outputs, ...data.extraOutputs])
}

/**
 * The ports a node gets from the setting its type names in outputsParam, one per value,
 * such as a button each; the setting's default when it is unset.
 */
export function paramPorts(data: Pick<WorkflowNodeData, 'spec' | 'type'>): string[] {
  const name = data.type?.outputsParam
  if (!name) return []

  const value = data.spec.params?.[name] ?? data.type?.params.find((p) => p.name === name)?.default
  if (!Array.isArray(value)) return []

  return value.filter((v): v is string => typeof v === 'string' && v.trim() !== '')
}

/**
 * Follows a change to the ports node gets from a setting. Values renamed in place, the
 * count unchanged, take their connections along; connections from values that are gone
 * go too, as there is no port left to draw them from.
 */
export function followPorts(
  edges: readonly FlowEdge[],
  node: string,
  before: readonly string[],
  after: readonly string[],
): FlowEdge[] {
  const renamed = new Map<string, string>()
  if (before.length === after.length) {
    before.forEach((port, i) => {
      if (port !== after[i] && !renamed.has(port)) renamed.set(port, after[i])
    })
  }

  const gone = new Set(before.filter((p) => !after.includes(p)))

  const out = edges.flatMap((e): FlowEdge[] => {
    const port = e.sourceHandle ?? defaultPort
    if (e.source !== node) return [e]

    const to = renamed.get(port)
    if (to !== undefined) {
      return [
        edgeOf({ from: node, fromPort: to, to: e.target, toPort: e.targetHandle ?? defaultPort }),
      ]
    }

    return gone.has(port) ? [] : [e]
  })

  // Two values renamed alike leave one connection where there were two.
  return out.filter((e, i) => out.findIndex((o) => o.id === e.id) === i)
}

/**
 * Converts a saved workflow into React Flow nodes and edges. Workflows saved without a
 * layout, such as ones created through the API, are laid out automatically.
 */
export function toFlow(
  workflow: Pick<Definition, 'nodes' | 'connections'>,
  types: readonly NodeType[],
): { nodes: FlowNode[]; edges: FlowEdge[] } {
  const byName = new Map(types.map((t) => [t.name, t]))
  const auto = hasLayout(workflow.nodes) ? undefined : layout(workflow.nodes, workflow.connections)

  const nodes = workflow.nodes.map((n): FlowNode => {
    const type = byName.get(n.type)
    const data: WorkflowNodeData = { spec: specOf(n), type, extraInputs: [], extraOutputs: [] }

    for (const c of workflow.connections) {
      if (c.to === n.id && !inputsOf(data).includes(c.toPort)) data.extraInputs.push(c.toPort)
      if (c.from === n.id && !outputsOf(data).includes(c.fromPort))
        data.extraOutputs.push(c.fromPort)
    }

    return {
      id: n.id,
      type: 'workflow',
      position: auto?.get(n.id) ?? n.position ?? { x: 0, y: 0 },
      data,
    }
  })

  return { nodes, edges: workflow.connections.map(edgeOf) }
}

/** The definition to save: what the editor shows, positions rounded to whole pixels. */
export function toDefinition(
  name: string,
  nodes: readonly FlowNode[],
  edges: readonly FlowEdge[],
): Definition {
  return {
    name,
    nodes: nodes.map((n) => ({
      ...specOf(n.data.spec),
      position: { x: Math.round(n.position.x), y: Math.round(n.position.y) },
    })),
    connections: edges.map((e) => ({
      from: e.source,
      fromPort: e.sourceHandle ?? defaultPort,
      to: e.target,
      toPort: e.targetHandle ?? defaultPort,
    })),
  }
}

/** Whether two definitions describe the same workflow, however their fields are ordered. */
export function sameDefinition(a: Definition, b: Definition): boolean {
  return canonical(a) === canonical(b)
}

export function edgeOf(c: Connection): FlowEdge {
  return {
    id: `${c.from}.${c.fromPort}->${c.to}.${c.toPort}`,
    source: c.from,
    sourceHandle: c.fromPort,
    target: c.to,
    targetHandle: c.toPort,
    className: c.fromPort === errorPort ? 'error-edge' : undefined,
  }
}

/** A new node of type at position, with an ID no other node has. */
export function newNode(
  type: NodeType,
  position: XYPosition,
  nodes: readonly FlowNode[],
): FlowNode {
  const taken = new Set(nodes.map((n) => n.id))
  const base = type.name.split('.').at(-1) || 'node'

  let id = base
  for (let i = 2; taken.has(id); i++) id = `${base}${i}`

  return {
    id,
    type: 'workflow',
    position,
    data: { spec: { id, type: type.name }, type, extraInputs: [], extraOutputs: [] },
    selected: true,
  }
}

type Link = {
  source: string
  sourceHandle?: string | null
  target: string
  targetHandle?: string | null
}

/** Why link cannot be added to the graph, or undefined when it can. */
export function linkProblem(link: Link, edges: readonly FlowEdge[]): string | undefined {
  if (link.source === link.target) return 'A node cannot connect to itself'

  const fromPort = link.sourceHandle ?? defaultPort
  const toPort = link.targetHandle ?? defaultPort

  const duplicate = edges.some(
    (e) =>
      e.source === link.source &&
      (e.sourceHandle ?? defaultPort) === fromPort &&
      e.target === link.target &&
      (e.targetHandle ?? defaultPort) === toPort,
  )
  if (duplicate) return 'These ports are already connected'

  if (reaches(link.target, link.source, edges)) return 'Workflows cannot loop'

  return undefined
}

/** Whether from can reach to by following edges. */
function reaches(from: string, to: string, edges: readonly FlowEdge[]): boolean {
  const seen = new Set<string>()
  const queue = [from]

  while (queue.length > 0) {
    const id = queue.shift()!
    if (id === to) return true
    if (seen.has(id)) continue

    seen.add(id)
    for (const e of edges) if (e.source === id) queue.push(e.target)
  }

  return false
}

function specOf(n: NodeSpec): NodeSpec {
  const params = Object.fromEntries(
    Object.entries(n.params ?? {}).filter(([, value]) => value !== undefined),
  )

  return {
    id: n.id,
    type: n.type,
    ...(n.name ? { name: n.name } : {}),
    ...(Object.keys(params).length > 0 && { params }),
    ...(n.continueOnFail && { continueOnFail: true }),
    ...(n.notes?.trim() ? { notes: n.notes } : {}),
  }
}

function hasLayout(nodes: readonly WorkflowNode[]): boolean {
  return nodes.some((n) => n.position && (n.position.x !== 0 || n.position.y !== 0))
}

function unique(ports: string[]): string[] {
  return [...new Set(ports)]
}

// canonical serialises a definition with every object's keys sorted and absent fields
// left out, so equal workflows give equal strings.
function canonical(def: Definition): string {
  const normalised: Definition = {
    name: def.name,
    nodes: def.nodes.map((n) => ({ ...specOf(n), position: n.position ?? { x: 0, y: 0 } })),
    connections: def.connections,
  }

  return JSON.stringify(normalised, (_, value: unknown) =>
    value && typeof value === 'object' && !Array.isArray(value)
      ? Object.fromEntries(Object.entries(value).sort(([a], [b]) => a.localeCompare(b)))
      : value,
  )
}
