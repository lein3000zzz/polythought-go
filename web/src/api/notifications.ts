import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, call, type Schemas } from './client'
import { keys } from './keys'

type Notification = Schemas['Notification']

/** The kinds of notification, and whether the user gets each. */
export function useNotifications() {
  return useQuery({
    queryKey: keys.notifications,
    queryFn: async () => (await call(api.GET('/notifications'))).notifications,
  })
}

export function useSetNotification() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ key, enabled }: { key: string; enabled: boolean }) =>
      call(api.PUT('/notifications/{key}', { params: { path: { key } }, body: { enabled } })),
    onSuccess: (updated) =>
      queryClient.setQueryData<Notification[]>(keys.notifications, (list) =>
        list?.map((n) => (n.key === updated.key ? updated : n)),
      ),
  })
}
