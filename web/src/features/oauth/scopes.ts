/**
 * What each scope lets an agent or a token do (D384, D399), as the consent page and Settings
 * say it. Every scope brings read: what it may change, it may see.
 */
export const scopeInfo: Record<string, { title: string; detail: string; risky?: boolean }> = {
  read: {
    title: 'Read',
    detail:
      "Your workflows and their runs, nodes, the library, market data, and your wallets' balances, positions and orders.",
  },
  build: {
    title: 'Build',
    detail:
      'Create, edit, check and run workflows, code and custom nodes, add credentials, use and share library entries, and switch workflows on and off; answer what runs ask you, where you let agents. Where you let agents use your credentials, a workflow it builds can send them wherever its nodes call.',
  },
  tasks: {
    title: 'Take tasks',
    detail:
      "Take and answer the tasks your workflows' Local agent nodes ask of your agents. A task's text comes from the workflow and its items, which can hold others' words.",
  },
  settings: {
    title: 'Change settings',
    detail:
      'Your notifications, linking Telegram, whether the kill switch stops your agents, and the agents connected.',
  },
  paper: {
    title: 'Trade on paper',
    detail: "Place and cancel paper orders, and set up a workflow's paper trading.",
  },
  live: {
    title: 'Trade live',
    detail:
      'Place and cancel orders with real money from your wallets, within your caps and kill switch.',
    risky: true,
  },
  admin: {
    title: 'Admin',
    detail: "An admin's work: users, invites, requests to join, the Config settings and plugins.",
    risky: true,
  },
}

export function scopeTitle(scope: string): string {
  return scopeInfo[scope]?.title ?? scope
}
