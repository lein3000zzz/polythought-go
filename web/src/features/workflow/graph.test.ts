import { describe, expect, it } from 'vitest'

import type { NodeType } from '@/api/client'
import { workflow } from '@/test/fixtures'

import {
  edgeOf,
  inputsOf,
  linkProblem,
  newNode,
  outputsOf,
  sameDefinition,
  toDefinition,
  toFlow,
  type Definition,
} from './graph'
import { columnWidth } from './layout'

const types: NodeType[] = [
  { name: 'trigger.manual', trigger: true, inputs: [], outputs: ['main'], params: [] },
  { name: 'output.log', trigger: false, inputs: ['main'], outputs: ['main'], params: [] },
  { name: 'logic.if', trigger: false, inputs: ['main'], outputs: ['true', 'false'], params: [] },
]

const link = (source: string, sourceHandle: string, target: string, targetHandle = 'main') => ({
  source,
  sourceHandle,
  target,
  targetHandle,
})

describe('toFlow', () => {
  it('keeps saved positions', () => {
    const { nodes } = toFlow(workflow(), types)

    expect(nodes.map((n) => n.position)).toEqual([
      { x: 0, y: 0 },
      { x: 300, y: 0 },
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

  it('gives nodes that continue on failure an error port, drawn in red', () => {
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

    expect(outputsOf(nodes[1].data)).toEqual(['true', 'false', 'error'])
    expect(nodes[1].data.extraOutputs).toEqual([])
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

    expect(nodes[1].data.type).toBeUndefined()
    expect(inputsOf(nodes[1].data)).toEqual(['main'])
    expect(outputsOf(nodes[1].data)).toEqual([])
  })
})

describe('toDefinition', () => {
  it('round-trips a saved workflow', () => {
    const saved = workflow({
      nodes: [
        { id: 'manual', type: 'trigger.manual', position: { x: 0, y: 0 } },
        {
          id: 'if',
          type: 'logic.if',
          name: 'Tight?',
          params: { field: 'spread', value: 0.02 },
          continueOnFail: true,
          position: { x: 300, y: 40 },
        },
      ],
      connections: [{ from: 'manual', fromPort: 'main', to: 'if', toPort: 'main' }],
    })
    const { nodes, edges } = toFlow(saved, types)

    expect(toDefinition(saved.name, nodes, edges)).toEqual({
      name: saved.name,
      nodes: saved.nodes,
      connections: saved.connections,
    })
  })

  it('rounds positions and leaves out empty settings', () => {
    const node = newNode(types[1], { x: 10.6, y: -3.2 }, [])
    node.data.spec = { ...node.data.spec, name: '', params: { message: undefined } }

    expect(toDefinition('n', [node], []).nodes).toEqual([
      { id: 'log', type: 'output.log', position: { x: 11, y: -3 } },
    ])
  })
})

describe('sameDefinition', () => {
  const base: Definition = {
    name: 'w',
    nodes: [{ id: 'a', type: 't', params: { x: 1, y: 2 }, position: { x: 0, y: 0 } }],
    connections: [],
  }

  it.each([
    {
      name: 'key order does not matter',
      other: { ...base, nodes: [{ ...base.nodes[0], params: { y: 2, x: 1 } }] },
      same: true,
    },
    {
      name: 'changed params differ',
      other: { ...base, nodes: [{ ...base.nodes[0], params: { x: 1 } }] },
      same: false,
    },
    {
      name: 'a moved node differs',
      other: { ...base, nodes: [{ ...base.nodes[0], position: { x: 1, y: 0 } }] },
      same: false,
    },
    { name: 'a renamed workflow differs', other: { ...base, name: 'v' }, same: false },
  ])('$name', ({ other, same }) => {
    expect(sameDefinition(base, other)).toBe(same)
  })

  it('treats empty settings and a missing position as absent', () => {
    const bare: Definition = { ...base, nodes: [{ id: 'a', type: 't' }] }
    const explicit: Definition = {
      ...base,
      nodes: [
        {
          id: 'a',
          type: 't',
          name: '',
          params: {},
          continueOnFail: false,
          position: { x: 0, y: 0 },
        },
      ],
    }

    expect(sameDefinition(bare, explicit)).toBe(true)
  })
})

describe('newNode', () => {
  it('names nodes after their type, numbering repeats', () => {
    const first = newNode(types[1], { x: 0, y: 0 }, [])
    const second = newNode(types[1], { x: 0, y: 0 }, [first])
    const third = newNode(types[1], { x: 0, y: 0 }, [first, second])

    expect([first.id, second.id, third.id]).toEqual(['log', 'log2', 'log3'])
    expect(third.data.spec).toEqual({ id: 'log3', type: 'output.log' })
    expect(third.selected).toBe(true)
  })
})

describe('linkProblem', () => {
  const edges = [
    edgeOf({ from: 'a', fromPort: 'main', to: 'b', toPort: 'main' }),
    edgeOf({ from: 'b', fromPort: 'true', to: 'c', toPort: 'main' }),
  ]

  it.each([
    { name: 'a new branch', link: link('a', 'main', 'd'), want: undefined },
    {
      name: 'fan-in to a port that has a connection',
      link: link('a', 'main', 'c'),
      want: undefined,
    },
    { name: 'another port of the same pair', link: link('b', 'false', 'c'), want: undefined },
    { name: 'to itself', link: link('a', 'main', 'a'), want: 'A node cannot connect to itself' },
    { name: 'twice', link: link('a', 'main', 'b'), want: 'These ports are already connected' },
    { name: 'back to an ancestor', link: link('c', 'main', 'a'), want: 'Workflows cannot loop' },
  ])('$name', ({ link, want }) => {
    expect(linkProblem(link, edges)).toBe(want)
  })
})
