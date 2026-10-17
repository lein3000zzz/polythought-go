// The printer of workflow code (D64): the graph as @polythought/workflow builds it, the
// same way every time. Nodes and connections keep their order, so running the code gives
// the graph back as it was. Positions are not code (A4): applying keeps them by node ID.
import type { NodeType } from '@/api/client'
import { defaultPort, type Definition, type NodeSpec } from '@/features/workflow/graph'

/** Past this, a node's settings and options go one per line. */
const width = 100

const indent = '  '

/** Workflow code that builds def, its node types telling triggers from the rest. */
export function printWorkflow(
  def: Pick<Definition, 'name' | 'nodes' | 'connections'>,
  types: readonly NodeType[],
): string {
  const byName = new Map(types.map((t) => [t.name, t]))
  const names = variableNames(def.nodes.map((n) => n.id))

  const lines = [
    'import { workflow } from "@polythought/workflow"',
    '',
    `export default workflow(${text(def.name)}, (w) => {`,
  ]

  for (const n of def.nodes) lines.push(...printNode(n, names.get(n.id)!, byName.get(n.type)))

  if (def.connections.length > 0 && def.nodes.length > 0) lines.push('')

  for (const c of def.connections) {
    const from = names.get(c.from) ?? c.from
    const to = names.get(c.to) ?? c.to
    const source = c.fromPort === defaultPort ? from : `${from}.port(${text(c.fromPort)})`
    const target = c.toPort === defaultPort ? to : `${to}.input(${text(c.toPort)})`

    lines.push(`${indent}${source}.to(${target})`)
  }

  lines.push('})', '')

  return lines.join('\n')
}

type Entry = [key: string, value: unknown]

function printNode(n: NodeSpec, name: string, type: NodeType | undefined): string[] {
  const call = type?.trigger ? 'trigger' : 'node'
  const settings = ordered(n.params ?? {}, type)
  const options: Entry[] = [
    ...(n.name ? [['name', n.name] as Entry] : []),
    ...(n.notes ? [['notes', n.notes] as Entry] : []),
    ...(n.continueOnFail ? [['continueOnFail', true] as Entry] : []),
  ]

  // Settings are there when options are, if only as {}.
  const objects = options.length > 0 ? [settings, options] : settings.length > 0 ? [settings] : []
  const head = `${indent}const ${name} = w.${call}(${text(n.id)}, ${text(n.type)}`

  const inline = `${head}${objects.map((o) => `, ${object(o)}`).join('')})`
  if (fits(inline)) return [inline]

  // The settings one per line, then the options after them, inline if they fit.
  let out = `${head}, ${settings.length === 0 ? '{}' : broken(settings)}`
  if (options.length > 0) {
    const inlined = object(options)
    const after = `${out}, ${inlined})`
    const last = after.slice(after.lastIndexOf('\n') + 1)
    out = fits(last) && !inlined.includes('\n') ? after : `${out}, ${broken(options)})`
  } else {
    out += ')'
  }

  return out.split('\n')
}

function fits(line: string): boolean {
  return line.length <= width && !line.includes('\n')
}

/** Settings in the order the type lists them, then any others as they are. */
function ordered(params: Record<string, unknown>, type: NodeType | undefined): Entry[] {
  const known = (type?.params ?? []).map((p) => p.name).filter((k) => k in params)
  const rest = Object.keys(params).filter((k) => !known.includes(k))

  return [...known, ...rest]
    .filter((k) => params[k] !== undefined)
    .map((k): Entry => [k, params[k]])
}

function broken(entries: Entry[]): string {
  const inner = indent + indent
  return `{\n${entries.map(([k, v]) => `${inner}${key(k)}: ${brokenValue(v, inner)},`).join('\n')}\n${indent}}`
}

// A table, a list of rows, goes a row per line; anything else as it is.
function brokenValue(v: unknown, at: string): string {
  const rows =
    Array.isArray(v) &&
    v.length > 0 &&
    v.every((r) => r !== null && typeof r === 'object' && !Array.isArray(r))
  if (!rows) return value(v)

  return `[\n${v.map((r) => `${at}${indent}${value(r)},`).join('\n')}\n${at}]`
}

function object(entries: Entry[]): string {
  if (entries.length === 0) return '{}'
  return `{ ${entries.map(([k, v]) => `${key(k)}: ${value(v)}`).join(', ')} }`
}

function value(v: unknown): string {
  if (typeof v === 'string') return text(v)
  if (Array.isArray(v)) return `[${v.map(value).join(', ')}]`
  if (v !== null && typeof v === 'object') return object(Object.entries(v))

  return JSON.stringify(v) ?? 'undefined'
}

const identifier = /^[\p{ID_Start}$_][\p{ID_Continue}$\u200c\u200d]*$/u

function key(k: string): string {
  // As a plain key, __proto__ would set the object's prototype instead.
  if (k === '__proto__') return '["__proto__"]'
  return identifier.test(k) ? k : JSON.stringify(k)
}

/**
 * A string literal: in double quotes, or for text of several lines, like code, a
 * template literal that keeps its lines as they are.
 */
function text(s: string): string {
  if (!s.includes('\n')) return JSON.stringify(s)

  // A carriage return in a template is read as a line feed: escaped, it stays one.
  return `\`${s.replace(/\\/g, '\\\\').replace(/`/g, '\\`').replace(/\$\{/g, '\\${').replace(/\r/g, '\\r')}\``
}

// Names a variable cannot have, or should not: the words of JavaScript, and the names the
// code itself uses.
const reserved = new Set(
  (
    'break case catch class const continue debugger default delete do else enum export ' +
    'extends false finally for function if import in instanceof new null return super ' +
    'switch this throw true try typeof var void while with yield let static implements ' +
    'interface package private protected public await arguments eval undefined NaN ' +
    'Infinity w workflow'
  ).split(' '),
)

/** A variable for each node, named after its ID: order-books is orderBooks. */
function variableNames(ids: readonly string[]): Map<string, string> {
  const taken = new Set<string>()
  const out = new Map<string, string>()

  for (const id of ids) {
    const words = id.split(/[^\p{ID_Continue}$]+/u).filter(Boolean)
    let base = words.map((w, i) => (i === 0 ? w : w[0].toUpperCase() + w.slice(1))).join('')
    if (!identifier.test(base)) base = `node${base[0]?.toUpperCase() ?? ''}${base.slice(1)}`

    let name = base
    for (let i = 2; taken.has(name) || reserved.has(name); i++) name = `${base}${i}`

    taken.add(name)
    out.set(id, name)
  }

  return out
}
