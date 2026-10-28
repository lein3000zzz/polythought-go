import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'

/**
 * Stands in for the API. Tests add handlers with server.use(...). The top bar asks for
 * the wallets on every page, for the kill switch: none, unless a test says otherwise.
 */
const emptyTotals = {
  workflows: 0,
  profit: '0',
  realized: '0',
  unrealized: '0',
  positions: 0,
  unpriced: 0,
  volume: '0',
  orders: 0,
  wins: 0,
  closed: 0,
}

export const server = setupServer(
  http.get('/api/v1/wallets', () => HttpResponse.json({ wallets: [] })),
  // Every page asks for the repository's stars: GitHub has not said, unless a test says.
  http.get('/api/v1/meta', () =>
    HttpResponse.json({ repo: 'https://github.com/lein3000zzz/polythought-go' }),
  ),
  // The Workflows page asks how they are doing (D429): nothing to say, unless a test says.
  http.get('/api/v1/workflows/stats', () =>
    HttpResponse.json({
      workflows: [],
      paper: emptyTotals,
      live: emptyTotals,
      day: { runs: 0, failed: 0 },
      week: { runs: 0, failed: 0 },
      at: '2026-10-28T12:00:00Z',
    }),
  ),
  // The admin pages count the requests to join waiting: none, unless a test says.
  http.get('/api/v1/admin/requests', () => HttpResponse.json({ requests: [], pending: 0 })),
  // Settings searches its pages' tokens, agents, notifications and credentials: none,
  // unless a test says.
  http.get('/api/v1/tokens', () => HttpResponse.json({ tokens: [] })),
  http.get('/api/v1/me/grants', () => HttpResponse.json({ grants: [] })),
  // Every page asks what waits for the user to confirm (D406): nothing, unless a test says.
  http.get('/api/v1/me/confirmations', () => HttpResponse.json({ confirmations: [] })),
  http.get('/api/v1/notifications', () => HttpResponse.json({ notifications: [], mailOn: false })),
  http.get('/api/v1/credentials', () => HttpResponse.json({ credentials: [] })),
  http.get('/api/v1/credential-types', () => HttpResponse.json({ credentialTypes: [] })),
  // Settings asks what the agents spent: nothing, and no caps, unless a test says.
  http.get('/api/v1/me/agent-spend', () =>
    HttpResponse.json({
      month: '2026-10-01T00:00:00Z',
      resets: '2026-11-01T00:00:00Z',
      inputTokens: 0,
      outputTokens: 0,
      totalTokens: 0,
      dollars: '0.00',
      calls: 0,
      costedCalls: 0,
      caps: {},
      own: {},
      killStopsAgents: false,
    }),
  ),
)
