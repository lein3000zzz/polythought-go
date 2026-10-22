import { useQuery } from '@tanstack/react-query'

import { api, call } from './client'
import { keys } from './keys'

/** What the server says about Polythought itself: its repository's stars, which it asks GitHub for about once an hour (D372). */
export function useMeta() {
  return useQuery({
    queryKey: keys.meta,
    queryFn: () => call(api.GET('/meta')),
    staleTime: 60 * 60 * 1000,
    retry: false,
  })
}
