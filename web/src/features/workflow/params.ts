import type { NodeType, ParamSpec } from '@/api/client'

// Go's time.ParseDuration syntax without a sign: 30s, 1m30s, 1.5h.
const duration = /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/
const wholeNumber = /^-?\d+$/
const decimal = /^-?(\d+(\.\d*)?|\.\d+)$/
const address = /^0x[0-9a-fA-F]{40}$/

export type Parsed = { value: unknown; problem?: string }

/**
 * Turns the text of a field into the param's value. Empty means unset, so the default
 * applies. Text that does not parse is kept as typed, with a problem to show; saving it
 * anyway gets the same complaint from the server.
 */
export function parseText(spec: ParamSpec, text: string): Parsed {
  const t = text.trim()
  if (t === '') return { value: undefined }

  // Worked out per item: the server checks what it gives, as the setting's kind.
  if (t.includes('{{') && spec.kind !== 'string') {
    const whole = t.startsWith('{{') && t.endsWith('}}') && !t.includes('\n')
    if (whole || (spec.kind !== 'list' && spec.kind !== 'tokens')) return { value: t }
  }

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

    case 'address':
      return address.test(t)
        ? { value: t }
        : { value: t, problem: 'Enter an address: 0x and 40 hex digits' }

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

/** Whether a value holds {{ }} with something inside, itself or in a list. */
export function hasExpressions(value: unknown): boolean {
  if (Array.isArray(value)) return value.some(hasExpressions)
  if (typeof value !== 'string') return false

  for (let at = value.indexOf('{{'); at >= 0; at = value.indexOf('{{', at)) {
    const end = value.indexOf('}}', at + 2)
    if (end < 0) return false
    if (value.slice(at + 2, end).trim() !== '') return true

    at = end + 2
  }

  return false
}

/**
 * Whether a setting of a node takes expressions, as the server says: not a trigger's,
 * nor wallets, credentials, field settings or the one that gives ports.
 */
export function takesExpressions(type: NodeType | undefined, spec: ParamSpec): boolean {
  return (
    !!type &&
    !type.trigger &&
    spec.kind !== 'wallet' &&
    spec.kind !== 'credential' &&
    !spec.field &&
    spec.name !== type.outputsParam
  )
}

/** What is wrong with {{ }} where it would stay as written, in the server's words. */
export function expressionProblem(
  type: NodeType | undefined,
  spec: ParamSpec,
  value: unknown,
): string | undefined {
  if (!type || !hasExpressions(value) || takesExpressions(type, spec)) return undefined

  return type.trigger
    ? 'Triggers work out no expressions: {{ }} would stay as written'
    : 'This setting takes no expressions: {{ }} would stay as written'
}
