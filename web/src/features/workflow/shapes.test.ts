import { describe, expect, it } from 'vitest'

import type { NodeType } from '@/api/client'

import { edgeOf, type FlowNode } from './graph'
import {
  fieldChoices,
  fieldProblem,
  lookup,
  receivedPorts,
  receivedShapes,
  type ItemField,
} from './shapes'

const field = (
  name: string,
  kind: ItemField['kind'],
  more: Partial<ItemField> = {},
): ItemField => ({
  name,
  kind,
  optional: false,
  nullable: false,
  ...more,
})

const outcomes = field('outcomes', 'list', {
  of: field('', 'object', { fields: [field('label', 'text'), field('price', 'decimal')] }),
})
const marketFields = [field('question', 'text'), field('price', 'decimal'), outcomes]

const type = (name: string, more: Partial<NodeType>): NodeType => ({
  name,
  trigger: false,
  code: false,
  inputs: ['main'],
  outputs: ['main'],
  params: [],
  ...more,
})

const types: Record<string, NodeType> = {
  markets: type('t.markets', {
    trigger: true,
    inputs: [],
    items: { fields: marketFields, passed: false },
  }),
  pass: type('t.pass', { items: { fields: [], passed: true } }),
  add: type('t.add', {
    items: {
      fields: [
        field('price', 'text'),
        field('copy', 'object', { fields: [field('dollars', 'decimal')] }),
      ],
      passed: true,
    },
  }),
  unknown: type('t.unknown', {}),
  own: type('t.own', {
    items: { fields: [field('price', 'number'), field('n', 'number')], passed: false },
  }),
  pick: type('t.pick', {
    items: { fields: [], passed: false, picked: 'fields' },
    params: [{ name: 'fields', kind: 'list', required: true, field: true }],
  }),
  split: type('t.split', {
    items: { fields: [], passed: false, split: 'field' },
    params: [{ name: 'field', kind: 'string', required: true, field: true }],
  }),
  merge: type('t.merge', {
    inputs: ['a', 'b'],
    items: { fields: [], passed: false, merged: 'mode', joinedAs: 'as' },
    params: [
      { name: 'mode', kind: 'string', required: false, default: 'both' },
      { name: 'as', kind: 'string', required: false, default: 'b' },
    ],
  }),
  ask: type('t.ask', {
    items: {
      fields: [field('answer', 'object', { fields: [field('text', 'text')] })],
      passed: true,
      named: 'field',
      alone: { param: 'call', value: 'together' },
    },
    params: [
      { name: 'field', kind: 'string', required: false, default: 'answer' },
      {
        name: 'call',
        kind: 'string',
        required: false,
        default: 'each',
        options: ['each', 'together'],
      },
    ],
  }),
  tokens: type('t.tokens', {
    trigger: true,
    inputs: [],
    items: {
      fields: [field('question', 'text'), field('tokens', 'list', { of: field('', 'text') })],
      passed: false,
    },
  }),
}

function node(id: string, t: NodeType, params?: Record<string, unknown>): FlowNode {
  return {
    id,
    type: 'workflow',
    position: { x: 0, y: 0 },
    data: { spec: { id, type: t.name, params }, type: t, extraInputs: [], extraOutputs: [] },
  }
}

const link = (from: string, to: string, fromPort = 'main') =>
  edgeOf({ from, fromPort, to, toPort: 'main' })

const names = (fields?: ItemField[]) => fields?.map((f) => f.name)

describe('receivedShapes', () => {
  it('follows items through nodes that pass them on', () => {
    const shapes = receivedShapes(
      [node('a', types.markets), node('b', types.pass), node('c', types.pass)],
      [link('a', 'b'), link('b', 'c')],
    )

    expect(shapes.get('a')).toBeUndefined()
    expect(names(shapes.get('c'))).toEqual(['question', 'price', 'outcomes'])
  })

  it('passes items on with the fields a node adds, replacing their namesakes', () => {
    const shapes = receivedShapes(
      [node('a', types.markets), node('b', types.add), node('c', types.pass)],
      [link('a', 'b'), link('b', 'c')],
    )

    const got = shapes.get('c')
    expect(names(got)).toEqual(['question', 'outcomes', 'price', 'copy'])
    expect(got?.find((f) => f.name === 'price')?.kind).toBe('text')
  })

  it('takes a node’s own fields, and knows nothing after one that does not say', () => {
    const shapes = receivedShapes(
      [
        node('a', types.markets),
        node('b', types.own),
        node('c', types.unknown),
        node('d', types.pass),
      ],
      [link('a', 'b'), link('b', 'c'), link('c', 'd')],
    )

    expect(names(shapes.get('c'))).toEqual(['price', 'n'])
    expect(shapes.get('d')).toBeUndefined()
  })

  it('keeps what is picked, and failed items are the ones received', () => {
    const shapes = receivedShapes(
      [
        node('a', types.markets),
        node('b', types.pick, { fields: ['question', 'outcomes.0.price', 'ghost'] }),
        node('c', types.pass),
        node('d', types.pass),
      ],
      [link('a', 'b'), link('b', 'c'), link('b', 'd', 'error')],
    )

    expect(shapes.get('c')).toEqual([
      field('question', 'text'),
      field('outcomes.0.price', 'decimal', { optional: true }),
      field('ghost', 'any', { optional: true }),
    ])
    expect(names(shapes.get('d'))).toEqual(['question', 'price', 'outcomes'])
  })

  it('splits a list: an entry’s fields at the top, or the entry in the list’s place', () => {
    const shapes = receivedShapes(
      [
        node('a', types.markets),
        node('b', types.split, { field: 'outcomes' }),
        node('c', types.pass),
        node('t', types.tokens),
        node('u', types.split, { field: 'tokens' }),
        node('v', types.pass),
        node('w', types.split, { field: 'question' }),
        node('x', types.pass),
      ],
      [
        link('a', 'b'),
        link('b', 'c'),
        link('t', 'u'),
        link('u', 'v'),
        link('a', 'w'),
        link('w', 'x'),
      ],
    )

    expect(shapes.get('c')).toEqual([
      field('question', 'text'),
      field('label', 'text'),
      field('price', 'decimal'),
    ])
    expect(shapes.get('v')).toEqual([field('question', 'text'), field('tokens', 'text')])
    expect(shapes.get('x')).toBeUndefined()
  })

  it('knows each input of a merge, and what it sends as it combines them', () => {
    const merged = (params?: Record<string, unknown>) => {
      const nodes = [
        node('m', types.markets),
        node('own', types.own),
        node('x', types.merge, params),
        node('after', types.pass),
      ]
      const edges = [
        link('m', 'own'),
        edgeOf({ from: 'm', fromPort: 'main', to: 'x', toPort: 'a' }),
        edgeOf({ from: 'own', fromPort: 'main', to: 'x', toPort: 'b' }),
        link('x', 'after'),
      ]

      return {
        ports: receivedPorts(nodes, edges),
        after: receivedShapes(nodes, edges).get('after'),
      }
    }

    const { ports, after } = merged()
    expect(names(ports.get('x')?.get('a'))).toEqual(['question', 'price', 'outcomes'])
    expect(names(ports.get('x')?.get('b'))).toEqual(['price', 'n'])
    expect(names(after)).toEqual(['question', 'price', 'outcomes'])

    expect(names(merged({ mode: 'either' }).after)).toEqual(['question', 'price', 'outcomes', 'n'])
    expect(merged({ mode: 'join', as: 'theirs' }).after?.at(-1)).toEqual(
      field('theirs', 'object', { fields: [field('price', 'number'), field('n', 'number')] }),
    )
  })

  it('puts an answer under the field its setting names, alone when taken together', () => {
    const after = (params?: Record<string, unknown>) =>
      receivedShapes(
        [node('m', types.markets), node('x', types.ask, params), node('after', types.pass)],
        [link('m', 'x'), link('x', 'after')],
      ).get('after')

    expect(names(after())).toEqual(['question', 'price', 'outcomes', 'answer'])
    expect(names(after({ field: 'verdict' }))).toEqual(['question', 'price', 'outcomes', 'verdict'])
    expect(names(after({ call: 'together' }))).toEqual(['answer'])
    expect(after({ field: '{{ params.where }}' })).toBeUndefined()
    expect(after({ call: '{{ params.how }}' })).toBeUndefined()
  })

  it('makes optional what only some connections carry', () => {
    const shapes = receivedShapes(
      [node('a', types.markets), node('b', types.own), node('c', types.pass)],
      [link('a', 'b'), link('b', 'c'), link('a', 'c')],
    )

    expect(shapes.get('c')).toEqual([
      field('price', 'any'),
      field('n', 'number', { optional: true }),
      field('question', 'text', { optional: true }),
      { ...outcomes, optional: true },
    ])
  })
})

describe('field settings', () => {
  it('looks fields up by path', () => {
    expect(lookup(marketFields, 'outcomes.1.price')?.kind).toBe('decimal')
    expect(lookup(marketFields, 'outcomes.first')).toBeUndefined()
    expect(lookup([field('extra', 'any')], 'extra.deep.2')?.kind).toBe('any')
  })

  it('offers the fields there, of the kinds a setting takes', () => {
    expect(fieldChoices(marketFields).map((c) => c.path)).toEqual([
      'question',
      'price',
      'outcomes',
      'outcomes.0.label',
      'outcomes.0.price',
    ])
    expect(fieldChoices(marketFields, ['number', 'decimal']).map((c) => c.path)).toEqual([
      'price',
      'outcomes.0.price',
    ])
  })

  it('says what is wrong, as the server would', () => {
    const numeric = {
      name: 'field',
      kind: 'string' as const,
      required: true,
      field: true,
      fieldKinds: ['number' as const, 'decimal' as const],
    }

    expect(fieldProblem(marketFields, numeric, 'price')).toBeUndefined()
    expect(fieldProblem(marketFields, numeric, 'Price')).toBe(
      'The items it receives have no field "Price"; "price"?',
    )
    expect(fieldProblem(marketFields, numeric, 'volume')).toBe(
      'The items it receives have no field "volume"',
    )
    expect(fieldProblem(marketFields, numeric, 'question')).toBe(
      'Field "question" is text, and this takes number or decimal',
    )
    expect(fieldProblem(undefined, numeric, 'anything')).toBeUndefined()
    expect(
      fieldProblem(marketFields, { ...numeric, kind: 'list', fieldKinds: undefined }, [
        'question',
        'spread',
      ]),
    ).toBe('The items it receives have no field "spread"')
  })
})
