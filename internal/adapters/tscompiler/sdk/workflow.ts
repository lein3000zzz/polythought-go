/**
 * @polythought/workflow: a workflow as code (D64). Declare each node once, then connect
 * them, one line per connection:
 *
 *   import { workflow } from "@polythought/workflow"
 *
 *   export default workflow("Spread watch", (w) => {
 *     const manual = w.trigger("manual", "trigger.manual")
 *     const tight = w.node("tight", "logic.if", { field: "spread", operator: "lt", value: "0.02" })
 *     const log = w.node("log", "output.log", { message: "Tight: {{ question }}" })
 *
 *     manual.to(tight)
 *     tight.port("true").to(log)
 *   })
 *
 * Settings are typed by node type, and so are port names (D73). This file is the package
 * wherever workflow code is: the editor loads it as @polythought/workflow, with the
 * user's node types, and the server bundles it with the code it runs for the Code tab's
 * Apply (D74), whose workflow goes on the canvas (D66).
 */
import type { NodeTypes } from '@polythought/sdk'

/** Every node type, by name. */
export type TypeName = keyof NodeTypes & string

/** The node types that start workflows. */
export type TriggerType = {
  [T in TypeName]: NodeTypes[T]['trigger'] extends true ? T : never
}[TypeName]

/** The node types items go through. */
export type StepType = Exclude<TypeName, TriggerType>

type Settings<T extends TypeName> = NodeTypes[T]['settings']

/** A node's name, notes about it, and whether failed items go out of its error port. */
export interface NodeOptions {
  name?: string
  notes?: string
  continueOnFail?: boolean
}

// The settings may be left out when the type needs none of them (D72).
type Args<T extends TypeName> =
  Record<string, never> extends Settings<T>
    ? [settings?: Settings<T>, options?: NodeOptions]
    : Partial<Settings<T>> extends Settings<T>
      ? [settings?: Settings<T>, options?: NodeOptions]
      : [settings: Settings<T>, options?: NodeOptions]

/** An input port of a node, to connect to: main, or another its type has. */
export interface Input {
  readonly node: string
  readonly port: string
}

/** An output port of a node, to connect from. */
export interface Output {
  readonly node: string
  readonly port: string
  /** Sends this port's items to target: a node's main input, or another of its inputs. */
  to(target: Node | Input): void
}

/** A node of the workflow. */
export interface Node<T extends TypeName = TypeName> {
  readonly id: string
  readonly type: T
  /** Sends the items of its main output to target. */
  to(target: Node | Input): void
  /** One of its output ports, or error, where failed items go with continueOnFail on. */
  port(name: NodeTypes[T]['outputs'] | 'error'): Output
  /** One of its input ports, for a type with more than main. */
  input(name: NodeTypes[T]['inputs']): Input
}

/** What workflow gives the function it is called with, to declare and connect nodes. */
export interface Builder {
  /** A trigger: where runs start. */
  trigger<T extends TriggerType>(id: string, type: T, ...args: Args<T>): Node<T>
  /** A step that items go through. */
  node<T extends StepType>(id: string, type: T, ...args: Args<T>): Node<T>
}

/** A workflow as the server takes it: what the Code tab puts on the canvas. */
export interface Workflow {
  name: string
  nodes: {
    id: string
    type: string
    name?: string
    notes?: string
    params?: Record<string, unknown>
    continueOnFail?: boolean
  }[]
  connections: { from: string; fromPort: string; to: string; toPort: string }[]
}

const main = 'main'

/**
 * Builds a workflow: build declares its nodes on w, then connects them. Nodes keep the
 * order they are declared in, and connections the order they are made in.
 */
export function workflow(name: string, build: (w: Builder) => void): Workflow {
  const out: Workflow = { name, nodes: [], connections: [] }
  const mine = new Set<string>()

  const connect = (from: string, fromPort: string) => (target: Node | Input) => {
    const to = 'id' in target ? target.id : target.node
    const toPort = 'id' in target ? main : target.port

    if (!mine.has(to)) {
      throw new Error(
        `${from} connects to ${JSON.stringify(to)}, which is not a node of this workflow`,
      )
    }

    out.connections.push({ from, fromPort, to, toPort })
  }

  const declare = (id: string, type: string, settings?: object, options?: NodeOptions): Node => {
    if (typeof id !== 'string' || id === '') throw new Error('a node needs an ID')
    if (mine.has(id)) throw new Error(`node ${JSON.stringify(id)} is declared twice`)
    if (typeof type !== 'string' || type === '') {
      throw new Error(`node ${JSON.stringify(id)} needs a type`)
    }

    mine.add(id)

    const node: Workflow['nodes'][number] = { id, type }
    if (options?.name) node.name = options.name
    if (options?.notes) node.notes = options.notes
    if (settings && Object.keys(settings).length > 0) node.params = { ...settings }
    if (options?.continueOnFail) node.continueOnFail = true
    out.nodes.push(node)

    return {
      id,
      type,
      to: connect(id, main),
      port: (name: string): Output => ({ node: id, port: name, to: connect(id, name) }),
      input: (name: string): Input => ({ node: id, port: name }),
    } as Node
  }

  build({
    trigger: (id, type, ...[settings, options]) => declare(id, type, settings, options),
    node: (id, type, ...[settings, options]) => declare(id, type, settings, options),
  } as Builder)

  return out
}
