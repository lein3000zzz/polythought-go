import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, call } from './client'
import { keys } from './keys'

/**
 * An Inbound webhook trigger's secret address (D439), made the first time it is asked
 * for; only once the workflow is saved with the trigger.
 */
export function useHook(workflowId: string, nodeId: string) {
  return useQuery({
    queryKey: keys.hook(workflowId, nodeId),
    queryFn: () =>
      call(
        api.GET('/workflows/{id}/hooks/{node}', {
          params: { path: { id: workflowId, node: nodeId } },
        }),
      ),
    retry: false,
  })
}

/** Gives the trigger a new address; the old one answers no more. */
export function useRenewHook(workflowId: string, nodeId: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: () =>
      call(
        api.POST('/workflows/{id}/hooks/{node}/renew', {
          params: { path: { id: workflowId, node: nodeId } },
        }),
      ),
    onSuccess: (hook) => queryClient.setQueryData(keys.hook(workflowId, nodeId), hook),
  })
}
