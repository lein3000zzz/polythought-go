import { useQuery } from '@tanstack/react-query'

import { api, call } from './client'
import { keys } from './keys'

export function useWallets() {
  return useQuery({
    queryKey: keys.wallets,
    queryFn: async () => (await call(api.GET('/wallets'))).wallets,
  })
}
