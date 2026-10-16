import { describe, expect, it } from 'vitest'

import type { NodeType, ParamSpec } from '@/api/client'

import {
  chosen,
  expressionProblem,
  hasExpressions,
  parseText,
  takesExpressions,
  textOf,
} from './params'

const spec = (kind: ParamSpec['kind'], extra: Partial<ParamSpec> = {}): ParamSpec => ({
  name: 'p',
  kind,
  required: false,
  ...extra,
})

describe('parseText', () => {
  it.each([
    { kind: 'string', text: '  spread ', want: { value: 'spread' } },
    { kind: 'string', text: '   ', want: { value: undefined } },
    { kind: 'number', text: '20', want: { value: 20 } },
    { kind: 'number', text: '-3', want: { value: -3 } },
    { kind: 'number', text: '2.5', want: { value: '2.5', problem: 'Enter a whole number' } },
    {
      kind: 'number',
      text: '99999999999999999999',
      want: { value: '99999999999999999999', problem: 'Enter a whole number' },
    },
    { kind: 'decimal', text: '0.02', want: { value: '0.02' } },
    { kind: 'decimal', text: '.5', want: { value: '.5' } },
    { kind: 'decimal', text: '1e3', want: { value: '1e3', problem: 'Enter a number' } },
    { kind: 'duration', text: '1m30s', want: { value: '1m30s' } },
    { kind: 'duration', text: '1.5h', want: { value: '1.5h' } },
    {
      kind: 'duration',
      text: '0s',
      want: { value: '0s', problem: 'Enter a duration like 30s, 5m or 1h30m' },
    },
    {
      kind: 'duration',
      text: '5 minutes',
      want: { value: '5 minutes', problem: 'Enter a duration like 30s, 5m or 1h30m' },
    },
    { kind: 'tokens', text: '123\n 456 \n\n789', want: { value: ['123', '456', '789'] } },
    { kind: 'list', text: 'a, b,c', want: { value: ['a', 'b', 'c'] } },
    { kind: 'list', text: '\n', want: { value: undefined } },
    // Expressions, worked out per item: the server checks what they give.
    { kind: 'number', text: '{{ params.max }}', want: { value: '{{ params.max }}' } },
    { kind: 'number', text: '{{ n }}0', want: { value: '{{ n }}0' } },
    { kind: 'decimal', text: '{{ spread * 2 }}', want: { value: '{{ spread * 2 }}' } },
    { kind: 'duration', text: '{{ wait }}s', want: { value: '{{ wait }}s' } },
    { kind: 'list', text: '{{ tags }}', want: { value: '{{ tags }}' } },
    { kind: 'list', text: 'a\n{{ b }}', want: { value: ['a', '{{ b }}'] } },
  ] as const)('$kind "$text"', ({ kind, text, want }) => {
    expect(parseText(spec(kind), text)).toEqual(want)
  })
})

describe('textOf', () => {
  it.each([
    { value: undefined, want: '' },
    { value: 0.02, want: '0.02' },
    { value: ['a', 'b'], want: 'a\nb' },
    { value: { x: 1 }, want: '{"x":1}' },
  ])('$value', ({ value, want }) => {
    expect(textOf(value)).toBe(want)
  })
})

describe('chosen', () => {
  const on = spec('list', { options: ['trade', 'book', 'quote'], default: ['trade'] })

  it('keeps the offered order', () => {
    expect(chosen(on, ['quote', 'trade'])).toEqual(['trade', 'quote'])
  })

  it('shows the default when nothing is set', () => {
    expect(chosen(on, undefined)).toEqual(['trade'])
  })

  it('drops values that are no longer offered', () => {
    expect(chosen(on, ['gossip', 'book'])).toEqual(['book'])
  })
})

describe('expressions', () => {
  const node: NodeType = {
    name: 'n',
    trigger: false,
    inputs: ['main'],
    outputs: ['main'],
    params: [],
    outputsParam: 'buttons',
  }
  const trigger: NodeType = { ...node, trigger: true, inputs: [] }

  it('finds {{ }} in a value', () => {
    expect(hasExpressions('Hi {{ question }}')).toBe(true)
    expect(hasExpressions(['a', '{{b}}'])).toBe(true)
    expect(hasExpressions('{{}} and {{ }}')).toBe(false)
    expect(hasExpressions(3)).toBe(false)
  })

  it('knows which settings take them', () => {
    expect(takesExpressions(node, spec('number'))).toBe(true)
    expect(takesExpressions(node, spec('wallet'))).toBe(false)
    expect(takesExpressions(node, spec('string', { field: true }))).toBe(false)
    expect(takesExpressions(node, spec('list', { name: 'buttons' }))).toBe(false)
    expect(takesExpressions(trigger, spec('string'))).toBe(false)
  })

  it('says where {{ }} would stay as written', () => {
    expect(expressionProblem(trigger, spec('string'), '{{ x }}')).toBe(
      'Triggers work out no expressions: {{ }} would stay as written',
    )
    expect(expressionProblem(node, spec('wallet'), '{{ x }}')).toBe(
      'This setting takes no expressions: {{ }} would stay as written',
    )
    expect(expressionProblem(node, spec('number'), '{{ x }}')).toBeUndefined()
    expect(expressionProblem(trigger, spec('string'), 'plain')).toBeUndefined()
  })
})
