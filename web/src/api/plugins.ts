import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, call, type Schemas } from './client'
import { keys } from './keys'

export type Plugin = Schemas['Plugin']
export type PluginVersion = Schemas['PluginVersion']
export type PluginStatus = Schemas['PluginStatus']
export type InstalledPlugin = Schemas['InstalledPlugin']
export type FoundPlugin = Schemas['FoundPlugin']
export type PluginSandbox = Schemas['PluginSandbox']
export type PluginCaps = Schemas['PluginCaps']
export type LeftPluginData = Schemas['LeftPluginData']

/** The plugins installed, and versions found on disk no admin installed (D321). */
export function usePlugins() {
  return useQuery({
    queryKey: keys.pluginList,
    queryFn: () => call(api.GET('/admin/plugins')),
  })
}

function useChange<T>(mutationFn: (arg: T) => Promise<unknown>) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.plugins }),
  })
}

/** Installs an archive from a file the admin picked: its bytes are the request's body. */
export function useInstallPlugin() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (archive: Blob) =>
      call(
        api.POST('/admin/plugins', {
          body: archive as unknown as string,
          bodySerializer: (body) => body,
          headers: { 'Content-Type': 'application/gzip' },
        }),
      ),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.plugins }),
  })
}

/** Installs the archive at an https URL. */
export function useFetchPlugin() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (url: string) => call(api.POST('/admin/plugins/fetch', { body: { url } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.plugins }),
  })
}

/** Installs a version found on disk, checked as an archive would be. */
export function useAdoptPlugin() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ id, version }: { id: string; version: string }) =>
      call(api.POST('/admin/plugins/found/{id}/{version}', { params: { path: { id, version } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.plugins }),
  })
}

/** Approves a version and exactly the permissions it asks for (D324). */
export function useApprovePlugin() {
  return useChange(
    ({ id, version, permissions }: { id: string; version: string; permissions: string[] }) =>
      call(
        api.POST('/admin/plugins/{id}/versions/{version}/approve', {
          params: { path: { id, version } },
          body: { permissions },
          parseAs: 'text',
        }),
      ),
  )
}

/** Removes a version not approved. */
export function useRemovePluginVersion() {
  return useChange(({ id, version }: { id: string; version: string }) =>
    call(
      api.DELETE('/admin/plugins/{id}/versions/{version}', {
        params: { path: { id, version } },
        parseAs: 'text',
      }),
    ),
  )
}

export function useSetPluginEnabled() {
  return useChange(({ id, enabled }: { id: string; enabled: boolean }) =>
    enabled
      ? call(api.POST('/admin/plugins/{id}/enable', { params: { path: { id } }, parseAs: 'text' }))
      : call(
          api.POST('/admin/plugins/{id}/disable', { params: { path: { id } }, parseAs: 'text' }),
        ),
  )
}

/** Says who may use a plugin (D282): at once, pausing the workflows of those who no longer may. */
export function useSetPluginUsers() {
  return useChange(({ id, everyone, users }: { id: string; everyone: boolean; users: string[] }) =>
    call(
      api.PUT('/admin/plugins/{id}/users', {
        params: { path: { id } },
        body: { everyone, users },
        parseAs: 'text',
      }),
    ),
  )
}

/** Starts a plugin stopped as crashed (D320). */
export function useStartPlugin() {
  return useChange((id: string) =>
    call(api.POST('/admin/plugins/{id}/start', { params: { path: { id } }, parseAs: 'text' })),
  )
}

/** Uninstalls a plugin: its data too when removeData says (D348), else left behind (D342). */
export function useUninstallPlugin() {
  return useChange(({ id, removeData }: { id: string; removeData: boolean }) =>
    call(
      api.DELETE('/admin/plugins/{id}', {
        params: { path: { id }, query: removeData ? { data: true } : {} },
        parseAs: 'text',
      }),
    ),
  )
}

/**
 * Sets a plugin's own caps, in MiB, 0 for the Config page's (D346). A cap out of bounds is
 * refused, the error's problems naming it in `param`: dataCap or memoryCap.
 */
export function useSetPluginCaps(id: string) {
  return useChange((caps: PluginCaps) =>
    call(
      api.PUT('/admin/plugins/{id}/caps', {
        params: { path: { id } },
        body: caps,
        parseAs: 'text',
      }),
    ),
  )
}

/** What plugins uninstalled without their data left behind (D342). */
export function usePluginDataLeft() {
  return useQuery({
    queryKey: keys.pluginDataLeft,
    queryFn: () => call(api.GET('/admin/plugins/left-behind')),
  })
}

/** Removes what a plugin no longer installed left behind. */
export function useRemovePluginDataLeft() {
  return useChange((id: string) =>
    call(
      api.DELETE('/admin/plugins/left-behind/{id}', { params: { path: { id } }, parseAs: 'text' }),
    ),
  )
}

/** What admins did with a plugin, newest first. */
export function usePluginHistory(id: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.pluginHistory(id),
    queryFn: () => call(api.GET('/admin/plugins/{id}/history', { params: { path: { id } } })),
    enabled,
  })
}

/** How long to wait for the server to come back before giving up. */
const restartTimeout = 60_000
const pollInterval = 1_000

export class PluginRestartTimeoutError extends Error {
  constructor() {
    super('The server did not come back within a minute. Check its logs.')
    this.name = 'PluginRestartTimeoutError'
  }
}

/**
 * Restarts the server so plugins run as admins decided (D317), and resolves once it is
 * back with nothing waiting for a restart: until then the old server may still answer.
 */
export function useRestartForPlugins() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async () => {
      await call(api.POST('/admin/restart', { parseAs: 'text' })) // 202, no body

      const deadline = Date.now() + restartTimeout

      while (Date.now() < deadline) {
        await new Promise((resolve) => setTimeout(resolve, pollInterval))

        const back = await call(api.GET('/admin/plugins')).catch(() => undefined)
        if (back && !back.plugins.some((p) => p.restart)) {
          queryClient.setQueryData(keys.pluginList, back)
          return
        }
      }

      throw new PluginRestartTimeoutError()
    },
    // What plugins bring, node types and credential types, may have changed.
    onSettled: () => queryClient.invalidateQueries(),
  })
}

export type PluginSetting = Schemas['PluginSetting']

/** A plugin's own settings, as the version it runs declares them (D329). */
export function usePluginSettings(id: string) {
  return useQuery({
    queryKey: keys.pluginSettings(id),
    queryFn: () => call(api.GET('/admin/plugins/{id}/settings', { params: { path: { id } } })),
  })
}

/**
 * Saves changed settings, keyed by setting; null resets one to its default, as does an
 * empty string for a secret. Nothing is saved if any is invalid: the error's problems
 * name each bad setting in `param`.
 */
export function useUpdatePluginSettings(id: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (values: Record<string, unknown>) =>
      call(
        api.PATCH('/admin/plugins/{id}/settings', { params: { path: { id } }, body: { values } }),
      ),
    onSuccess: (data) => {
      queryClient.setQueryData(keys.pluginSettings(id), data)
      void queryClient.invalidateQueries({ queryKey: keys.pluginHistory(id) })
    },
  })
}
