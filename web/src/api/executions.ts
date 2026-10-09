import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, call } from './client'
import { executionListSize } from './events'
import { keys } from './keys'

/** The workflow's latest executions, newest first, without item samples. */
export function useExecutions(workflowId: string, enabled = true) {
  return useQuery({
    queryKey: keys.executions(workflowId),
    queryFn: async () =>
      (
        await call(
          api.GET('/workflows/{id}/executions', {
            params: { path: { id: workflowId }, query: { limit: executionListSize } },
          }),
        )
      ).executions,
    enabled,
  })
}

/** One execution with the item samples its runs kept. Finished executions never change. */
export function useExecution(workflowId: string, id: string | undefined) {
  return useQuery({
    queryKey: keys.execution(workflowId, id ?? ''),
    queryFn: () =>
      call(
        api.GET('/workflows/{id}/executions/{eid}', {
          params: { path: { id: workflowId, eid: id! } },
        }),
      ),
    enabled: id !== undefined,
    staleTime: Infinity,
  })
}

/** Runs the saved workflow from its manual trigger and waits for the result. */
export function useRunWorkflow(workflowId: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: () =>
      call(api.POST('/workflows/{id}/run', { params: { path: { id: workflowId } } })),
    onSuccess: (execution) => {
      queryClient.setQueryData(keys.execution(workflowId, execution.id), execution)
      void queryClient.invalidateQueries({ queryKey: keys.executions(workflowId) })
    },
  })
}
