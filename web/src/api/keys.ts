/**
 * Query keys. Each family shares a prefix, "workflows", "wallets", "library" or "admin",
 * so one invalidation covers it.
 */
export const keys = {
  me: ['me'],
  authConfig: ['auth-config'],
  nodeTypes: ['node-types'],
  tokens: ['tokens'],
  users: ['admin', 'users'],
  invites: ['admin', 'invites'],
  settings: ['admin', 'settings'],
  settingList: ['admin', 'settings', 'list'],
  settingHistory: ['admin', 'settings', 'history'],
  wallets: ['wallets'],
  walletList: ['wallets', 'list'],
  credentials: (walletId: string) => ['wallets', 'credentials', walletId],
  library: ['library'],
  templateList: ['library', 'list'],
  template: (id: string) => ['library', 'template', id],
  sharedCopy: (workflowId: string) => ['library', 'shared', workflowId],
  workflows: ['workflows'],
  workflowList: ['workflows', 'list'],
  workflow: (id: string) => ['workflows', 'detail', id],
  executions: (workflowId: string) => ['workflows', 'executions', workflowId],
  execution: (workflowId: string, id: string) => ['workflows', 'execution', workflowId, id],
} as const
