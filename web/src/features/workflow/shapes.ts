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
  const ports = receivedPorts(nodes, edges)

  return new Map(nodes.map((n) => [n.id, ports.get(n.id)?.get(defaultPort)]))
}

/** For each node, the shape of the items each of its inputs receives. */
export function receivedPorts(
  nodes: readonly FlowNode[],
  edges: readonly FlowEdge[],
): Map<string, Map<string, Shape>> {
  const received = new Map<string, Map<string, Shape>>()
  const emitted = new Map<string, Shape>()

  for (const node of topological(nodes, edges)) {
    const from = new Map<string, Shape[]>()

    for (const e of edges.filter((e) => e.target === node.id)) {
      const port = e.targetHandle ?? defaultPort
      const shape =
        (e.sourceHandle ?? defaultPort) === errorPort
          ? failed(received.get(e.source)) // the ones received
          : emitted.get(e.source)
      from.set(port, [...(from.get(port) ?? []), shape])
    }

    const ports = new Map([...from].map(([port, shapes]) => [port, merge(shapes)]))
    received.set(node.id, ports)
    emitted.set(node.id, emits(node.data, ports))
  }

  return received
}

/** The shape of the items a node fails: those of its inputs. */
function failed(ports?: Map<string, Shape>): Shape {
  if (!ports || ports.size === 0) return undefined
  if (ports.size === 1) return [...ports.values()][0]

  return merge([...ports.keys()].sort().map((p) => ports.get(p)))
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

function emits(data: WorkflowNodeData, ports: Map<string, Shape>): Shape {
  const items = data.type?.items
  const received = ports.get(defaultPort)
  if (!items) return undefined
  if (items.merged) return merged(data, ports)
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

  if (items.split) {
    const path = data.spec.params?.[items.split]
    if (typeof path !== 'string' || !received) return undefined

    return split(received, path)
  }

  return [...items.fields] // known, even with no fields
}

/** A setting as set, or its default. */
function settingOf(data: WorkflowNodeData, name: string): unknown {
  return data.spec.params?.[name] ?? data.type?.params.find((p) => p.name === name)?.default
}

/**
 * What a node that merges two inputs sends (D119): A's items, A's or B's, or A's with
 * B's item under a field of its own.
 */
function merged(data: WorkflowNodeData, ports: Map<string, Shape>): Shape {
  const [inA, inB] = data.type?.inputs ?? []
  const a = ports.get(inA)
  const b = ports.get(inB)

  switch (settingOf(data, data.type?.items?.merged ?? '')) {
    case 'both':
    case 'onlyA':
      return a
    case 'either':
    case 'onlyOne':
      return merge([a, b])
    case 'join': {
      const name = settingOf(data, data.type?.items?.joinedAs ?? '')
      if (!a || typeof name !== 'string' || name === '' || name.includes('{{')) return undefined

      const joined: ItemField = b
        ? { name, kind: 'object', optional: false, nullable: false, fields: b }
        : { name, kind: 'any', optional: false, nullable: false }

      return [...a.filter((f) => f.name !== name), joined]
    }
  }

  return undefined
}

/**
 * An item of each entry of the list at path (D132): an object's fields at the top with
 * the rest but the list, anything else in the list's place. Unknown when the list's
 * entries are.
 */
function split(received: ItemField[], path: string): Shape {
  const list = lookup(received, path)
  if (!list || list.kind !== 'list' || !list.of) return undefined

  const steps = path.split('.')
  const entry = list.of

  if (entry.kind === 'object') {
    const fields = entry.fields ?? []
    const names = new Set(fields.map((f) => f.name))
    return [...replaced(received, steps).filter((f) => !names.has(f.name)), ...fields]
  }

  return replaced(received, steps, {
    ...entry,
    name: steps[steps.length - 1],
    optional: list.optional,
  })
}

/** shape with the field at steps, through objects, set to field, or left out without one. */
function replaced(shape: ItemField[], steps: string[], field?: ItemField): ItemField[] {
  return shape.flatMap((f) => {
    if (f.name !== steps[0]) return [f]
    if (steps.length === 1) return field ? [field] : []
    if (f.kind === 'object')
      return [{ ...f, fields: replaced(f.fields ?? [], steps.slice(1), field) }]

    return [f] // through a list: the list stays, as at run time
  })
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
