import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'

/**
 * Stands in for the API. Tests add handlers with server.use(...). The top bar asks for
 * the wallets on every page, for the kill switch: none, unless a test says otherwise.
 */
export const server = setupServer(
  http.get('/api/v1/wallets', () => HttpResponse.json({ wallets: [] })),
  // Every page asks for the repository's stars: GitHub has not said, unless a test says.
  http.get('/api/v1/meta', () =>
    HttpResponse.json({ repo: 'https://github.com/lein3000zzz/polythought-go' }),
  ),
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
