import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, call, type Schemas } from './client'
import { keys } from './keys'

const pageSize = 50

export function useUsers() {
  return useInfiniteQuery({
    queryKey: keys.users,
    queryFn: ({ pageParam }) =>
      call(api.GET('/admin/users', { params: { query: { limit: pageSize, cursor: pageParam } } })),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
  })
}

export function useCreateUser() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (body: Schemas['CreateUserRequest']) => call(api.POST('/admin/users', { body })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.users }),
  })
}

/** Changes a user's role or disables them; the server refuses to leave no active admin. */
export function useUpdateUser() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ id, ...body }: Schemas['UpdateUserRequest'] & { id: string }) =>
      call(api.PATCH('/admin/users/{id}', { params: { path: { id } }, body })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.users }),
  })
}

/** Mails a user that must verify a new link. */
export function useSendVerificationLink() {
  return useMutation({
    mutationFn: (id: string) =>
      call(api.POST('/admin/users/{id}/verification', { params: { path: { id } } })),
  })
}

/** The invites, and whether they can be mailed now (D180). */
export function useInvites() {
  return useQuery({
    queryKey: keys.invites,
    queryFn: () => call(api.GET('/admin/invites')),
  })
}

/**
 * Creates an invite (D371): its code typed or made, its uses and its end; or, with an
 * email, one mailed there, one use (D180).
 */
export function useCreateInvite() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (body: Schemas['CreateInviteRequest']) =>
      call(api.POST('/admin/invites', { body })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.invites }),
  })
}

/**
 * What keeps a typed code from making an invite, asked as the admin types it (D393):
 * problem is absent while it can. Under the invites, so a new one asks again.
 */
export function useInviteCodeCheck(code: string) {
  return useQuery({
    queryKey: [...keys.invites, 'check', code],
    queryFn: () => call(api.POST('/admin/invites/check', { body: { code } })),
    enabled: code !== '',
  })
}

/** Ends an invite: it admits no more sign-ups, and stays listed. */
export function useRevokeInvite() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) =>
      call(api.POST('/admin/invites/{id}/revoke', { params: { path: { id } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.invites }),
  })
}

/** The requests to join admins see (D394): pending ones first, and how many wait. */
export function useJoinRequests(enabled = true) {
  return useQuery({
    queryKey: keys.joinRequests,
    queryFn: () => call(api.GET('/admin/requests')),
    enabled,
  })
}

/** Approves a request, mailing its address an invite: the invites change too. */
export function useApproveJoin() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) =>
      call(api.POST('/admin/requests/{id}/approve', { params: { path: { id } } })),
    onSuccess: () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: keys.joinRequests }),
        queryClient.invalidateQueries({ queryKey: keys.invites }),
      ]),
  })
}

export function useDenyJoin() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) =>
      call(api.POST('/admin/requests/{id}/deny', { params: { path: { id } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.joinRequests }),
  })
}
