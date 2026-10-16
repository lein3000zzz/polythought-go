import { describe, expect, it } from 'vitest'

import { scopeDeclarations } from './expressionScope'

describe('what an expression sees', () => {
  it('declares the item, its fields by name, params and execution', () => {
    const declarations = scopeDeclarations(
      [
        { name: 'question', kind: 'text', optional: false, nullable: false },
        { name: 'spread', kind: 'decimal', optional: false, nullable: true },
        { name: 'side', kind: 'text', optional: true, nullable: false, options: ['buy', 'sell'] },
        {
          name: 'outcomes',
          kind: 'list',
          optional: false,
          nullable: false,
          of: {
            name: '',
            kind: 'object',
            optional: false,
            nullable: false,
            fields: [{ name: 'price', kind: 'decimal', optional: false, nullable: false }],
          },
        },
        { name: 'params', kind: 'number', optional: false, nullable: false }, // always params
        { name: 'default', kind: 'number', optional: false, nullable: false }, // a reserved word
      ],
      [
        { name: 'least', kind: 'decimal', required: false },
        { name: 'mode', kind: 'string', required: false, options: ['a', 'b'] },
      ],
    )

    expect(declarations).toBe(
      [
        'import { dec } from "@polythought/sdk"',
        'import type { DecimalText, Execution, Item, Json, TimeText } from "@polythought/sdk"',
        '',
        'declare const item: { question: string; spread: DecimalText | null; side?: "buy" | "sell"; ' +
          'outcomes: ({ price: DecimalText })[]; params: number; default: number }',
        'declare const params: { least?: DecimalText; mode?: "a" | "b" }',
        'declare const execution: Execution',
        'declare const question: string',
        'declare const spread: DecimalText | null',
        'declare const side: "buy" | "sell" | undefined',
        'declare const outcomes: ({ price: DecimalText })[]',
        'export {}',
        '',
      ].join('\n'),
    )
  })

  it('makes item an Item when nobody knows its fields', () => {
    expect(scopeDeclarations(undefined, [])).toContain('declare const item: Item\n')
  })

  it('lets a field named dec be the field', () => {
    const declarations = scopeDeclarations(
      [{ name: 'dec', kind: 'number', optional: false, nullable: false }],
      [],
    )

    expect(declarations).not.toContain('import { dec }')
    expect(declarations).toContain('declare const dec: number')
  })
})
