import { describe, expect, it } from 'vitest'

import { fieldMessage } from './problems'

describe('fieldMessage', () => {
  it.each([
    ['node "if": param "value" must be a number', 'Must be a number'],
    [
      'node "if": param "field": the items it receives have no field "x"',
      'The items it receives have no field "x"',
    ],
    ['node "if": missing required param "field"', 'Missing required param "field"'],
  ])('%s', (message, want) => {
    expect(fieldMessage({ message, nodeId: 'if' })).toBe(want)
  })
})
