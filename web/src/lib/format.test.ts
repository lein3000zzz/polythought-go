import { describe, expect, it } from 'vitest'

import { formatDuration } from './format'

describe('formatDuration', () => {
  it.each([
    { ms: 0, want: '0 ms' },
    { ms: 850, want: '850 ms' },
    { ms: 1250, want: '1.3 s' },
    { ms: 59_940, want: '59.9 s' },
    { ms: 125_000, want: '2 min 5 s' },
    { ms: 120_000, want: '2 min' },
  ])('$ms ms', ({ ms, want }) => {
    expect(formatDuration(ms)).toBe(want)
  })
})
