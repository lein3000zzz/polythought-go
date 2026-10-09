import { describe, expect, it } from 'vitest'

import type { ParamSpec } from '@/api/client'

import { chosen, parseText, textOf } from './params'

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
