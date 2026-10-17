import { describe, expect, it } from 'vitest'

import type { Definition } from '@/features/workflow/graph'

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
