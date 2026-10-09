import { setupServer } from 'msw/node'

/** Stands in for the API. Tests add handlers with server.use(...). */
export const server = setupServer()
