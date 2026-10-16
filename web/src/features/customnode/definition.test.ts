import { describe, expect, it } from 'vitest'

import type { CustomNode } from '@/api/customNodes'

import { definitionOf, formOf, otherProblems, problemsAt } from './definition'

const node: CustomNode = {
  id: 'n-1',
  type: 'custom.spread-split',
  version: 2,
  createdAt: '2026-10-16T12:00:00Z',
  updatedAt: '2026-10-16T12:00:00Z',
  name: 'spread-split',
  label: 'Spread split',
  description: 'Wide books one way.',
  settings: [
    { name: 'threshold', label: 'threshold', kind: 'decimal', default: '0.02' },
    { name: 'side', label: 'Side', kind: 'string', options: ['buy', 'sell'], required: true },
    { name: 'on', label: 'on', kind: 'string', field: true, fieldKinds: ['decimal'] },
    { name: 'wallet', label: 'Wallet', kind: 'wallet' },
  ],
  outputs: ['wide', 'tight'],
  items: {
    passed: false,
    fields: [
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
          fields: [{ name: 'price', kind: 'decimal', optional: false, nullable: true }],
        },
      },
      { name: 'side', kind: 'text', optional: true, nullable: false, options: ['buy', 'sell'] },
    ],
  },
  source: 'export function run(items) { return { wide: items } }',
}

describe('the form', () => {
  it('round-trips a node', () => {
    const { id: _id, type: _type, version: _v, createdAt: _c, updatedAt: _u, ...definition } = node
    const expected = {
      ...definition,
      settings: [
        { name: 'threshold', kind: 'decimal', default: '0.02' }, // labelled by its name: none
        { name: 'side', label: 'Side', kind: 'string', options: ['buy', 'sell'], required: true },
        { name: 'on', kind: 'string', field: true, fieldKinds: ['decimal'] },
        { name: 'wallet', label: 'Wallet', kind: 'wallet' },
      ],
    }

    expect(definitionOf(formOf(node))).toEqual(expected)
  })

  it('names choices and fields as the form offers them', () => {
    const form = formOf(node)

    expect(form.settings.map((s) => s.kind)).toEqual(['decimal', 'choice', 'field', 'wallet'])
    expect(form.settings[0].label).toBe('')
  })

  it('sends no fields for items passed on, and no default for a wallet', () => {
    const form = formOf(node)
    form.passed = true
    form.settings[3].default = 'w-1'

    const def = definitionOf(form)
    expect(def.items).toEqual({ passed: true, fields: [] })
    expect(def.settings[3]).toEqual({ name: 'wallet', label: 'Wallet', kind: 'wallet' })
  })

  it('finds the problems about each part', () => {
    const problems = [
      { param: 'settings.0.default', message: 'the default must be a number' },
      { param: 'settings.0.default', message: 'and more' },
      { param: 'name', message: 'a name is lowercase' },
      { message: 'something else' },
    ]

    expect(problemsAt(problems, 'settings.0.default')).toEqual([
      'the default must be a number',
      'and more',
    ])
    expect(otherProblems(problems, (p) => p === 'name' || p.startsWith('settings.'))).toEqual([
      { message: 'something else' },
    ])
  })
})
