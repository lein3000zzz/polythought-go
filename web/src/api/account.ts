import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, call, type Schemas } from './client'
import { keys } from './keys'

export function useChangePassword() {
  return useMutation({
    mutationFn: (body: Schemas['ChangePasswordRequest']) => call(api.PUT('/me/password', { body })),
  })
}

export function useTokens() {
  return useQuery({
    queryKey: keys.tokens,
    queryFn: async () => (await call(api.GET('/tokens'))).tokens,
  })
}

/** Creates a token. The response carries its secret, which cannot be read again. */
export function useCreateToken() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (body: Schemas['CreateTokenRequest']) => call(api.POST('/tokens', { body })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.tokens }),
  })
}

export function useRevokeToken() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) => call(api.DELETE('/tokens/{id}', { params: { path: { id } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.tokens }),
  })
}
