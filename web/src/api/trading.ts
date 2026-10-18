import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, call } from './client'
import { keys } from './keys'

// Marks move with the books: what is shown is refreshed this often while it shows.
const refreshEvery = 30_000

export const orderPageSize = 50

/** How a workflow's trading went (D104): its paper account, traders and holdings. */
export function useTradingSummary(workflowId: string, enabled = true) {
  return useQuery({
    queryKey: keys.tradingSummary(workflowId),
    queryFn: () =>
      call(api.GET('/workflows/{id}/trading', { params: { path: { id: workflowId } } })),
    enabled,
    refetchInterval: refreshEvery,
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
