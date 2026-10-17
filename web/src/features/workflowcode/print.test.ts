import * as builder from '@polythought/workflow'
import ts from 'typescript'
import { describe, expect, it } from 'vitest'

import type { NodeType } from '@/api/client'
import type { Definition } from '@/features/workflow/graph'
import { nodeTypes } from '@/test/fixtures'

import { printWorkflow } from './print'

// run runs workflow code as the server does, here with TypeScript's own transpiler.
function run(code: string): unknown {
  const js = ts.transpileModule(code, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText

  const exports: { default?: unknown } = {}
  const require = (name: string) => {
    if (name !== '@polythought/workflow') throw new Error(`no ${name} here`)
    return builder
  }
  new Function('require', 'exports', js)(require, exports)

  return exports.default
}

// roundTrip prints def and runs the code: the graph it builds is def's, positions aside.
function roundTrip(def: Definition, types: readonly NodeType[] = nodeTypes) {
  const code = printWorkflow(def, types)
  const { nodes, ...rest } = def

  expect(run(code), code).toEqual({
    ...rest,
    nodes: nodes.map((n) => {
      const spec = { ...n }
      delete spec.position
      return spec
    }),
  })

  return code
}

describe('printing workflow code', () => {
  it('reads as written: nodes once, then their connections', () => {
    const code = printWorkflow(
      {
        name: 'Spread watch',
        nodes: [
          { id: 'manual', type: 'trigger.manual', position: { x: 0, y: 0 } },
          {
            id: 'tight',
            type: 'logic.if',
            name: 'Tight spread',
            // Not in the type's order: printed in it.
            params: { value: '0.02', operator: 'lt', field: 'spread' },
            position: { x: 300, y: 0 },
          },
          {
            id: 'log',
            type: 'output.log',
            params: { message: 'Tight: {{ question }}, at {{ spread }}, worth a closer look' },
          },
          { id: 'failed', type: 'output.log', continueOnFail: true },
        ],
        connections: [
          { from: 'manual', fromPort: 'main', to: 'tight', toPort: 'main' },
          { from: 'tight', fromPort: 'true', to: 'log', toPort: 'main' },
          { from: 'tight', fromPort: 'error', to: 'failed', toPort: 'b' },
        ],
      },
      nodeTypes,
    )

    expect(code).toBe(`import { workflow } from "@polythought/workflow"

export default workflow("Spread watch", (w) => {
  const manual = w.trigger("manual", "trigger.manual")
  const tight = w.node("tight", "logic.if", {
    field: "spread",
    operator: "lt",
    value: "0.02",
  }, { name: "Tight spread" })
  const log = w.node("log", "output.log", {
    message: "Tight: {{ question }}, at {{ spread }}, worth a closer look",
  })
  const failed = w.node("failed", "output.log", {}, { continueOnFail: true })

  manual.to(tight)
  tight.port("true").to(log)
  tight.port("error").to(failed.input("b"))
})
`)
  })

  it('gives the graph back, whatever its settings hold', () => {
    roundTrip({
      name: 'Odd "names" and \\ slashes',
      nodes: [
        { id: 'manual', type: 'trigger.manual', notes: 'Starts it\nby hand' },
        {
          id: 'code',
          type: 'code.filter',
          params: {
            code: 'export function keep(item) {\r\n  return `${item.a}` !== "\\n" // ${not}\n}\n',
          },
        },
        {
          id: 'ask',
          type: 'telegram.ask',
          params: {
            message: 'Buy? ✓',
            buttons: ['Yes', 'No'],
            // As JSON reads it: an object literal's __proto__ is its prototype.
            nested: {
              deep: [1, 2.5, null, true, JSON.parse('{"with-dash": "x", "__proto__": 1}')],
            },
          },
        },
        { id: 'empty', type: 'logic.unknown' }, // a type the editor does not know
      ],
      connections: [
        { from: 'manual', fromPort: 'main', to: 'code', toPort: 'main' },
        { from: 'code', fromPort: 'main', to: 'ask', toPort: 'main' },
        { from: 'ask', fromPort: 'Yes', to: 'empty', toPort: 'main' },
      ],
    })
  })

  it('names variables after IDs, apart from each other and from the words of JavaScript', () => {
    const code = roundTrip({
      name: 'w',
      nodes: [
        { id: 'order-books', type: 'polymarket.orderbook' },
        { id: 'orderBooks', type: 'output.log' },
        { id: 'new', type: 'output.log' },
        { id: 'w', type: 'output.log' },
        { id: '1st', type: 'output.log' },
        { id: '--', type: 'output.log' },
      ],
      connections: [
        { from: 'order-books', fromPort: 'main', to: 'new', toPort: 'main' },
        { from: 'orderBooks', fromPort: 'main', to: 'w', toPort: 'main' },
      ],
    })

    expect(code).toContain('const orderBooks = w.node("order-books"')
    expect(code).toContain('const orderBooks2 = w.node("orderBooks"')
    expect(code).toContain('const new2 = w.node("new"')
    expect(code).toContain('const w2 = w.node("w"')
    expect(code).toContain('const node1st = w.node("1st"')
    expect(code).toContain('const node = w.node("--"')
  })

  it('prints a workflow with nothing in it', () => {
    expect(roundTrip({ name: 'Empty', nodes: [], connections: [] })).toBe(
      'import { workflow } from "@polythought/workflow"\n\nexport default workflow("Empty", (w) => {\n})\n',
    )
  })
})
