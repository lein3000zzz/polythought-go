import { describe, expect, it } from 'vitest'

import { emptyForm, newField, newSetting, type Form } from './definition'
import { starter } from './starter'

const form = (patch: Partial<Form>): Form => ({ ...emptyForm(), ...patch })

describe('the starter', () => {
  it('passes the items on, with one output', () => {
    expect(starter(form({}))).toBe(`import type { Context, Item } from "@polythought/sdk"

// Gets every item at once; returns the items to send on.
export function run(items: Item[], ctx: Context) {
  return items
}
`)
  })

  it('types the settings and returns by output', () => {
    const settings = [
      { ...newSetting(), name: 'threshold', kind: 'decimal' as const, default: '0.02' },
      {
        ...newSetting(),
        name: 'side',
        kind: 'choice' as const,
        options: ['buy', 'sell'],
        required: true,
      },
      { ...newSetting(), name: 'every', kind: 'duration' as const },
      { ...newSetting(), name: 'n', kind: 'number' as const, default: 3 },
      { ...newSetting(), name: 'tags', kind: 'list' as const, default: [] },
      { ...newSetting(), name: 'on', kind: 'bool' as const, default: false },
      { ...newSetting(), name: '', kind: 'string' as const }, // not named yet: left out
    ]

    expect(starter(form({ settings, outputs: ['wide', 'tight', 'not-sure'] })))
      .toBe(`import type { Context, Item } from "@polythought/sdk"

type Settings = {
  threshold: string
  side: "buy" | "sell"
  every?: string
  n: number
  tags: string[]
  on: boolean
}

// Gets every item at once; returns the items of each output, by name.
export function run(items: Item[], ctx: Context<Settings>) {
  return {
    wide: items,
    tight: [],
    "not-sure": [],
  }
}
`)
  })

  it('makes items with the declared fields', () => {
    const best = { ...newField('object', 'best'), fields: [newField('decimal', 'price')] }
    const fields = [
      newField('number', 'count'),
      { ...newField('text', 'side'), options: ['buy', 'sell'] },
      newField('time', 'at'),
      newField('list', 'tags'),
      best,
      { ...newField('text', 'note'), optional: true },
      newField('any', 'extra'),
    ]

    expect(starter(form({ passed: false, fields }))).toContain(`  return items.map(() => ({
    count: 0,
    side: "buy",
    at: ctx.execution.startedAt,
    tags: [],
    best: {
      price: "0",
    },
    extra: null,
  }))`)
  })
})
