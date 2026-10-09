import { describe, expect, it } from 'vitest'

import type { NodeType } from '@/api/client'
import { workflow } from '@/test/fixtures'

import { toFlow } from './flow'
import { columnWidth } from './layout'

const types: NodeType[] = [
  { name: 'trigger.manual', trigger: true, inputs: [], outputs: ['main'], params: [] },
  { name: 'output.log', trigger: false, inputs: ['main'], outputs: ['main'], params: [] },
  { name: 'logic.if', trigger: false, inputs: ['main'], outputs: ['true', 'false'], params: [] },
]

describe('toFlow', () => {
  it('keeps saved positions', () => {
    const { nodes } = toFlow(
      workflow({
        nodes: [
          { id: 'manual', type: 'trigger.manual', position: { x: 10, y: 20 } },
          { id: 'log', type: 'output.log', position: { x: 400, y: -5 } },
        ],
      }),
      types,
    )

    expect(nodes.map((n) => n.position)).toEqual([
      { x: 10, y: 20 },
      { x: 400, y: -5 },
    ])
  })

  it('lays out a workflow saved without positions', () => {
    const { nodes } = toFlow(
      workflow({
        nodes: [
          { id: 'log', type: 'output.log' },
          { id: 'manual', type: 'trigger.manual' },
        ],
      }),
      types,
    )

    expect(nodes.map((n) => [n.id, n.position.x])).toEqual([
      ['log', columnWidth],
      ['manual', 0],
    ])
  })

  it('gives nodes that continue on failure an error port', () => {
    const { nodes, edges } = toFlow(
      workflow({
        nodes: [
          { id: 'manual', type: 'trigger.manual' },
          { id: 'if', type: 'logic.if', continueOnFail: true },
          { id: 'log', type: 'output.log' },
        ],
        connections: [
          { from: 'manual', fromPort: 'main', to: 'if', toPort: 'main' },
          { from: 'if', fromPort: 'error', to: 'log', toPort: 'main' },
        ],
      }),
      types,
    )

    expect(nodes[1].data.outputs).toEqual(['true', 'false', 'error'])
    expect(edges[1]).toMatchObject({ source: 'if', sourceHandle: 'error', className: 'error-edge' })
    expect(edges[0].className).toBeUndefined()
  })

  it('gives a node of an unknown type the ports its connections use', () => {
    const { nodes } = toFlow(
      workflow({
        nodes: [
          { id: 'manual', type: 'trigger.manual' },
          { id: 'old', type: 'polymarket.retired' },
        ],
        connections: [{ from: 'manual', fromPort: 'main', to: 'old', toPort: 'main' }],
      }),
      types,
    )

    expect(nodes[1].data).toMatchObject({ type: undefined, inputs: ['main'], outputs: [] })
  })
})
