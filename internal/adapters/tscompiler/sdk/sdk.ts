/**
 * The Polythought SDK: what your code imports from "@polythought/sdk".
 *
 * Items are plain JSON. Decimals in them, like prices and sizes, are text such as
 * "0.0200", so they stay exact: do arithmetic on them with {@link dec}. Times are RFC 3339
 * text. Data comes from Polythought through the requests below, which are read-only,
 * reach only your own wallets, and count against the run's limit of data requests.
 *
 * @packageDocumentation
 */

import Big from './big.mjs'

/** A JSON value: what items are made of. */
export type Json = string | number | boolean | null | Json[] | { [field: string]: Json }

/** One record flowing between nodes. */
export type Item = { [field: string]: Json }

// Decimals

/**
 * An exact decimal, for prices, sizes and money, which must not become floats. Make one
 * with {@link dec}. It turns into text in JSON, so you can return it in an item.
 */
export interface Decimal {
  plus(n: DecimalLike): Decimal
  minus(n: DecimalLike): Decimal
  times(n: DecimalLike): Decimal
  /** Keeps 20 decimal places, rounding half up. */
  div(n: DecimalLike): Decimal
  mod(n: DecimalLike): Decimal
  /** To a whole power; negative powers divide, keeping 20 decimal places. */
  pow(exponent: number): Decimal
  abs(): Decimal
  neg(): Decimal
  /** To dp decimal places, 0 unless given; half up unless another mode is given. */
  round(dp?: number, mode?: RoundingMode): Decimal
  /** -1 when less than n, 0 when equal, 1 when greater. */
  cmp(n: DecimalLike): -1 | 0 | 1
  eq(n: DecimalLike): boolean
  gt(n: DecimalLike): boolean
  gte(n: DecimalLike): boolean
  lt(n: DecimalLike): boolean
  lte(n: DecimalLike): boolean
  /** As text with dp decimal places, rounding half up; all of them unless dp is given. */
  toFixed(dp?: number): string
  /** As text, never in exponent form. */
  toString(): string
  /** As a float: for display and Math only, never for prices you act on. */
  toNumber(): number
  /** As text, like toString: what JSON holds. */
  toJSON(): string
}

/** What {@link dec} takes: text like "0.0200", a number, or a decimal. */
export type DecimalLike = Decimal | string | number

/** 0 towards zero, 1 half up, 2 half to even, 3 away from zero. */
export type RoundingMode = 0 | 1 | 2 | 3

// A constructor of our own, so the settings are ours: 20 places for division, half up,
// and never exponent form, which would make "0.00000001" into "1e-8".
const Dec = Big()
Dec.DP = 20
Dec.RM = 1
Dec.NE = -1e6
Dec.PE = 1e6

/**
 * Makes an exact decimal of n. Text that is not a number throws.
 *
 * @example
 * const spread = dec(item.bestAsk).minus(item.bestBid)
 * if (spread.lt("0.02")) return { ...item, spread }
 */
export function dec(n: DecimalLike): Decimal {
  return Dec(n as string) as Decimal
}

// The context

/** The run your code is part of, passed to your function after its own arguments. */
export interface Context<Params extends Record<string, Json> = Record<string, Json>> {
  /** The node's settings, as you configured them. */
  readonly params: Readonly<Params>
  readonly execution: Execution
  /** Writes a line to the run's log, as console.log does. */
  log(...values: unknown[]): void
}

export interface Execution {
  readonly id: string
  readonly workflowId: string
  /** When it started, as RFC 3339 text. */
  readonly startedAt: string
}

// Data requests

/** A market, with the fields of the Markets node's items. Decimals are text. */
export interface Market {
  /** The condition ID, 0x… */
  id: string
  slug: string
  question: string
  /** open, closed, resolved or archived. */
  status: string
  acceptingOrders: boolean
  negRisk: boolean
  tickSize: string
  minOrderSize: string
  volume24h: string
  liquidity: string
  /** RFC 3339 text. */
  endsAt: string
  outcomes: Outcome[]
}

export interface Outcome {
  tokenId: string
  label: string
  price: string
}

/** A market search found, with its event. */
export interface FoundMarket extends Market {
  event: string
  eventSlug: string
}

/** One outcome token's order book. Prices and sizes are text. */
export interface OrderBook {
  tokenId: string
  /** Null when that side is empty, as are mid and spread. */
  bestBid: string | null
  bestAsk: string | null
  mid: string | null
  spread: string | null
  /** The sizes on each side, summed over the levels returned. */
  bidDepth: string
  askDepth: string
  /** Best first. */
  bids: Level[]
  asks: Level[]
  /** When Polymarket took it, as RFC 3339 text. */
  at: string
}

export interface Level {
  price: string
  size: string
}

/** An open position in one of your wallets, with the fields of the Positions node's items. */
export interface Position {
  walletId: string
  address: string
  marketId: string
  tokenId: string
  title: string
  outcome: string
  size: string
  avgPrice: string
  currentPrice: string
  value: string
  realizedPnl: string
  unrealizedPnl: string
  redeemable: boolean
}

type Host = (name: string, args: unknown) => unknown

// The runtime's one way out. Only the SDK keeps it: code reaches data through the
// requests below, which Polythought checks.
const g = globalThis as unknown as Record<string, unknown>
const host = g.__host as Host
delete g.__host

async function request<T>(name: string, args: unknown): Promise<T> {
  return host(name, args) as T
}

/** Markets on Polymarket. */
export const markets = {
  /**
   * Finds open markets by words in their event's title, like the Search markets node.
   * At most 50; 10 unless limit says otherwise. closed searches closed markets instead.
   */
  search(
    query: string,
    options: { limit?: number; closed?: boolean } = {},
  ): Promise<FoundMarket[]> {
    return request('markets.search', { query, ...options })
  },

  /** A market by its condition ID (0x…) or its slug; null when there is none. */
  get(idOrSlug: string): Promise<Market | null> {
    return request('markets.get', { id: idOrSlug })
  },
}

/** Order books on Polymarket. */
export const orderBooks = {
  /**
   * The books of up to 50 outcome tokens, in the order asked, null for a token that has
   * none. Up to depth levels on each side: 10 unless given, at most 50.
   */
  get(tokenIds: string[], options: { depth?: number } = {}): Promise<(OrderBook | null)[]> {
    return request('orderBooks.get', { tokenIds, ...options })
  },
}

/** Your positions. */
export const positions = {
  /** The open positions of one of your wallets, by its ID: at most 500; 50 unless limit says otherwise. */
  list(walletId: string, options: { limit?: number } = {}): Promise<Position[]> {
    return request('positions.list', { walletId, ...options })
  },
}

// Running code

declare const console: { log(...values: unknown[]): void }

interface Invocation {
  exports: string
  export: string
  args: Json[]
  each?: boolean
  context: { params: Record<string, Json>; execution: Execution }
}

// invoke is what the runner calls: one of the code's exports, with its arguments and then
// the context. With each, it is called once for each element of the first argument,
// keeping each call's value or what it threw, so one element failing fails only itself.
async function invoke(call: Invocation): Promise<unknown> {
  const fn = (g[call.exports] as Record<string, unknown> | undefined)?.[call.export]
  if (typeof fn !== 'function') {
    throw new Error(`the code exports no function named ${call.export}`)
  }

  const ctx: Context = Object.freeze({
    params: Object.freeze(call.context.params),
    execution: Object.freeze(call.context.execution),
    log: (...values: unknown[]) => console.log(...values),
  })

  if (!call.each) return fn(...call.args, ctx)

  const [elements, ...rest] = call.args as [Json[], ...Json[]]
  const out: { value?: unknown; error?: string }[] = []

  for (const element of elements) {
    try {
      out.push({ value: (await fn(element, ...rest, ctx)) ?? null })
    } catch (e) {
      out.push({ error: String(e) })
    }
  }

  return out
}

Object.defineProperty(globalThis, '__polythought_invoke', { value: invoke })
