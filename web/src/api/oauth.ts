import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, call, type Schemas } from './client'
import { keys } from './keys'

export type AuthRequest = Schemas['OAuthAuthRequest']

/**
 * What an agent asks to be allowed, checked for the consent page (D384). Asked once: the
 * page may approve at once, and must not do it twice.
 */
export function useConsent(request: AuthRequest) {
  return useQuery({
    queryKey: keys.consent(request),
    queryFn: () => call(api.POST('/oauth/authorization', { body: request })),
    staleTime: Infinity,
    retry: false,
    refetchOnWindowFocus: false,
  })
}

/** Allows the agent the scopes ticked; the answer says where the browser goes. */
export function useApprove() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (body: Schemas['OAuthApproveRequest']) =>
      call(api.POST('/oauth/authorization/approve', { body })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.grants }),
  })
}

export function useDeny() {
  return useMutation({
    mutationFn: (body: AuthRequest) => call(api.POST('/oauth/authorization/deny', { body })),
  })
}

/** The agents the user allowed, revoked ones too. */
export function useGrants() {
  return useQuery({
    queryKey: keys.grants,
    queryFn: async () => (await call(api.GET('/me/grants'))).grants,
  })
}

export function useRevokeGrant() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) => call(api.DELETE('/me/grants/{id}', { params: { path: { id } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.grants }),
  })
}
