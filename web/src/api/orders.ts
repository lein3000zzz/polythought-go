import { useInfiniteQuery, useQuery } from '@tanstack/react-query'

import { api, call, type Schemas } from './client'
import { keys } from './keys'

type Status = NonNullable<Schemas['LoggedOrder']['status']>

/**
 * What the order log is narrowed to: a wallet, a workflow, a status (D228), and who is
 * behind them, you or agent:<id> or token:<id> (D408).
 */
export type OrderLogFilter = { wallet?: string; workflow?: string; status?: Status; by?: string }

const pageSize = 50

/** The user's live orders, all their workflows', deleted ones' too, newest first (D228). */
export function useLiveOrders(f: OrderLogFilter) {
  return useInfiniteQuery({
    queryKey: keys.liveOrders(f),
    queryFn: ({ pageParam }) =>
      call(
        api.GET('/orders', {
          params: { query: { ...f, limit: pageSize, cursor: pageParam || undefined } },
        }),
      ),
    initialPageParam: '',
    getNextPageParam: (page) => page.nextCursor || undefined,
    refetchInterval: 30_000,
  })
}

/** Where the CSV of the orders f keeps downloads from. */
export function exportURL(f: OrderLogFilter): string {
  const query = new URLSearchParams(
    Object.entries(f).filter((e): e is [string, string] => Boolean(e[1])),
  ).toString()

  return `/api/v1/orders/export${query ? `?${query}` : ''}`
}

/** The agents and tokens behind your live orders, each once, to filter by (D408). */
export function useOrderActors() {
  return useQuery({
    queryKey: keys.orderActors,
    queryFn: async () => (await call(api.GET('/orders/actors'))).actors,
  })
}
