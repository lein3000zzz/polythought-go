// Code written against the declarations, so tsc checks they hold together and read as
// meant (make sdk-types-check). It never runs.

import { dec, markets } from './types/index'
import type {
  Context,
  NodeTypes,
  PolymarketMarketsItem,
  PolymarketOrderbookItem,
  TriggerMarketItem,
} from './types/index'

export async function run(items: PolymarketMarketsItem[], ctx: Context<{ least: string }>) {
  const settings: NodeTypes['logic.if']['settings'] = { field: 'liquidity', value: '1000' }
  const passes: NodeTypes['logic.if']['items'] = 'received'
  const top: NodeTypes['transform.top']['settings'] = {
    field: 'liquidity',
    limit: '{{ params.max }}',
  }

  const deep = items.filter((m) => dec(m.liquidity).gt(settings.value ?? 0) && m.status === 'open')
  const found = await markets.search(ctx.params.least)

  return { tokens: deep.map((m) => m.outcomes[0].tokenId), found: found.length, passes, top }
}

export function spread(update: TriggerMarketItem): string | undefined {
  if (update.kind !== 'quote' || update.bestBid == null || update.bestAsk == null) return undefined

  return dec(update.bestAsk).minus(update.bestBid).toString()
}

// The example in the Code nodes guide.
export function keep(item: PolymarketOrderbookItem, ctx: Context<{ least: string }>): boolean {
  return item.spread !== null && dec(item.spread).lt(ctx.params.least ?? '0.02')
}

// The example in the custom nodes guide, whose run it is.
export function runSplit(
  items: PolymarketOrderbookItem[],
  ctx: Context<{ threshold: string }>,
): { wide: PolymarketOrderbookItem[]; tight: PolymarketOrderbookItem[] } {
  const wide = items.filter((b) => b.spread !== null && dec(b.spread).gt(ctx.params.threshold))
  return { wide, tight: items.filter((b) => !wide.includes(b)) }
}
