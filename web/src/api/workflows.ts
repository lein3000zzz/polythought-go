import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, call, type Schemas, type Workflow } from './client'
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

/** The workflow with id; disabled while id is undefined (a workflow not saved yet). */
export function useWorkflow(id: string | undefined) {
  return useQuery({
    queryKey: keys.workflow(id ?? ''),
    queryFn: () => call(api.GET('/workflows/{id}', { params: { path: { id: id! } } })),
    enabled: id !== undefined,
  })
}

/**
 * What saving the workflow as it is would refuse, checked on the server without saving:
 * what no longer holds since its node types changed. Disabled while id is undefined.
 */
export function useWorkflowProblems(id: string | undefined) {
  return useQuery({
    queryKey: keys.workflowProblems(id ?? ''),
    queryFn: async () =>
      (await call(api.GET('/workflows/{id}/problems', { params: { path: { id: id! } } }))).problems,
    enabled: id !== undefined,
    staleTime: 0,
  })
}

/**
 * What can be placed in the user's workflows: the built-in types, which change only when
 * the server is upgraded, and their custom nodes, which refresh these when they change.
 */
export function useNodeTypes() {
  return useQuery({
    queryKey: keys.nodeTypes,
    queryFn: async () => (await call(api.GET('/node-types'))).nodeTypes,
    staleTime: Infinity,
  })
}

// saved puts a workflow the server returned into the cache, so its page shows it without
// asking again, and marks the list as out of date.
function useSaved() {
  const queryClient = useQueryClient()

  return (w: Workflow) => {
    queryClient.setQueryData(keys.workflow(w.id), w)
    void queryClient.invalidateQueries({ queryKey: keys.workflowList })
  }
}

export function useCreateWorkflow() {
  const saved = useSaved()

  return useMutation({
    mutationFn: (body: Schemas['WorkflowDefinition']) => call(api.POST('/workflows', { body })),
    onSuccess: saved,
  })
}

export function useUpdateWorkflow(id: string) {
  const saved = useSaved()

  return useMutation({
    mutationFn: (body: Schemas['UpdateWorkflowRequest']) =>
      call(api.PUT('/workflows/{id}', { params: { path: { id } }, body })),
    onSuccess: saved,
  })
}

export function useSetActive(id: string) {
  const saved = useSaved()

  return useMutation({
    mutationFn: (active: boolean) => {
      const params = { params: { path: { id } } }
      return call(
        active
          ? api.POST('/workflows/{id}/activate', params)
          : api.POST('/workflows/{id}/deactivate', params),
      )
    },
    onSuccess: saved,
  })
}

export function useDeleteWorkflow(id: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: () => call(api.DELETE('/workflows/{id}', { params: { path: { id } } })),
    onSuccess: () => {
      queryClient.removeQueries({ queryKey: keys.workflow(id) })
      void queryClient.invalidateQueries({ queryKey: keys.workflowList })
    },
  })
}
