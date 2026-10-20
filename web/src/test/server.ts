import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'

/**
 * Stands in for the API. Tests add handlers with server.use(...). The top bar asks for
 * the wallets on every page, for the kill switch: none, unless a test says otherwise.
 */
export const server = setupServer(
  http.get('/api/v1/wallets', () => HttpResponse.json({ wallets: [] })),
)
