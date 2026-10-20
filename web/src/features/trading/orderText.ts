import type { Schemas } from '@/api/client'
import { formatDateTime, formatDollars, formatShares, shortAddress } from '@/lib/format'

type Order = Schemas['Order']

/** Whose trade an order copies: their label, or their address. */
export function traderName(label?: string, trader?: string): string {
  if (!trader) return 'Copied from no one'
  return label || shortAddress(trader)
}

/** What an order asked for: buy 10 shares at up to 0.52, GTC. */
export function orderText(o: Order): string {
  if (o.type === 'SETTLE') return `settled at ${o.price}`

  const size = o.dollars ? formatDollars(o.dollars) : `${formatShares(o.shares ?? '0')} shares`
  const limit = o.side === 'buy' ? `up to ${o.price}` : `at least ${o.price}`
  const until = o.expiresAt ? ` until ${formatDateTime(o.expiresAt)}` : ''

  return `${o.side} ${size} at ${limit}, ${o.type}${until}`
}

/** What came of an order: filled 10 shares for $5.20, refused: why. */
export function resultText(o: Order): string {
  // A live fill whose trade failed was undone (D193).
  const fills = o.fills.filter((f) => f.status !== 'failed')
  const shares = fills.reduce((n, f) => n + Number(f.shares), 0)
  const dollars = fills.reduce((n, f) => n + Number(f.dollars), 0)
  const fees = fills.reduce((n, f) => n + Number(f.fee), 0)

  if (o.status !== 'filled' && o.status !== 'partial')
    return `${o.status}: ${o.reason ?? ''}`.trim()

  let what = `${o.status}: ${formatShares(String(shares))} shares for ${formatDollars(String(dollars))}`
  if (fees > 0) what += `, ${formatDollars(String(fees))} in fees`

  // Counted at match, followed until its trades are final on chain (D193).
  const settling = fills.some((f) => f.status !== undefined && f.status !== 'confirmed')

  return settling ? `${what}, confirming on chain` : what
}
