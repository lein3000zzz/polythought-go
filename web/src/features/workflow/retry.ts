import type { NodeType } from '@/api/client'

export const maxRetry = 5

/**
 * How many more times a node tries what failed (D440): 3 for one that only reads, as an
 * HTTP request's GET, else none, unless its Retry setting says otherwise.
 */
export function retriesByDefault(type: NodeType, params: Record<string, unknown>): number {
  if (!type.reads || type.noRetry) return 0
  if (!type.readsWhen) return 3

  const { param, value } = type.readsWhen
  const set = params[param] ?? type.params.find((p) => p.name === param)?.default

  return typeof set === 'string' && set.trim().toUpperCase() === value.toUpperCase() ? 3 : 0
}
