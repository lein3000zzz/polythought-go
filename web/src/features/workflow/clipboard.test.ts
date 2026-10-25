import { describe, expect, it } from 'vitest'

import type { NodeType } from '@/api/client'

import { clipText, copyOf, fromClip, placed } from './clipboard'
import { toFlow } from './graph'

const hook: NodeType = {
  name: 'output.webhook',
  label: 'Webhook',
  category: 'output',
  description: '',
  inputs: ['main'],
  outputs: ['main'],
  params: [
    { name: 'url', kind: 'string', required: true, sensitive: true },
    { name: 'method', kind: 'string', required: false },
    { name: 'credential', kind: 'credential', required: false },
  ],
} as unknown as NodeType

const manual = {
  name: 'trigger.manual',
  label: 'Manual',
  category: 'trigger',
  description: '',
  inputs: [],
  outputs: ['main'],
  params: [],
} as unknown as NodeType
const types = [hook, manual]

function canvas() {
  const flow = toFlow(
    {
      nodes: [
        { id: 'manual', type: 'trigger.manual', position: { x: 0, y: 0 } },
        {
          id: 'hook',
          type: 'output.webhook',
          name: 'Ping',
          params: { url: 'https://hooks.example/secret', method: 'POST', credential: 'c-1' },
          position: { x: 300, y: 10 },
        },
      ],
      connections: [{ from: 'manual', fromPort: 'main', to: 'hook', toPort: 'main' }],
    },
    types,
  )

  return { nodes: flow.nodes.map((n) => ({ ...n, selected: true })), edges: flow.edges }
}

describe('nodes on the clipboard (D420)', () => {
  it('go with the connections between them, their personal settings left behind', () => {
    const { nodes, edges } = canvas()
    const c = copyOf(nodes, edges)

    expect(c.nodes[1]).toEqual({
      id: 'hook',
      type: 'output.webhook',
      name: 'Ping',
      params: { method: 'POST' },
      position: { x: 300, y: 10 },
    })
    expect(c.connections).toEqual([
      { from: 'manual', fromPort: 'main', to: 'hook', toPort: 'main' },
    ])

    expect(copyOf(nodes, edges, true).nodes[1].params, 'a duplicate keeps them').toEqual({
      url: 'https://hooks.example/secret',
      method: 'POST',
      credential: 'c-1',
    })

    const one = copyOf([nodes[0], { ...nodes[1], selected: false }], edges)
    expect(one.connections, 'none to a node not copied').toEqual([])
  })

  it('come back from the text, and only the editor’s', () => {
    const { nodes, edges } = canvas()
    const c = copyOf(nodes, edges)

    expect(fromClip(clipText(c))).toEqual(c)
    expect(fromClip('hello')).toBeUndefined()
    expect(fromClip('{"nodes": []}')).toBeUndefined()
    expect(
      fromClip('{"polythought/nodes": 1, "nodes": [{"id": 1}], "connections": []}'),
    ).toBeUndefined()
  })

  it('are placed under new IDs, by the offset, selected, those of unknown types left out', () => {
    const { nodes, edges } = canvas()
    const c = copyOf(nodes, edges)
    c.nodes.push({ id: 'mystery', type: 'custom.gone', position: { x: 0, y: 0 } })

    const got = placed(c, nodes, types, { x: 40, y: 40 })

    expect(got.nodes.map((n) => n.id)).toEqual(['manual2', 'hook2'])
    expect(got.nodes[1].position).toEqual({ x: 340, y: 50 })
    expect(got.nodes.every((n) => n.selected)).toBe(true)
    expect(got.edges.map((e) => [e.source, e.target])).toEqual([['manual2', 'hook2']])
    expect(got.skipped).toEqual(['custom.gone'])
  })
})
