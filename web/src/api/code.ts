import { useQuery } from '@tanstack/react-query'

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
