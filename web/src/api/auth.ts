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

/** What signing up did: signed straight in, or wait for the mailed link first (D166). */
export type SignUp = { verify: false; user: User } | { verify: true; email: string; sent: boolean }

/** Creates the account and signs straight in, unless it must verify its address first. */
export function useRegister() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: async (body: Schemas['RegisterRequest']): Promise<SignUp> => {
      const registration = await call(api.POST('/auth/register', { body }))
      if (registration.mustVerify) {
        return { verify: true, email: registration.user.email, sent: registration.sent }
      }

      const user = await call(
        api.POST('/auth/login', { body: { email: body.email, password: body.password } }),
      )

      return { verify: false, user }
    },
    onSuccess: (result) => {
      if (!result.verify) queryClient.setQueryData(keys.me, result.user)
    },
  })
}

/** Mails a new verification link, if the address has an account that still needs one. */
export function useResendVerification() {
  return useMutation({
    mutationFn: (email: string) => call(api.POST('/auth/verification', { body: { email } })),
  })
}

/**
 * Whether an invite works, and the address it is for when it was mailed (D181). Read once:
 * the code is used up by signing up.
 */
export function useInviteFor(code: string) {
  return useQuery({
    queryKey: ['invite', code],
    queryFn: () => call(api.POST('/auth/invite', { body: { code } })),
    enabled: code !== '',
    retry: false,
    staleTime: Infinity,
  })
}

/** Mails a link setting a new password, if the address has an account (D176). */
export function useRequestPasswordReset() {
  return useMutation({
    mutationFn: (email: string) => call(api.POST('/auth/password-reset', { body: { email } })),
  })
}

/** Sets a new password with its link's token; signing in comes after (D177). */
export function useResetPassword() {
  return useMutation({
    mutationFn: (body: Schemas['ResetPasswordRequest']) =>
      call(api.POST('/auth/password-reset/confirm', { body })),
  })
}

/** Verifies an address with its link's token; signing in comes after (D172). */
export function useVerifyEmail() {
  return useMutation({
    mutationFn: (token: string) => call(api.POST('/auth/verify-email', { body: { token } })),
  })
}

/** Asks to join while requests are open (D394); the server mails a link to confirm it. */
export function useRequestToJoin() {
  return useMutation({
    mutationFn: (body: Schemas['RequestToJoinRequest']) =>
      call(api.POST('/auth/requests', { body })),
  })
}

/** Confirms a request to join by its mailed link, so the admins see it. */
export function useConfirmJoin() {
  return useMutation({
    mutationFn: (token: string) => call(api.POST('/auth/requests/confirm', { body: { token } })),
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

/** Keeps the keys the user set for the editor's shortcuts, in place of all before (D420). */
export function useSetShortcuts() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (shortcuts: Record<string, string>) =>
      call(api.PUT('/me/shortcuts', { body: { shortcuts } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.me }),
  })
}
