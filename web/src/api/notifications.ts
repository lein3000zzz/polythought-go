import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, call, type Schemas } from './client'
import { keys } from './keys'

/** The kinds of notification, how the user gets each, and whether mail can carry them. */
export function useNotifications() {
  return useQuery({
    queryKey: keys.notifications,
    queryFn: () => call(api.GET('/notifications')),
  })
}

/** Switches a kind on Telegram (enabled) or by mail (email). */
export function useSetNotification() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ key, ...body }: { key: string; enabled?: boolean; email?: boolean }) =>
      call(api.PUT('/notifications/{key}', { params: { path: { key } }, body })),
    onSuccess: (updated) =>
      queryClient.setQueryData<Schemas['NotificationList']>(
        keys.notifications,
        (list) =>
          list && {
            ...list,
            notifications: list.notifications.map((n) => (n.key === updated.key ? updated : n)),
          },
      ),
  })
}
