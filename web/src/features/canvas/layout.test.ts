import { describe, expect, it } from 'vitest'

import { columnWidth as W, layout, rowHeight as H } from './layout'

const nodes = (...ids: string[]) => ids.map((id) => ({ id }))
const link = (from: string, to: string) => ({ from, to })

describe('layout', () => {
  it.each([
    {
      name: 'chain declared backwards',
      nodes: nodes('c', 'b', 'a'),
      connections: [link('a', 'b'), link('b', 'c')],
      want: { a: [0, 0], b: [W, 0], c: [2 * W, 0] },
    },
    {
      name: 'branches share a column in declaration order',
      nodes: nodes('t', 'yes', 'no', 'merge'),
      connections: [link('t', 'yes'), link('t', 'no'), link('yes', 'merge'), link('no', 'merge')],
      want: { t: [0, 0], yes: [W, 0], no: [W, H], merge: [2 * W, 0] },
    },
    {
      name: 'a node goes after its longest path',
      nodes: nodes('t', 'a', 'b', 'end'),
      connections: [link('t', 'a'), link('a', 'b'), link('b', 'end'), link('t', 'end')],
      want: { t: [0, 0], a: [W, 0], b: [2 * W, 0], end: [3 * W, 0] },
    },
    {
      name: 'disconnected nodes stack in the first column',
      nodes: nodes('x', 'y'),
      connections: [],
      want: { x: [0, 0], y: [0, H] },
    },
    {
      name: 'connections to unknown nodes are ignored',
      nodes: nodes('a', 'b'),
      connections: [link('ghost', 'a'), link('a', 'ghost'), link('a', 'b')],
      want: { a: [0, 0], b: [W, 0] },
    },
  ])('$name', ({ nodes, connections, want }) => {
    const got = layout(nodes, connections)

    expect(Object.fromEntries([...got].map(([id, p]) => [id, [p.x, p.y]]))).toEqual(want)
  })

  it('does not hang on a cycle', () => {
    const got = layout(nodes('a', 'b'), [link('a', 'b'), link('b', 'a')])

    expect(got.size).toBe(2)
  })
})
