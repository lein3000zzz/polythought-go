import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, call, type Schemas } from './client'
import { keys } from './keys'

export type Credential = Schemas['Credential']
export type CredentialType = Schemas['CredentialType']

/** The kinds of credential nodes can use, with their fields. They change only on deploy. */
export function useCredentialTypes() {
  return useQuery({
    queryKey: keys.vaultTypes,
    queryFn: async () => (await call(api.GET('/credential-types'))).credentialTypes,
    staleTime: Infinity,
  })
}

/** The user's credentials, by name. Secret values never come with them. */
export function useCredentialList() {
  return useQuery({
    queryKey: keys.vaultList,
    queryFn: async () => (await call(api.GET('/credentials'))).credentials,
  })
}

export function useCreateCredential() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (body: Schemas['CreateCredentialRequest']) =>
      call(api.POST('/credentials', { body })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.vaultList }),
  })
}

/** Renames a credential or changes values. A value left out, or an empty secret, is kept. */
export function useUpdateCredential() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ id, ...body }: Schemas['UpdateCredentialRequest'] & { id: string }) =>
      call(api.PATCH('/credentials/{id}', { params: { path: { id } }, body })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.vaultList }),
  })
}

export function useDeleteCredential() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) => call(api.DELETE('/credentials/{id}', { params: { path: { id } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.vaultList }),
  })
}

/** Tries a credential against its service. A failed test resolves, with testError set. */
export function useTestCredential() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) =>
      call(api.POST('/credentials/{id}/test', { params: { path: { id } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.vaultList }),
  })
}
