import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, call, type Schemas } from './client'
import { keys } from './keys'

export type Setting = Schemas['Setting']

const historyPageSize = 20

export function useSettings() {
  return useQuery({
    queryKey: keys.settingList,
    queryFn: () => call(api.GET('/admin/settings')),
  })
}

/**
 * Saves changed settings, keyed by setting; null resets one to its default. Nothing is
 * saved if any value is invalid: the error's problems name each bad setting in `param`.
 */
export function useUpdateSettings() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (values: Record<string, unknown>) =>
      call(api.PATCH('/admin/settings', { body: { values } })),
    onSuccess: (data) => {
      queryClient.setQueryData(keys.settingList, data)
      void queryClient.invalidateQueries({ queryKey: keys.settingHistory })
      // Sign-up and password forms read some settings from here, and whether one may write
      // code depends on them.
      void queryClient.invalidateQueries({ queryKey: keys.authConfig })
      void queryClient.invalidateQueries({ queryKey: keys.me })
      void queryClient.invalidateQueries({ queryKey: keys.users })
    },
  })
}

export function useSettingHistory() {
  return useInfiniteQuery({
    queryKey: keys.settingHistory,
    queryFn: ({ pageParam }) =>
      call(
        api.GET('/admin/settings/history', {
          params: { query: { limit: historyPageSize, cursor: pageParam } },
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
  })
}

/** How long to wait for the server to come back before giving up. */
const restartTimeout = 60_000
const pollInterval = 1_000

export class RestartTimeoutError extends Error {
  constructor() {
    super('The server did not come back within a minute. Check its logs.')
    this.name = 'RestartTimeoutError'
  }
}

/**
 * Restarts the server and resolves once it is back with nothing left waiting for a
 * restart. Until then the old server may still answer, so asking whether it is up is not
 * enough.
 */
export function useRestart() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async () => {
      await call(api.POST('/admin/restart', { parseAs: 'text' })) // 202, no body

      const deadline = Date.now() + restartTimeout

      while (Date.now() < deadline) {
        await new Promise((resolve) => setTimeout(resolve, pollInterval))

        const back = await call(api.GET('/admin/settings')).catch(() => undefined)
        if (back && !back.restartPending) {
          queryClient.setQueryData(keys.settingList, back)
          return
        }
      }

      throw new RestartTimeoutError()
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: keys.settings }),
  })
}

/** Sends a test email through the mail server as saved (D168). */
export function useSendTestMail() {
  return useMutation({
    mutationFn: (to: string) => call(api.POST('/admin/mail/test', { body: { to } })),
  })
}

/** Where user code runs, and how its runner processes are (D357), as the server started. */
export function useCodeRunners() {
  return useQuery({
    queryKey: keys.codeRunners,
    queryFn: () => call(api.GET('/admin/code-runners')),
  })
}

/** Whether Polymarket takes orders from the server, as it last answered (D186). */
export function useGeoblock() {
  return useQuery({
    queryKey: ['admin', 'geoblock'],
    queryFn: () => call(api.GET('/admin/geoblock')),
  })
}

/** Asks Polymarket again. */
export function useCheckGeoblock() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: () => call(api.POST('/admin/geoblock/check')),
    onSuccess: (data) => queryClient.setQueryData(['admin', 'geoblock'], data),
  })
}
