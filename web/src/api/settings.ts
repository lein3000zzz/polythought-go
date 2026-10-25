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

/** The build of Polythought the server runs (D425). */
export function useServerVersion() {
  return useQuery({
    queryKey: ['admin', 'version'],
    queryFn: () => call(api.GET('/admin/version')),
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

/** Whether a newer Polythought is out, and how the updater is (D424, D425). */
export function useUpdates(refetchInterval?: number) {
  return useQuery({
    queryKey: ['admin', 'updates'],
    queryFn: () => call(api.GET('/admin/updates')),
    refetchInterval,
  })
}

/** Asks GitHub again for the newest release. */
export function useCheckUpdates() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: () => call(api.POST('/admin/updates/check')),
    onSuccess: (data) => queryClient.setQueryData(['admin', 'updates'], data),
  })
}

/** How long an update may take, its images pulled and the server restarted. */
const updateTimeout = 10 * 60_000
const updatePollInterval = 2_000

export class UpdateTimeoutError extends Error {
  constructor() {
    super(
      "The server did not come back on the new version within 10 minutes. Check its logs, and the updater's.",
    )
    this.name = 'UpdateTimeoutError'
  }
}

/** The updater's own account of an update that failed: why, and the end of what it did. */
export class UpdateFailedError extends Error {
  log: string

  constructor(message: string, log: string) {
    super(message)
    this.name = 'UpdateFailedError'
    this.log = log
  }
}

/**
 * Has the updater update the server to a release (D425), and resolves once the server
 * says it runs it. Meanwhile the old server may answer, or none: the updater's status says
 * whether it failed.
 */
export function useApplyUpdate() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (version: string) => {
      await call(api.POST('/admin/updates/apply', { body: { version }, parseAs: 'text' })) // 202

      const deadline = Date.now() + updateTimeout

      while (Date.now() < deadline) {
        await new Promise((resolve) => setTimeout(resolve, updatePollInterval))

        const server = await call(api.GET('/admin/version')).catch(() => undefined)
        if (server?.version === version) {
          queryClient.setQueryData(['admin', 'version'], server)
          return
        }

        const update = (await call(api.GET('/admin/updates')).catch(() => undefined))?.update
        if (update?.state === 'failed' && update.version === version) {
          throw new UpdateFailedError(update.error ?? 'The update failed.', update.log ?? '')
        }
      }

      throw new UpdateTimeoutError()
    },
    onSettled: async () => {
      await queryClient.invalidateQueries({ queryKey: ['admin', 'version'] })
      await queryClient.invalidateQueries({ queryKey: ['admin', 'updates'] })
    },
  })
}
