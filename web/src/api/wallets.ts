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
