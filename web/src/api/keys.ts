/** Query keys. Everything about workflows shares the "workflows" prefix, for invalidation. */
export const keys = {
  me: ['me'],
  authConfig: ['auth-config'],
  nodeTypes: ['node-types'],
  wallets: ['wallets'],
  workflows: ['workflows'],
  workflowList: ['workflows', 'list'],
  workflow: (id: string) => ['workflows', 'detail', id],
} as const
