import { describe, expect, it } from 'vitest'

import { formatBytes, formatDuration } from './format'

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

describe('formatBytes', () => {
  it.each([
    { n: 0, want: '0 B' },
    { n: 512 << 10, want: '512 KiB' },
    { n: 1 << 20, want: '1 MiB' },
    { n: (1 << 20) + 4096, want: '1.0 MiB' },
    { n: 1.5 * (1 << 20), want: '1.5 MiB' },
    { n: 40 << 20, want: '40 MiB' },
    { n: 1 << 30, want: '1 GiB' },
  ])('$n bytes', ({ n, want }) => {
    expect(formatBytes(n)).toBe(want)
  })
})
