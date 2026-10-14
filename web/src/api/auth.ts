import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, ApiError, call, type Schemas, type User } from './client'
import { keys } from './keys'

/** The signed-in user, or null when nobody is signed in. */
export function useMe() {
  return useQuery({
    queryKey: keys.me,
    queryFn: async (): Promise<User | null> => {
      try {
        return await call(api.GET('/me'))
      } catch (error) {
        if (error instanceof ApiError && error.status === 401) return null
        throw error
      }
    },
    staleTime: 5 * 60_000,
  })
}

export function useAuthConfig() {
  return useQuery({
    queryKey: keys.authConfig,
    queryFn: () => call(api.GET('/auth/config')),
    staleTime: Infinity,
  })
}

/** The fewest characters a new password may have, once the server has said. */
export function useMinPasswordLength(): number | undefined {
  return useAuthConfig().data?.minPasswordLength
}

export function useLogin() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (body: Schemas['LoginRequest']) => call(api.POST('/auth/login', { body })),
    onSuccess: (user) => queryClient.setQueryData(keys.me, user),
  })
}

/** Creates the account and signs straight in. */
export function useRegister() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (body: Schemas['RegisterRequest']) => {
      await call(api.POST('/auth/register', { body }))
      return call(api.POST('/auth/login', { body: { email: body.email, password: body.password } }))
    },
    onSuccess: (user) => queryClient.setQueryData(keys.me, user),
  })
}

export function useLogout() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: () => call(api.POST('/auth/logout')),
    // Forget the user first, then everything else. Removing the user's query instead would
    // make the mounted pages fetch it again straight away.
    onSuccess: () => {
      queryClient.setQueryData(keys.me, null)
      queryClient.removeQueries({ predicate: (query) => query.queryKey[0] !== keys.me[0] })
    },
  })
}
