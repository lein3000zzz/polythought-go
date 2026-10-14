import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, call } from './client'
import { keys } from './keys'

/** How often to look for the link to land while a link is out. */
const linkPoll = 2_000

/**
 * Whether Telegram can be linked here, and to whom the user did. While `waitingUntil` is
 * in the future, it asks again every few seconds, to notice the link landing.
 */
export function useTelegram(waitingUntil?: string) {
  return useQuery({
    queryKey: keys.telegram,
    queryFn: () => call(api.GET('/telegram')),
    refetchInterval: (query) =>
      waitingUntil && !query.state.data?.link && Date.parse(waitingUntil) > Date.now()
        ? linkPoll
        : false,
  })
}

/** Makes a single-use link to the bot; opening it in Telegram links that account. */
export function useCreateTelegramLink() {
  return useMutation({
    mutationFn: () => call(api.POST('/telegram/link')),
  })
}

export function useUnlinkTelegram() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: () => call(api.DELETE('/telegram/link')),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.telegram }),
  })
}
