import { useInfiniteQuery, useQuery } from '@tanstack/react-query'

import { api, call } from './client'
import { keys } from './keys'

const pageSize = 50

export function useWorkflows() {
  return useInfiniteQuery({
    queryKey: keys.workflowList,
    queryFn: ({ pageParam }) =>
      call(api.GET('/workflows', { params: { query: { limit: pageSize, cursor: pageParam } } })),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
  })
}

export function useWorkflow(id: string) {
  return useQuery({
    queryKey: keys.workflow(id),
    queryFn: () => call(api.GET('/workflows/{id}', { params: { path: { id } } })),
  })
}

/** What can be placed in a workflow. It only changes when the server is upgraded. */
export function useNodeTypes() {
  return useQuery({
    queryKey: keys.nodeTypes,
    queryFn: async () => (await call(api.GET('/node-types'))).nodeTypes,
    staleTime: Infinity,
  })
}
