import { describe, expect, it } from 'vitest'

import { formatAgo, formatBytes, formatCount, formatDuration } from './format'

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

describe('formatCount', () => {
  it.each([
    { n: 0, want: '0' },
    { n: 999, want: '999' },
    { n: 1000, want: '1k' },
    { n: 1234, want: '1.2k' },
    { n: 2300, want: '2.3k' },
    { n: 9999, want: '9.9k' },
    { n: 12_345, want: '12k' },
    { n: 999_999, want: '999k' },
    { n: 1_234_567, want: '1.2m' },
  ])('$n', ({ n, want }) => {
    expect(formatCount(n)).toBe(want)
  })
})

describe('formatAgo', () => {
  const now = Date.parse('2026-10-28T12:00:00Z')

  it('says how long ago, coarser as it goes', () => {
    expect(formatAgo('2026-10-28T11:59:30Z', now)).toBe('just now')
    expect(formatAgo('2026-10-28T11:55:00Z', now)).toBe('5 min ago')
    expect(formatAgo('2026-10-28T09:00:00Z', now)).toBe('3 h ago')
    expect(formatAgo('2026-10-26T12:00:00Z', now)).toBe('2 d ago')
  })

  it('gives the date past a week', () => {
    expect(formatAgo('2026-10-01T12:00:00Z', now)).not.toMatch(/ago/)
  })
})
