import { MutationCache, QueryCache, QueryClient } from '@tanstack/react-query'

import { ApiError } from './client'
import { keys } from './keys'

export function createQueryClient(): QueryClient {
  // "unauthenticated" from any call means the session is gone: it expired, or the user
  // signed out in another tab. Forgetting the user sends them to the sign-in page. Other
  // 401s, such as a mistyped password, leave the session alone.
  const signOutWhenSessionEnds = (error: Error) => {
    if (error instanceof ApiError && error.code === 'unauthenticated') {
      queryClient.setQueryData(keys.me, null)
    }
  }

  const queryClient = new QueryClient({
    queryCache: new QueryCache({ onError: signOutWhenSessionEnds }),
    mutationCache: new MutationCache({ onError: signOutWhenSessionEnds }),
    defaultOptions: {
      queries: {
        // Client errors will not change on a retry; network trouble and 5xx might.
        retry: (failures, error) =>
          failures < 2 && !(error instanceof ApiError && error.status < 500),
      },
    },
  })

  return queryClient
}
