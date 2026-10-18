import type { ParamSpec, Schemas } from '@/api/client'

import {
  defaultPort,
  errorPort,
  type FlowEdge,
  type FlowNode,
  type WorkflowNodeData,
} from './graph'

// The items at each point of a workflow, as the server works them out when it saves
// (engine.Received and CheckFields): the editor does the same, so it can offer the fields
// a setting can name and point at one the items do not have before saving.

export type ItemField = Schemas['ItemField']

/** The fields of items at some point; undefined when nobody knows. */
export type Shape = ItemField[] | undefined

/** For each node, the shape of the items its main input receives. */
export function receivedShapes(
  nodes: readonly FlowNode[],
  edges: readonly FlowEdge[],
): Map<string, Shape> {
  const received = new Map<string, Shape>()
  const emitted = new Map<string, Shape>()

  for (const node of topological(nodes, edges)) {
    const from = edges
      .filter((e) => e.target === node.id && (e.targetHandle ?? defaultPort) === defaultPort)
      .map((e) =>
        (e.sourceHandle ?? defaultPort) === errorPort
          ? received.get(e.source) // failed items are the ones received
          : emitted.get(e.source),
      )

    const shape = merge(from)
    received.set(node.id, shape)
    emitted.set(node.id, emits(node.data, shape))
  }

  return received
}

// topological orders nodes so each comes after the ones that feed it. The editor allows
// no loops; nodes in one anyway are left out.
function topological(nodes: readonly FlowNode[], edges: readonly FlowEdge[]): FlowNode[] {
  const waiting = new Map(nodes.map((n) => [n.id, 0]))
  for (const e of edges) waiting.set(e.target, (waiting.get(e.target) ?? 0) + 1)

  const ready = nodes.filter((n) => waiting.get(n.id) === 0)
  const out: FlowNode[] = []

  while (ready.length > 0) {
    const n = ready.shift()!
    out.push(n)

    for (const e of edges) {
      if (e.source !== n.id) continue

      const left = (waiting.get(e.target) ?? 0) - 1
      waiting.set(e.target, left)

      const next = nodes.find((m) => m.id === e.target)
      if (left === 0 && next) ready.push(next)
    }
  }

  return out
}

function emits(data: WorkflowNodeData, received: Shape): Shape {
  const items = data.type?.items
  if (!items) return undefined
  // Passed on, with the fields it adds.
  if (items.passed) {
    if (!received || items.fields.length === 0) return received
    const names = new Set(items.fields.map((f) => f.name))
    return [...received.filter((f) => !names.has(f.name)), ...items.fields]
  }

  if (items.picked) {
    const value =
      data.spec.params?.[items.picked] ??
      data.type?.params.find((p) => p.name === items.picked)?.default
    if (!Array.isArray(value) || !received) return undefined

    return pick(
      received,
      value.filter((v): v is string => typeof v === 'string'),
    )
  }

  return [...items.fields] // known, even with no fields
}

function pick(received: ItemField[], paths: string[]): ItemField[] {
  return paths.map((path) => {
    const found = lookup(received, path)
    if (!found) return { name: path, kind: 'any', optional: true, nullable: false }

    return { ...found, name: path, optional: optionalAt(received, path) }
  })
}

function optionalAt(shape: ItemField[], path: string): boolean {
  const steps = path.split('.')

  return steps.some((_, i) => {
    const f = lookup(shape, steps.slice(0, i + 1).join('.'))
    return f && (f.optional || f.nullable || f.kind === 'list') // a list may be shorter
  })
}

function merge(shapes: Shape[]): Shape {
  if (shapes.length === 0 || shapes.some((s) => s === undefined)) return undefined

  const [first, ...rest] = shapes as ItemField[][]
  let out = first.map((f) => ({ ...f }))

  for (const s of rest) {
    out = out.map((f) => {
      const other = s.find((o) => o.name === f.name)
      if (!other) return { ...f, optional: true }
      if (other.kind !== f.kind) {
        return {
          name: f.name,
          kind: 'any',
          optional: f.optional || other.optional,
          nullable: false,
        }
      }

      return {
        ...f,
        optional: f.optional || other.optional,
        nullable: f.nullable || other.nullable,
      }
    })

    for (const f of s) {
      if (!out.some((o) => o.name === f.name)) out.push({ ...f, optional: true })
    }
  }

  return out
}

/**
 * The field at a path like spread or outcomes.0.price, as nodes read them: names through
 * objects, indexes through lists. Anything under a field of any kind may be there.
 */
export function lookup(shape: ItemField[], path: string): ItemField | undefined {
  let cur: ItemField = { name: '', kind: 'object', optional: false, nullable: false, fields: shape }

  for (const step of path.split('.')) {
    if (cur.kind === 'any') return { name: '', kind: 'any', optional: false, nullable: false }

    if (cur.kind === 'object') {
      const next = cur.fields?.find((f) => f.name === step)
      if (!next) return undefined
      cur = next
    } else if (cur.kind === 'list' && /^\d+$/.test(step) && cur.of) {
      cur = cur.of
    } else {
      return undefined
    }
  }

  return cur
}

/** A field a setting can name, with its kind and what it is. */
export type FieldChoice = { path: string; kind: ItemField['kind']; description?: string }

/**
 * The fields a field setting can name in shape: nested ones with dots, a list's elements
 * by index, the first standing for all; only the kinds it takes, when it says.
 */
export function fieldChoices(shape: ItemField[], kinds?: ParamSpec['fieldKinds']): FieldChoice[] {
  const out: FieldChoice[] = []

  const walk = (fields: readonly ItemField[], prefix: string) => {
    for (const f of fields) {
      const path = prefix + f.name
      if (!kinds?.length || f.kind === 'any' || kinds.includes(f.kind)) {
        out.push({ path, kind: f.kind, description: f.description })
      }

      if (f.kind === 'object' && f.fields) walk(f.fields, `${path}.`)
      if (f.kind === 'list' && f.of?.kind === 'object' && f.of.fields)
        walk(f.of.fields, `${path}.0.`)
    }
  }
  walk(shape, '')

  return out
}

/**
 * What is wrong with a field setting's value for items of shape, in the server's words,
 * or undefined when nothing is, or nobody knows.
 */
export function fieldProblem(shape: Shape, spec: ParamSpec, value: unknown): string | undefined {
  if (!shape || !spec.field) return undefined

  const paths = Array.isArray(value) ? value : typeof value === 'string' ? [value] : []

  for (const path of paths) {
    if (typeof path !== 'string' || path === '') continue

    const found = lookup(shape, path)
    if (!found) {
      const near = closest(
        path,
        fieldChoices(shape).map((c) => c.path),
      )
      return `The items it receives have no field "${path}"${near ? `; "${near}"?` : ''}`
    }

    const kinds = spec.fieldKinds ?? []
    if (kinds.length > 0 && found.kind !== 'any' && !kinds.includes(found.kind)) {
      return `Field "${path}" is ${found.kind}, and this takes ${kinds.join(' or ')}`
    }
  }

  return undefined
}

// closest is the path most like path: the same but for letter case, or a couple of
// letters off.
function closest(path: string, paths: string[]): string | undefined {
  let best: string | undefined
  let bestDistance = 3

  for (const p of paths) {
    if (p.toLowerCase() === path.toLowerCase()) return p

    const d = distance(p.toLowerCase(), path.toLowerCase())
    if (d < bestDistance) {
      best = p
      bestDistance = d
    }
  }

  return best
}

function distance(a: string, b: string): number {
  let prev = Array.from({ length: b.length + 1 }, (_, j) => j)

  for (let i = 1; i <= a.length; i++) {
    const cur = [i]
    for (let j = 1; j <= b.length; j++) {
      cur[j] = Math.min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + (a[i - 1] === b[j - 1] ? 0 : 1))
    }
    prev = cur
  }

  return prev[b.length]
}
