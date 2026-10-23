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
      'Create, edit, check and run workflows and code nodes, switch them on and off, and answer runs that wait.',
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
