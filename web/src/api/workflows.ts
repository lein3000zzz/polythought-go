import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, call, type Schemas, type Workflow } from './client'
import { keys } from './keys'

const pageSize = 50

/** What the workflows list narrows to: a search of names and descriptions, on or off. */
export type WorkflowFilter = { q?: string; active?: boolean }

/**
 * Your workflows, the newest first, a page at a time; with pinned false, those not
 * pinned, as the Workflows page lists them below the pinned.
 */
export function useWorkflows(filter: WorkflowFilter = {}, pinned?: false) {
  return useInfiniteQuery({
    queryKey: [...keys.workflowList, filter, pinned ?? 'all'],
    queryFn: ({ pageParam }) =>
      call(
        api.GET('/workflows', {
          params: {
            query: {
              limit: pageSize,
              cursor: pageParam,
              q: filter.q || undefined,
              active: filter.active,
              pinned,
            },
          },
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
  })
}

export type WorkflowStats = Schemas['WorkflowStats']

/**
 * How all your workflows are doing (D429): what each made trading, in the mode it trades
 * in now, and its runs lately, with paper and live totals apart. Asked again each minute,
 * so "ran 5 min ago" stays so.
 */
export function useWorkflowStats() {
  return useQuery({
    queryKey: keys.workflowStats,
    queryFn: () => call(api.GET('/workflows/stats')),
    refetchInterval: 60_000,
  })
}

/** Your pinned workflows, in their order, all of them. */
export function usePinnedWorkflows(filter: WorkflowFilter = {}) {
  return useQuery({
    queryKey: [...keys.workflowPins, filter],
    queryFn: async () =>
      (
        await call(
          api.GET('/workflows', {
            params: { query: { pinned: true, q: filter.q || undefined, active: filter.active } },
          }),
        )
      ).workflows,
  })
}

/** Pins a workflow after your others, or unpins it; its version stays. */
export function usePinWorkflow() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ id, pinned }: { id: string; pinned: boolean }) =>
      call(api.PUT('/workflows/{id}/pin', { params: { path: { id } }, body: { pinned } })),
    onSettled: () => queryClient.invalidateQueries({ queryKey: keys.workflowList }),
  })
}

/**
 * Puts your pinned workflows in a new order, shown at once; put back as they were when
 * the server refuses it.
 */
export function useOrderPins() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (ordered: Workflow[]) =>
      call(api.PUT('/workflows/pins', { body: { ids: ordered.map((w) => w.id) } })),
    onMutate: async (ordered) => {
      await queryClient.cancelQueries({ queryKey: keys.workflowPins })
      const before = queryClient.getQueriesData<Workflow[]>({ queryKey: keys.workflowPins })

      // The unfiltered list takes the new order; a filtered one keeps its own, in it.
      for (const [key, list] of before) {
        if (!list) continue
        const shown = new Set(list.map((w) => w.id))
        queryClient.setQueryData(
          key,
          ordered.filter((w) => shown.has(w.id)),
        )
      }

      return { before }
    },
    onError: (_error, _ordered, context) => {
      for (const [key, list] of context?.before ?? []) {
        queryClient.setQueryData(key, list)
      }
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: keys.workflowPins }),
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

/** Switches a workflow on or off; off, cancelOrders cancels its live resting orders (D217). */
export function useSetActive(id: string) {
  const saved = useSaved()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ active, cancelOrders }: { active: boolean; cancelOrders?: boolean }) =>
      call(
        active
          ? api.POST('/workflows/{id}/activate', { params: { path: { id } } })
          : api.POST('/workflows/{id}/deactivate', {
              params: { path: { id }, query: cancelOrders ? { cancelOrders } : {} },
            }),
      ),
    onSuccess: (w) => {
      saved(w)
      return queryClient.invalidateQueries({ queryKey: keys.trading(id) })
    },
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
