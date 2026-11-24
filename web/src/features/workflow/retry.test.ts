import { describe, expect, it } from 'vitest'

import type { NodeType } from '@/api/client'

import { retriesByDefault } from './retry'

const type = (more: Partial<NodeType>): NodeType => ({
  name: 'test.node',
  trigger: false,
  code: false,
  inputs: ['main'],
  outputs: ['main'],
  params: [{ name: 'method', kind: 'string', required: false, default: 'GET' }],
  ...more,
})

describe('retries to begin with (D440)', () => {
  it('are 3 for a node that only reads, none for one that acts', () => {
    expect(retriesByDefault(type({ reads: true }), {})).toBe(3)
    expect(retriesByDefault(type({}), {})).toBe(0)
    expect(retriesByDefault(type({ reads: true, noRetry: true }), {})).toBe(0)
  })

  it('follow the setting a node reads with, its default when unset', () => {
    const request = type({ reads: true, readsWhen: { param: 'method', value: 'GET' } })

    expect(retriesByDefault(request, {})).toBe(3)
    expect(retriesByDefault(request, { method: 'get' })).toBe(3)
    expect(retriesByDefault(request, { method: 'POST' })).toBe(0)
    expect(retriesByDefault(request, { method: '{{ m }}' })).toBe(0)
  })
})
