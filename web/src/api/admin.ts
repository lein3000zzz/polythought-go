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

/** How every user's workflows are doing (D431), asked again each minute. */
export function useInstanceStats() {
  return useQuery({
    queryKey: keys.instanceStats,
    queryFn: () => call(api.GET('/admin/stats')),
    refetchInterval: 60_000,
  })
}

/** How one user's workflows are doing, as their dashboard has it (D431). */
export function useUserStats(id: string) {
  return useQuery({
    queryKey: keys.userStats(id),
    queryFn: () => call(api.GET('/admin/stats/users/{id}', { params: { path: { id } } })),
    refetchInterval: 60_000,
    retry: false,
  })
}

/** Every user's workflows, the busiest first (D435), asked again each minute. */
export function useAdminWorkflows() {
  return useQuery({
    queryKey: keys.adminWorkflows,
    queryFn: () => call(api.GET('/admin/workflows')),
    refetchInterval: 60_000,
  })
}

/** One of a user's workflows, read-only, its personal settings hidden (D435). */
export function useAdminWorkflow(owner: string, id: string) {
  return useQuery({
    queryKey: keys.adminWorkflow(owner, id),
    queryFn: () =>
      call(
        api.GET('/admin/users/{id}/workflows/{wid}', { params: { path: { id: owner, wid: id } } }),
      ),
    retry: false,
  })
}

/** Its latest runs, without the items they carried. */
export function useAdminExecutions(owner: string, id: string) {
  return useQuery({
    queryKey: keys.adminExecutions(owner, id),
    queryFn: async () =>
      (
        await call(
          api.GET('/admin/users/{id}/workflows/{wid}/executions', {
            params: { path: { id: owner, wid: id }, query: { limit: 50 } },
          }),
        )
      ).executions,
  })
}

/** Who switched it and why, a page at a time, of the kinds asked; none, every kind. */
export function useAdminHistory(owner: string, id: string, kinds: string[], enabled = true) {
  return useInfiniteQuery({
    queryKey: keys.adminHistory(owner, id, kinds),
    queryFn: ({ pageParam }) =>
      call(
        api.GET('/admin/users/{id}/workflows/{wid}/history', {
          params: {
            path: { id: owner, wid: id },
            query: {
              limit: 50,
              cursor: pageParam || undefined,
              kinds: kinds.length > 0 ? kinds.join(',') : undefined,
            },
          },
        }),
      ),
    initialPageParam: '',
    getNextPageParam: (page) => page.nextCursor || undefined,
    enabled,
  })
}

/** Switches a user's workflow off, saying why, and with hold keeps it off (D435). */
export function useAdminSwitchOff() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({
      owner,
      id,
      ...body
    }: {
      owner: string
      id: string
      reason: string
      hold: boolean
    }) =>
      call(
        api.POST('/admin/users/{id}/workflows/{wid}/switch-off', {
          params: { path: { id: owner, wid: id } },
          body,
        }),
      ),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.adminWorkflows }),
  })
}

/** Lets a user's workflow an admin held off be switched on again (D435). */
export function useAdminRelease() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ owner, id }: { owner: string; id: string }) =>
      call(
        api.POST('/admin/users/{id}/workflows/{wid}/release', {
          params: { path: { id: owner, wid: id } },
        }),
      ),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.adminWorkflows }),
  })
}
