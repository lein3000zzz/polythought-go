import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, call, type Schemas } from './client'
import { keys } from './keys'

export type Confirmation = Schemas['Confirmation']

/**
 * Your tokens' and agents' live actions, waiting for you or ended (D406), looked at again
 * every 15 seconds, so one an agent asks for shows up while you are here.
 */
export function useConfirmations() {
  return useQuery({
    queryKey: keys.confirmations,
    queryFn: async () => (await call(api.GET('/me/confirmations'))).confirmations,
    refetchInterval: 15_000,
  })
}

/** The ones still waiting for you. */
export function usePendingConfirmations() {
  const list = useConfirmations()

  return { ...list, data: list.data?.filter((c) => c.status === 'pending') }
}

export function useConfirm() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) =>
      call(api.POST('/me/confirmations/{id}/confirm', { params: { path: { id } } })),
    // It is carried out in the background: look again shortly, for how it ended.
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: keys.confirmations })
      setTimeout(() => void queryClient.invalidateQueries({ queryKey: keys.confirmations }), 2_000)
    },
  })
}

export function useRefuse() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) =>
      call(api.POST('/me/confirmations/{id}/refuse', { params: { path: { id } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.confirmations }),
  })
}

/** Which live actions of your tokens and agents wait for you, the switches named, at a browser only. */
export function useSetLiveConfirm() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (body: Schemas['LiveConfirmRequest']) =>
      call(api.PUT('/me/live-confirm', { body })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.me }),
  })
}
