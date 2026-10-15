// Code written against the declarations, so tsc checks they hold together and read as
// meant (make sdk-types-check). It never runs.

import { dec, markets } from './types/index'
import type { Context, NodeTypes, PolymarketMarketsItem, TriggerMarketItem } from './types/index'

export async function run(items: PolymarketMarketsItem[], ctx: Context<{ least: string }>) {
  const settings: NodeTypes['logic.if']['settings'] = { field: 'liquidity', value: '1000' }
  const passes: NodeTypes['logic.if']['items'] = 'received'

  const deep = items.filter((m) => dec(m.liquidity).gt(settings.value) && m.status === 'open')
  const found = await markets.search(ctx.params.least)

  return { tokens: deep.map((m) => m.outcomes[0].tokenId), found: found.length, passes }
}

export function spread(update: TriggerMarketItem): string | undefined {
  if (update.kind !== 'quote' || update.bestBid == null || update.bestAsk == null) return undefined

  return dec(update.bestAsk).minus(update.bestBid).toString()
}
