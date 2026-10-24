import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, call, type Schemas } from './client'
import { applyExecution, executionListSize } from './events'
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

/** Whether an execution can still change: it runs, or waits to carry on. */
export function unfinished(e: Schemas['Execution']): boolean {
  return e.status === 'running' || e.status === 'waiting'
}

/**
 * One execution with the item samples its runs kept. Finished executions never change; a
 * waiting one is refreshed by the live events when it carries on.
 */
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
    staleTime: (query) => (query.state.data && unfinished(query.state.data) ? 0 : Infinity),
  })
}

/** Cancels an execution that waits. */
export function useCancelExecution(workflowId: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) =>
      call(
        api.POST('/workflows/{id}/executions/{eid}/cancel', {
          params: { path: { id: workflowId, eid: id } },
        }),
      ),
    onSuccess: (execution) => applyExecution(queryClient, { workflowId, execution }),
  })
}

export type ExecutionTask = Schemas['ExecutionTask']

/**
 * The task a run waits on at a Local agent node, and which of your agents has it (D413),
 * looked at again every few seconds, as an agent takes it or lets it go.
 */
export function useExecutionTask(workflowId: string, executionId: string, enabled = true) {
  return useQuery({
    queryKey: keys.executionTask(workflowId, executionId),
    queryFn: () =>
      call(
        api.GET('/workflows/{id}/executions/{eid}/task', {
          params: { path: { id: workflowId, eid: executionId } },
        }),
      ),
    enabled,
    refetchInterval: 5000,
    retry: false,
  })
}

/** Takes a run's task from the agent that has it, for another of yours to take at once. */
export function useHandBackTask(workflowId: string, executionId: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: () =>
      call(
        api.POST('/workflows/{id}/executions/{eid}/task/hand-back', {
          params: { path: { id: workflowId, eid: executionId } },
        }),
      ),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: keys.executionTask(workflowId, executionId) }),
  })
}

/** Answers a run's task yourself: answers, one per item asking each; answer, together. */
export function useAnswerTask(workflowId: string, executionId: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (body: Schemas['AnswerTaskRequest']) =>
      call(
        api.POST('/workflows/{id}/executions/{eid}/task/answer', {
          params: { path: { id: workflowId, eid: executionId } },
          body,
        }),
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: keys.execution(workflowId, executionId) })
      void queryClient.invalidateQueries({ queryKey: keys.executions(workflowId) })
    },
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
      void queryClient.invalidateQueries({ queryKey: keys.memory(workflowId) }) // a run remembers
    },
  })
}
