import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, call } from './client'
import { keys } from './keys'

/** How much a node remembers across runs (D121, D137). */
export function useNodeMemory(workflowId: string, nodeId: string, enabled = true) {
  return useQuery({
    queryKey: keys.nodeMemory(workflowId, nodeId),
    queryFn: () =>
      call(
        api.GET('/workflows/{id}/nodes/{node}/memory', {
          params: { path: { id: workflowId, node: nodeId } },
        }),
      ),
    enabled,
  })
}

/** Forgets what a node remembers, for a fresh start. */
export function useForgetNodeMemory(workflowId: string, nodeId: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: () =>
      call(
        api.DELETE('/workflows/{id}/nodes/{node}/memory', {
          params: { path: { id: workflowId, node: nodeId } },
        }),
      ),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: keys.nodeMemory(workflowId, nodeId) }),
  })
}
