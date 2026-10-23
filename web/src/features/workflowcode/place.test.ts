import { readdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

import type { XYPosition } from '@xyflow/react'
import { describe, expect, it } from 'vitest'

import type { Definition } from '@/features/workflow/graph'
import { layout } from '@/features/workflow/layout'

import { place } from './place'

const link = (from: string, to: string, fromPort = 'main') => ({
  from,
  fromPort,
  to,
  toPort: 'main',
})

function positions(def: Definition) {
  return Object.fromEntries(def.nodes.map((n) => [n.id, n.position]))
}

describe('placing what workflow code built', () => {
  it('keeps the nodes the canvas had where they were, and puts new ones beside theirs', () => {
    const built: Definition = {
      name: 'w',
      nodes: [
        { id: 'manual', type: 'trigger.manual' },
        { id: 'tight', type: 'logic.if' },
        { id: 'log', type: 'output.log' }, // new, after tight
        { id: 'more', type: 'output.log' }, // new, after log
        { id: 'also', type: 'output.log' }, // new, after tight too: under log
        { id: 'before', type: 'output.log' }, // new, feeding manual
        { id: 'alone', type: 'output.log' }, // new, connected to nothing
      ],
      connections: [
        link('manual', 'tight'),
        link('tight', 'log', 'true'),
        link('log', 'more'),
        link('tight', 'also', 'false'),
        link('before', 'manual'),
      ],
    }

    const before = new Map([
      ['manual', { x: 0, y: 0 }],
      ['tight', { x: 320, y: 10 }],
      ['gone', { x: 900, y: 900 }], // the code took it away
    ])

    expect(positions(place(built, before))).toEqual({
      manual: { x: 0, y: 0 },
      tight: { x: 320, y: 10 },
      log: { x: 620, y: 10 },
      more: { x: 920, y: 10 },
      also: { x: 620, y: 150 },
      before: { x: -300, y: 0 },
      alone: { x: -300, y: 150 },
    })
  })

  it('leaves a workflow all new for the canvas to lay out', () => {
    const built: Definition = {
      name: 'w',
      nodes: [{ id: 'a', type: 'trigger.manual', position: { x: 5, y: 5 } }],
      connections: [],
    }

    expect(positions(place(built, new Map([['b', { x: 1, y: 1 }]])))).toEqual({
      a: { x: 0, y: 0 },
    })
  })
})

// The server places an agent's nodes too (D403), with a Go port of layout and place. These
// fixtures hold the two to the same places: each is nodes, connections and the places some
// had, beside what layout and place give them. WRITE_PLACE_FIXTURES=1 writes them anew.
describe('placing shared with the server', () => {
  // Tests run in web/.
  const fixtures = join(process.cwd(), '../internal/core/workflowcode/testdata/place')
  const names = readdirSync(fixtures).filter((f) => f.endsWith('.json') && !f.endsWith('.out.json'))

  it.each(names)('places %s as the fixture has it', (file) => {
    const { nodes, connections, before } = JSON.parse(
      readFileSync(join(fixtures, file), 'utf8'),
    ) as {
      nodes: { id: string }[]
      connections: { from: string; to: string }[]
      before?: Record<string, XYPosition>
    }
    const def = {
      name: 'w',
      nodes: nodes.map((n) => ({ ...n, type: 'output.log' })),
      connections: connections.map((c) => ({ ...c, fromPort: 'main', toPort: 'main' })),
    } as unknown as Definition

    const placed = place(def, new Map(Object.entries(before ?? {})))
    const got = {
      layout: Object.fromEntries(layout(nodes, connections)),
      place: Object.fromEntries(placed.nodes.map((n) => [n.id, n.position])),
    }
    const want = join(fixtures, file.replace(/\.json$/, '.out.json'))

    if (process.env.WRITE_PLACE_FIXTURES) writeFileSync(want, JSON.stringify(got, null, 2) + '\n')

    expect(got).toEqual(JSON.parse(readFileSync(want, 'utf8')))
  })
})
