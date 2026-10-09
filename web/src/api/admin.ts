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

export function useInvites() {
  return useQuery({
    queryKey: keys.invites,
    queryFn: async () => (await call(api.GET('/admin/invites'))).invites,
  })
}

/** Creates an invite. The response carries its code, which cannot be read again. */
export function useCreateInvite() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: () => call(api.POST('/admin/invites')),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.invites }),
  })
}
