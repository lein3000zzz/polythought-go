/**
 * Query keys. Everything about workflows shares the "workflows" prefix and everything
 * about wallets the "wallets" prefix, so one invalidation covers a family.
 */
export const keys = {
  me: ['me'],
  authConfig: ['auth-config'],
  nodeTypes: ['node-types'],
  tokens: ['tokens'],
  users: ['admin', 'users'],
  invites: ['admin', 'invites'],
  wallets: ['wallets'],
  walletList: ['wallets', 'list'],
  credentials: (walletId: string) => ['wallets', 'credentials', walletId],
  workflows: ['workflows'],
  workflowList: ['workflows', 'list'],
  workflow: (id: string) => ['workflows', 'detail', id],
  executions: (workflowId: string) => ['workflows', 'executions', workflowId],
  execution: (workflowId: string, id: string) => ['workflows', 'execution', workflowId, id],
} as const
