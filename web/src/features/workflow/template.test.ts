import { describe, expect, it } from 'vitest'

import { expressionParts, shadowOf } from './template'

describe('expression parts', () => {
  it.each([
    ['no expression', 'plain', []],
    ['one', 'Tight: {{ question }}!', [{ from: 9, to: 19, closed: true }]],
    ['an object literal inside', '{{ ({ a: { b: 1 } }).a }}', [{ from: 2, to: 23, closed: true }]],
    [
      'two',
      '{{a}} and {{b}}',
      [
        { from: 2, to: 3, closed: true },
        { from: 12, to: 13, closed: true },
      ],
    ],
    ['one being typed', 'Tight: {{ ques', [{ from: 9, to: 14, closed: false }]],
  ])('%s', (_, text, want) => {
    expect(expressionParts(text)).toEqual(want)
  })
})

describe('the shadow file', () => {
  it('holds each expression after the prelude, where the parts say', () => {
    const text = 'Spread {{ spread }} of {{ question }}{{}}'
    const shadow = shadowOf(text, 'declare const spread: string\n')

    expect(shadow.source).toBe('declare const spread: string\n;( spread )\n;( question )\n')
    expect(shadow.parts).toHaveLength(2) // the empty one is text

    for (const p of shadow.parts) {
      const expr = text.slice(p.from, p.to)
      expect(shadow.source.slice(p.at, p.at + expr.length)).toBe(expr)
    }
  })
})
