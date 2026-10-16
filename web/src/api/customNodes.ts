import { useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query'

import { api, call, type Schemas } from './client'
import { keys } from './keys'

export type CustomNode = Schemas['CustomNode']
export type CustomNodeDefinition = Schemas['CustomNodeDefinition']
export type CustomSetting = Schemas['CustomSetting']
export type ItemField = Schemas['ItemField']
export type BrokenWorkflow = Schemas['BrokenWorkflow']

/** The user's custom nodes, by name. */
export function useCustomNodes() {
  return useQuery({
    queryKey: keys.customNodeList,
    queryFn: async () => (await call(api.GET('/custom-nodes'))).customNodes,
  })
}

/** One custom node; disabled while id is undefined (a node not made yet). */
export function useCustomNode(id: string | undefined) {
  return useQuery({
    queryKey: keys.customNode(id ?? ''),
    queryFn: () => call(api.GET('/custom-nodes/{id}', { params: { path: { id: id! } } })),
    enabled: id !== undefined,
  })
}

// changed refreshes what a custom node changing changes: the list, the node types the
// editor offers, and what the user's workflows are found to lack.
function changed(queryClient: QueryClient, saved?: CustomNode) {
  if (saved) queryClient.setQueryData(keys.customNode(saved.id), saved)

  void queryClient.invalidateQueries({ queryKey: keys.customNodeList })
  void queryClient.invalidateQueries({ queryKey: keys.nodeTypes })
  void queryClient.invalidateQueries({ queryKey: ['workflows', 'problems'] })
}

export function useCreateCustomNode() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (body: CustomNodeDefinition) => call(api.POST('/custom-nodes', { body })),
    onSuccess: (saved) => changed(queryClient, saved),
  })
}

/**
 * Replaces a custom node's definition. A change that breaks workflows using it fails with
 * an ApiError of code breaks_workflows listing them, unless confirm is set.
 */
export function useUpdateCustomNode(id: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (body: Schemas['UpdateCustomNodeRequest']) =>
      call(api.PUT('/custom-nodes/{id}', { params: { path: { id } }, body })),
    onSuccess: (saved) => changed(queryClient, saved),
  })
}

/** Deletes a custom node; while workflows use it, only with confirm. */
export function useDeleteCustomNode() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ id, confirm }: { id: string; confirm?: boolean }) =>
      call(
        api.DELETE('/custom-nodes/{id}', {
          params: { path: { id }, query: confirm ? { confirm: true } : {} },
        }),
      ),
    onSuccess: (_, { id }) => {
      queryClient.removeQueries({ queryKey: keys.customNode(id) })
      changed(queryClient)
    },
  })
}
