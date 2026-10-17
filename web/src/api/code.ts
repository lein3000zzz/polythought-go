import { useMutation, useQuery } from '@tanstack/react-query'

import { api, call } from './client'
import { keys } from './keys'

/**
 * The @polythought/sdk package code is written against: the SDK's declarations and the
 * user's node types, custom nodes included, which refresh it when they change.
 */
export function useCodeTypes() {
  return useQuery({
    queryKey: keys.codeTypes,
    queryFn: async () => (await call(api.GET('/code/types'))).files,
    staleTime: Infinity,
  })
}

/**
 * Runs workflow code on the server, in the sandbox, for the workflow it builds (D74).
 * Code that fails is an ApiError invalid_code, its problems by line and column.
 */
export function useRunWorkflowCode() {
  return useMutation({
    mutationFn: async (source: string) =>
      (await call(api.POST('/code/workflow', { body: { source } }))).workflow,
  })
}
