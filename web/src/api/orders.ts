import { useInfiniteQuery } from '@tanstack/react-query'

import { api, call, type Schemas } from './client'
import { keys } from './keys'

type Status = NonNullable<Schemas['LoggedOrder']['status']>

/** What the order log is narrowed to: a wallet, a workflow, a status (D228). */
export type OrderLogFilter = { wallet?: string; workflow?: string; status?: Status }

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
