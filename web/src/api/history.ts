import { useInfiniteQuery } from '@tanstack/react-query'

import { api, call, type Schemas } from './client'
import { keys } from './keys'

export type WorkflowSwitch = Schemas['WorkflowSwitch']

/** The kinds the history is narrowed by (D433), as the server groups them. */
export const switchKinds = ['on_off', 'trading', 'reset', 'kill_switch'] as const
export type SwitchKind = (typeof switchKinds)[number]

const pageSize = 50

/**
 * Who switched a workflow and why (D433), newest first, a page at a time, of the kinds
 * asked; none, every kind. Asked again every 30 seconds, as agents switch it too.
 */
export function useWorkflowHistory(workflowId: string, kinds: SwitchKind[]) {
  return useInfiniteQuery({
    queryKey: keys.workflowHistory(workflowId, kinds),
    queryFn: ({ pageParam }) =>
      call(
        api.GET('/workflows/{id}/history', {
          params: {
            path: { id: workflowId },
            query: {
              limit: pageSize,
              cursor: pageParam || undefined,
              kinds: kinds.length > 0 ? kinds.join(',') : undefined,
            },
          },
        }),
      ),
    initialPageParam: '',
    getNextPageParam: (page) => page.nextCursor || undefined,
    refetchInterval: 30_000,
  })
}
