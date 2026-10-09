import type { ParamSpec } from '@/api/client'

// Go's time.ParseDuration syntax without a sign: 30s, 1m30s, 1.5h.
const duration = /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/
const wholeNumber = /^-?\d+$/
const decimal = /^-?(\d+(\.\d*)?|\.\d+)$/

export type Parsed = { value: unknown; problem?: string }

/**
 * Turns the text of a field into the param's value. Empty means unset, so the default
 * applies. Text that does not parse is kept as typed, with a problem to show; saving it
 * anyway gets the same complaint from the server.
 */
export function parseText(spec: ParamSpec, text: string): Parsed {
  const t = text.trim()
  if (t === '') return { value: undefined }

  switch (spec.kind) {
    case 'number':
      return wholeNumber.test(t) && Number.isSafeInteger(Number(t))
        ? { value: Number(t) }
        : { value: t, problem: 'Enter a whole number' }

    case 'decimal':
      // Kept as text: the server reads numeric strings exactly, where a float could round.
      return decimal.test(t) ? { value: t } : { value: t, problem: 'Enter a number' }

    case 'duration':
      // No sign is allowed, so any non-zero digit makes the duration positive.
      return duration.test(t) && /[1-9]/.test(t)
        ? { value: t }
        : { value: t, problem: 'Enter a duration like 30s, 5m or 1h30m' }

    case 'list':
    case 'tokens': {
      const items = t
        .split(/[\n,]/)
        .map((s) => s.trim())
        .filter((s) => s !== '')

      return { value: items }
    }

    default:
      return { value: t } // surrounding spaces are never meant, and break field names
  }
}

/** The text a field shows for a stored value. */
export function textOf(value: unknown): string {
  if (value === undefined || value === null) return ''
  if (Array.isArray(value)) return value.join('\n')
  if (typeof value === 'object') return JSON.stringify(value)

  return String(value)
}

/** The options chosen in a list param, in the order the options are offered. */
export function chosen(spec: ParamSpec, value: unknown): string[] {
  const list = Array.isArray(value) ? value : Array.isArray(spec.default) ? spec.default : []

  return (spec.options ?? []).filter((o) => list.includes(o))
}
