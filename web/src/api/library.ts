import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, ApiError, call, type Schemas, type Workflow } from './client'
import { keys } from './keys'

export type Template = Schemas['Template']
export type TemplateInput = Schemas['TemplateInput']

/** Built-in scenarios first, then members' shared workflows, newest first. */
export function useTemplates() {
  return useInfiniteQuery({
    queryKey: keys.templateList,
    queryFn: ({ pageParam }) =>
      call(api.GET('/library', { params: { query: { cursor: pageParam } } })),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
  })
}

export function useTemplate(id: string) {
  return useQuery({
    queryKey: keys.template(id),
    queryFn: () => call(api.GET('/library/{id}', { params: { path: { id } } })),
  })
}

/** Starts a new workflow from a template. */
export function useUseTemplate(id: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (body: Schemas['UseTemplateRequest']) =>
      call(api.POST('/library/{id}/use', { params: { path: { id } }, body })),
    onSuccess: (w: Workflow) => {
      queryClient.setQueryData(keys.workflow(w.id), w)
      void queryClient.invalidateQueries({ queryKey: keys.workflowList })
    },
  })
}

export function useDeleteTemplate() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) => call(api.DELETE('/library/{id}', { params: { path: { id } } })),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: keys.library }),
  })
}

/** The copy of a workflow its owner shared, or null when there is none. */
export function useSharedCopy(workflowId: string, enabled: boolean) {
  return useQuery({
    queryKey: keys.sharedCopy(workflowId),
    queryFn: async () => {
      try {
        return await call(
          api.GET('/workflows/{id}/share', { params: { path: { id: workflowId } } }),
        )
      } catch (error) {
        if (error instanceof ApiError && error.status === 404) return null
        throw error
      }
    },
    enabled,
  })
}

export function useShareWorkflow(workflowId: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (body: Schemas['ShareRequest']) =>
      call(api.PUT('/workflows/{id}/share', { params: { path: { id: workflowId } }, body })),
    onSuccess: (t) => {
      queryClient.setQueryData(keys.sharedCopy(workflowId), t)
      void queryClient.invalidateQueries({ queryKey: keys.library })
    },
  })
}
