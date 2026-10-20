import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, call } from './client'
import { keys } from './keys'

// Marks move with the books: what is shown is refreshed this often while it shows.
const refreshEvery = 30_000

export const orderPageSize = 50

export type TradingMode = 'paper' | 'live'

/**
 * How a workflow's trading went in a mode (D104): its paper account or its wallet,
 * traders and holdings; with no mode, in the one it trades in now.
 */
export function useTradingSummary(workflowId: string, mode?: TradingMode, enabled = true) {
  return useQuery({
    queryKey: keys.tradingSummary(workflowId, mode),
    queryFn: () =>
      call(
        api.GET('/workflows/{id}/trading', {
          params: { path: { id: workflowId }, query: { mode } },
        }),
      ),
    enabled,
    refetchInterval: refreshEvery,
  })
}

/** How a workflow trades (D191): on paper, or live from one of its owner's wallets. */
export function useTradingSetup(workflowId: string) {
  return useQuery({
    queryKey: keys.tradingSetup(workflowId),
    queryFn: () =>
      call(api.GET('/workflows/{id}/trading/setup', { params: { path: { id: workflowId } } })),
  })
}

/**
 * Switches a workflow between paper and live, or picks its wallet: going live needs the
 * owner's confirmation (D201).
 */
export function useSetTradingSetup(workflowId: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (body: { mode: TradingMode; walletId?: string; confirm: boolean }) =>
      call(
        api.PUT('/workflows/{id}/trading/setup', {
          params: { path: { id: workflowId } },
          body,
        }),
      ),
    onSuccess: (setup) => {
      queryClient.setQueryData(keys.tradingSetup(workflowId), setup)
      return queryClient.invalidateQueries({ queryKey: keys.trading(workflowId) })
    },
  })
}

/** A wallet's pUSD on Polymarket, what going live with it spends (D201). */
export function useWalletCash(walletId: string | undefined) {
  return useQuery({
    queryKey: keys.walletCash(walletId ?? ''),
    queryFn: () =>
      call(api.GET('/wallets/{id}/cash', { params: { path: { id: walletId ?? '' } } })),
    enabled: walletId !== undefined && walletId !== '',
    retry: false,
  })
}

/** A workflow's orders, newest first, a page at a time. */
export function useOrders(workflowId: string, enabled = true) {
  return useInfiniteQuery({
    queryKey: keys.orders(workflowId),
    queryFn: ({ pageParam }) =>
      call(
        api.GET('/workflows/{id}/orders', {
          params: {
            path: { id: workflowId },
            query: { limit: orderPageSize, cursor: pageParam || undefined },
          },
        }),
      ),
    initialPageParam: '',
    getNextPageParam: (page) => page.nextCursor || undefined,
    enabled,
    refetchInterval: refreshEvery,
  })
}

/** Starts the paper account over (D107). */
export function useResetPaperAccount(workflowId: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: () =>
      call(
        api.POST('/workflows/{id}/paper-account/reset', { params: { path: { id: workflowId } } }),
      ),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.trading(workflowId) }),
  })
}
