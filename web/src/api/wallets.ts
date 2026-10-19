import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, ApiError, call, type Schemas } from './client'
import { keys } from './keys'

function walletList() {
  return call(api.GET('/wallets'))
}

export function useWallets() {
  return useQuery({
    queryKey: keys.walletList,
    queryFn: walletList,
    select: (list) => list.wallets,
  })
}

/** Whether the instance takes private keys now (D21). */
export function useTakesKeys() {
  return useQuery({
    queryKey: keys.walletList,
    queryFn: walletList,
    select: (list) => list.takesKeys,
  }).data
}

/** Whether the instance offers session keys: once an admin has entered a Builder key (D156). */
export function useTakesSessionKeys() {
  return useQuery({
    queryKey: keys.walletList,
    queryFn: walletList,
    select: (list) => list.takesSessionKeys,
  }).data
}

export function useLinkWallet() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (body: Schemas['LinkWalletRequest']) => call(api.POST('/wallets', { body })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.walletList }),
  })
}

export function useUnlinkWallet() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) => call(api.DELETE('/wallets/{id}', { params: { path: { id } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.wallets }),
  })
}

/** What is stored of a wallet's API credentials, or null when there are none. */
export function useCredentials(walletId: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.credentials(walletId),
    queryFn: async () => {
      try {
        return await call(
          api.GET('/wallets/{id}/credentials', { params: { path: { id: walletId } } }),
        )
      } catch (error) {
        if (error instanceof ApiError && error.status === 404) return null
        throw error
      }
    },
    enabled,
  })
}

export function useSetCredentials(walletId: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (body: Schemas['SetCredentialsRequest']) =>
      call(api.PUT('/wallets/{id}/credentials', { params: { path: { id: walletId } }, body })),
    onSuccess: (info) => {
      queryClient.setQueryData(keys.credentials(walletId), info)
      void queryClient.invalidateQueries({ queryKey: keys.walletList })
    },
  })
}

/** Imports a wallet's private key, and makes its API credentials from it (D153, D154). */
export function useImportKey(walletId: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (privateKey: string) =>
      call(
        api.PUT('/wallets/{id}/key', {
          params: { path: { id: walletId } },
          body: { privateKey },
        }),
      ),
    onSuccess: (info) => {
      queryClient.setQueryData(keys.credentials(walletId), info)
      void queryClient.invalidateQueries({ queryKey: keys.walletList })
    },
  })
}

export function useRemoveCredentials(walletId: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: () =>
      call(api.DELETE('/wallets/{id}/credentials', { params: { path: { id: walletId } } })),
    onSuccess: () => {
      queryClient.setQueryData(keys.credentials(walletId), null)
      void queryClient.invalidateQueries({ queryKey: keys.walletList })
    },
  })
}

/**
 * A wallet's session keys, followed every few seconds while one is being authorised. When
 * that ends, the wallet and its credentials are read again: the key may trade now.
 */
export function useSessionKeys(walletId: string, enabled: boolean) {
  const queryClient = useQueryClient()

  return useQuery({
    queryKey: keys.sessionKeys(walletId),
    queryFn: async () => {
      const before = queryClient.getQueryData<Schemas['SessionKeys']>(keys.sessionKeys(walletId))
      const now = await call(
        api.GET('/wallets/{id}/session-key', { params: { path: { id: walletId } } }),
      )

      if (before?.pending?.state === 'authorizing' && now.pending?.state !== 'authorizing') {
        void queryClient.invalidateQueries({ queryKey: keys.walletList })
        void queryClient.invalidateQueries({ queryKey: keys.credentials(walletId) })
      }

      return now
    },
    enabled,
    refetchInterval: (query) => (query.state.data?.pending?.state === 'authorizing' ? 3000 : false),
  })
}

/** Makes a session key, and gives the batch authorising it for owner to sign. */
export function useStartSessionKey(walletId: string) {
  return useMutation({
    mutationFn: (owner: string) =>
      call(
        api.POST('/wallets/{id}/session-key', {
          params: { path: { id: walletId } },
          body: { owner },
        }),
      ),
  })
}

/** Submits the owner's signature authorising the session key. */
export function useSignSessionKey(walletId: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (signature: string) =>
      call(
        api.POST('/wallets/{id}/session-key/signature', {
          params: { path: { id: walletId } },
          body: { signature },
        }),
      ),
    onSettled: () => queryClient.invalidateQueries({ queryKey: keys.sessionKeys(walletId) }),
  })
}

/** Gives the batch revoking the active session key, for its owner to sign (D159). */
export function useStartRevocation(walletId: string) {
  return useMutation({
    mutationFn: () =>
      call(
        api.POST('/wallets/{id}/session-key/revocation', { params: { path: { id: walletId } } }),
      ),
  })
}

/** Submits the owner's signature revoking the session key, and removes it either way. */
export function useRevokeSessionKey(walletId: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (signature: string) =>
      call(
        api.POST('/wallets/{id}/session-key/revocation/signature', {
          params: { path: { id: walletId } },
          body: { signature },
        }),
      ),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.wallets }),
  })
}

/** Removes the session key without revoking it. */
export function useRemoveSessionKey(walletId: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: () =>
      call(api.DELETE('/wallets/{id}/session-key', { params: { path: { id: walletId } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.wallets }),
  })
}
