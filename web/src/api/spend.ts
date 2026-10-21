import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { api, call, type Schemas } from './client'
import { keys } from './keys'

export type AgentSpend = Schemas['AgentSpend']
export type AgentCaps = Schemas['AgentCaps']

/** What your AI agent nodes spent this month, and the caps on it (D277). */
export function useAgentSpend() {
  return useQuery({
    queryKey: keys.agentSpend,
    queryFn: async () => call(api.GET('/me/agent-spend')),
    retry: false,
  })
}

/** Your choices for your agents: whether your kill switch stops them too (D276). */
export function useSetAgentChoices() {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (body: Schemas['AgentChoicesRequest']) => call(api.PUT('/me/agents', { body })),
    onSuccess: (spend) => {
      queryClient.setQueryData(keys.agentSpend, spend)
      return queryClient.invalidateQueries({ queryKey: keys.me })
    },
  })
}

/** What a user's agents spent this month, for an admin. */
export function useUserAgentSpend(id: string) {
  return useQuery({
    queryKey: keys.userAgentSpend(id),
    queryFn: async () =>
      call(api.GET('/admin/users/{id}/agent-spend', { params: { path: { id } } })),
    retry: false,
  })
}

/** n for people: 20,000,000. */
export function tokens(n: number): string {
  return n.toLocaleString('en-US')
}
